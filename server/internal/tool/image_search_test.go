package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/app"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// fakeVectorStore — 测试用内存向量库（只做点积排序，够验证候选/过滤/删除语义）。
type fakeVectorStore struct {
	vecs map[string][]float64
	meta map[string]map[string]string
}

func newFakeVectorStore() *fakeVectorStore {
	return &fakeVectorStore{vecs: map[string][]float64{}, meta: map[string]map[string]string{}}
}

func (f *fakeVectorStore) Upsert(id string, vec []float64, metadata map[string]string) {
	f.vecs[id] = vec
	f.meta[id] = metadata
}

func (f *fakeVectorStore) Search(query []float64, topK int, filter app.VectorFilter) []app.VectorMatch {
	var out []app.VectorMatch
	for id, vec := range f.vecs {
		if filter != nil && !filter(id, vec, f.meta[id]) {
			continue
		}
		var score float64
		for i := range query {
			if i < len(vec) {
				score += query[i] * vec[i]
			}
		}
		out = append(out, app.VectorMatch{ID: id, Score: score, Metadata: f.meta[id]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > topK {
		out = out[:topK]
	}
	return out
}

func (f *fakeVectorStore) GetVector(id string) ([]float64, bool) {
	v, ok := f.vecs[id]
	return v, ok
}

func (f *fakeVectorStore) Delete(id string) {
	delete(f.vecs, id)
	delete(f.meta, id)
}

func (f *fakeVectorStore) Size() int { return len(f.vecs) }

// fixedEmbedder 对任意文本返回同一向量，便于精确控制相似度。
type fixedEmbedder struct{ vec []float64 }

func (e *fixedEmbedder) Embed(context.Context, string) ([]float64, error) { return e.vec, nil }
func (e *fixedEmbedder) ModelName() string                                { return "fixed" }

func newSearchTestTool(t *testing.T) (*ImageTool, *gorm.DB, *fakeVectorStore) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "i.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.ImageAsset{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := service.NewImageAssetRepo(db)
	store := service.NewImageStore(filepath.Join(t.TempDir(), "images"), 32<<20, repo, nil)
	vs := newFakeVectorStore()
	return NewImageTool(repo, store, &fixedEmbedder{vec: []float64{1, 0}}, vs), db, vs
}

func seedImage(t *testing.T, db *gorm.DB, id, userID, desc string, created time.Time) {
	t.Helper()
	if err := db.Create(&model.ImageAsset{
		ID: id, UserID: userID, Ext: "png", Mime: "image/png",
		Visibility: model.ImageVisibilityPrivate, Source: model.ImageSourceChat,
		Description: desc, CreatedAt: created,
	}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func decodeImages(t *testing.T, data []byte) []struct {
	ID          string  `json:"id"`
	Description string  `json:"description"`
	Score       float64 `json:"score"`
} {
	t.Helper()
	var d struct {
		Images []struct {
			ID          string  `json:"id"`
			Description string  `json:"description"`
			Score       float64 `json:"score"`
		} `json:"images"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return d.Images
}

// 关键词按描述匹配（OR），只返回本人的图，新的在前。
func TestImageToolSearchKeyword(t *testing.T) {
	tl, db, _ := newSearchTestTool(t)
	now := time.Now()
	seedImage(t, db, "old-beach", "u1", "海边日落", now.Add(-time.Hour))
	seedImage(t, db, "cat", "u1", "一只橘猫趴在沙发上", now)
	seedImage(t, db, "empty", "u1", "", now)
	seedImage(t, db, "other-user", "u2", "海边", now)

	res := tl.Execute(&ExecuteContext{Ctx: context.Background(), UserID: "u1"}, "search_images",
		mustJSON(t, map[string]any{"query": "海边 猫"}))
	if !res.Success {
		t.Fatalf("search_images: %s", res.Error)
	}
	got := decodeImages(t, res.Data)
	if len(got) != 2 {
		t.Fatalf("应命中 2 张（old-beach/cat），实际 %+v", got)
	}
	// 新的在前
	if got[0].ID != "cat" || got[1].ID != "old-beach" {
		t.Fatalf("排序不对: %+v", got)
	}
	for _, it := range got {
		if it.ID == "other-user" {
			t.Fatalf("不能搜到别人的图")
		}
	}
}

// 关键词大小写不敏感（描述存原文，查询按小写匹配）。
func TestImageToolSearchKeywordCaseInsensitive(t *testing.T) {
	tl, db, _ := newSearchTestTool(t)
	seedImage(t, db, "ascii", "u1", "Sunset Beach", time.Now())

	res := tl.Execute(&ExecuteContext{Ctx: context.Background(), UserID: "u1"}, "search_images",
		mustJSON(t, map[string]any{"query": "sunset"}))
	if !res.Success {
		t.Fatalf("search_images: %s", res.Error)
	}
	if got := decodeImages(t, res.Data); len(got) != 1 || got[0].ID != "ascii" {
		t.Fatalf("大小写不敏感匹配失败: %+v", got)
	}
}

// 语义模式按 top_k 截断：候选取 2 倍，DB 过滤后必须截回契约上限。
func TestImageToolSearchSemanticTopKTruncation(t *testing.T) {
	tl, db, vs := newSearchTestTool(t)
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("img%d", i)
		seedImage(t, db, id, "u1", "海边", time.Now())
		vs.Upsert(model.VectorPrefixImage+id, []float64{1, 0},
			map[string]string{"type": model.VectorTypeImage, "user_id": "u1"})
	}

	res := tl.Execute(&ExecuteContext{Ctx: context.Background(), UserID: "u1"}, "search_images",
		mustJSON(t, map[string]any{"query": "海边", "mode": "semantic", "top_k": 2}))
	if !res.Success {
		t.Fatalf("semantic search: %s", res.Error)
	}
	if got := decodeImages(t, res.Data); len(got) != 2 {
		t.Fatalf("top_k=2 应只返回 2 条，实际 %d 条", len(got))
	}
}

// 语义搜索按分数排序；低于阈值的丢弃；向量还在但记录已删的图必须搜不到。
func TestImageToolSearchSemanticDeletionGuarantee(t *testing.T) {
	tl, db, vs := newSearchTestTool(t)
	seedImage(t, db, "hit", "u1", "海边日落", time.Now())
	seedImage(t, db, "low", "u1", "会议室白板", time.Now())

	vs.Upsert(model.VectorPrefixImage+"hit", []float64{1, 0}, map[string]string{"type": model.VectorTypeImage, "user_id": "u1"})
	vs.Upsert(model.VectorPrefixImage+"low", []float64{0, 1}, map[string]string{"type": model.VectorTypeImage, "user_id": "u1"})
	vs.Upsert(model.VectorPrefixImage+"other", []float64{1, 0}, map[string]string{"type": model.VectorTypeImage, "user_id": "u2"})

	res := tl.Execute(&ExecuteContext{Ctx: context.Background(), UserID: "u1"}, "search_images",
		mustJSON(t, map[string]any{"query": "海边", "mode": "semantic"}))
	if !res.Success {
		t.Fatalf("semantic search: %s", res.Error)
	}
	got := decodeImages(t, res.Data)
	if len(got) != 1 || got[0].ID != "hit" || math.Abs(got[0].Score-1) > 1e-9 {
		t.Fatalf("应只命中 hit 且 score=1，实际 %+v", got)
	}

	// 删掉记录，向量故意留着：结果必须为空（搜索以 DB 为准）。
	if err := db.Delete(&model.ImageAsset{}, "id = ?", "hit").Error; err != nil {
		t.Fatalf("delete: %v", err)
	}
	res = tl.Execute(&ExecuteContext{Ctx: context.Background(), UserID: "u1"}, "search_images",
		mustJSON(t, map[string]any{"query": "海边", "mode": "semantic"}))
	if !res.Success {
		t.Fatalf("semantic search after delete: %s", res.Error)
	}
	if got := decodeImages(t, res.Data); len(got) != 0 {
		t.Fatalf("已删除的图不能出现在结果里: %+v", got)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
