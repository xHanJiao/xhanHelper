package agent

import (
	"context"
	"encoding/json"
	"log"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// SetImageIDs 把图片 id 列表以 JSON 数组写入消息（空列表不写，保持列为 NULL）。
func SetImageIDs(m *model.Message, ids []string) {
	if len(ids) == 0 {
		return
	}
	b, err := json.Marshal(ids)
	if err != nil {
		log.Printf("[image] marshal image ids for msg %s failed: %v", m.ID[:8], err)
		return
	}
	s := string(b)
	m.ImageIDs = &s
}

// GetImageIDs 从消息读出图片 id 列表，未设置/解析失败返回 nil。
func GetImageIDs(m *model.Message) []string {
	if m.ImageIDs == nil || *m.ImageIDs == "" {
		return nil
	}
	var ids []string
	if err := json.Unmarshal([]byte(*m.ImageIDs), &ids); err != nil {
		log.Printf("[image] unmarshal image ids for msg %s failed: %v", m.ID[:8], err)
		return nil
	}
	return ids
}

// AttachReplayImages 给最近 rounds 条带图 user 消息挂上图片（Files API file_id）。
//
// 只重放最近若干轮：图片 token 不复用文本前缀缓存，全量重放每轮都白烧钱，
// 且更早的图与当前对话通常已无关。
//
// 任何一张图出错（记录被清理、文件缺失、上传失败）都只跳过它，不让整轮对话失败。
func AttachReplayImages(ctx context.Context, store *service.ImageStore, msgs []llm.Message, userID string, rounds int) {
	if store == nil || rounds <= 0 {
		return
	}

	// 从后往前挑出窗口内需要挂图的 user 消息
	targets := make([]int, 0, rounds)
	for i := len(msgs) - 1; i >= 0 && len(targets) < rounds; i-- {
		if msgs[i].Role == "user" && len(msgs[i].ImageIDs) > 0 {
			targets = append(targets, i)
		}
	}
	if len(targets) == 0 {
		return
	}

	// 一次性批量查记录，避免逐条 N+1
	seen := make(map[string]bool)
	ids := make([]string, 0, len(targets))
	for _, i := range targets {
		for _, id := range msgs[i].ImageIDs {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	assets, err := store.Repo().GetByIDsAndUser(ctx, ids, userID)
	if err != nil {
		log.Printf("[image] replay: load assets failed: %v", err)
		return
	}
	byID := make(map[string]*model.ImageAsset, len(assets))
	for i := range assets {
		byID[assets[i].ID] = &assets[i]
	}

	attached := 0
	for _, i := range targets {
		refs := make([]llm.ImageRef, 0, len(msgs[i].ImageIDs))
		for _, id := range msgs[i].ImageIDs {
			a := byID[id]
			if a == nil {
				continue // 记录已被清理：客户端显示占位，模型只看到文字
			}
			fileID, err := store.EnsureFileID(ctx, a)
			if err != nil {
				log.Printf("[image] replay: ensure file_id for %s failed: %v", id[:8], err)
				continue
			}
			refs = append(refs, llm.ImageRef{FileID: fileID})
		}
		msgs[i].Images = refs
		attached += len(refs)
	}
	if attached > 0 {
		log.Printf("[image] replay: attached %d image(s) across %d message(s)", attached, len(targets))
	}
}
