package service

import (
	"context"
	"fmt"
)

// VectorDeleter — 从内存向量库删除条目的最小接口；app.VectorStore 天然满足。
type VectorDeleter interface {
	Delete(id string)
}

// fullVectorKeyPrefix — 各实体的「全文向量」在内存向量库中的键前缀。
// 必须与写入侧一致：note 用 "note:"（tool/note.go embedAll），memory 用 "mem:"（agent/extractor.go storeMemory）。
var fullVectorKeyPrefix = map[string]string{
	"note":   "note:",
	"memory": "mem:",
}

// EntityPurger — 删除笔记/记忆等实体时，统一清理其派生数据，避免留下孤儿：
// 全文向量行（embeddings）+ 句级 chunk（embedding_chunks 行、chunk 向量行、内存 chunk 向量）+ 版本快照。
//
// 为什么需要它：删除入口散落在 tool 与 controller，各写各的，漏项不一致（见 docs/bugs/017）。
// 幂等：重复调用安全；未做事务，中途失败留下的残留靠重跑清理。
type EntityPurger struct {
	embeddingRepo *EmbeddingRepo
	chunkRepo     *EmbeddingChunkRepo
	versionRepo   *VersionRepo
	vectorStore   VectorDeleter
}

// NewEntityPurger 创建实体清理器
func NewEntityPurger(embeddingRepo *EmbeddingRepo, chunkRepo *EmbeddingChunkRepo, versionRepo *VersionRepo, vs VectorDeleter) *EntityPurger {
	return &EntityPurger{embeddingRepo: embeddingRepo, chunkRepo: chunkRepo, versionRepo: versionRepo, vectorStore: vs}
}

// Purge 清理单个实体的全部派生数据。entityType ∈ {note, memory}。
// DB 行删除失败向上抛（附加语境、保留错误链）；内存向量删除不返回错误。
func (p *EntityPurger) Purge(ctx context.Context, entityType, entityID string) error {
	prefix, ok := fullVectorKeyPrefix[entityType]
	if !ok {
		return fmt.Errorf("entity purger: unsupported entity type %q", entityType)
	}

	if err := p.PurgeChunks(ctx, entityType, entityID); err != nil {
		return err
	}

	// 全文向量：内存 + 向量行
	p.vectorStore.Delete(prefix + entityID)
	if err := p.embeddingRepo.DeleteByEntity(ctx, entityType, entityID); err != nil {
		return fmt.Errorf("delete full embedding %s/%s: %w", entityType, entityID, err)
	}

	// 版本快照（note/agent 有；memory 无对应行，为 no-op）
	if err := p.versionRepo.DeleteByEntity(ctx, entityType, entityID); err != nil {
		return fmt.Errorf("delete versions %s/%s: %w", entityType, entityID, err)
	}
	return nil
}

// PurgeChunks 只清理句级 chunk（内存 chunk 向量 + chunk 向量行 + chunk 行）。
// 用于「更新后重建 chunk」这类只替换 chunk 的场景，不动全文向量与版本快照。
func (p *EntityPurger) PurgeChunks(ctx context.Context, entityType, entityID string) error {
	chunks, err := p.chunkRepo.ListByParent(ctx, entityType, entityID)
	if err != nil {
		return fmt.Errorf("list chunks %s/%s: %w", entityType, entityID, err)
	}
	chunkKeys := make([]string, 0, len(chunks))
	for _, c := range chunks {
		key := fmt.Sprintf("chunk:%s:%s/%d", entityType, entityID, c.ChunkIndex)
		chunkKeys = append(chunkKeys, key)
		p.vectorStore.Delete(key)
	}
	if _, err := p.embeddingRepo.DeleteByEntities(ctx, "chunk", chunkKeys); err != nil {
		return fmt.Errorf("delete chunk embeddings %s/%s: %w", entityType, entityID, err)
	}
	if err := p.chunkRepo.DeleteByParent(ctx, entityType, entityID); err != nil {
		return fmt.Errorf("delete chunks %s/%s: %w", entityType, entityID, err)
	}
	return nil
}
