// Package archive implements the evidence-retention red line: overdue records are archived
// (encrypted, SM3 hash-chained, independently verifiable) before any of them is deleted.
// Package archive 落实存证留存红线：到期存证记录必须先归档（SM4-GCM 加密 + SM3 行哈希链、
// 归档段可独立验真）后，才允许被物理删除。
//
// 归档段格式（一次运行产出一组「段文件 + 清单文件」）：
//
//	audit-archive-<cutoff>-<seq>.ndjson.gz.enc   SM4-GCM(gzip(NDJSON 记录行))
//	audit-archive-<cutoff>-<seq>.manifest.json   段元数据与行哈希链尾值
//
// 行哈希链：`chain[i] = SM3(chain[i-1] || line[i])`，链尾值写入清单。核验时无需访问数据库，
// 仅凭段文件 + 清单 + 密钥即可判定归档证据是否被增删改；每条日志行另按 9 要素重算自身
// `integrity_hash`，与链式存证口径一致。
package archive

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fengzhizi319/PrivShield-go/pkg/crypto"
	"github.com/fengzhizi319/PrivShield-go/pkg/store"
)

const (
	// SegmentVersion 是归档段格式版本标识。
	SegmentVersion = "privshield-audit-archive/v1"
	// ChainAlgo 是归档段行哈希链算法标识。
	ChainAlgo = "SM3-LINE-CHAIN:v1"
	// EncryptionAlgo 是归档段加密口径标识。
	EncryptionAlgo = "SM4-GCM/HKDF-SM3(enc:v2)"

	segmentSuffix  = ".ndjson.gz.enc"
	manifestSuffix = ".manifest.json"

	maxArchivePages = 100000
)

var (
	// ErrMissingKey 表示未配置归档加密密钥。
	ErrMissingKey = errors.New("archive: encryption key is required for archived evidence")
	// ErrMissingDir 表示未配置归档目录。
	ErrMissingDir = errors.New("archive: archive directory is required")
	// ErrStoreUnsupported 表示底层存储不具备归档读取能力，禁止删除。
	ErrStoreUnsupported = errors.New("archive: audit store does not support archive reading, deletion refused")
	// ErrNotDeleted 表示归档成功但删除未生效，立即中止以避免重复归档。
	ErrNotDeleted = errors.New("archive: archived records were not deleted, aborting further pages")
)

// Options 配置归档器。
type Options struct {
	ArchiveDir    string           // 归档目录
	EncryptionKey string           // SM4-GCM 归档加密密钥
	PageSize      int              // 单段日志条数（默认 500）
	Now           func() time.Time // 时间源，测试可注入
}

// Archiver 以「先归档后删除」策略执行存证留存清理。
type Archiver struct {
	opts   Options
	logger *slog.Logger
}

