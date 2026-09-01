package archive

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fengzhizi319/PrivShield-go/pkg/crypto"
	"github.com/fengzhizi319/PrivShield-go/pkg/store"
	"github.com/fengzhizi319/PrivShield-go/pkg/store/memory"
)

const testKey = "unit-test-archive-key"

func seedStore(t *testing.T, expired, recent int, bogusHash bool) (*memory.AuditStore, time.Time) {
	t.Helper()
	st := memory.NewAuditStore()
	cutoff := time.Now().UTC().AddDate(0, 0, -400).Truncate(time.Second)

	var prev string
	put := func(ts time.Time, id string) {
		l := &store.AuditLog{
			ID: id, TaskID: "task-" + id, APICode: "api1_yibao", DatasourceID: "ds_yibao",
			Timestamp: ts, Operation: "mask", DataSource: "ds_yibao",
			InputHash: "in-" + id, OutputHash: "out-" + id, Algorithm: "SM4-GCM",
			ParametersJSON: `{"fields":["phone"]}`, User: "tester", Status: "success",
			SecurityLevel: "L4", PrevHash: prev,
		}
		if bogusHash {
			l.IntegrityHash = strings.Repeat("f", 64)
		}
		if err := st.SaveLog(l); err != nil {
			t.Fatal(err)
		}
		prev = l.IntegrityHash
		snap := &store.SnapshotRecord{
			ID: "snap-" + id, AuditLogID: id, Timestamp: ts,
			InputSample: crypto.EncryptedPrefixV2 + "sample-" + id, OutputSample: crypto.EncryptedPrefixV2 + "masked-" + id,
			Algorithm: "SM4-GCM", ParametersJSON: `{"fields":["phone"]}`,
		}
		if err := st.SaveSnapshot(snap); err != nil {
			t.Fatal(err)
		}
	}

	for i := 0; i < expired; i++ {
		put(cutoff.Add(-time.Duration(expired-i)*time.Hour), idOf("exp", i))
	}
	for i := 0; i < recent; i++ {
		put(time.Now().UTC().Add(time.Duration(i)*time.Minute), idOf("rec", i))
	}
	return st, cutoff
}

func idOf(kind string, i int) string {
	return kind + "-" + string(rune('a'+i))
}

func newArchiver(t *testing.T, pageSize int) (*Archiver, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "archives")
	a, err := New(Options{ArchiveDir: dir, EncryptionKey: testKey, PageSize: pageSize}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return a, dir
}

func TestNewRejectsMissingDirAndKey(t *testing.T) {
	if _, err := New(Options{EncryptionKey: testKey}, nil); !errors.Is(err, ErrMissingDir) {
		t.Fatalf("expected ErrMissingDir, got %v", err)
	}
	if _, err := New(Options{ArchiveDir: t.TempDir()}, nil); !errors.Is(err, ErrMissingKey) {
		t.Fatalf("expected ErrMissingKey, got %v", err)
	}
}

