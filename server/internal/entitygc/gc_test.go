package entitygc

import (
	"reflect"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// PurgeCancelledNotes：dry-run 不删；write 后 note + 四类派生数据全消失；重跑为 0。
func TestPurgeCancelledNotes(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Note{}, &model.Embedding{}, &model.EmbeddingChunk{}, &model.ContentVersion{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// 一个取消态笔记 + 一个正常笔记（正常的不该被动）。
	cancelled, normal := "n-cancel", "n-normal"
	if err := db.Create(&model.Note{ID: cancelled, UserID: "u1", Topic: "旧主题", Content: "c", Status: model.NoteStatusCancelled}).Error; err != nil {
		t.Fatalf("seed cancelled note: %v", err)
	}
	if err := db.Create(&model.Note{ID: normal, UserID: "u1", Topic: "正常", Content: "c", Status: model.NoteStatusNormal}).Error; err != nil {
		t.Fatalf("seed normal note: %v", err)
	}
	// 派生数据：全文向量、chunk 行 + chunk 向量、版本快照。
	chunkKey := model.ChunkVectorKey(model.EntityTypeNote, cancelled, 0)
	derived := []model.Embedding{
		{ID: "e-full", EntityType: model.EntityTypeNote, EntityID: cancelled, UserID: "u1", Model: "m"},
		{ID: "e-chunk", EntityType: model.EntityTypeChunk, EntityID: chunkKey, UserID: "u1", Model: "m"},
		{ID: "e-keep", EntityType: model.EntityTypeNote, EntityID: normal, UserID: "u1", Model: "m"},
	}
	for i := range derived {
		if err := db.Create(&derived[i]).Error; err != nil {
			t.Fatalf("seed embedding: %v", err)
		}
	}
	if err := db.Create(&model.EmbeddingChunk{ID: "c1", ParentType: model.EntityTypeNote, ParentID: cancelled, UserID: "u1", ChunkIndex: 0, Content: "x"}).Error; err != nil {
		t.Fatalf("seed chunk: %v", err)
	}
	if err := db.Create(&model.ContentVersion{ID: "v1", EntityType: model.EntityTypeNote, EntityID: cancelled, Version: 1, Snapshot: "{}"}).Error; err != nil {
		t.Fatalf("seed version: %v", err)
	}

	// dry-run：一条不删。
	n, err := PurgeCancelledNotes(db, Options{Write: false})
	if err != nil || n != 1 {
		t.Fatalf("dry-run want (1,nil), got (%d,%v)", n, err)
	}
	assertCount(t, db, &model.Note{}, "id = ?", cancelled, 1)
	assertCount(t, db, &model.EmbeddingChunk{}, "parent_id = ?", cancelled, 1)

	// write：全部消失，正常笔记与它的向量保留。
	n, err = PurgeCancelledNotes(db, Options{Write: true})
	if err != nil || n != 1 {
		t.Fatalf("write want (1,nil), got (%d,%v)", n, err)
	}
	assertCount(t, db, &model.Note{}, "id = ?", cancelled, 0)
	assertCount(t, db, &model.Embedding{}, "entity_id = ?", cancelled, 0)
	assertCount(t, db, &model.Embedding{}, "entity_id = ?", chunkKey, 0)
	assertCount(t, db, &model.EmbeddingChunk{}, "parent_id = ?", cancelled, 0)
	assertCount(t, db, &model.ContentVersion{}, "entity_id = ?", cancelled, 0)
	assertCount(t, db, &model.Note{}, "id = ?", normal, 1)
	assertCount(t, db, &model.Embedding{}, "entity_id = ?", normal, 1)

	// 幂等：重跑为 0。
	n, err = PurgeCancelledNotes(db, Options{Write: true})
	if err != nil || n != 0 {
		t.Fatalf("rerun want (0,nil), got (%d,%v)", n, err)
	}
}

func assertCount(t *testing.T, db *gorm.DB, m interface{}, cond string, arg interface{}, want int64) {
	t.Helper()
	var got int64
	if err := db.Model(m).Where(cond, arg).Count(&got).Error; err != nil {
		t.Fatalf("count %s=%v: %v", cond, arg, err)
	}
	if got != want {
		t.Errorf("count %s=%v = %d, want %d", cond, arg, got, want)
	}
}

func TestDiff(t *testing.T) {
	valid := toSet([]string{"n1", "n2", "n3"})
	got := diff([]string{"n1", "n9", "n2", "n9", "n8"}, valid)
	want := []string{"n9", "n8"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("diff = %v, want %v", got, want)
	}
}

func TestDiffAllValid(t *testing.T) {
	valid := toSet([]string{"a", "b"})
	if got := diff([]string{"a", "b"}, valid); len(got) != 0 {
		t.Errorf("diff = %v, want empty", got)
	}
}

func TestUnion(t *testing.T) {
	got := union([]string{"a", "b"}, []string{"b", "c"})
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("union = %v, want %v", got, want)
	}
}

func TestUnionDoesNotMutateInput(t *testing.T) {
	a := []string{"a"}
	_ = union(a, []string{"b"})
	if !reflect.DeepEqual(a, []string{"a"}) {
		t.Errorf("input mutated: %v", a)
	}
}

// PurgeLegacyThemes：dry-run 只统计；write 清掉旧主题全部残留，note 数据不受影响；重跑幂等。
func TestPurgeLegacyThemes(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.ThemeState{}, &model.ConceptEmbedding{}, &model.Embedding{}, &model.EmbeddingChunk{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if err := db.Create(&model.ThemeState{ID: "t1", UserID: "u1", Name: "旧主题"}).Error; err != nil {
		t.Fatalf("seed theme: %v", err)
	}
	concepts := []model.ConceptEmbedding{
		{ID: "c-theme", Type: model.EntityTypeTheme, Value: "旧主题", UserID: "u1", ParentType: model.EntityTypeTheme},
		{ID: "c-topic", Type: "topic", Value: "保留", UserID: "u1"},
	}
	for i := range concepts {
		if err := db.Create(&concepts[i]).Error; err != nil {
			t.Fatalf("seed concept: %v", err)
		}
	}
	themeChunkKey := model.ChunkVectorKey(model.EntityTypeTheme, "t1", 0)
	noteChunkKey := model.ChunkVectorKey(model.EntityTypeNote, "n1", 0)
	embeddings := []model.Embedding{
		{ID: "e-theme", EntityType: model.EntityTypeTheme, EntityID: "c-theme", UserID: "u1", Model: "m"},
		{ID: "e-theme-chunk", EntityType: model.EntityTypeChunk, EntityID: themeChunkKey, UserID: "u1", Model: "m"},
		{ID: "e-note", EntityType: model.EntityTypeNote, EntityID: "n1", UserID: "u1", Model: "m"},
		{ID: "e-note-chunk", EntityType: model.EntityTypeChunk, EntityID: noteChunkKey, UserID: "u1", Model: "m"},
	}
	for i := range embeddings {
		if err := db.Create(&embeddings[i]).Error; err != nil {
			t.Fatalf("seed embedding: %v", err)
		}
	}
	chunks := []model.EmbeddingChunk{
		{ID: "tc1", ParentType: model.EntityTypeTheme, ParentID: "t1", UserID: "u1", ChunkIndex: 0, Content: "x"},
		{ID: "nc1", ParentType: model.EntityTypeNote, ParentID: "n1", UserID: "u1", ChunkIndex: 0, Content: "x"},
	}
	for i := range chunks {
		if err := db.Create(&chunks[i]).Error; err != nil {
			t.Fatalf("seed chunk: %v", err)
		}
	}

	// dry-run：统计完整，一条不删。
	sum, err := PurgeLegacyThemes(db, Options{Write: false})
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if want := (ThemePurgeSummary{Themes: 1, ThemeVectors: 1, ChunkVectors: 1, ChunkRows: 1, Concepts: 1}); *sum != want {
		t.Fatalf("dry-run summary = %+v, want %+v", *sum, want)
	}
	assertCount(t, db, &model.ThemeState{}, "id = ?", "t1", 1)

	// write：旧主题消失，note 数据与 topic concept 保留。
	if _, err := PurgeLegacyThemes(db, Options{Write: true}); err != nil {
		t.Fatalf("write: %v", err)
	}
	assertCount(t, db, &model.ThemeState{}, "id = ?", "t1", 0)
	assertCount(t, db, &model.ConceptEmbedding{}, "id = ?", "c-theme", 0)
	assertCount(t, db, &model.ConceptEmbedding{}, "id = ?", "c-topic", 1)
	assertCount(t, db, &model.Embedding{}, "id = ?", "e-theme", 0)
	assertCount(t, db, &model.Embedding{}, "id = ?", "e-theme-chunk", 0)
	assertCount(t, db, &model.Embedding{}, "id = ?", "e-note", 1)
	assertCount(t, db, &model.Embedding{}, "id = ?", "e-note-chunk", 1)
	assertCount(t, db, &model.EmbeddingChunk{}, "id = ?", "tc1", 0)
	assertCount(t, db, &model.EmbeddingChunk{}, "id = ?", "nc1", 1)

	// 幂等：重跑统计全 0。
	sum, err = PurgeLegacyThemes(db, Options{Write: false})
	if err != nil || *sum != (ThemePurgeSummary{}) {
		t.Fatalf("rerun want empty summary, got (%+v,%v)", sum, err)
	}
}