// New 构建归档器；目录或密钥缺失时直接返回错误（fail-closed）。
func New(opts Options, logger *slog.Logger) (*Archiver, error) {
	if strings.TrimSpace(opts.ArchiveDir) == "" {
		return nil, ErrMissingDir
	}
	if strings.TrimSpace(opts.EncryptionKey) == "" {
		return nil, ErrMissingKey
	}
	if opts.PageSize <= 0 {
		opts.PageSize = store.DefaultArchivePageSize
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if logger == nil {
		logger = slog.Default()
	}
	if err := os.MkdirAll(opts.ArchiveDir, 0o700); err != nil {
		return nil, fmt.Errorf("archive: create dir %s: %w", opts.ArchiveDir, err)
	}
	return &Archiver{opts: opts, logger: logger}, nil
}

// Stats 汇总单次清理运行的归档与删除结果。
type Stats struct {
	LogsArchived      int64    `json:"logs_archived"`
	SnapshotsArchived int64    `json:"snapshots_archived"`
	LogsDeleted       int64    `json:"logs_deleted"`
	Segments          []string `json:"segments"`
}

// ArchiveAndCleanup 将早于 cutoff 的存证记录逐段归档落盘，并在每段通过磁盘回读验真后
// 按该段 ID 精确删除；任一环节失败即返回错误且不再继续删除。
func (a *Archiver) ArchiveAndCleanup(audit store.AuditStore, cutoff time.Time) (*Stats, error) {
	reader, ok := audit.(store.AuditArchiveReader)
	if !ok {
		return nil, ErrStoreUnsupported
	}

	stats := &Stats{}
	for page := 0; page < maxArchivePages; page++ {
		logs, snaps, err := reader.FetchOldestForArchive(cutoff, a.opts.PageSize)
		if err != nil {
			return stats, fmt.Errorf("archive: fetch expired records: %w", err)
		}
		if len(logs) == 0 {
			return stats, nil
		}

		segment, err := a.writeSegment(logs, snaps, cutoff)
		if err != nil {
			return stats, fmt.Errorf("archive segment failed, deletion refused: %w", err)
		}
		if err := VerifySegment(a.opts.ArchiveDir, segment, a.opts.EncryptionKey); err != nil {
			return stats, fmt.Errorf("archive segment verification failed, deletion refused: %w", err)
		}

		ids := make([]string, 0, len(logs))
		for i := range logs {
			ids = append(ids, logs[i].ID)
		}
		deleted, err := reader.DeleteLogsByIDs(ids)
		if err != nil {
			return stats, fmt.Errorf("archive: delete after archive failed for segment %s: %w", segment, err)
		}
		if deleted == 0 {
			return stats, fmt.Errorf("%w: segment %s archived %d records", ErrNotDeleted, segment, len(ids))
		}

		stats.LogsArchived += int64(len(logs))
		stats.SnapshotsArchived += int64(len(snaps))
		stats.LogsDeleted += deleted
		stats.Segments = append(stats.Segments, segment)
		a.logger.Info("audit evidence archived before deletion",
			"segment", segment,
			"logs", len(logs),
			"snapshots", len(snaps),
			"deleted", deleted,
		)

		if len(logs) < a.opts.PageSize {
			return stats, nil
		}
	}
	return stats, fmt.Errorf("archive: exceeded %d archive pages, aborting", maxArchivePages)
}

// SegmentFiles 列出归档目录内已产出的段文件名（不含清单），按名称升序。
func SegmentFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), segmentSuffix) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// archiveLine 是归档段的一行 NDJSON 记录。parameters_json 独立成字段，
// 因为存储实体的该字段带 json:"-"，直接序列化会丢失重算哈希所需的原文。
type archiveLine struct {
	Kind           string                `json:"kind"`
	ParametersJSON string                `json:"parameters_json,omitempty"`
	Log            *store.AuditLog       `json:"log,omitempty"`
	Snapshot       *store.SnapshotRecord `json:"snapshot,omitempty"`
}

// Manifest 是归档段清单（独立验真凭据）。
type Manifest struct {
	Version        string    `json:"version"`
	ChainAlgo      string    `json:"chain_algo"`
	Encryption     string    `json:"encryption"`
	SegmentFile    string    `json:"segment_file"`
	CreatedAt      time.Time `json:"created_at"`
	Cutoff         time.Time `json:"cutoff"`
	LogCount       int64     `json:"log_count"`
	SnapshotCount  int64     `json:"snapshot_count"`
	FirstLogID     string    `json:"first_log_id"`
	LastLogID      string    `json:"last_log_id"`
	FirstTimestamp time.Time `json:"first_timestamp"`
	LastTimestamp  time.Time `json:"last_timestamp"`
	ChainTail      string    `json:"chain_tail"`
}

func manifestName(segment string) string {
	return strings.TrimSuffix(segment, segmentSuffix) + manifestSuffix
}

