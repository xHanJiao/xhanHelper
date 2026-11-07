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
	"xhan-helper/internal/util/imgshrink"
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

// ImageIndexer — 图片描述的向量索引（语义搜索用）。
// 可选依赖（nil 时不建索引，语义搜索会缺结果）；由 agent 层实现，避免 service 依赖具体 LLM 厂商。
type ImageIndexer interface {
	IndexImage(ctx context.Context, userID, imageID, description string) error
	RemoveImages(ctx context.Context, imageIDs []string) error
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
	indexer   ImageIndexer   // 可为 nil：未接线时不建描述向量

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

// SetIndexer 注入描述向量索引器（启动装配调用一次）。i 为 nil 时不建索引。
func (s *ImageStore) SetIndexer(i ImageIndexer) { s.indexer = i }

// FilePath 返回记录的磁盘绝对/相对路径。id 来自 DB，不含用户输入，天然免疫目录穿越。
func (s *ImageStore) FilePath(a *model.ImageAsset) string {
	return filepath.Join(s.dir, a.ID+"."+a.Ext)
}

// HasFile — 记录在、磁盘文件还在。给 list_images 标「死图」用：导库只搬 DB、
// 换机、外部删除都会留下这种记录；不标出来，模型会挑中它再在 create 里整笔失败。
func (s *ImageStore) HasFile(a *model.ImageAsset) bool {
	_, err := os.Stat(s.FilePath(a))
	return err == nil
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
	existing, err := s.repo.FindBySHA(ctx, userID, digest)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("lookup image by sha: %w", err)
	}
	if existing != nil {
		// 记录在、文件没了（导库不带 images / 换机 / 外部删除）：用这次上传的字节补回去。
		// 不补的话 DataURI 会一直报「图片文件已丢失」，而且每次重传都命中这条坏记录。
		path := s.FilePath(existing)
		if _, statErr := os.Stat(path); statErr != nil {
			if err := os.MkdirAll(s.dir, 0o755); err != nil {
				return nil, fmt.Errorf("create image dir: %w", err)
			}
			if err := os.WriteFile(path, data, 0o644); err != nil {
				return nil, fmt.Errorf("restore image file: %w", err)
			}
			log.Printf("[image] restored missing file for %s", shortID(existing.ID))
		}
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
	// 描述已落库；向量索引失败只影响语义搜索（下次启动 warmup 会补），不打断描述生成。
	// 单独给短超时：describe 的 90s 预算主要留给识图，识图慢时别把 embedding 饿死。
	if s.indexer != nil {
		idxCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		err := s.indexer.IndexImage(idxCtx, a.UserID, a.ID, desc)
		cancel()
		if err != nil {
			log.Printf("[image] index description %s failed: %v", a.ID[:8], err)
		}
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

// 内联压缩参数 — 产物页按 760px 宽设计、右栏最宽 1200，Retina 到顶也就 ~2400 物理像素，
// 1600 够看；q85 在照片上看不出差别，体积通常只有原图的 1/8（见 docs/design/html-artifact §4.3）。
//
// 为什么要压：原图动辄 4000×3000 / 3–5MB，再乘 1.33 倍 base64 膨胀，几张就顶满整页上限。
const (
	InlineMaxEdge     = 1600
	InlineJPEGQuality = 85
)

// shortcut: 缓存只按 id 命名、没有版本键 —— 改 InlineMaxEdge / InlineJPEGQuality 之后
// 老缓存仍会吐旧尺寸旧画质，必须手删 images/inline/ 才生效；要长期调参再加参数进文件名。
func (s *ImageStore) inlineCacheDir() string { return filepath.Join(s.dir, "inline") }

// inlineCache 找已压好的内联副本（jpg 优先，其次 png）。没有就返回 ok=false。
func (s *ImageStore) inlineCache(id string) (path, mime string, ok bool) {
	for _, c := range []struct{ ext, mime string }{{"jpg", "image/jpeg"}, {"png", "image/png"}} {
		p := filepath.Join(s.inlineCacheDir(), id+"."+c.ext)
		if fi, err := os.Stat(p); err == nil && fi.Size() > 0 {
			return p, c.mime, true
		}
	}
	return "", "", false
}

// removeInlineCache — 原图删除/清理时一并删掉压缩副本（否则 inline/ 会越攒越多）。
func (s *ImageStore) removeInlineCache(id string) {
	for _, ext := range []string{"jpg", "png"} {
		if err := os.Remove(filepath.Join(s.inlineCacheDir(), id+"."+ext)); err != nil && !os.IsNotExist(err) {
			log.Printf("[image] remove inline cache %s failed: %v", shortID(id), err)
		}
	}
}

// writeInlineCache — 原子落盘（临时文件 + rename）：读者要么看不到，要么看到完整文件。
func (s *ImageStore) writeInlineCache(id, mime string, data []byte) error {
	ext := "jpg"
	if mime == "image/png" {
		ext = "png"
	}
	dir := s.inlineCacheDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create inline cache dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, id+".*.tmp")
	if err != nil {
		return fmt.Errorf("create inline temp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write inline temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close inline temp: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(dir, id+"."+ext)); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename inline cache: %w", err)
	}
	return nil
}

func dataURI(mime string, data []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// DataURI — 把用户的图片读成 data: URI，供 HTML 产物内联。
//
// 必须带 owner：产物页只允许嵌自己的图。文件缺失（被清理）按错误返回，
// 让调用方整页失败，而不是静默少一张用户点名要的图。
//
// 内联前**先压一遍**（imgshrink）：原图直接 base64 会让整页秒顶上限，且解码渲染都慢。
// 压缩结果按 id 落盘缓存，同一张图第二次内联直接读。
func (s *ImageStore) DataURI(ctx context.Context, userID, id string) (string, error) {
	a, err := s.repo.GetByIDAndUser(ctx, id, userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", errors.New("图片不存在或已被清理")
		}
		return "", fmt.Errorf("查图片记录: %w", err)
	}

	if p, mime, ok := s.inlineCache(a.ID); ok {
		data, err := os.ReadFile(p)
		if err == nil {
			return dataURI(mime, data), nil
		}
		// 缓存坏了不影响正确性：记一笔，往下重新压
		log.Printf("[image] read inline cache %s failed: %v", shortID(a.ID), err)
	}

	src, err := os.ReadFile(s.FilePath(a))
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.New("图片文件已丢失（可能被清理）")
		}
		return "", fmt.Errorf("读图片文件: %w", err)
	}
	out, mime, err := imgshrink.Shrink(src, InlineMaxEdge, InlineJPEGQuality)
	if err != nil {
		if errors.Is(err, imgshrink.ErrTooLarge) {
			return "", fmt.Errorf("%w，换一张小一点的图", err)
		}
		return "", err
	}
	if err := s.writeInlineCache(a.ID, mime, out); err != nil {
		// 写不进缓存不影响本次结果，下次再压一遍而已
		log.Printf("[image] write inline cache %s failed: %v", shortID(a.ID), err)
	}
	return dataURI(mime, out), nil
}

