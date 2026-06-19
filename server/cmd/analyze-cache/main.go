// analyze-cache 分析 DeepSeek 缓存命中情况。
//
// 用法: go run ./cmd/analyze-cache/main.go
//
// 数据来源:
//   - messages 表: request_json（LLM 请求原文）和 extra.Usage（token 用量统计）
//   - 只处理 role='assistant' 且 status=1 的消息（每轮 ReAct 至少产生一条 assistant 消息）
//
// 统计维度:
//  1. 按 user 消息轮数（request_json 中 role='user' 的数量）分组
//  2. 区分「工具调用轮」（请求末尾是 tool role）和「普通轮」——
//     工具调用轮的前缀在 ReAct 循环内天然不变，缓存命中率虚高，需单独标注
//  3. 按时间序列展示 cache_hit 值，观察振荡模式
//  4. 找出全部数据中最大的 cache_hit 值，判断是否存在天花板
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

// CacheResult 从一条 assistant 消息中提取的缓存相关字段。
type CacheResult struct {
	MsgID          string  // 消息 ID（只取前 8 位显示）
	Time           string  // 创建时间，格式 "01-02 15:04:05"
	Rounds         int     // request 中的 user 消息数（≈ 窗口大小）
	PromptTok      int     // prompt_tokens
	CacheHit       int     // prompt_cache_hit_tokens
	HitPct         float64 // CacheHit / PromptTok * 100
	IsToolRound    bool    // 该请求的最后一条消息是否为 tool role（ReAct 内部轮）
	TotalMsgsInReq int     // request 中 messages 总数（system + user + assistant + tool）
	LastRole       string  // request 中最后一条消息的角色
}