func (a *Archiver) writeSegment(logs []store.AuditLog, snaps []store.SnapshotRecord, cutoff time.Time) (string, error) {
	segment, err := a.nextSegmentName(cutoff)
	if err != nil {
		return "", err
	}

	byLog := make(map[string][]int, len(logs))
	for i := range snaps {
		byLog[snaps[i].AuditLogID] = append(byLog[snaps[i].AuditLogID], i)
	}

	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	var logCount, snapCount int64
	chain := ""
	for i := range logs {
		l := logs[i]
		if err := writeChainLine(bw, &chain, &archiveLine{Kind: "log", ParametersJSON: l.ParametersJSON, Log: &l}); err != nil {
			return "", err
		}
		logCount++
		for _, si := range byLog[l.ID] {
			s := snaps[si]
			if err := writeChainLine(bw, &chain, &archiveLine{Kind: "snapshot", ParametersJSON: s.ParametersJSON, Snapshot: &s}); err != nil {
				return "", err
			}
			snapCount++
		}
	}
	if err := bw.Flush(); err != nil {
		return "", err
	}

	gz, err := gzipBytes(buf.Bytes())
	if err != nil {
		return "", err
	}
	sealed, err := crypto.EncryptString(string(gz), a.opts.EncryptionKey)
	if err != nil {
		return "", fmt.Errorf("seal archive segment: %w", err)
	}

	path, err := resolveInDir(a.opts.ArchiveDir, segment)
	if err != nil {
		return "", err
	}
	if err := writeFsync(path, []byte(sealed)); err != nil {
		return "", err
	}

	manifest := Manifest{
		Version:        SegmentVersion,
		ChainAlgo:      ChainAlgo,
		Encryption:     EncryptionAlgo,
		SegmentFile:    segment,
		CreatedAt:      a.opts.Now().UTC(),
		Cutoff:         cutoff.UTC(),
		LogCount:       logCount,
		SnapshotCount:  snapCount,
		FirstLogID:     logs[0].ID,
		LastLogID:      logs[len(logs)-1].ID,
		FirstTimestamp: logs[0].Timestamp.UTC(),
		LastTimestamp:  logs[len(logs)-1].Timestamp.UTC(),
		ChainTail:      chain,
	}
	mb, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	mb = append(mb, '\n')
	manifestPath, err := resolveInDir(a.opts.ArchiveDir, manifestName(segment))
	if err != nil {
		return "", err
	}
	if err := writeFsync(manifestPath, mb); err != nil {
		return "", err
	}
	return segment, nil
}

// nextSegmentName 返回未被占用的段文件名，绝不覆盖既有归档证据。
func (a *Archiver) nextSegmentName(cutoff time.Time) (string, error) {
	prefix := "audit-archive-" + cutoff.UTC().Format("20060102T150405Z") + "-"
	for seq := 0; seq < 1000000; seq++ {
		name := fmt.Sprintf("%s%06d%s", prefix, seq, segmentSuffix)
		path, err := resolveInDir(a.opts.ArchiveDir, name)
		if err != nil {
			return "", err
		}
		switch _, err := os.Stat(path); {
		case errors.Is(err, fs.ErrNotExist):
			return name, nil
		case err != nil:
			return "", fmt.Errorf("archive: stat %s: %w", name, err)
		}
	}
	return "", fmt.Errorf("archive: no free segment name under %s", prefix)
}

// resolveInDir 把文件名解析到归档目录内，拒绝任何越出目录的路径片段。
func resolveInDir(dir, name string) (string, error) {
	if name == "" || strings.ContainsRune(name, os.PathSeparator) || name == "." || name == ".." ||
		strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return "", fmt.Errorf("archive: unsafe file name %q", name)
	}
	base, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("archive: resolve dir: %w", err)
	}
	full, err := filepath.Abs(filepath.Join(base, name))
	if err != nil {
		return "", fmt.Errorf("archive: resolve %s: %w", name, err)
	}
	if filepath.Dir(full) != base {
		return "", fmt.Errorf("archive: %q escapes archive dir %s", name, dir)
	}
	return full, nil
}

func writeChainLine(w io.Writer, chain *string, line *archiveLine) error {
	raw, err := json.Marshal(line)
	if err != nil {
		return fmt.Errorf("marshal archive line: %w", err)
	}
	*chain = crypto.SumSM3Hex(append([]byte(*chain), raw...))
	raw = append(raw, '\n')
	if _, err := w.Write(raw); err != nil {
		return fmt.Errorf("write archive line: %w", err)
	}
	return nil
}

func gzipBytes(data []byte) ([]byte, error) {
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	if _, err := zw.Write(data); err != nil {
		return nil, fmt.Errorf("gzip archive segment: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("close gzip: %w", err)
	}
	return out.Bytes(), nil
}

func gunzipBytes(data []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("open gzip: %w", err)
	}
	defer zr.Close()
	out, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("read gzip: %w", err)
	}
	return out, nil
}

