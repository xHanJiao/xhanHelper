package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// WarmupImageEmbeddings 三条支线各走一次：加载已有向量、清孤儿、回填「有描述没向量」的老图。
// 用 httptest 假 embedding 端点，避免真外呼。
func TestWarmupImageEmbeddingsLoadsPrunesAndBackfills(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.ImageAsset{}, &model.Embedding{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	newImg := func(id, desc string) model.ImageAsset {
		return model.ImageAsset{
			ID:          id,
			UserID:      "u1",
			Ext:         "png",
			Mime:        "image/png",
			Source:      model.ImageSourceChat,
			Visibility:  model.ImageVisibilityPrivate,
			Description: desc,
		}
	}
	imgs := []model.ImageAsset{
		newImg("img-a", "一只橘猫"), // 已有向量
		newImg("img-b", "海边日落"), // 有描述但没向量 → 回填
		newImg("img-c", ""),     // 没描述 → 不参与
	}
	if err := db.Create(&imgs).Error; err != nil {
		t.Fatalf("seed images: %v", err)
	}

	vec := llm.EmbeddingToString([]float64{0.1, 0.2})
	for _, id := range []string{"img-a", "img-gone"} { // img-gone 没有对应资产 → 孤儿
		if err := db.Create(&model.Embedding{
			ID:         id,
			EntityType: model.EntityTypeImage,
			EntityID:   id,
			UserID:     "u1",
			Model:      "test",
			Embedding:  &vec,
		}).Error; err != nil {
			t.Fatalf("seed embedding %s: %v", id, err)
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"embedding": []float64{0.3, 0.4}},
		})
	}))
	defer srv.Close()

	vs := NewVectorStore()
	embeddingRepo := service.NewEmbeddingRepo(db)
	WarmupImageEmbeddings(ctx, NewVectorSearchAdapter(vs), service.NewImageAssetRepo(db), embeddingRepo,
		llm.NewEmbedder("test-key", srv.URL, "test"))

	for _, id := range []string{"img-a", "img-b"} {
		if _, ok := vs.GetVector(model.VectorPrefixImage + id); !ok {
			t.Errorf("vector %s missing after warmup", id)
		}
	}
	if _, ok := vs.GetVector(model.VectorPrefixImage + "img-gone"); ok {
		t.Error("orphan vector should be pruned from store")
	}
	if got := vs.Size(); got != 2 {
		t.Errorf("vector store size = %d, want 2 (a loaded + b backfilled, orphan pruned)", got)
	}
	if emb, err := embeddingRepo.GetByEntity(ctx, model.EntityTypeImage, "img-gone"); err != nil {
		t.Fatalf("query orphan row: %v", err)
	} else if emb != nil {
		t.Error("orphan embedding row should be deleted")
	}
	if emb, err := embeddingRepo.GetByEntity(ctx, model.EntityTypeImage, "img-b"); err != nil {
		t.Fatalf("query backfilled row: %v", err)
	} else if emb == nil {
		t.Error("backfill should create an embedding row for img-b")
	}
	if emb, err := embeddingRepo.GetByEntity(ctx, model.EntityTypeImage, "img-c"); err != nil {
		t.Fatalf("query undescribed row: %v", err)
	} else if emb != nil {
		t.Error("image without description must not be indexed")
	}
}
