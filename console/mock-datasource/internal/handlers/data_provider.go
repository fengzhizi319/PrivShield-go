// Package handlers provides data provider utilities for mock datasets.
// Package handlers 提供模拟数据集的数据提供者工具与数据加载引擎。
//
// 该文件实现了：
// 1. 模拟数据源注册表（MockDataSources）与其元数据维护；
// 2. CSV 文件跨层级自适应搜索与安全路径解析（findCSVFile）；
// 3. 通用 CSV 文件解析器与动态类型推断（LoadCSVRecords：字符串、整数、浮点数自动转换）；
// 4. 按身份证号查询单条记录（GetRecordByIDCard）；
// 5. 数据源 Schema 元数据构建器（GetMetadata）。
package handlers

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"

	naming "github.com/fengzhizi319/PrivShield-go/pkg/naming"
	"github.com/fengzhizi319/PrivShield-go/services/datasource-mgr/internal/models"
)

// Pre-defined mock data sources
// MockDataSources 定义了系统内置注册的模拟数据源清单，用于开发测试与演示环境。
var MockDataSources = []models.MockDataSource{
	{
		ID:           naming.DSYibao,
		DatasourceID: naming.DSYibao,
		APICode:      naming.API1Yibao,
		Name:         "医保就医与结算模拟数据库 (yibao.csv)",
		Type:         "file",
		Description:  "模拟医保局患者就医、诊断与费用结算明细数据",
		Status:       "connected",
		RowCount:     50,
		Tags:         []string{"医保", "门诊住院", "结算流水", "敏感数据"},
	},
	{
		ID:           naming.DSKangyang,
		DatasourceID: naming.DSKangyang,
		APICode:      naming.API2Kangyang,
		Name:         "康养体检与慢病模拟数据库 (kangyang.csv)",
		Type:         "file",
		Description:  "模拟民政/卫健康养中心体检、慢病随访与残疾评估数据",
		Status:       "connected",
		RowCount:     50,
		Tags:         []string{"康养", "慢病随访", "体检报告", "健康档案"},
	},
	{
		ID:           naming.DSMock3,
		DatasourceID: naming.DSMock3,
		Name:         "预留政务数据源 3 (Reserved Mock Source 3)",
		Type:         "mock",
		Description:  "预留扩展模拟数据源 3，用于后续政务跨部门联合调试",
		Status:       "connected",
		RowCount:     10,
		Tags:         []string{"预留", "政务流通", "扩展接口"},
	},
	{
		ID:           naming.DSMock4,
		DatasourceID: naming.DSMock4,
		Name:         "预留政务数据源 4 (Reserved Mock Source 4)",
		Type:         "mock",
		Description:  "预留扩展模拟数据源 4，用于后续企业端数据合规流转调试",
		Status:       "connected",
		RowCount:     10,
		Tags:         []string{"预留", "金融统计", "扩展接口"},
	},
}

// ListMockDataSources returns all registered mock sources.
// ListMockDataSources 返回当前已注册的全部模拟数据源列表。
func ListMockDataSources() []models.MockDataSource {
	return MockDataSources
}

// GetMockDataSource returns a mock datasource by ID.
// GetMockDataSource 根据数据源 ID（或常用别名如 "yibao", "kangyang"）查找对应的数据源元数据。
func GetMockDataSource(id string) (*models.MockDataSource, error) {
	normID, err := naming.NormalizeDataSourceID(id)
	if err != nil {
		for _, ds := range MockDataSources {
			if ds.ID == id || ds.DatasourceID == id {
				return &ds, nil
			}
		}
		return nil, fmt.Errorf("mock datasource not found: %s", id)
	}
	for _, ds := range MockDataSources {
		if ds.DatasourceID == normID || ds.ID == normID {
			return &ds, nil
		}
	}
	return nil, fmt.Errorf("mock datasource not found: %s", id)
}

// candidateDirs for finding mock CSV files
// candidateDirs 预置了常见的样本 CSV 文件存放相对路径，支持在不同运行工作目录下自动定位数据文件。
var candidateDirs = []string{
	"samples",
	"services/datasource-mgr/samples",
	"data",
	"../../data",
	"../../services/datasource-mgr/samples",
	"console/bff-go/internal/samples",
}

