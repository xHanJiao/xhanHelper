package agent

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/tool"
)

// PlanEvent — 计划执行中的事件
type PlanEvent struct {
	Type    string `json:"type"` // plan | plan_node_start | plan_node_done | plan_node_fail | text_delta | plan_done
	NodeID  string `json:"node_id,omitempty"`
	Label   string `json:"label,omitempty"`
	Content string `json:"content,omitempty"`
	Result  string `json:"result,omitempty"`
	Error   string `json:"error,omitempty"`
	Plan    *Plan  `json:"plan,omitempty"`
}

// PlanExecutor — Plan 并发执行器
type PlanExecutor struct {
	registry *tool.Registry
	llm      llm.LLMClient
}

// NewPlanExecutor 创建执行器
func NewPlanExecutor(registry *tool.Registry, llmClient llm.LLMClient) *PlanExecutor {
	return &PlanExecutor{registry: registry, llm: llmClient}
}

// Execute 并发执行 Plan，返回事件 channel
func (e *PlanExecutor) Execute(ctx context.Context, plan *Plan, deviceID string) <-chan PlanEvent {
	events := make(chan PlanEvent, 64)

	go func() {
		defer close(events)

		// 1. 发整个 plan
		log.Printf("[plan] start plan=%s nodes=%d", plan.ID[:8], len(plan.Nodes))
		plan.Status = "running"
		for i := range plan.Nodes {
			plan.Nodes[i].Status = "pending"
		}
		events <- PlanEvent{Type: "plan", Plan: plan}

		done := make(map[string]bool)
		iterCount := 0

		for plan.Status == "running" && iterCount < 20 {
			iterCount++
			ready := e.findReadyNodes(plan, done)
			if len(ready) == 0 {
				if len(done) == len(plan.Nodes) {
					break // 全部完成
				}
				// 有节点没完成但不是等待中 → 死锁，标记失败
				for i := range plan.Nodes {
					if !done[plan.Nodes[i].ID] && plan.Nodes[i].Status == "pending" {
						plan.Nodes[i].Status = "failed"
						plan.Nodes[i].Error = "dependency deadlock"
					}
				}
				break
			}

			log.Printf("[plan] iteration=%d ready=%d done=%d/%d", iterCount, len(ready), len(done), len(plan.Nodes))

			// 2. 并发执行所有就绪节点
			var wg sync.WaitGroup
			for _, node := range ready {
				wg.Add(1)
				go func(n PlanNode) {
					defer wg.Done()
					e.executeNode(ctx, n, deviceID, events)
				}(node)
			}
			wg.Wait()
		}

		plan.Status = "done"
		log.Printf("[plan] done plan=%s", plan.ID[:8])
		events <- PlanEvent{Type: "plan_done"}
	}()

	return events
}

// findReadyNodes 找出所有依赖已满足的 pending 节点
func (e *PlanExecutor) findReadyNodes(plan *Plan, done map[string]bool) []PlanNode {
	var ready []PlanNode
	for i := range plan.Nodes {
		if done[plan.Nodes[i].ID] || plan.Nodes[i].Status == "running" {
			continue
		}
		if plan.Nodes[i].Status != "pending" {
			continue
		}
		// 检查依赖
		depsMet := true
		for _, dep := range plan.Nodes[i].DependsOn {
			if !done[dep] {
				depsMet = false
				break
			}
		}
		if depsMet {
			ready = append(ready, plan.Nodes[i])
		}
	}
	return ready
}

// executeNode 执行单个节点
func (e *PlanExecutor) executeNode(ctx context.Context, node PlanNode, deviceID string, events chan<- PlanEvent) {
	now := time.Now()
	node.Status = "running"
	node.StartedAt = &now

	log.Printf("[plan] node_start id=%s label=%s type=%s", node.ID[:8], node.Label, node.Type)
	events <- PlanEvent{Type: "plan_node_start", NodeID: node.ID, Label: node.Label}

	switch node.Type {
	case "tool_call":
		result := e.registry.ExecuteByName(&tool.ExecuteContext{Ctx: ctx, DeviceID: deviceID},
			node.Tool, node.Function, json.RawMessage(mustMarshal(node.Args)))

		done := time.Now()
		node.EndedAt = &done
		if result.Success {
			node.Status = "done"
			node.Result = string(result.Data)
			log.Printf("[plan] node_done id=%s duration=%v", node.ID[:8], done.Sub(now))
			events <- PlanEvent{Type: "plan_node_done", NodeID: node.ID, Result: node.Result}
		} else {
			node.Status = "failed"
			node.Error = result.Error
			log.Printf("[plan] node_fail id=%s error=%s duration=%v", node.ID[:8], result.Error, done.Sub(now))
			events <- PlanEvent{Type: "plan_node_fail", NodeID: node.ID, Error: result.Error}
		}

	case "llm":
		// LLM 总结节点：收集之前的节点结果，让 LLM 输出
		log.Printf("[plan] llm_node id=%s", node.ID[:8])
		// 简化处理：先标记完成
		done := time.Now()
		node.EndedAt = &done
		node.Status = "done"
		events <- PlanEvent{Type: "plan_node_done", NodeID: node.ID}

	case "reply":
		// 回复节点：由 orchestrator 在 plan_done 后处理
		done := time.Now()
		node.EndedAt = &done
		node.Status = "done"
		events <- PlanEvent{Type: "plan_node_done", NodeID: node.ID}

	default:
		done := time.Now()
		node.EndedAt = &done
		node.Status = "done"
		log.Printf("[plan] node_done(unknown) id=%s type=%s", node.ID[:8], node.Type)
		events <- PlanEvent{Type: "plan_node_done", NodeID: node.ID}
	}
}

func mustMarshal(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}
