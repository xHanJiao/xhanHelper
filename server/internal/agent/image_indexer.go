package agent

import (
	"context"
	"fmt"
	"strings"

	"xhan-helper/internal/app"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// ImageIndexer — 把图片描述嵌入成向量，供 image.search_images 的语义模式使用。
// 由 ImageStore 在描述生成后调用；删除图片时同步清向量。
type ImageIndexer struct {
	embedder  *llm.Embedder
	vs        app.VectorStore
	embedding *service.EmbeddingRepo
}

// NewImageIndexer 创建图片描述索引器。依赖为 nil 直接 panic（接线 bug）。
func NewImageIndexer(embedder *llm.Embedder, vs app.VectorStore, embedding *service.EmbeddingRepo) *ImageIndexer {
	if embedder == nil || vs == nil || embedding == nil {
		panic("image_indexer: nil dependency (wiring bug)")
	}
	return &ImageIndexer{embedder: embedder, vs: vs, embedding: embedding}
}

// IndexImage 嵌入描述并写入向量行 + 内存向量。
func (i *ImageIndexer) IndexImage(ctx context.Context, userID, imageID, description string) error {
	description = strings.TrimSpace(description)
	if description == "" {
		return nil
	}
	vec, err := i.embedder.Embed(ctx, description)
	if err != nil {
		return fmt.Errorf("embed image description: %w", err)
	}
	if len(vec) == 0 {
		return fmt.Errorf("embed image description: empty vector")
	}
	embStr := llm.EmbeddingToString(vec)
	if err := i.embedding.Upsert(ctx, &model.Embedding{
		ID:         imageID,
		EntityType: model.EntityTypeImage,
		EntityID:   imageID,
		UserID:     userID,
		Model:      i.embedder.ModelName(),
		Embedding:  &embStr,
	}); err != nil {
		return fmt.Errorf("save image embedding: %w", err)
	}
	i.vs.Upsert(model.VectorPrefixImage+imageID, vec, map[string]string{
		"type":    model.VectorTypeImage,
		"user_id": userID,
	})
	return nil
}

// RemoveImages 删除图片的描述向量（内存 + 向量行）。幂等。
func (i *ImageIndexer) RemoveImages(ctx context.Context, imageIDs []string) error {
	if len(imageIDs) == 0 {
		return nil
	}
	for _, id := range imageIDs {
		i.vs.Delete(model.VectorPrefixImage + id)
	}
	if _, err := i.embedding.DeleteByEntities(ctx, model.EntityTypeImage, imageIDs); err != nil {
		return fmt.Errorf("delete image embeddings: %w", err)
	}
	return nil
}
