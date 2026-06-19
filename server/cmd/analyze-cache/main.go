package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"

	"xhan-helper/internal/app"
	"xhan-helper/internal/config"
	"xhan-helper/internal/model"
)

func main() {
	os.Setenv("APP_ENV", "dev")
	cfg, err := config.Load()
	if err != nil {
		log.Fatal("config:", err)
	}
	db := app.InitDB(cfg)

	var msgs []model.Message
	db.Where("role = 'assistant' AND status = 1 AND created_at >= ? AND created_at < ?",
		"2026-06-19", "2026-06-20").
		Order("created_at ASC").
		Find(&msgs)

	type Result struct {
		MsgID     string
		Rounds    int
		PromptTok int
		CacheHit  int
		HitPct    float64
		Time      string
	}
	var results []Result

	for _, m := range msgs {
		if m.RequestJSON == nil || m.Extra == nil || *m.RequestJSON == "" {
			continue
		}
		var req struct {
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		if err := json.Unmarshal([]byte(*m.RequestJSON), &req); err != nil {
			continue
		}
		rounds := 0
		for _, msg := range req.Messages {
			if msg.Role == "user" {
				rounds++
			}
		}
		var extra struct {
			Usage *struct {
				PromptTokens         int `json:"prompt_tokens"`
				PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"`
			} `json:"Usage"`
		}
		if err := json.Unmarshal([]byte(*m.Extra), &extra); err != nil || extra.Usage == nil {
			continue
		}
		u := extra.Usage
		if u.PromptTokens == 0 {
			continue
		}
		results = append(results, Result{
			MsgID:     m.ID,
			Rounds:    rounds,
			PromptTok: u.PromptTokens,
			CacheHit:  u.PromptCacheHitTokens,
			HitPct:    float64(u.PromptCacheHitTokens) / float64(u.PromptTokens) * 100,
			Time:      m.CreatedAt.Format("15:04:05"),
		})
	}

	// Count distribution of cache_hit values
	fmt.Println("\n=== cache_hit 值分布 ===")
	hitFreq := make(map[int]int)
	for _, r := range results {
		hitFreq[r.CacheHit]++
	}
	var vals []int
	for v := range hitFreq {
		vals = append(vals, v)
	}
	sort.Ints(vals)
	for _, v := range vals {
		fmt.Printf("  cache_hit=%d: %d 次\n", v, hitFreq[v])
	}

	// Analyze chunk hypothesis: differences between discrete cache_hit levels
	fmt.Println("\n=== cache_hit 层级差（可能对应 chunk 边界）===")
	levels := vals
	for i := 1; i < len(levels); i++ {
		diff := levels[i] - levels[i-1]
		fmt.Printf("  %d → %d: diff=%d (÷256=%.1f, ÷512=%.1f, ÷1024=%.1f, ÷2048=%.2f)\n",
			levels[i-1], levels[i], diff,
			float64(diff)/256, float64(diff)/512, float64(diff)/1024, float64(diff)/2048)
	}

	// For the largest cache_hit (93.6%), show what's NOT cached
	fmt.Println("\n=== 接近全命中时，未命中部分 ===")
	for _, r := range results {
		if r.CacheHit > 10000 {
			miss := r.PromptTok - r.CacheHit
			fmt.Printf("  %s: prompt=%d cache=%d miss=%d (%.1f%%), rounds=%d\n",
				r.MsgID[:8], r.PromptTok, r.CacheHit, miss,
				float64(miss)/float64(r.PromptTok)*100, r.Rounds)
		}
	}

	// Show the stable 6656 group: prompt sizes vs cache
	fmt.Println("\n=== 6656 命中组：prompt 大小变化 ===")
	for _, r := range results {
		if r.CacheHit == 6656 {
			fmt.Printf("  %s: prompt=%d cache=%d hit=%.1f%% time=%s rounds=%d\n",
				r.MsgID[:8], r.PromptTok, r.CacheHit, r.HitPct, r.Time, r.Rounds)
		}
	}
}