// shortID — 日志里只打 id 前缀（id 是 uuid，取 8 位够定位）。
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
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
	s.removeInlineCache(a.ID)
	if err := s.repo.Delete(ctx, a.ID, a.UserID); err != nil {
		return fmt.Errorf("delete image record: %w", err)
	}
	s.removeVectors(ctx, []string{a.ID})
	return nil
}

// removeVectors 清理描述向量。best-effort：搜索结果最终以 DB 为准，
// 向量删不掉最多留孤儿，不会让已删除的图被搜出来。
func (s *ImageStore) removeVectors(ctx context.Context, ids []string) {
	if s.indexer == nil || len(ids) == 0 {
		return
	}
	if err := s.indexer.RemoveImages(ctx, ids); err != nil {
		log.Printf("[image] remove vectors (%d ids) failed: %v", len(ids), err)
	}
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
			// 部分删除时不动向量：分不清哪几条还在，留给下次对账/重启 warmup。
			if int(n) == len(missing) {
				s.removeVectors(ctx, missing)
			}
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
				log.Printf("[image_cleanup] remove file %s failed: %v", shortID(a.ID), err)
			}
			s.removeInlineCache(a.ID)
			ids = append(ids, a.ID)
		}
		n, err := s.repo.DeleteBatch(ctx, ids)
		if err != nil {
			return total, fmt.Errorf("delete image records: %w", err)
		}
		total += int(n)
		if int(n) == len(ids) {
			s.removeVectors(ctx, ids)
		}
		// 删除没生效时必须退出，否则下一轮会取到同一批（死循环）
		if int(n) < len(ids) {
			log.Printf("[image_cleanup] delete affected %d of %d rows, stopping to avoid a loop", n, len(ids))
			return total, nil
		}
	}
}