// TestArchivesBeforeDeleting 验证红线核心：到期记录先落加密归档段并回读验真，再被删除，
// 未到期记录一律不动。
func TestArchivesBeforeDeleting(t *testing.T) {
	st, cutoff := seedStore(t, 3, 2, false)
	a, dir := newArchiver(t, 10)

	stats, err := a.ArchiveAndCleanup(st, cutoff)
	if err != nil {
		t.Fatalf("archive and cleanup: %v", err)
	}
	if stats.LogsArchived != 3 || stats.SnapshotsArchived != 3 || stats.LogsDeleted != 3 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	segments, err := SegmentFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 1 {
		t.Fatalf("expected 1 segment, got %v", segments)
	}
	if _, err := os.Stat(filepath.Join(dir, manifestName(segments[0]))); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	if err := VerifySegment(dir, segments[0], testKey); err != nil {
		t.Fatalf("segment must verify: %v", err)
	}

	remaining, _, err := st.ListLogs(store.AuditFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 2 {
		t.Fatalf("expected the 2 unexpired logs to survive, got %d", len(remaining))
	}
	for _, l := range remaining {
		if !strings.HasPrefix(l.ID, "rec-") {
			t.Fatalf("unexpected log deleted/kept: %s", l.ID)
		}
	}
	if snaps, total, err := st.ListSnapshots(100, 0); err != nil || total != 2 || len(snaps) != 2 {
		t.Fatalf("expected 2 surviving snapshots, got %d/%d err=%v", len(snaps), total, err)
	}
}

// TestMultiSegmentPaging 验证大批量到期数据被切成多段，且每段都能独立验真。
func TestMultiSegmentPaging(t *testing.T) {
	st, cutoff := seedStore(t, 7, 0, false)
	a, dir := newArchiver(t, 3)

	stats, err := a.ArchiveAndCleanup(st, cutoff)
	if err != nil {
		t.Fatalf("archive and cleanup: %v", err)
	}
	if stats.LogsArchived != 7 || stats.LogsDeleted != 7 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	segments, _ := SegmentFiles(dir)
	if len(segments) != 3 {
		t.Fatalf("expected 3 segments for 7 records at page size 3, got %v", segments)
	}
	for _, s := range segments {
		if err := VerifySegment(dir, s, testKey); err != nil {
			t.Fatalf("segment %s must verify: %v", s, err)
		}
	}
}

// TestDeletionRefusedWhenSegmentFailsVerification 是 fail-closed 断言：归档内容自身不自洽
// （伪造完整性哈希）时，一条记录都不允许被删除。
func TestDeletionRefusedWhenSegmentFailsVerification(t *testing.T) {
	st, cutoff := seedStore(t, 2, 1, true)
	a, dir := newArchiver(t, 10)

	_, err := a.ArchiveAndCleanup(st, cutoff)
	if err == nil {
		t.Fatal("expected verification failure to abort cleanup")
	}
	if !strings.Contains(err.Error(), "deletion refused") {
		t.Fatalf("expected a refusal error, got %v", err)
	}
	remaining, _, _ := st.ListLogs(store.AuditFilter{Limit: 100})
	if len(remaining) != 3 {
		t.Fatalf("no record may be deleted when archiving fails, got %d remaining", len(remaining))
	}
	segments, _ := SegmentFiles(dir)
	for _, s := range segments {
		if err := VerifySegment(dir, s, testKey); err == nil {
			t.Fatalf("tampered segment %s must not verify", s)
		}
	}
}

// TestStoreWithoutArchiveCapabilityRefusesDeletion 保证不支持归档读取的存储不会被清理。
func TestStoreWithoutArchiveCapabilityRefusesDeletion(t *testing.T) {
	a, _ := newArchiver(t, 10)
	st := interfaceOnlyStore{AuditStore: memory.NewAuditStore()}
	if _, err := a.ArchiveAndCleanup(st, time.Now()); !errors.Is(err, ErrStoreUnsupported) {
		t.Fatalf("expected ErrStoreUnsupported, got %v", err)
	}
}

// interfaceOnlyStore 只暴露 store.AuditStore 方法集，用于验证缺少归档能力时的 fail-closed。
type interfaceOnlyStore struct{ store.AuditStore }

// TestTamperedSegmentIsDetected 模拟持有密钥的攻击者改写归档内容：行哈希链必须暴露改写。
func TestTamperedSegmentIsDetected(t *testing.T) {
	st, cutoff := seedStore(t, 3, 0, false)
	a, dir := newArchiver(t, 10)
	if _, err := a.ArchiveAndCleanup(st, cutoff); err != nil {
		t.Fatal(err)
	}
	segments, _ := SegmentFiles(dir)
	seg := segments[0]

	lines := decryptSegmentLines(t, dir, seg)
	if len(lines) != 6 {
		t.Fatalf("expected 3 logs + 3 snapshots, got %d lines", len(lines))
	}
	// 删除最后一条记录行后重新封箱（等价于从归档证据里抹掉一条存证）。
	var buf bytes.Buffer
	for _, l := range lines[:len(lines)-1] {
		buf.Write(l)
		buf.WriteByte('\n')
	}
	reseal(t, dir, seg, buf.Bytes())

	err := VerifySegment(dir, seg, testKey)
	if err == nil {
		t.Fatal("tampered segment must fail verification")
	}
	if !strings.Contains(err.Error(), "line hash chain mismatch") {
		t.Fatalf("expected a chain mismatch, got %v", err)
	}
}

func decryptSegmentLines(t *testing.T, dir, seg string) [][]byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, seg))
	if err != nil {
		t.Fatal(err)
	}
	gz, err := crypto.DecryptString(strings.TrimSpace(string(raw)), testKey)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader([]byte(gz)))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	var out [][]byte
	for _, line := range strings.Split(strings.TrimRight(string(plain), "\n"), "\n") {
		out = append(out, []byte(line))
	}
	return out
}

func reseal(t *testing.T, dir, seg string, plain []byte) {
	t.Helper()
	gz, err := gzipBytes(plain)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := crypto.EncryptString(string(gz), testKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, seg), []byte(sealed), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestUnsafeSegmentNamesRejected 验证归档路径拼接不会越出归档目录。
func TestUnsafeSegmentNamesRejected(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"", "..", "../escape.ndjson.gz.enc", "a/b.ndjson.gz.enc", string(os.PathSeparator) + "abs.enc"} {
		if _, err := resolveInDir(dir, name); err == nil {
			t.Fatalf("expected unsafe name %q to be rejected", name)
		}
	}
	if _, err := resolveInDir(dir, "audit-archive-x.ndjson.gz.enc"); err != nil {
		t.Fatalf("safe name rejected: %v", err)
	}
}
