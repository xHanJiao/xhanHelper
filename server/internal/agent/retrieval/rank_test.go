package retrieval

import (
	"math"
	"testing"
	"time"
)

func TestDefaultRanker(t *testing.T) {
	r := DefaultRanker()
	if r.VectorWeight != 0.5 || r.KeywordWeight != 0.3 || r.FreshnessWeight != 0.2 || r.DecayRate != 0 {
		t.Errorf("DefaultRanker: got {%.2f, %.2f, %.2f, %.2f}", r.VectorWeight, r.KeywordWeight, r.FreshnessWeight, r.DecayRate)
	}
}

// ===== Score 测试 =====

func TestRankerScore_EmptyItems(t *testing.T) {
	r := DefaultRanker()
	results := r.Score("查询", nil)
	if len(results) != 0 {
		t.Errorf("expected empty, got %d results", len(results))
	}
}

func TestRankerScore_SingleItem(t *testing.T) {
	r := DefaultRanker()
	items := []scoreInput{
		{ID: "a", Content: "与查询无关", CreatedAt: time.Now(), VectorScore: 0.8},
	}
	results := r.Score("查询", items)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].ID != "a" {
		t.Errorf("expected ID 'a', got %q", results[0].ID)
	}
	if results[0].VectorScore != 0.8 {
		t.Errorf("expected VectorScore 0.8, got %.4f", results[0].VectorScore)
	}
}

func TestRankerScore_SortedByFinalScore(t *testing.T) {
	r := DefaultRanker()
	// 三个 item，向量分递减，确保逆序后能验证排序
	items := []scoreInput{
		{ID: "low", Content: "无关", CreatedAt: time.Now(), VectorScore: 0.3},
		{ID: "mid", Content: "无关", CreatedAt: time.Now(), VectorScore: 0.6},
		{ID: "high", Content: "无关", CreatedAt: time.Now(), VectorScore: 0.9},
	}
	results := r.Score("查询", items)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if results[0].ID != "high" || results[1].ID != "mid" || results[2].ID != "low" {
		t.Errorf("wrong order: %v", results)
	}
	// 严格降序
	for i := 1; i < len(results); i++ {
		if results[i-1].FinalScore < results[i].FinalScore {
			t.Errorf("not sorted descending: [%d]=%.4f < [%d]=%.4f", i-1, results[i-1].FinalScore, i, results[i].FinalScore)
		}
	}
}

func TestRankerScore_VectorWeightOnly(t *testing.T) {
	r := &Ranker{VectorWeight: 1.0, KeywordWeight: 0, FreshnessWeight: 0, DecayRate: 0}
	items := []scoreInput{
		{ID: "a", Content: "任意内容", CreatedAt: time.Now(), VectorScore: 0.75},
	}
	results := r.Score("任意查询", items)
	// FinalScore = 0.75 * 1.0 = 0.75
	if math.Abs(results[0].FinalScore-0.75) > 0.0001 {
		t.Errorf("expected 0.75, got %.4f", results[0].FinalScore)
	}
}

func TestRankerScore_KeywordWeightOnly(t *testing.T) {
	r := &Ranker{VectorWeight: 0, KeywordWeight: 1.0, FreshnessWeight: 0, DecayRate: 0}
	// query = "今天天气真好" → 5 bigrams
	// target = "今天天气" → 3 bigrams: {"今天","天天","天气"}
	// overlap = 3/5 = 0.6
	items := []scoreInput{
		{ID: "a", Content: "今天天气", CreatedAt: time.Now(), VectorScore: 0},
	}
	results := r.Score("今天天气真好", items)
	expected := 3.0 / 5.0 // 0.6
	if math.Abs(results[0].FinalScore-expected) > 0.0001 {
		t.Errorf("expected %.4f, got %.4f", expected, results[0].FinalScore)
	}
	if math.Abs(results[0].KeywordScore-expected) > 0.0001 {
		t.Errorf("expected KeywordScore %.4f, got %.4f", expected, results[0].KeywordScore)
	}
}

func TestRankerScore_FreshnessWeightOnly_NoDecay(t *testing.T) {
	r := &Ranker{VectorWeight: 0, KeywordWeight: 0, FreshnessWeight: 1.0, DecayRate: 0}
	items := []scoreInput{
		{ID: "a", Content: "任意", CreatedAt: time.Now(), VectorScore: 0},
	}
	results := r.Score("查询", items)
	// DecayRate=0 → freshness=1.0, FinalScore = 1.0 * 1.0 = 1.0
	if math.Abs(results[0].FinalScore-1.0) > 0.0001 {
		t.Errorf("expected 1.0, got %.4f", results[0].FinalScore)
	}
	if math.Abs(results[0].Freshness-1.0) > 0.0001 {
		t.Errorf("expected Freshness 1.0, got %.4f", results[0].Freshness)
	}
}