// allowedCSVFiles restricts which files can be loaded by LoadCSVRecords to prevent
// path traversal / LFI attacks. Only the two official mock datasets are exposed.
// allowedCSVFiles 限制只允许加载官方白名单内的模拟数据集文件，防止路径遍历与恶意文件读取。
var allowedCSVFiles = map[string]struct{}{
	"yibao.csv":    {},
	"kangyang.csv": {},
}

// findCSVFile searches for a given CSV filename across candidate directories and parent directory trees.
// findCSVFile 根据文件名在系统候选目录及向上父级目录中递归探查实际文件路径，执行逻辑如下：
//  1. 路径清洗与白名单校验：使用 filepath.Clean 与 filepath.Base 提取纯文件名，并校验必须在 allowedCSVFiles 白名单内且后缀为 .csv，防止目录穿越攻击；
//  2. 候选目录扫描：遍历 candidateDirs 列表，检查 filepath.Join(dir, baseName) 是否存在且非目录；
//  3. 向上回溯搜索：若候选目录未命中，获取当前工作目录 (os.Getwd)，向上逐层遍历最多 6 级父目录，
//     在每级目录的常见样本子目录中查找；
//  4. 若最终仍未找到，返回明确的“文件未找到”错误。
func findCSVFile(filename string) (string, error) {
	// Normalize and extract the final basename. Using filepath.Base drops any
	// leading directory components, but an attacker could still try to access an
	// unintended file with the same basename. We therefore also enforce an
	// explicit allow-list and a strict ".csv" suffix.
	cleanName := filepath.Clean(filename)
	baseName := filepath.Base(cleanName)

	if filepath.Ext(baseName) != ".csv" {
		return "", fmt.Errorf("only .csv files are allowed: %s", baseName)
	}
	if _, ok := allowedCSVFiles[baseName]; !ok {
		return "", fmt.Errorf("csv file is not in allow-list: %s", baseName)
	}

	// 阶段 1：在预定义的候选相对目录中查找
	for _, dir := range candidateDirs {
		cand := filepath.Join(dir, baseName)
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			return cand, nil
		}
	}

	// 阶段 2：以当前工作目录为基准，向上逐层回溯探查
	if curr, err := os.Getwd(); err == nil {
		for i := 0; i < 6; i++ {
			for _, sub := range []string{"samples", "services/datasource-mgr/samples", "data", "engine/medical_pipeline/samples"} {
				cand := filepath.Join(curr, sub, baseName)
				if info, err := os.Stat(cand); err == nil && !info.IsDir() {
					return cand, nil
				}
			}
			parent := filepath.Dir(curr)
			if parent == curr { // 已到达文件系统根目录
				break
			}
			curr = parent
		}
	}

	return "", fmt.Errorf("csv file not found: %s", baseName)
}

// strictDataIntegrity 是 P0-4「禁静音降级」开关：由 main() 依据 cfg.StrictStorage 在启动阶段置位。
// 为真时，样本 CSV 中的损坏行不再被静默丢弃（原实现 `continue` 会让调用方拿到一个比文件实际内容
// 更小的数据集却返回 200），而是上抛为查询失败。
// 默认即为 true（fail-closed）：即使某个入口忘记调用 SetStrictDataIntegrity，也不会退回静音降级；
// 只有显式 DATASOURCE_MGR_STRICT_STORAGE=false 才会关闭。
var strictDataIntegrity atomic.Bool

func init() { strictDataIntegrity.Store(true) }

// SetStrictDataIntegrity 设置样本数据集完整性严格模式（进程启动时调用一次）。
// SetStrictDataIntegrity enables or disables strict sample-data integrity for the whole process.
func SetStrictDataIntegrity(strict bool) { strictDataIntegrity.Store(strict) }

