// Package backup 本地数据根的备份引擎。
//
// 备份 = 对活动 SQLite 库做一致性快照（VACUUM INTO）+ 打包数据根其余内容为单个 tar.gz。
// 只在本地模式（SQLite）使用；调度由 async.IntervalRunner 驱动（见 main.go）。
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"xhan-helper/internal/config"
)

const (
	archivePrefix = "xhanHelper"
	dbFileName    = "xhan_helper.db"
	weeklyAge     = 7 * 24 * time.Hour

	// 快照重试上限：与后台 worker 抢锁时 VACUUM 可能 SQLITE_BUSY，退避几次。
	snapshotAttempts = 3
	// 单连接 busy_timeout：VACUUM 期间若他人在写，让 SQLite 自己等待而非立刻报错。
	snapshotBusyMS = 10_000
)

// Archive — 一份备份归档的元信息。
type Archive struct {
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	Bytes     int64     `json:"bytes"`
	CreatedAt time.Time `json:"created_at"`
}

// Options — 一次备份的参数（由 config 或 API 构造）。
type Options struct {
	DataRoot     string // 数据根（库 / images / 各 JSON 都在它下面）
	Dest         string // 目标目录（用户选定，可为 iCloud）
	Env          string // prod / dev，进入归档名，避免两环境在同一目录互删
	IntervalDays int
	DailyKeep    int
	WeeklyKeep   int
}

// OptionsFromConfig 从配置构造；dataRoot 取 SQLite 库文件所在目录。
func OptionsFromConfig(cfg *config.Config) Options {
	dataRoot := ""
	if cfg.DB.Driver == "sqlite" {
		dataRoot = filepath.Dir(cfg.DB.Database)
	}
	return Options{
		DataRoot:     dataRoot,
		Dest:         cfg.Backup.Dest,
		Env:          cfg.Server.LocalEnv,
		IntervalDays: cfg.Backup.IntervalDays,
		DailyKeep:    cfg.Backup.DailyKeep,
		WeeklyKeep:   cfg.Backup.WeeklyKeep,
	}
}

func (o Options) normalized() Options {
	if o.IntervalDays <= 0 {
		o.IntervalDays = 1
	}
	if o.DailyKeep <= 0 {
		o.DailyKeep = 3
	}
	if o.WeeklyKeep <= 0 {
		o.WeeklyKeep = 1
	}
	return o
}

func (o Options) validate() error {
	if o.DataRoot == "" {
		return errors.New("数据根未配置")
	}
	if o.Dest == "" {
		return errors.New("备份目标目录未配置")
	}
	if o.Env == "" {
		return errors.New("环境标签未配置")
	}
	if isWithin(o.Dest, o.DataRoot) {
		return errors.New("备份目标目录不能位于数据根内")
	}
	fi, err := os.Stat(o.DataRoot)
	if err != nil || !fi.IsDir() {
		return fmt.Errorf("数据根不可用：%s", o.DataRoot)
	}
	return nil
}

// Service 持有 DB 句柄（快照走同一连接池），并记录最近一次运行用于监控。
type Service struct {
	db *gorm.DB

	mu      sync.Mutex
	runs    int64
	lastRun time.Time
	lastErr string
}

// New 创建备份服务。db 必须非 nil（接线 bug 炸在明处）。
func New(db *gorm.DB) *Service {
	if db == nil {
		panic("[backup] db is nil")
	}
	return &Service{db: db}
}

// Stats 返回监控快照（monitor.Register 用）。
func (s *Service) Stats() any {
	s.mu.Lock()
	defer s.mu.Unlock()
	var last string
	if !s.lastRun.IsZero() {
		last = s.lastRun.Format(time.RFC3339)
	}
	return map[string]any{
		"runs":     s.runs,
		"last_run": last,
		"last_err": s.lastErr,
	}
}

func (s *Service) record(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs++
	s.lastRun = time.Now()
	if err != nil {
		s.lastErr = err.Error()
	} else {
		s.lastErr = ""
	}
}

