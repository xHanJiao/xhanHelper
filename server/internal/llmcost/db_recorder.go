package llmcost

import (
	"context"
	"encoding/json"
	"log"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// DBRecorder 基于 channel 的异步 DB Recorder。
// 单 goroutine worker 消费，顺序写入，避免并发写竞争。
type DBRecorder struct {
	repo *service.LlmCostRepo
	ch   chan UsageRecord
}

// NewDBRecorder 创建 DBRecorder 并启动后台 worker。
func NewDBRecorder(repo *service.LlmCostRepo) *DBRecorder {
	r := &DBRecorder{
		repo: repo,
		ch:   make(chan UsageRecord, 1024),
	}
	go r.worker()
	return r
}

// Record 非阻塞投递记录。channel 满时丢弃并打 log。
func (r *DBRecorder) Record(record UsageRecord) {
	select {
	case r.ch <- record:
	default:
		log.Printf("[llmcost] record channel full, dropping: cat=%s sub=%s model=%s",
			record.Category, record.SubCategory, record.Model)
	}
}

// Close 关闭 channel，停止 worker（调用方负责等待 in-flight 写入完成）。
func (r *DBRecorder) Close() {
	close(r.ch)
}

// worker 消费 channel 并写入 DB。
func (r *DBRecorder) worker() {
	for record := range r.ch {
		detail := &model.LlmCostDetail{
			Date:                  record.Date.Format("2006-01-02"),
			UserID:                record.UserID,
			Category:              record.Category,
			SubCategory:           record.SubCategory,
			Model:                 record.Model,
			ConvID:                record.ConvID,
			RefType:               record.RefType,
			RefID:                 record.RefID,
			PromptTokens:          int64(record.PromptTokens),
			PromptCacheHitTokens:  int64(record.PromptCacheHitTokens),
			PromptCacheMissTokens: int64(record.PromptCacheMissTokens),
			CompletionTokens:      int64(record.CompletionTokens),
			TotalTokens:           int64(record.TotalTokens),
		}
		if record.Meta != nil {
			b, err := json.Marshal(record.Meta)
			if err != nil {
				log.Printf("[llmcost] marshal meta error: %v", err)
			} else {
				detail.MetaJSON = string(b)
			}
		}
		if err := r.repo.Insert(context.Background(), detail); err != nil {
			log.Printf("[llmcost] insert error: %v", err)
		}
	}
}