func main() {
	os.Setenv("APP_ENV", "dev")
	cfg, err := config.Load()
	if err != nil {
		log.Fatal("config:", err)
	}
	db := app.InitDB(cfg)

	// ---- 加载全部数据 ----
	var msgs []model.Message
	db.Where("role = 'assistant' AND status = 1").
		Order("created_at ASC").
		Find(&msgs)
	log.Printf("加载消息: %d 条", len(msgs))

	// ---- 解析每条消息 ----
	var results []CacheResult
	skipped := 0

	for _, m := range msgs {
		if m.RequestJSON == nil || m.Extra == nil || *m.RequestJSON == "" || *m.Extra == "" {
			skipped++
			continue
		}

		// 解析 request_json:
		//   - 统计 role='user' 的数量 → 窗口轮数
		//   - 记录最后一条消息的角色 → 判断是否为工具调用轮
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal([]byte(*m.RequestJSON), &req); err != nil {
			skipped++
			continue
		}
		if len(req.Messages) == 0 {
			skipped++
			continue
		}

		rounds := 0
		for _, msg := range req.Messages {
			if msg.Role == "user" {
				rounds++
			}
		}
		lastRole := req.Messages[len(req.Messages)-1].Role

		// 解析 extra.Usage
		var extra struct {
			Usage *struct {
				PromptTokens          int `json:"prompt_tokens"`
				PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
				PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
			} `json:"Usage"`
		}
		if err := json.Unmarshal([]byte(*m.Extra), &extra); err != nil || extra.Usage == nil {
			skipped++
			continue
		}
		u := extra.Usage
		if u.PromptTokens == 0 {
			skipped++
			continue
		}

		results = append(results, CacheResult{
			MsgID:          m.ID,
			Time:           m.CreatedAt.Format("01-02 15:04:05"),
			Rounds:         rounds,
			PromptTok:      u.PromptTokens,
			CacheHit:       u.PromptCacheHitTokens,
			HitPct:         float64(u.PromptCacheHitTokens) / float64(u.PromptTokens) * 100,
			IsToolRound:    lastRole == "tool",
			TotalMsgsInReq: len(req.Messages),
			LastRole:       lastRole,
		})
	}
	log.Printf("解析成功: %d, 跳过: %d", len(results), skipped)

	// ---- 1. 按轮数 + 是否工具轮 分组汇总 ----
	fmt.Println("\n========================================")
	fmt.Println("1. 全量汇总：按轮数 × 是否工具轮分组")
	fmt.Println("========================================")
	fmt.Printf("%-6s %-10s %5s %8s %8s %8s %s\n",
		"轮数", "类型", "样本", "均prompt", "均hit%", "最大hit", "常见hit值(top3)")

	// 分组键: round * 100 + toolFlag
	groups := make(map[string][]CacheResult)
	for _, r := range results {
		key := fmt.Sprintf("%d-%v", r.Rounds, r.IsToolRound)
		groups[key] = append(groups[key], r)
	}

	// 按 key 排序输出
	var keys []string
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// 全局最大 cache_hit 追踪
	var globalMaxNonTool CacheResult
	var globalMaxTool CacheResult

	for _, key := range keys {
		list := groups[key]
		var sumHit, sumPrompt float64
		maxHit := 0
		hitFreq := make(map[int]int)
		for _, v := range list {
			sumHit += v.HitPct
			sumPrompt += float64(v.PromptTok)
			hitFreq[v.CacheHit]++
			if v.CacheHit > maxHit {
				maxHit = v.CacheHit
			}
			// 跟踪全局最大
			if v.IsToolRound && v.CacheHit > globalMaxTool.CacheHit {
				globalMaxTool = v
			}
			if !v.IsToolRound && v.CacheHit > globalMaxNonTool.CacheHit {
				globalMaxNonTool = v
			}
		}

		// 取 top 3 常见 hit 值
		type kv struct{ k, v int }
		var freqList []kv
		for k, v := range hitFreq {
			freqList = append(freqList, kv{k, v})
		}
		sort.Slice(freqList, func(i, j int) bool { return freqList[i].v > freqList[j].v })
		topStr := ""
		for i := 0; i < 3 && i < len(freqList); i++ {
			if i > 0 {
				topStr += " "
			}
			topStr += fmt.Sprintf("%d(%d)", freqList[i].k, freqList[i].v)
		}

		typeLabel := "普通"
		if list[0].IsToolRound {
			typeLabel = "工具轮"
		}

		fmt.Printf("%-6d %-10s %5d %8.0f %8.1f%% %8d %s\n",
			list[0].Rounds, typeLabel, len(list),
			sumPrompt/float64(len(list)), sumHit/float64(len(list)),
			maxHit, topStr)
	}

	// ---- 2. 全局最大 cache_hit 值 ----
	fmt.Println("\n========================================")
	fmt.Println("2. 全局最大 cache_hit 值")
	fmt.Println("========================================")

	fmt.Printf("\n普通轮（非工具）最大 cache_hit:\n")
	fmt.Printf("  msg=%s time=%s rounds=%d cache_hit=%d (%.1f%%) prompt=%d lastRole=%s\n",
		globalMaxNonTool.MsgID[:8], globalMaxNonTool.Time, globalMaxNonTool.Rounds,
		globalMaxNonTool.CacheHit, globalMaxNonTool.HitPct, globalMaxNonTool.PromptTok,
		globalMaxNonTool.LastRole)

	fmt.Printf("\n工具轮（ReAct内部）最大 cache_hit:\n")
	fmt.Printf("  msg=%s time=%s rounds=%d cache_hit=%d (%.1f%%) prompt=%d lastRole=%s\n",
		globalMaxTool.MsgID[:8], globalMaxTool.Time, globalMaxTool.Rounds,
		globalMaxTool.CacheHit, globalMaxTool.HitPct, globalMaxTool.PromptTok,
		globalMaxTool.LastRole)

	// ---- 3. cache_hit 全量降序排列，看前 20 个 ----
	fmt.Println("\n========================================")
	fmt.Println("3. cache_hit 值降序排列（前 30）——找天花板")
	fmt.Println("========================================")
	fmt.Printf("%-4s %-10s %-17s %-6s %-6s %-8s %-10s %-8s\n",
		"#", "hit值", "msg_id", "轮数", "类型", "hit%", "prompt", "lastRole")
	sort.Slice(results, func(i, j int) bool { return results[i].CacheHit > results[j].CacheHit })
	for i := 0; i < 30 && i < len(results); i++ {
		r := results[i]
		typeLabel := "普通"
		if r.IsToolRound {
			typeLabel = "工具"
		}
		fmt.Printf("%-4d %-10d %-17s %-6d %-6s %7.1f%% %-10d %-8s\n",
			i+1, r.CacheHit, r.MsgID[:8], r.Rounds, typeLabel, r.HitPct, r.PromptTok, r.LastRole)
	}

	// ---- 4. 普通轮 cache_hit 分布 ----
	fmt.Println("\n========================================")
	fmt.Println("4. 普通轮（非工具）cache_hit 值分布")
	fmt.Println("========================================")
	nonTool := make(map[int]int)
	for _, r := range results {
		if !r.IsToolRound {
			nonTool[r.CacheHit]++
		}
	}
	var hitVals []int
	for v := range nonTool {
		hitVals = append(hitVals, v)
	}
	sort.Ints(hitVals)
	totalNonTool := float64(0)
	for _, c := range nonTool {
		totalNonTool += float64(c)
	}
	for _, v := range hitVals {
		cnt := nonTool[v]
		bar := ""
		barLen := int(float64(cnt) / totalNonTool * 50)
		for i := 0; i < barLen; i++ {
			bar += "█"
		}
		pct := float64(cnt) / totalNonTool * 100
		label := ""
		if v == 768 {
			label = " ← system prompt 基线"
		} else if v == 6656 {
			label = " ← 疑似缓存配额上限"
		}
		fmt.Printf("  %6d: %4d 次 (%5.1f%%) %s%s\n", v, cnt, pct, bar, label)
	}
}
