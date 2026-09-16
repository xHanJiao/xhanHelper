package model

import "time"

// 图片可见性
const (
	// ImageVisibilityPrivate — 私有：读取需鉴权（聊天图片输入默认）
	ImageVisibilityPrivate = "private"
	// ImageVisibilityPublic — 公开：可无鉴权寻址（文档配图，用于分享页/PDF 导出）
	ImageVisibilityPublic = "public"
)

// 图片来源
const (
	ImageSourceChat  = "chat"  // 用户在输入框选/传的图
	ImageSourceDoc   = "doc"   // 文档配图（图表渲染 / 搬运图）
	ImageSourceLocal = "local" // agent 读本地文件发现
	ImageSourceWeb   = "web"   // agent 浏览网页发现
)

// ImageAsset — 图片资源（图床记录）。
// 文件落盘在 storage.image_dir/<id>.<ext>，id 即 UUID 主干，本表是它的唯一事实来源。
// 文档配图与聊天图片输入共用此表，靠 Visibility 区分读取策略。
type ImageAsset struct {
	ID         string `gorm:"primaryKey;size:36" json:"id"`
	UserID     string `gorm:"size:36;not null;index" json:"user_id"`
	Ext        string `gorm:"size:10;not null" json:"ext"`  // png | jpg | gif | webp
	Mime       string `gorm:"size:50;not null" json:"mime"` // image/png ...
	Bytes      int64  `gorm:"not null" json:"bytes"`
	SHA256     string `gorm:"size:64;index" json:"sha256"` // 同用户内去重
	Visibility string `gorm:"size:10;not null;default:private" json:"visibility"`
	Source     string `gorm:"size:10;not null" json:"source"`
	// DeepSeek Files API 的 file_id（多模态输入用）。空 = 尚未上传，用时懒补。
	// 官方无下载/删除接口，因此本地副本必须保留。
	DeepSeekFileID string `gorm:"size:100" json:"deepseek_file_id,omitempty"`
	// Referenced — 是否曾被任何消息引用过，决定走哪条保留期（见 StorageConfig）
	Referenced bool `gorm:"not null;default:false;index:idx_image_cleanup,priority:1;index:idx_image_orphan,priority:1" json:"referenced"`
	// LastUsedAt 被挂到消息时刷新。必须是指针：零值会被序列化成 '0000-00-00'，
	// MySQL 严格模式（NO_ZERO_DATE）直接拒绝 INSERT（Error 1292），NULL 才表达"从未被引用过"。
	LastUsedAt *time.Time `gorm:"index:idx_image_cleanup,priority:2" json:"last_used_at"`
	CreatedAt  time.Time  `gorm:"index:idx_image_orphan,priority:2" json:"created_at"`
}