// LoadCSVRecords loads CSV records from disk with dynamic type inference.
// LoadCSVRecords 加载并解析指定的 CSV 数据文件，执行动态类型推断，执行逻辑如下：
// 1. 定位文件：调用 findCSVFile(filename) 获取文件物理路径；
// 2. 打开文件：以只读方式打开文件并注册 defer file.Close()；
// 3. 解析表头：使用 csv.NewReader 读取首行作为字段名映射表（设置 FieldsPerRecord = -1 支持变长字段）；
// 4. 行流式解析与类型推断：
//   - 逐行读取数据记录直至 io.EOF；
//   - 将每个字段值映射到表头列名；
//   - 智能类型转换：优先尝试转为 int64 整数；若包含小数点则尝试转为 float64 浮点数；否则保留为 string；
//   - 将解析后的 map[string]any 追加至 allRows；
//   - 严格存储模式（DATASOURCE_MGR_STRICT_STORAGE=true，默认）下损坏行直接报错，不静默丢弃；
//
// 5. 返回全部数据行与总行数。
func LoadCSVRecords(filename string) ([]map[string]any, int, error) {
	// 1. 定位物理文件路径
	filePath, err := findCSVFile(filename)
	if err != nil {
		return nil, 0, err
	}

	// 2. 打开文件
	file, err := os.Open(filePath)
	if err != nil {
		return nil, 0, fmt.Errorf("open csv file: %w", err)
	}
	defer file.Close()

	// 3. 构建 CSV 读取器并解析首行表头
	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1 // 允许每行字段数动态浮动

	header, err := reader.Read()
	if err != nil {
		return nil, 0, fmt.Errorf("read csv header: %w", err)
	}

	// 4. 逐行读取与动态类型推断
	var allRows []map[string]any
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			if strictDataIntegrity.Load() {
				// 禁止静音降级：损坏行会让返回记录数小于文件实际行数，必须让调用方观测到失败。
				line := -1
				var pe *csv.ParseError
				if errors.As(err, &pe) {
					line = pe.StartLine
				}
				return nil, 0, fmt.Errorf("read csv %s: malformed record at line %d: %w", filePath, line, err)
			}
			continue // 忽略损坏或空行
		}

		rowMap := make(map[string]any, len(header))
		for i, col := range header {
			colName := strings.TrimSpace(col)
			if i < len(record) {
				val := strings.TrimSpace(record[i])
				// 优先推断整数
				if intVal, err := strconv.ParseInt(val, 10, 64); err == nil {
					rowMap[colName] = intVal
					// 包含小数点时推断浮点数
				} else if floatVal, err := strconv.ParseFloat(val, 64); err == nil && strings.Contains(val, ".") {
					rowMap[colName] = floatVal
					// 兜底作为纯文本字符串
				} else {
					rowMap[colName] = val
				}
			} else {
				rowMap[colName] = ""
			}
		}
		allRows = append(allRows, rowMap)
	}

	return allRows, len(allRows), nil
}

// ErrRecordNotFound is returned when no record matches the given ID card number.
// ErrRecordNotFound 表示按身份证号查询时未找到匹配记录。
var ErrRecordNotFound = fmt.Errorf("record not found")

// GetRecordByIDCard queries a single record from the specified datasource by ID card number.
// GetRecordByIDCard 根据身份证号从指定数据源中查找单条记录。
// ds_yibao 和 ds_kangyang 均按 id_card_no 字段匹配。
// 返回值为：匹配的记录、数据源规范 ID、错误（未找到时为 ErrRecordNotFound）。
func GetRecordByIDCard(sourceID, idCardNo string) (map[string]any, string, error) {
	normID, err := naming.NormalizeDataSourceID(sourceID)
	if err != nil {
		return nil, "", fmt.Errorf("unknown mock source: %s", sourceID)
	}

	// 确定要加载的数据文件和源名称
	var csvFile string
	switch normID {
	case naming.DSYibao:
		csvFile = "yibao.csv"
	case naming.DSKangyang:
		csvFile = "kangyang.csv"
	default:
		return nil, normID, fmt.Errorf("datasource %s does not support ID card lookup", normID)
	}

	// 加载全部数据
	rows, _, err := LoadCSVRecords(csvFile)
	if err != nil {
		return nil, normID, fmt.Errorf("load csv records: %w", err)
	}

	// 遍历查找 id_card_no 匹配的行
	for _, row := range rows {
		if val, ok := row["id_card_no"]; ok {
			valStr := fmt.Sprintf("%v", val)
			if valStr == idCardNo {
				return row, normID, nil
			}
		}
	}

	return nil, normID, ErrRecordNotFound
}

