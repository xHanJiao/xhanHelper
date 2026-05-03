package model

import "time"

// Category1List 一级分类枚举
var Category1List = []string{"餐饮", "服装", "交通", "居住", "旅游", "美容", "人情", "日用", "数码", "学习", "医疗", "娱乐"}

// Category2List 二级分类枚举
var Category2List = []string{"保险", "餐饮", "厨房用具", "床上用品", "打车", "地铁", "电影", "订阅", "房租", "服务费", "服装", "机票", "纪念品", "驾照培训", "交通", "聚会", "咖啡奶茶", "老家", "零食", "买菜", "买药", "女性日用", "朋友", "啤酒", "漂染头发", "日用", "日用耗材", "日用小家电", "社会", "生活用品", "市内短途", "数码配件", "数码设备", "水电费", "堂食", "堂食（伴侣）", "堂食（请客）", "体检", "外卖", "玩具", "箱包", "休闲", "医疗", "医疗器械", "饮料", "饮用水", "长途（火车）", "住宿"}

// ConsumeTypeList 消费类型枚举
var ConsumeTypeList = []string{"长期", "一次性", "周期"}

// PayTypeList 支付方式枚举
var PayTypeList = []string{"现金", "公积金"}

// 账单状态
const (
	BillStatusNormal = 1
	BillStatusCancel = 2
)

// Bill — 账单（对齐 bill-assistant-server）
type Bill struct {
	ID          string    `gorm:"primaryKey;size:36" json:"id"`
	UserID    string    `gorm:"size:36;not null;index" json:"user_id"`
	Item        string    `gorm:"size:100;not null" json:"item"`
	Amount      float64   `gorm:"type:decimal(10,2);not null" json:"amount"`
	Category1   string    `gorm:"size:50;not null" json:"category1"`
	Category2   string    `gorm:"size:50;not null" json:"category2"`
	ConsumeType string    `gorm:"size:20;not null" json:"consume_type"` // 长期/一次性/周期
	Date        string    `gorm:"size:20;not null" json:"date"`         // YYYY-MM-DD
	PayType     string    `gorm:"size:20;not null" json:"pay_type"`     // 现金/公积金
	Owner       string    `gorm:"size:50;not null" json:"owner"`
	Remark      string    `gorm:"size:200" json:"remark"`
	Status      int       `gorm:"default:1" json:"status"` // 1=正常 2=已作废
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