func TestRankerScore_FreshnessWithDecay(t *testing.T) {
	r := &Ranker{VectorWeight: 0, KeywordWeight: 0, FreshnessWeight: 1.0, DecayRate: 0.1}
	now := time.Now()

	// 10 天前的 item
	oldItem := scoreInput{ID: "old", Content: "a", CreatedAt: now.AddDate(0, 0, -10), VectorScore: 0}
	// 刚刚的 item
	newItem := scoreInput{ID: "new", Content: "a", CreatedAt: now, VectorScore: 0}

	results := r.Score("x", []scoreInput{oldItem, newItem})
	if results[0].ID != "new" {
		t.Errorf("expected new item first, got %v", results)
	}
	// old freshness = 1/(1+10*0.1) = 1/2 = 0.5
	if math.Abs(results[1].Freshness-0.5) > 0.01 {
		t.Errorf("expected old freshness ~0.5, got %.4f", results[1].Freshness)
	}
	// new freshness = 1/(1+0*0.1) = 1.0
	if math.Abs(results[0].Freshness-1.0) > 0.0001 {
		t.Errorf("expected new freshness 1.0, got %.4f", results[0].Freshness)
	}
}

func TestRankerScore_FreshnessVeryOld(t *testing.T) {
	r := &Ranker{VectorWeight: 0, KeywordWeight: 0, FreshnessWeight: 1.0, DecayRate: 0.02}
	// 1000 天前 → freshness ≈ 1/(1+1000*0.02) = 1/21 ≈ 0.0476
	oldItem := scoreInput{ID: "ancient", Content: "a", CreatedAt: time.Now().AddDate(-3, 0, 0), VectorScore: 0}
	results := r.Score("x", []scoreInput{oldItem})
	if results[0].Freshness > 0.1 {
		t.Errorf("expected very low freshness for 3-year-old item, got %.4f", results[0].Freshness)
	}
}

func TestRankerScore_AllZeroWeights(t *testing.T) {
	r := &Ranker{VectorWeight: 0, KeywordWeight: 0, FreshnessWeight: 0, DecayRate: 0}
	items := []scoreInput{
		{ID: "a", Content: "无关", CreatedAt: time.Now(), VectorScore: 0.9},
		{ID: "b", Content: "无关", CreatedAt: time.Now(), VectorScore: 0.5},
	}
	results := r.Score("查询", items)
	for _, res := range results {
		if res.FinalScore != 0 {
			t.Errorf("expected 0 final score with all zero weights, got %.4f", results[0].FinalScore)
		}
	}
}

func TestRankerScore_CustomWeights(t *testing.T) {
	r := &Ranker{VectorWeight: 0.8, KeywordWeight: 0.1, FreshnessWeight: 0.1, DecayRate: 0}
	items := []scoreInput{
		{ID: "a", Content: "无关内容", CreatedAt: time.Now(), VectorScore: 1.0},
	}
	results := r.Score("查询", items)
	// FinalScore = 1.0*0.8 + 0*0.1 + 1.0*0.1 = 0.9
	if math.Abs(results[0].FinalScore-0.9) > 0.0001 {
		t.Errorf("expected 0.9, got %.4f", results[0].FinalScore)
	}
}

func TestRankerScore_MultipleItemsWithKeywords(t *testing.T) {
	r := DefaultRanker()
	now := time.Now()
	items := []scoreInput{
		{ID: "good", Content: "今天我去了北京旅游", CreatedAt: now, VectorScore: 0.8},
		{ID: "bad", Content: "完全不相关的文字", CreatedAt: now, VectorScore: 0.8},
	}
	results := r.Score("北京旅游", items)
	// "good" should rank higher due to keyword match
	if results[0].ID != "good" {
		t.Errorf("expected 'good' first due to keyword match, got %v", results)
	}
	if results[0].KeywordScore <= results[1].KeywordScore {
		t.Errorf("expected higher keyword score for 'good': %.4f vs %.4f", results[0].KeywordScore, results[1].KeywordScore)
	}
}

func TestRankerScore_ResultsFieldsAreRounded(t *testing.T) {
	r := DefaultRanker()
	items := []scoreInput{
		{ID: "a", Content: "今天天气", CreatedAt: time.Now(), VectorScore: 0.123456},
	}
	results := r.Score("测试查询", items)
	// KeywordScore, Freshness, FinalScore 都应被 Round 到 4 位小数
	for _, res := range results {
		if res.KeywordScore != math.Round(res.KeywordScore*10000)/10000 {
			t.Errorf("KeywordScore not rounded: %.10f", res.KeywordScore)
		}
		if res.Freshness != math.Round(res.Freshness*10000)/10000 {
			t.Errorf("Freshness not rounded: %.10f", res.Freshness)
		}
		if res.FinalScore != math.Round(res.FinalScore*10000)/10000 {
			t.Errorf("FinalScore not rounded: %.10f", res.FinalScore)
		}
	}
}

func TestRankerScore_NilRankerDefaults(t *testing.T) {
	// NewMemorySource / NewNoteSource should handle nil ranker
	// but we don't call them here — just verify DefaultRanker produces sensible output
	r := DefaultRanker()
	if r == nil {
		t.Fatal("DefaultRanker returned nil")
	}
	items := []scoreInput{
		{ID: "a", Content: "测试", CreatedAt: time.Now(), VectorScore: 1.0},
	}
	results := r.Score("查询", items)
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}
