package agent

import (
	"encoding/json"
	"fmt"

	"xhan-helper/internal/agent/retrieval"
	"xhan-helper/internal/model"
	"xhan-helper/internal/tool"
)

// searchSemantic — 语义检索。复用 retrieval 的 EntitySource（与 unified_search 同源）。
func (s *recordLoopState) searchSemantic(ec *tool.ExecuteContext, typ string, args json.RawMessage) *tool.Result {
	var a struct {
		Query string `json:"query"`
		TopK  int    `json:"top_k"`
	}
	if err := decodeRecordArgs(args, &a); err != nil {
		return errResult(err)
	}
	if a.Query == "" {
		return &tool.Result{Success: false, Error: "query 不能为空"}
	}
	topK := clampTopK(a.TopK)

	vec, err := s.deps.Embedder.Embed(ec.Ctx, a.Query)
	if err != nil {
		return errResult(fmt.Errorf("embed query: %w", err))
	}
	opts := retrieval.SearchOptions{
		Query: a.Query, UserID: s.userID, Embedding: vec,
		Memory: retrieval.SourceConfig{Threshold: s.cfg.NeighborThresh, Limit: topK},
		Note:   retrieval.SourceConfig{Threshold: s.cfg.NeighborThresh, Limit: topK},
	}
	var packets []retrieval.ContextPacket
	if typ == model.EntityTypeNote {
		packets, _, err = s.deps.NoteSource.Fetch(ec.Ctx, opts)
	} else {
		packets, _, err = s.deps.MemSource.Fetch(ec.Ctx, opts)
	}
	if err != nil {
		return errResult(fmt.Errorf("fetch neighbors: %w", err))
	}
	return s.packetsResult(typ, packets, true)
}

// searchKeyword — 关键词检索。走 repo 的 LIKE 查询（字面量专长）。
func (s *recordLoopState) searchKeyword(ec *tool.ExecuteContext, typ string, args json.RawMessage) *tool.Result {
	var a struct {
		Keywords string `json:"keywords"`
		Topic    string `json:"topic"`
		TopK     int    `json:"top_k"`
	}
	if err := decodeRecordArgs(args, &a); err != nil {
		return errResult(err)
	}
	keywords := splitKeywords(a.Keywords)
	if len(keywords) == 0 {
		return &tool.Result{Success: false, Error: "keywords 不能为空"}
	}
	topK := clampTopK(a.TopK)

	out := make([]recordNeighbor, 0, topK)
	switch typ {
	case model.EntityTypeMemory:
		mems, _, err := s.deps.MemRepo.SearchMemoriesByCursor(ec.Ctx, s.userID, keywords, a.Topic, nil, nil, nil, topK)
		if err != nil {
			return errResult(fmt.Errorf("search memories: %w", err))
		}
		for _, m := range mems {
			out = append(out, recordNeighbor{ID: m.ID, Type: typ, Topic: m.Topic, Content: m.MemoryValue})
		}
	case model.EntityTypeNote:
		notes, _, err := s.deps.NoteRepo.SearchNotesByCursor(ec.Ctx, s.userID, keywords, a.Topic, nil, nil, nil, topK)
		if err != nil {
			return errResult(fmt.Errorf("search notes: %w", err))
		}
		for _, n := range notes {
			out = append(out, recordNeighbor{ID: n.ID, Type: typ, Topic: n.Topic, Content: n.Content})
		}
	}
	return s.neighborsResult(out, false)
}

// packetsResult 把 retrieval 的 ContextPacket 转成给模型的近邻 JSON，并并入近邻集合。
func (s *recordLoopState) packetsResult(typ string, packets []retrieval.ContextPacket, includeScore bool) *tool.Result {
	out := make([]recordNeighbor, 0, len(packets))
	for _, p := range packets {
		n := recordNeighbor{ID: p.ID, Type: typ, Score: p.Score}
		if meta, ok := p.Meta.(retrieval.EntityMeta); ok {
			n.Topic, n.Content = meta.Topic, meta.Content
		}
		out = append(out, n)
	}
	return s.neighborsResult(out, includeScore)
}

// neighborsResult 序列化近邻，并把它们并入 idSet/idType（追加检索也能成为合法的 target_id 来源）。
func (s *recordLoopState) neighborsResult(items []recordNeighbor, includeScore bool) *tool.Result {
	type item struct {
		ID      string  `json:"id"`
		Type    string  `json:"type"`
		Topic   string  `json:"topic,omitempty"`
		Content string  `json:"content"`
		Score   float64 `json:"score,omitempty"`
	}
	list := make([]item, 0, len(items))
	for _, n := range items {
		s.idSet[n.ID] = true
		s.idType[n.ID] = n.Type
		it := item{ID: n.ID, Type: n.Type, Topic: n.Topic, Content: n.Content}
		if includeScore {
			it.Score = roundScore(n.Score)
		}
		list = append(list, it)
	}
	data, err := json.Marshal(list)
	if err != nil {
		return errResult(fmt.Errorf("marshal neighbors: %w", err))
	}
	return &tool.Result{Success: true, Data: data}
}