func writeFsync(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return nil
	}
	_ = d.Sync()
	_ = d.Close()
	return nil
}

// ReadManifest 读取并解析归档段清单。
func ReadManifest(dir, segment string) (*Manifest, error) {
	path, err := resolveInDir(dir, manifestName(segment))
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if m.Version != SegmentVersion {
		return nil, fmt.Errorf("unsupported archive segment version %q", m.Version)
	}
	return &m, nil
}

// VerifySegment 仅凭段文件、清单与密钥独立验真一个归档段：校验行哈希链、条数、时间边界，
// 以及每条日志自身的 9 要素完整性哈希。
func VerifySegment(dir, segment, key string) error {
	manifest, err := ReadManifest(dir, segment)
	if err != nil {
		return err
	}
	if strings.TrimSpace(key) == "" {
		return ErrMissingKey
	}
	if manifest.SegmentFile != segment {
		return fmt.Errorf("archive: manifest refers to segment %q, not %q", manifest.SegmentFile, segment)
	}

	path, err := resolveInDir(dir, segment)
	if err != nil {
		return err
	}
	sealed, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read segment: %w", err)
	}
	gz, err := crypto.DecryptString(strings.TrimSpace(string(sealed)), key)
	if err != nil {
		return fmt.Errorf("decrypt segment: %w", err)
	}
	raw, err := gunzipBytes([]byte(gz))
	if err != nil {
		return err
	}

	chain := ""
	var logCount, snapCount int64
	var firstID, lastID string
	var firstTS, lastTS time.Time
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 32*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		chain = crypto.SumSM3Hex(append([]byte(chain), line...))

		var parsed archiveLine
		if err := json.Unmarshal(line, &parsed); err != nil {
			return fmt.Errorf("parse archived line: %w", err)
		}
		switch parsed.Kind {
		case "log":
			if parsed.Log == nil {
				return fmt.Errorf("segment %s: log line missing payload", segment)
			}
			if err := verifyLogRecord(parsed.Log, parsed.ParametersJSON); err != nil {
				return fmt.Errorf("segment %s: %w", segment, err)
			}
			logCount++
			if firstID == "" {
				firstID, firstTS = parsed.Log.ID, parsed.Log.Timestamp.UTC()
			}
			lastID, lastTS = parsed.Log.ID, parsed.Log.Timestamp.UTC()
		case "snapshot":
			if parsed.Snapshot == nil {
				return fmt.Errorf("segment %s: snapshot line missing payload", segment)
			}
			snapCount++
		default:
			return fmt.Errorf("segment %s: unknown archived line kind %q", segment, parsed.Kind)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan segment: %w", err)
	}

	if chain != manifest.ChainTail {
		return fmt.Errorf("segment %s: line hash chain mismatch (archived %s, recomputed %s) - evidence modified, truncated or reordered",
			segment, manifest.ChainTail, chain)
	}
	if logCount != manifest.LogCount || snapCount != manifest.SnapshotCount {
		return fmt.Errorf("segment %s: record count mismatch (manifest %d logs / %d snapshots, actual %d logs / %d snapshots)",
			segment, manifest.LogCount, manifest.SnapshotCount, logCount, snapCount)
	}
	if manifest.LogCount > 0 {
		if firstID != manifest.FirstLogID || lastID != manifest.LastLogID {
			return fmt.Errorf("segment %s: boundary log ids mismatch", segment)
		}
		if !firstTS.Equal(manifest.FirstTimestamp) || !lastTS.Equal(manifest.LastTimestamp) {
			return fmt.Errorf("segment %s: boundary timestamps mismatch", segment)
		}
	}
	return nil
}

func verifyLogRecord(l *store.AuditLog, parametersJSON string) error {
	if l.IntegrityHash == "" {
		return fmt.Errorf("log %s carries no integrity hash", l.ID)
	}
	ok, _ := store.VerifyAuditIntegrityHash(
		l.IntegrityHash, l.ID, l.PrevHash, l.Timestamp, l.Algorithm,
		l.InputHash, l.OutputHash, l.User, l.SecurityLevel, parametersJSON,
	)
	if !ok {
		return fmt.Errorf("log %s: integrity hash does not match archived content", l.ID)
	}
	return nil
}
