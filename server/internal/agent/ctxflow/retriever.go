package ctxflow

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"

	"xhan-helper/internal/agent/retrieval"
)

// ContextRetriever 并发运行所有 ContextSource，返回按 type 分组的结果。
type ContextRetriever struct {
	sources []retrieval.ContextSource
	config  *RetrieveConfig
}

// NewContextRetriever 创建检索器。
func NewContextRetriever(sources []retrieval.ContextSource, cfg *RetrieveConfig) *ContextRetriever {
	if cfg == nil {
		c := DefaultRetrieveConfig()
		cfg = &c
	}
	return &ContextRetriever{
		sources: sources,
		config:  cfg,
	}
}

// Retrieve 并发运行所有 source，返回按 type 分组的结果。
// opts.Embedding 为空时直接返回空结果（不报错）。
// 单个 source panic 或出错不影响其他 source。
func (r *ContextRetriever) Retrieve(ctx context.Context, opts retrieval.SearchOptions) (*RetrieveResult, error) {
	if len(opts.Embedding) == 0 {
		return &RetrieveResult{}, nil
	}

	r.fillDefaults(&opts)

	// 并发检索，每个 goroutine 有 recover 保护
	type slot struct {
		name     string
		packets  []retrieval.ContextPacket
		nearMiss []retrieval.NearMissItem
		err      error
	}
	slots := make([]slot, len(r.sources))
	var wg sync.WaitGroup
	for i, src := range r.sources {
		wg.Add(1)
		go func(i int, src retrieval.ContextSource) {
			defer wg.Done()
			defer func() {
				if p := recover(); p != nil {
					log.Printf("[retriever] %s panic: %v", src.Name(), p)
					slots[i] = slot{
						name: src.Name(),
						err:  fmt.Errorf("panic: %v", p),
					}
				}
			}()
			packets, nearMiss, err := src.Fetch(ctx, opts)
			slots[i] = slot{
				name:     src.Name(),
				packets:  packets,
				nearMiss: nearMiss,
				err:      err,
			}
		}(i, src)
	}
	wg.Wait()

	// 收集结果
	trace := &RetrieveTrace{}
	packetsByType := make(map[string][]retrieval.ContextPacket)

	for _, s := range slots {
		if s.err != nil {
			log.Printf("[retriever] %s error: %v", s.name, s.err)
			trace.Providers = append(trace.Providers, ProviderTrace{
				Name:  s.name,
				Type:  "search",
				Input: &TraceInput{Query: opts.Query},
				Error: s.err.Error(),
			})
			continue
		}

		// 构建 trace
		traceItems := buildTraceItems(s.packets, s.nearMiss)
		trace.Providers = append(trace.Providers, ProviderTrace{
			Name:  s.name,
			Type:  "search",
			Input: &TraceInput{Query: opts.Query},
			Items: traceItems,
		})

		if len(s.packets) == 0 {
			continue
		}

		// 按 type 分组
		for _, pkt := range s.packets {
			packetsByType[pkt.Type] = append(packetsByType[pkt.Type], pkt)
		}
	}

	// 组内按 Score 降序统一排序（event 除外：EventSource 已按 pinned/形态/分数排好，
	// 再按裸 score 重排会让 score=0 的 sticky/carried 事件排到最后，被预算优先砍掉）
	for typ, packets := range packetsByType {
		if typ == TypeEvent {
			continue
		}
		sort.Slice(packets, func(i, j int) bool {
			return packets[i].Score > packets[j].Score
		})
	}

	return &RetrieveResult{
		Packets: packetsByType,
		Trace:   trace,
	}, nil
}

// fillDefaults 用 RetrieveConfig 填充 opts 中零值的 SourceConfig。
func (r *ContextRetriever) fillDefaults(opts *retrieval.SearchOptions) {
	opts.Memory.Resolve(r.config.Memory)
	opts.Note.Resolve(r.config.Note)
	opts.History.Resolve(r.config.History)
	opts.Summary.Resolve(r.config.Summary)
	opts.Event.Resolve(r.config.Event)
}

func buildTraceItems(packets []retrieval.ContextPacket, nearMiss []retrieval.NearMissItem) []TraceItem {
	items := make([]TraceItem, 0, len(packets)+len(nearMiss))

	for _, pkt := range packets {
		text := truncateTraceText(pkt.Text, 120)
		items = append(items, TraceItem{
			ID:    pkt.ID,
			Text:  text,
			Score: pkt.Score,
		})
	}

	for _, nm := range nearMiss {
		text := truncateTraceText(nm.Text, 120)
		items = append(items, TraceItem{
			ID:            nm.ID,
			Text:          text,
			Score:         nm.VectorScore,
			DiscardReason: nm.Reason,
		})
	}

	return items
}

func truncateTraceText(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