// GetMetadata returns table schema for a mock source.
// GetMetadata 根据数据源 ID 生成并返回对应的数据表模式（Schema）元数据定义。
// 字段定义与 engine/medical_pipeline/samples 及 scripts/data/ 生成脚本保持严格一致。
func GetMetadata(sourceID string) (*models.MetadataResponse, error) {
	ds, err := GetMockDataSource(sourceID)
	if err != nil {
		return nil, err
	}

	// 默认基础字段集合
	fields := []models.MetadataField{
		{Name: "id", Type: "string"},
		{Name: "name", Type: "string"},
		{Name: "created_at", Type: "timestamp"},
	}

	// 针对特定数据集定制其业务 Schema，与 CSV 表头严格对齐
	if ds.ID == naming.DSYibao {
		// yibao.csv 19 字段
		fields = []models.MetadataField{
			{Name: "insurance_settlement_id", Type: "string"},
			{Name: "person_id", Type: "string"},
			{Name: "gender", Type: "string"},
			{Name: "birth_date", Type: "string"},
			{Name: "admission_date", Type: "string"},
			{Name: "discharge_date", Type: "string"},
			{Name: "length_of_stay", Type: "integer"},
			{Name: "admission_dept", Type: "string"},
			{Name: "discharge_dept", Type: "string"},
			{Name: "hospital_code", Type: "string"},
			{Name: "medical_category", Type: "string"},
			{Name: "discharge_mode", Type: "string"},
			{Name: "settlement_seq_no", Type: "string"},
			{Name: "diagnosis_seq", Type: "integer"},
			{Name: "diagnosis_type", Type: "string"},
			{Name: "icd10_code", Type: "string"},
			{Name: "diagnosis_name", Type: "string"},
			{Name: "admission_condition", Type: "string"},
			{Name: "id_card_no", Type: "string"},
		}
	} else if ds.ID == naming.DSKangyang {
		// kangyang.csv 27 字段
		fields = []models.MetadataField{
			{Name: "gender", Type: "string"},
			{Name: "age", Type: "integer"},
			{Name: "diagnosis_name", Type: "string"},
			{Name: "chief_complaint", Type: "string"},
			{Name: "present_illness", Type: "string"},
			{Name: "past_history", Type: "string"},
			{Name: "personal_history", Type: "string"},
			{Name: "is_smoking", Type: "string"},
			{Name: "smoking_duration", Type: "string"},
			{Name: "family_history", Type: "string"},
			{Name: "allergic_history", Type: "string"},
			{Name: "department", Type: "string"},
			{Name: "height", Type: "integer"},
			{Name: "weight", Type: "integer"},
			{Name: "disability_category", Type: "string"},
			{Name: "disability_level", Type: "string"},
			{Name: "assess_type_name", Type: "string"},
			{Name: "assess_result_name", Type: "string"},
			{Name: "assess_score", Type: "integer"},
			{Name: "assess_time", Type: "string"},
			{Name: "progress_note", Type: "string"},
			{Name: "progress_note_time", Type: "string"},
			{Name: "name", Type: "string"},
			{Name: "id_card_no", Type: "string"},
			{Name: "registered_address", Type: "string"},
			{Name: "disability_cert_no", Type: "string"},
			{Name: "medical_insurance_no", Type: "string"},
		}
	}

	return &models.MetadataResponse{
		DataSourceID: ds.ID,
		Tables: []models.TableMetadata{
			{
				Name:     ds.Name,
				RowCount: ds.RowCount,
				Fields:   fields,
			},
		},
		Via: "datasource-mgr",
	}, nil
}