// RunIfDue — 到期才备份；未到期返回 (nil, nil)。调度器每小时调用。
func (s *Service) RunIfDue(ctx context.Context, opts Options) (*Archive, error) {
	opts = opts.normalized()
	if err := opts.validate(); err != nil {
		return nil, err
	}
	due, err := IsDue(opts.Dest, opts.Env, time.Duration(opts.IntervalDays)*24*time.Hour, time.Now())
	if err != nil {
		return nil, fmt.Errorf("检查备份是否到期：%w", err)
	}
	if !due {
		return nil, nil
	}
	ar, err := s.Create(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &ar, nil
}

// Create — 立即执行一次全量备份。
func (s *Service) Create(ctx context.Context, opts Options) (Archive, error) {
	opts = opts.normalized()
	if err := opts.validate(); err != nil {
		s.record(err)
		return Archive{}, err
	}
	if err := os.MkdirAll(opts.Dest, 0o755); err != nil {
		s.record(err)
		return Archive{}, fmt.Errorf("创建目标目录：%w", err)
	}

	// 1) 本机临时 staging（不进 iCloud，避免同步半成品）
	staging, err := os.MkdirTemp("", "xhan-backup-*")
	if err != nil {
		s.record(err)
		return Archive{}, fmt.Errorf("创建临时目录：%w", err)
	}
	defer os.RemoveAll(staging)

	// 2) 活动库一致性快照 → staging/xhan_helper.db
	if err := s.snapshot(ctx, filepath.Join(staging, dbFileName)); err != nil {
		s.record(err)
		return Archive{}, fmt.Errorf("数据库快照：%w", err)
	}

	// 3) 复制数据根其余内容（排除活库 / 侧车 / 历史备份 / 归档本身）
	if err := copyTree(opts.DataRoot, staging); err != nil {
		s.record(err)
		return Archive{}, fmt.Errorf("复制数据根：%w", err)
	}

	// 4) tar.gz 写到目标目录的 .tmp-，再原子 rename
	name := fmt.Sprintf("%s-%s-%s.tar.gz", archivePrefix, opts.Env, time.Now().Format("20060102-150405"))
	final := filepath.Join(opts.Dest, name)
	tmp := filepath.Join(opts.Dest, ".tmp-"+name)
	if err := tarGzDir(staging, tmp); err != nil {
		os.Remove(tmp)
		s.record(err)
		return Archive{}, fmt.Errorf("打包归档：%w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		s.record(err)
		return Archive{}, fmt.Errorf("落位归档：%w", err)
	}
	var size int64
	if fi, statErr := os.Stat(final); statErr == nil {
		size = fi.Size()
	}
	ar := Archive{Name: name, Path: final, Bytes: size, CreatedAt: time.Now()}

	// 5) 剪枝：留最新 dailyKeep + 1 份 ≥7 天前的；失败不影响本次备份结果
	if err := prune(opts.Dest, opts.Env, opts.DailyKeep, opts.WeeklyKeep, time.Now()); err != nil {
		log.Printf("[backup] 剪枝失败（本次备份已成功）：%v", err)
	}

	s.record(nil)
	log.Printf("[backup] 完成 %s（%.1f MB）", ar.Name, float64(ar.Bytes)/1024/1024)
	return ar, nil
}

// snapshot 用 VACUUM INTO 对活动库取一致快照；同一条连接设 busy_timeout 并退避重试。
func (s *Service) snapshot(ctx context.Context, dst string) error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout=%d", snapshotBusyMS)); err != nil {
		return err
	}
	q := "VACUUM INTO " + quoteSQLiteString(dst)
	var lastErr error
	for attempt := 1; attempt <= snapshotAttempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// VACUUM INTO 要求目标不存在；上一次失败可能留下残file
		_ = os.Remove(dst)
		if _, err := conn.ExecContext(ctx, q); err != nil {
			lastErr = err
			log.Printf("[backup] VACUUM INTO 第 %d/%d 次失败：%v", attempt, snapshotAttempts, err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
			continue
		}
		return nil
	}
	return lastErr
}

// quoteSQLiteString 把路径转成 SQLite 字符串字面量（VACUUM INTO 不支持绑定参数）。
func quoteSQLiteString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// copyTree 把 root 下除排除项外的所有文件复制进 staging（顶层镜像）。
func copyTree(root, staging string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if excluded(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		dst := filepath.Join(staging, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		return copyFile(path, dst)
	})
}

