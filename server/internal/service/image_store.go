package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// 图片魔数白名单：mime → 扩展名。只认这四种，svg/可执行/HTML 一律拒绝（防脚本注入）。
var imageExtByMime = map[string]string{
	"image/png":  "png",
	"image/jpeg": "jpg",
	"image/gif":  "gif",
	"image/webp": "webp",
}

var (
	// ErrImageTooLarge 超出单图大小上限
	ErrImageTooLarge = errors.New("图片超出大小上限")
	// ErrImageUnsupported 不是受支持的图片类型（魔数校验失败）
	ErrImageUnsupported = errors.New("不支持的图片类型（仅 png/jpeg/webp/gif）")
)

// FileUploader — 把图片字节上传到 LLM 侧，返回其文件 ID（DeepSeek Files API 的 file_id）。
// 抽象成接口是为了让 service 不依赖具体 LLM 厂商，也便于测试。
type FileUploader interface {
	Upload(ctx context.Context, filename string, data []byte) (string, error)
}

// ImageDescriber — 把 LLM 侧的 file_id 转成一句简短文字描述。
// 可选依赖（nil 时跳过描述生成）；由 agent 层实现，避免 service 依赖具体 LLM 厂商。
type ImageDescriber interface {
	Describe(ctx context.Context, fileID string) (string, error)
}

// imageDescribeConcurrency 同时进行的识图外呼上限（单次最长 90s，批量上传时避免打爆）。
const imageDescribeConcurrency = 3

// ImageStore — 图床：磁盘文件 + ImageAsset 记录的唯一入口。
// 文档配图与聊天图片输入共用；读取策略由 ImageAsset.Visibility 决定。
type ImageStore struct {
	dir       string
	maxBytes  int64
	repo      *ImageAssetRepo
	uploader  FileUploader   // 可为 nil：未配置时图片无法送模型，但不影响存取
	describer ImageDescriber // 可为 nil：未接线时不生成描述

	descMu     sync.Mutex
	describing map[string]bool // 在途去重：同一张图不并发识图
	descSem    chan struct{}   // 识图并发信号量
}

// NewImageStore 创建图床。repo 为 nil 时 panic（接线 bug 炸在明处）。
func NewImageStore(dir string, maxBytes int64, repo *ImageAssetRepo, uploader FileUploader) *ImageStore {
	if repo == nil {
		panic("image_store: nil repo (wiring bug)")
	}
	if dir == "" {
		panic("image_store: empty image_dir")
	}
	return &ImageStore{
		dir:        dir,
		maxBytes:   maxBytes,
		repo:       repo,
		uploader:   uploader,
		describing: make(map[string]bool),
		descSem:    make(chan struct{}, imageDescribeConcurrency),
	}
}

// Dir 返回图片根目录（启动时创建 + 打日志用）
func (s *ImageStore) Dir() string { return s.dir }

// MaxBytes 返回单图大小上限（上传接口据此限制请求体大小）
func (s *ImageStore) MaxBytes() int64 { return s.maxBytes }

// Repo 暴露底层 repo，供控制器做查询/鉴权校验
func (s *ImageStore) Repo() *ImageAssetRepo { return s.repo }

// SetDescriber 注入描述生成器（启动装配调用一次）。d 为 nil 时表示不生成描述。
func (s *ImageStore) SetDescriber(d ImageDescriber) { s.describer = d }

// FilePath 返回记录的磁盘绝对/相对路径。id 来自 DB，不含用户输入，天然免疫目录穿越。
func (s *ImageStore) FilePath(a *model.ImageAsset) string {
	return filepath.Join(s.dir, a.ID+"."+a.Ext)
}

