package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestExcluded(t *testing.T) {
	excludedPaths := []string{
		"xhan_helper.db",
		"xhan_helper.db-journal",
		"xhan_helper.db-wal",
		"xhan_helper.db.pre-migrate-20260101-000000",
		"xhan_helper.db.pre-restore-20260101-000000",
		".tmp-xhanHelper-prod-20260101-000000.tar.gz",
		"xhanHelper-prod-20260101-000000.tar.gz",
	}
	for _, p := range excludedPaths {
		if !excluded(p) {
			t.Errorf("应排除 %q", p)
		}
	}
	for _, p := range []string{"images/a.png", "history.json", "favicons/x.ico", "tool_output/1.txt", "some-other.tar.gz"} {
		if excluded(p) {
			t.Errorf("不应排除 %q", p)
		}
	}
}

func TestParseStamp(t *testing.T) {
	fallback := time.Date(2000, 1, 1, 0, 0, 0, 0, time.Local)
	got := parseStamp("xhanHelper-prod-20260925-030000.tar.gz", fallback)
	want := time.Date(2026, 9, 25, 3, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("parseStamp = %v, want %v", got, want)
	}
	if got := parseStamp("garbage", fallback); !got.Equal(fallback) {
		t.Errorf("坏名字应回退 mtime, got %v", got)
	}
}

func TestIsWithin(t *testing.T) {
	if !isWithin("/a/b", "/a") {
		t.Error("/a/b 应在 /a 内")
	}
	if !isWithin("/a", "/a") {
		t.Error("自身视为在内")
	}
	if isWithin("/ab", "/a") {
		t.Error("/ab 不在 /a 内（前缀误判）")
	}
	if isWithin("/x", "/a") {
		t.Error("/x 不在 /a 内")
	}
}

func TestPrune(t *testing.T) {
	dest := t.TempDir()
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.Local)
	daysAgo := func(n int) string {
		return now.AddDate(0, 0, -n).Format("20060102-150405")
	}
	write := func(env string, n int) string {
		name := "xhanHelper-" + env + "-" + daysAgo(n) + ".tar.gz"
		if err := os.WriteFile(filepath.Join(dest, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return name
	}
	write("test", 0)
	write("test", 1)
	write("test", 2)
	write("test", 3)
	write("test", 4)
	write("test", 10)
	write("test", 20)
	otherEnv := write("prod", 0) // 别的 env，不应被动到

	if err := prune(dest, "test", 3, 1, now); err != nil {
		t.Fatal(err)
	}

	kept := map[string]bool{}
	for _, a := range mustList(t, dest, "test") {
		kept[a.Name] = true
	}
	wantKept := map[string]bool{
		"xhanHelper-test-" + daysAgo(0) + ".tar.gz":  true,
		"xhanHelper-test-" + daysAgo(1) + ".tar.gz":  true,
		"xhanHelper-test-" + daysAgo(2) + ".tar.gz":  true,
		"xhanHelper-test-" + daysAgo(10) + ".tar.gz": true,
	}
	if len(kept) != len(wantKept) {
		t.Fatalf("保留 %d 份，want %d：%v", len(kept), len(wantKept), kept)
	}
	for name := range wantKept {
		if !kept[name] {
			t.Errorf("应保留 %s", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, otherEnv)); err != nil {
		t.Errorf("其它 env 的归档不应被删：%v", err)
	}
}

func TestIsDue(t *testing.T) {
	dest := t.TempDir()
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.Local)
	due, err := IsDue(dest, "test", 24*time.Hour, now)
	if err != nil || !due {
		t.Fatalf("空目录应到期：due=%v err=%v", due, err)
	}
	name := "xhanHelper-test-" + now.Add(-2*time.Hour).Format("20060102-150405") + ".tar.gz"
	if err := os.WriteFile(filepath.Join(dest, name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	due, err = IsDue(dest, "test", 24*time.Hour, now)
	if err != nil || due {
		t.Fatalf("2 小时前刚备份，24h 间隔不应到期：due=%v err=%v", due, err)
	}
}

func TestCreateRoundTrip(t *testing.T) {
	root := t.TempDir()
	dest := t.TempDir()

	// 数据根：活库 + 一个应被带上的文件 + 一个应被排除的历史备份
	dbPath := filepath.Join(root, dbFileName)
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO t (v) VALUES ('hello')").Error; err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "history.json"), []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, dbFileName+".pre-migrate-20260101-000000"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "images"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "images", "p.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := New(db)
	ar, err := svc.Create(context.Background(), Options{DataRoot: root, Dest: dest, Env: "test", DailyKeep: 3, WeeklyKeep: 1})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := os.Stat(ar.Path); err != nil {
		t.Fatalf("归档未生成：%v", err)
	}
	if ar.Bytes == 0 {
		t.Error("归档大小应为非零")
	}

	entries := tarEntries(t, ar.Path)
	for _, want := range []string{dbFileName, "history.json", "images/", "images/p.png"} {
		if !entries[want] {
			t.Errorf("归档应含 %q，实际：%v", want, entries)
		}
	}
	if entries[dbFileName+".pre-migrate-20260101-000000"] {
		t.Error("历史备份不应进归档")
	}

	// 快照库可正常打开且行数一致
	snapPath := filepath.Join(t.TempDir(), dbFileName)
	extractEntry(t, ar.Path, dbFileName, snapPath)
	snap, err := gorm.Open(sqlite.Open(snapPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开快照库失败：%v", err)
	}
	var n int64
	if err := snap.Table("t").Count(&n).Error; err != nil || n != 1 {
		t.Fatalf("快照库行数 = %d, want 1 (err=%v)", n, err)
	}
}

func TestCreateRejectsDestInRoot(t *testing.T) {
	root := t.TempDir()
	svc := New(mustDB(t, filepath.Join(root, dbFileName)))
	_, err := svc.Create(context.Background(), Options{DataRoot: root, Dest: filepath.Join(root, "backups"), Env: "test"})
	if err == nil {
		t.Fatal("目标目录在数据根内应被拒绝")
	}
}

// --- helpers ---

func mustDB(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func mustList(t *testing.T, dest, env string) []Archive {
	t.Helper()
	as, err := List(dest, env)
	if err != nil {
		t.Fatal(err)
	}
	return as
}

func tarEntries(t *testing.T, path string) map[string]bool {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	out := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		out[h.Name] = true
	}
	return out
}

func extractEntry(t *testing.T, archive, name, dst string) {
	t.Helper()
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Name != name {
			continue
		}
		out, err := os.Create(dst)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			t.Fatal(err)
		}
		if err := out.Close(); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("归档里没有 %s", name)
}