// excluded 判断相对路径是否应排除出归档。只看基名，够用（根内没有同名嵌套需求）。
func excluded(rel string) bool {
	base := filepath.Base(rel)
	switch {
	case base == dbFileName: // 活库（用快照替代）
		return true
	case strings.HasPrefix(base, dbFileName+"-"): // -journal / -wal / -shm
		return true
	case strings.HasPrefix(base, dbFileName+".pre-"): // pre-migrate / pre-restore
		return true
	case strings.HasPrefix(base, ".tmp-"): // 半成品归档
		return true
	case strings.HasPrefix(base, archivePrefix+"-") && strings.HasSuffix(base, ".tar.gz"): // 归档本身
		return true
	}
	return false
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// tarGzDir 把 srcDir 的内容（顶层镜像）打包到 dst 文件。
func tarGzDir(srcDir, dst string) error {
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	walkErr := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == srcDir {
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if d.IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		_, cpErr := io.Copy(tw, in)
		in.Close()
		return cpErr
	})

	if err := tw.Close(); err != nil && walkErr == nil {
		walkErr = err
	}
	if err := gz.Close(); err != nil && walkErr == nil {
		walkErr = err
	}
	if err := f.Close(); err != nil && walkErr == nil {
		walkErr = err
	}
	return walkErr
}

// List 返回目标目录里本 env 的归档，按时间倒序。
func List(dest, env string) ([]Archive, error) {
	entries, err := os.ReadDir(dest)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	prefix := archivePrefix + "-" + env + "-"
	var out []Archive
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".tar.gz") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, Archive{
			Name:      name,
			Path:      filepath.Join(dest, name),
			Bytes:     info.Size(),
			CreatedAt: parseStamp(name, info.ModTime()),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// parseStamp 从 xhanHelper-<env>-YYYYMMDD-HHMMSS.tar.gz 解析时间；失败回退 mtime。
func parseStamp(name string, fallback time.Time) time.Time {
	base := strings.TrimSuffix(name, ".tar.gz")
	parts := strings.Split(base, "-")
	if len(parts) < 3 {
		return fallback
	}
	stamp := parts[len(parts)-2] + "-" + parts[len(parts)-1]
	t, err := time.ParseInLocation("20060102-150405", stamp, time.Local)
	if err != nil {
		return fallback
	}
	return t
}

// IsDue 最新归档是否已超过 interval（无归档即到期）。
func IsDue(dest, env string, interval time.Duration, now time.Time) (bool, error) {
	archives, err := List(dest, env)
	if err != nil {
		return false, err
	}
	if len(archives) == 0 {
		return true, nil
	}
	return now.Sub(archives[0].CreatedAt) >= interval, nil
}

// prune 保留最新 dailyKeep 份 + 从其余里挑 weeklyKeep 份「≥7 天前」的，删除其余。
func prune(dest, env string, dailyKeep, weeklyKeep int, now time.Time) error {
	archives, err := List(dest, env)
	if err != nil {
		return err
	}
	keep := make(map[string]bool, len(archives))
	for i, a := range archives {
		if i < dailyKeep {
			keep[a.Name] = true
		}
	}
	weekly := 0
	for i, a := range archives {
		if i < dailyKeep || weekly >= weeklyKeep {
			continue
		}
		if now.Sub(a.CreatedAt) >= weeklyAge {
			keep[a.Name] = true
			weekly++
		}
	}
	for _, a := range archives {
		if keep[a.Name] {
			continue
		}
		if err := os.Remove(a.Path); err != nil && !os.IsNotExist(err) {
			log.Printf("[backup] 剪枝删除 %s 失败：%v", a.Name, err)
		}
	}
	// 顺手清掉本 env 的半成品
	if matches, err := filepath.Glob(filepath.Join(dest, ".tmp-"+archivePrefix+"-"+env+"-*")); err == nil {
		for _, m := range matches {
			if err := os.Remove(m); err != nil && !os.IsNotExist(err) {
				log.Printf("[backup] 清理临时归档 %s 失败：%v", m, err)
			}
		}
	}
	return nil
}

// isWithin 报告 p 是否等于 base 或位于 base 之内（用于禁止把备份写进数据根）。
func isWithin(p, base string) bool {
	p = filepath.Clean(p)
	base = filepath.Clean(base)
	if p == base {
		return true
	}
	return strings.HasPrefix(p+string(os.PathSeparator), base+string(os.PathSeparator))
}
