// Package catalog 把跨服务 canonical 注册表（pkg/naming）与 App-LZ 的展示层
// 元数据（中文名、描述、字段清单）组合成 BFF 使用的预设数据 API 目录。
//
// 设计约束（见 console/app-lz/docs/api_rename_design.md §5.2、D-13/D-14）：
//  1. 业务标识（api_code / datasource_id / 别名 / 状态）只能来自 pkg/naming，
//     本包不得另造 ID，也不得出现裸数据源字面量。
//  2. 本包只补充「展示与 schema」信息：中文名、英文名、描述、字段清单。
//  3. handlers 的 /data-api/definitions 与 clients 的降级样本数据必须共用本包，
//     避免同名数据源在两条链路上字段语义漂移（D-14）。
package catalog

import (
	"fmt"

	"github.com/fengzhizi319/PrivShield-go/console/app-lz/bff-go/internal/models"
	naming "github.com/fengzhizi319/PrivShield-go/pkg/naming"
)

// schema 是单个数据源的展示元数据 + 字段清单。
type schema struct {
	NameZh      string
	NameEn      string
	Description string
	Fields      []string
}

// schemas 以 canonical datasource_id 为键，键名一律取 naming 常量（禁止字面量）。
var schemas = map[string]schema{
	naming.DSYibao: {
		NameZh: "医保结算数据 API",
		NameEn: "Medical Insurance Settlement API",
		Description: fmt.Sprintf(
			"城镇职工基本医疗保险结算数据 (%s 18 字段)，包含结算流水号、人员标识、性别、出生日期、入院/出院日期、住院天数、科室、医院编码、医疗类别、离院方式、ICD-10 诊断编码、诊断名称、入院病情等敏感字段。",
			fileNameOf(naming.DSYibao)),
		Fields: []string{
			"insurance_settlement_id", "person_id", "gender", "birth_date",
			"admission_date", "discharge_date", "length_of_stay",
			"admission_dept", "discharge_dept", "hospital_code",
			"medical_category", "discharge_mode", "settlement_seq_no",
			"diagnosis_seq", "diagnosis_type", "icd10_code",
			"diagnosis_name", "admission_condition",
		},
	},
	naming.DSKangyang: {
		NameZh: "康养体征数据 API",
		NameEn: "Elderly-Care Health Record API",
		Description: fmt.Sprintf(
			"智慧养老健康监护与慢病随访数据 (%s 27 字段)，包含姓名、身份证号、主诉、现病史、既往史、个人史、吸烟史、家族史、过敏史、残疾评估、体征指标、病程记录等敏感字段。",
			fileNameOf(naming.DSKangyang)),
		Fields: []string{
			"gender", "age", "diagnosis_name", "chief_complaint",
			"present_illness", "past_history", "personal_history",
			"is_smoking", "smoking_duration", "family_history",
			"allergic_history", "department", "height", "weight",
			"disability_category", "disability_level",
			"assess_type_name", "assess_result_name", "assess_score",
			"assess_time", "progress_note", "progress_note_time",
			"name", "id_card_no", "registered_address",
			"disability_cert_no", "medical_insurance_no",
		},
	},
}

const reservedDescription = "预留接口，待后续业务接入新的数据源。"

// fileNameOf returns the source file name registered for a datasource id.
func fileNameOf(datasourceID string) string {
	if e, ok := naming.EntryByDataSourceID(datasourceID); ok {
		return e.FileName
	}
	return datasourceID
}

// SlugOf returns the URL slug (alias first entry) of a datasource, used only by
// the deprecated per-source endpoints.
// SlugOf 返回数据源的 URL slug（仅供弃用端点使用）。
func SlugOf(datasourceID string) string {
	if e, ok := naming.EntryByDataSourceID(datasourceID); ok && len(e.Aliases) > 0 {
		return e.Aliases[0]
	}
	return ""
}

// Definitions 返回全部预设数据 API 定义（含 reserved 占位），顺序即 naming 注册表顺序。
func Definitions() []models.DataApiDef {
	out := make([]models.DataApiDef, 0, len(naming.Registry))
	for _, e := range naming.Registry {
		def := models.DataApiDef{
			APICode:      e.APICode,
			DatasourceID: e.DataSourceID,
			Seq:          e.Seq,
			ID:           e.Seq,
			Category:     e.Category,
			Status:       e.Status,
			FileName:     e.FileName,
		}
		if len(e.Aliases) > 0 {
			def.Slug = e.Aliases[0]
		}
		if s, ok := schemas[e.DataSourceID]; ok {
			def.Name = s.NameZh
			def.NameEn = s.NameEn
			def.Description = s.Description
			def.Fields = append([]string(nil), s.Fields...)
		}
		if def.Name == "" {
			def.Name = e.DisplayName["zh-CN"]
		}
		if def.NameEn == "" {
			def.NameEn = e.DisplayName["en-US"]
		}
		if def.Status != naming.StatusActive {
			def.Description = reservedDescription
			def.Fields = []string{}
		}
		out = append(out, def)
	}
	return out
}