// Upload 落盘 + 建记录。同用户内容相同则直接复用已有记录（去重）。
func (s *ImageStore) Upload(ctx context.Context, userID, source, visibility string, data []byte) (*model.ImageAsset, error) {
	if int64(len(data)) > s.maxBytes {
		return nil, fmt.Errorf("%w: %d > %d", ErrImageTooLarge, len(data), s.maxBytes)
	}
	mime := http.DetectContentType(data)
	ext, ok := imageExtByMime[mime]
	if !ok {
		return nil, fmt.Errorf("%w: detected %s", ErrImageUnsupported, mime)
	}

	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	if existing, err := s.repo.FindBySHA(ctx, userID, digest); err == nil && existing != nil {
		// 老图（本功能上线前入库）没有描述，补齐一次
		if existing.DescriptionAt == nil {
			s.describeAsync(*existing)
		}
		return existing, nil
	}

	id := uuid.NewString()
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return nil, fmt.Errorf("create image dir: %w", err)
	}
	// 直接写即可：文件只在 DB 记录建立之后才可达，读者不会看到半截文件
	if err := os.WriteFile(filepath.Join(s.dir, id+"."+ext), data, 0o644); err != nil {
		return nil, fmt.Errorf("write image file: %w", err)
	}

	asset := &model.ImageAsset{
		ID:         id,
		UserID:     userID,
		Ext:        ext,
		Mime:       mime,
		Bytes:      int64(len(data)),
		SHA256:     digest,
		Visibility: visibility,
		Source:     source,
	}
	if err := s.repo.Create(ctx, asset); err != nil {
		_ = os.Remove(filepath.Join(s.dir, id+"."+ext)) // 记录失败则回滚文件，避免孤儿
		return nil, fmt.Errorf("create image record: %w", err)
	}
	s.describeAsync(*asset) // 传值拷贝：后台不要读/写调用方持有的同一指针
	return asset, nil
}

// describeAsync 上传后异步生成描述。best-effort：失败只记日志，不阻塞上传。
// 同一张图在途时跳过（去重路径可能对同一 id 重复调用）；外呼并发受信号量限制。
func (s *ImageStore) describeAsync(asset model.ImageAsset) {
	if s.describer == nil {
		return
	}
	s.descMu.Lock()
	if s.describing[asset.ID] {
		s.descMu.Unlock()
		return
	}
	s.describing[asset.ID] = true
	s.descMu.Unlock()

	go func() {
		defer func() {
			s.descMu.Lock()
			delete(s.describing, asset.ID)
			s.descMu.Unlock()
		}()
		s.descSem <- struct{}{}
		defer func() { <-s.descSem }()

		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if err := s.describe(ctx, &asset); err != nil {
			log.Printf("[image] describe %s failed: %v", asset.ID[:8], err)
		}
	}()
}

// describe 生成并回写描述；失败时记录一次尝试时间，避免无限重试。
func (s *ImageStore) describe(ctx context.Context, a *model.ImageAsset) error {
	fileID, err := s.EnsureFileID(ctx, a)
	if err != nil {
		s.noteDescribed(a.ID)
		return fmt.Errorf("ensure file id: %w", err)
	}
	desc, err := s.describer.Describe(ctx, fileID)
	if err != nil {
		s.noteDescribed(a.ID)
		return fmt.Errorf("describe: %w", err)
	}
	if err := s.repo.SetDescription(ctx, a.ID, desc); err != nil {
		s.noteDescribed(a.ID) // 写库失败也标记一次，避免持久故障下反复识图
		return fmt.Errorf("save description: %w", err)
	}
	log.Printf("[image] described %s (user=%s)", a.ID[:8], a.UserID)
	return nil
}

// noteDescribed 记录一次失败尝试（写入失败不影响已完成的失败判定）。
func (s *ImageStore) noteDescribed(id string) {
	if err := s.repo.MarkDescriptionAttempted(context.Background(), id); err != nil {
		log.Printf("[image] mark description attempted %s failed: %v", id[:8], err)
	}
}

// Open 打开图片文件（供私有/公开读取）
func (s *ImageStore) Open(a *model.ImageAsset) (*os.File, error) {
	return os.Open(s.FilePath(a))
}

// InlineMaxBytes — 单张图内联进 HTML 产物时的原字节上限。
//
// 不做压缩：压缩要引 golang.org/x/image 并处理 EXIF 方向（iPhone 竖拍会横躺），
// v1 宁可对超大图报错（docs/design/html-artifact §4.3）。
const InlineMaxBytes int64 = 4 << 20

// DataURI — 把用户的图片读成 data: URI，供 HTML 产物内联。
// 必须带 owner：产物页只允许嵌自己的图。文件缺失（被清理）按错误返回，
// 让调用方整页失败，而不是静默少一张用户点名要的图。
func (s *ImageStore) DataURI(ctx context.Context, userID, id string) (string, error) {
	a, err := s.repo.GetByIDAndUser(ctx, id, userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", errors.New("图片不存在或已被清理")
		}
		return "", fmt.Errorf("查图片记录: %w", err)
	}
	// 先 stat 再看大小：上传上限 32MB，为判 4MB 把整个文件读进内存不值。
	path := s.FilePath(a)
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.New("图片文件已丢失（可能被清理）")
		}
		return "", fmt.Errorf("查图片文件: %w", err)
	}
	if fi.Size() > InlineMaxBytes {
		return "", fmt.Errorf("图片太大（%d 字节，上限 %d），换一张小点的图", fi.Size(), InlineMaxBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读图片文件: %w", err)
	}
	mime := a.Mime
	if mime == "" {
		mime = http.DetectContentType(data)
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

// EnsureFileID 确保图片在 LLM 侧有 file_id（缺失则懒上传并回填）。
// 上传过或上传成功都返回可用的 file_id；不可用时返回 error，调用方应跳过该图而非整体失败。
func (s *ImageStore) EnsureFileID(ctx context.Context, a *model.ImageAsset) (string, error) {
	if a.DeepSeekFileID != "" {
		return a.DeepSeekFileID, nil
	}
	if s.uploader == nil {
		return "", errors.New("未配置图片上传能力（LLM provider 不支持）")
	}
	data, err := os.ReadFile(s.FilePath(a))
	if err != nil {
		return "", fmt.Errorf("read image file: %w", err)
	}
	fileID, err := s.uploader.Upload(ctx, a.ID+"."+a.Ext, data)
	if err != nil {
		return "", fmt.Errorf("upload to llm: %w", err)
	}
	if err := s.repo.SetDeepSeekFileID(ctx, a.ID, fileID); err != nil {
		// 回填失败不影响本次使用，只是下次会重复上传
		log.Printf("[image] backfill file_id for %s failed: %v", a.ID[:8], err)
	}
	a.DeepSeekFileID = fileID
	return fileID, nil
}

// Delete 删除磁盘文件 + 记录。文件不存在不算错（幂等）。
func (s *ImageStore) Delete(ctx context.Context, a *model.ImageAsset) error {
	if err := os.Remove(s.FilePath(a)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove image file: %w", err)
	}
	return s.repo.Delete(ctx, a.ID, a.UserID)
}

// ImageCleanupStats — 一次清理的结果
type ImageCleanupStats struct {
	Referenced int
	Orphans    int
	Missing    int // 记录还在但磁盘文件已丢失（迁移/外部删除遗留）
}

// Cleanup 按保留期清理过期图片：
//   - 被消息引用过的：last_used_at 早于 ttlDays 天
//   - 从未被引用的孤儿：created_at 早于 orphanDays 天
//   - 记录在、磁盘文件已丢失的：无条件删记录（脏数据自愈）
//
// source=doc 的文档配图一律豁免（文档是"作品"，配图不能被 TTL 删；文档删除后也不回收，见设计文档）。
// ttlDays/orphanDays < 0 表示该类过期清理关闭（自愈恒开）。分批处理，失败只记日志不中断。
func (s *ImageStore) Cleanup(ctx context.Context, ttlDays, orphanDays int) (ImageCleanupStats, error) {
	var stats ImageCleanupStats
	now := time.Now()

	// 目录整体不可用（未挂载 / 被改名 / 换机只搬 DB）时直接放弃本次清理：
	// 否则逐文件 os.Stat 一律 ENOENT，会把全部记录（含 referenced）误判为 missing 删光。
	fi, err := os.Stat(s.dir)
	if err != nil {
		return stats, fmt.Errorf("image dir unavailable, skip cleanup: %w", err)
	}
	if !fi.IsDir() {
		return stats, fmt.Errorf("image dir not a directory, skip cleanup: %s", s.dir)
	}

	if orphanDays >= 0 {
		cutoff := now.AddDate(0, 0, -orphanDays)
		n, err := s.purge(ctx, cutoff, s.repo.ListStaleOrphans)
		stats.Orphans = n
		if err != nil {
			return stats, err
		}
	}
	if ttlDays >= 0 {
		cutoff := now.AddDate(0, 0, -ttlDays)
		n, err := s.purge(ctx, cutoff, s.repo.ListStaleReferenced)
		stats.Referenced = n
		if err != nil {
			return stats, err
		}
	}
	// 脏数据自愈：记录在、文件没了（迁移/外部删除遗留）一律删记录，不分 referenced。
	// 无条件执行：这类记录留着只会让前端反复 404。
	n, err := s.purgeMissingFiles(ctx)
	stats.Missing = n
	if err != nil {
		return stats, err
	}
	return stats, nil
}

// purgeMissingFiles 分页扫描全部记录，删掉磁盘文件已不存在的那些（返回删除的记录数）。
// 键集分页（id 游标）避免边扫边删时跳行。
func (s *ImageStore) purgeMissingFiles(ctx context.Context) (int, error) {
	const batch = 500
	const sampleMax = 5
	total := 0
	cursor := ""
	var sample []string
	defer func() {
		if total > 0 {
			log.Printf("[image_cleanup] purged %d broken records (file missing), sample=%v", total, sample)
		}
	}()
	for {
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
		rows, err := s.repo.ListBatchAfter(ctx, cursor, batch)
		if err != nil {
			return total, fmt.Errorf("list image batch: %w", err)
		}
		if len(rows) == 0 {
			return total, nil
		}
		var missing []string
		for i := range rows {
			a := &rows[i]
			cursor = a.ID
			if _, statErr := os.Stat(s.FilePath(a)); os.IsNotExist(statErr) {
				missing = append(missing, a.ID)
				if len(sample) < sampleMax {
					sample = append(sample, a.ID)
				}
			} else if statErr != nil {
				// 权限/IO/网络盘未挂等：保守跳过，绝不误删有效记录
				log.Printf("[image_cleanup] stat %s failed, skip: %v", a.ID[:8], statErr)
			}
		}
		if len(missing) > 0 {
			n, err := s.repo.DeleteBatch(ctx, missing)
			if err != nil {
				return total, fmt.Errorf("delete broken image records: %w", err)
			}
			total += int(n)
		}
	}
}

type staleLister func(ctx context.Context, cutoff time.Time, limit int) ([]model.ImageAsset, error)

// purge 分批拉取 + 删除，避免长事务与一次性大查询
func (s *ImageStore) purge(ctx context.Context, cutoff time.Time, list staleLister) (int, error) {
	const batch = 200
	total := 0
	for {
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
		rows, err := list(ctx, cutoff, batch)
		if err != nil {
			return total, fmt.Errorf("list stale images: %w", err)
		}
		if len(rows) == 0 {
			return total, nil
		}
		ids := make([]string, 0, len(rows))
		for i := range rows {
			a := &rows[i]
			if err := os.Remove(s.FilePath(a)); err != nil && !os.IsNotExist(err) {
				// 文件删不掉不阻断记录清理：留着记录只会反复重试，反而更糟
				log.Printf("[image_cleanup] remove file %s failed: %v", a.ID[:8], err)
			}
			ids = append(ids, a.ID)
		}
		n, err := s.repo.DeleteBatch(ctx, ids)
		if err != nil {
			return total, fmt.Errorf("delete image records: %w", err)
		}
		total += int(n)
		// 删除没生效时必须退出，否则下一轮会取到同一批（死循环）
		if int(n) < len(ids) {
			log.Printf("[image_cleanup] delete affected %d of %d rows, stopping to avoid a loop", n, len(ids))
			return total, nil
		}
	}
}