// ActiveDatasourceIDs returns the canonical ids that may be invoked.
// ActiveDatasourceIDs 返回允许调用的 canonical 数据源 ID。
func ActiveDatasourceIDs() []string { return naming.ActiveDataSourceIDs() }

// ByDatasourceID returns the definition of one datasource (exact canonical id).
// ByDatasourceID 按 canonical 数据源 ID 精确返回定义。
func ByDatasourceID(datasourceID string) (models.DataApiDef, bool) {
	for _, d := range Definitions() {
		if d.DatasourceID == datasourceID {
			return d, true
		}
	}
	return models.DataApiDef{}, false
}

// ByAPICode returns the definition bound to a canonical api_code.
// ByAPICode 按 canonical api_code 返回定义。
func ByAPICode(apiCode string) (models.DataApiDef, bool) {
	dsID, ok := naming.DataSourceForAPICode(apiCode)
	if !ok {
		return models.DataApiDef{}, false
	}
	return ByDatasourceID(dsID)
}

// BySeq returns the definition whose display sequence equals seq.
// BySeq 按展示序号返回定义（兼容期 api_id 语义）。
func BySeq(seq int) (models.DataApiDef, bool) {
	for _, d := range Definitions() {
		if d.Seq == seq {
			return d, true
		}
	}
	return models.DataApiDef{}, false
}

// Fields returns the schema field list of a datasource, or nil when unknown.
// Fields 返回数据源字段清单，未知数据源返回 nil。
func Fields(datasourceID string) []string {
	if s, ok := schemas[datasourceID]; ok {
		return s.Fields
	}
	return nil
}

// Resolve maps an inbound api identification payload (api_code / api_id /
// datasource_id) onto a single definition, enforcing the precedence and the
// consistency rules of api_rename_design.md §4.2.
//
// 返回的 error 总是 *ValidationError，便于 handler 直接渲染 §6.3 错误体。
func Resolve(apiCode string, apiID int, datasourceID string) (models.DataApiDef, error) {
	lookupByCode := func(code string) (models.DataApiDef, error) {
		d, ok := ByAPICode(code)
		if !ok {
			return models.DataApiDef{}, &ValidationError{
				Code:     "INVALID_API_CODE",
				Field:    "api_code",
				Received: code,
				Message:  fmt.Sprintf("unknown api_code %q (allowed: %v)", code, naming.APICodes()),
			}
		}
		return d, nil
	}

	var def models.DataApiDef
	var err error

	switch {
	case apiCode != "":
		if def, err = lookupByCode(apiCode); err != nil {
			return models.DataApiDef{}, err
		}
	case apiID > 0:
		d, ok := BySeq(apiID)
		if !ok {
			return models.DataApiDef{}, &ValidationError{
				Code:     "INVALID_API_CODE",
				Field:    "api_id",
				Received: fmt.Sprintf("%d", apiID),
				Message:  fmt.Sprintf("unknown api_id %d (allowed seq: 1..%d)", apiID, len(naming.Registry)),
			}
		}
		def = d
	case datasourceID != "":
		d, found := ByDatasourceID(datasourceID)
		if !found {
			return models.DataApiDef{}, &ValidationError{
				Code:     "INVALID_DATASOURCE_ID",
				Field:    "datasource_id",
				Received: datasourceID,
				Message:  fmt.Sprintf("datasource_id %q is not registered", datasourceID),
			}
		}
		def = d
	default:
		return models.DataApiDef{}, &ValidationError{
			Code:    "INVALID_REQUEST",
			Field:   "api_code",
			Message: "one of api_code / api_id / datasource_id is required",
		}
	}

	if datasourceID != "" && def.DatasourceID != "" && datasourceID != def.DatasourceID {
		return models.DataApiDef{}, &ValidationError{
			Code:     "API_DATASOURCE_MISMATCH",
			Field:    "datasource_id",
			Received: datasourceID,
			Message: fmt.Sprintf("api_code %q belongs to %s, not %s",
				def.APICode, def.DatasourceID, datasourceID),
		}
	}
	if def.APICode == "" {
		return models.DataApiDef{}, &ValidationError{
			Code:     "RESERVED_DATASOURCE",
			Field:    "datasource_id",
			Received: def.DatasourceID,
			Message:  "this data source is a reserved placeholder without a bound api_code",
		}
	}
	return def, nil
}

// ValidationError is a request-level contract violation with an explicit error
// code from api_rename_design.md §6.3.
// ValidationError 表示请求契约违规，携带 §6.3 定义的错误码。
type ValidationError struct {
	Code     string
	Field    string
	Received string
	Message  string
}

func (e *ValidationError) Error() string { return e.Message }
