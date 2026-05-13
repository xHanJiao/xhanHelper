package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// 运行时状态
type serverState int32

const (
	stateStopped serverState = iota
	stateStarting
	stateReady
	stateError
)

func (s serverState) String() string {
	switch s {
	case stateStopped:
		return "stopped"
	case stateStarting:
		return "starting"
	case stateReady:
		return "ready"
	case stateError:
		return "error"
	default:
		return "unknown"
	}
}

// jsonrpcRequest — JSON-RPC 2.0 请求
type jsonrpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// jsonrpcResponse — JSON-RPC 2.0 响应
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// jsonrpcNotification — JSON-RPC 2.0 通知（无 id）
type jsonrpcNotification struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// ToolDef — tools/list 返回的工具定义
type ToolDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// MCPServer — 单个 MCP server 运行时
type MCPServer struct {
	Config ServerConfig

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	rpcID   atomic.Int64
	pending map[int64]chan *jsonrpcResponse
	mu      sync.Mutex

	ToolsCache []ToolDef
	lastUsedAt atomic.Int64 // unix nano
	inFlight   atomic.Int32
	state      atomic.Int32 // serverState

	stopCh    chan struct{}
	stoppedCh chan struct{}
	stopOnce  sync.Once
}

// NewMCPServer 创建未启动的 server
func NewMCPServer(cfg ServerConfig) *MCPServer {
	return &MCPServer{
		Config:    cfg,
		pending:   make(map[int64]chan *jsonrpcResponse),
		stopCh:    make(chan struct{}),
		stoppedCh: make(chan struct{}),
	}
}

// Start 启动子进程，完成 MCP 握手
func (s *MCPServer) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.state.Load() != int32(stateStopped) {
		s.mu.Unlock()
		return fmt.Errorf("server %q already started", s.Config.Name)
	}
	s.state.Store(int32(stateStarting))
	s.mu.Unlock()

	cmd := exec.CommandContext(ctx, s.Config.Command, s.Config.Args...)
	cmd.Env = cmd.Environ()
	for k, v := range s.Config.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = nil // discard

	if err := cmd.Start(); err != nil {
		s.state.Store(int32(stateError))
		return fmt.Errorf("start %q: %w", s.Config.Name, err)
	}

	s.cmd = cmd
	s.stdin = stdin
	s.stdout = stdout
	s.lastUsedAt.Store(time.Now().UnixNano())

	go s.readLoop()

	// —— MCP 握手 ——
	initResp, err := s.call(ctx, "initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]string{
			"name":    "xhanHelper",
			"version": "0.1.0",
		},
	})
	if err != nil {
		s.Stop()
		return fmt.Errorf("initialize: %w", err)
	}
	log.Printf("[mcp] %s: initialize response: %s", s.Config.Name, string(initResp))

	// 发送 initialized 通知
	s.sendNotification("notifications/initialized", nil)

	// 获取工具列表
	toolsResp, err := s.call(ctx, "tools/list", nil)
	if err != nil {
		s.Stop()
		return fmt.Errorf("tools/list: %w", err)
	}

	var listResult struct {
		Tools []ToolDef `json:"tools"`
	}
	if err := json.Unmarshal(toolsResp, &listResult); err != nil {
		s.Stop()
		return fmt.Errorf("parse tools/list: %w", err)
	}
	s.ToolsCache = listResult.Tools

	s.state.Store(int32(stateReady))
	log.Printf("[mcp] %s: ready with %d tools", s.Config.Name, len(s.ToolsCache))
	return nil
}

// Stop 优雅停止子进程（幂等，可重复调用）
func (s *MCPServer) Stop() {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.state.Store(int32(stateStopped))
		s.mu.Unlock()

		if s.cmd != nil && s.cmd.Process != nil {
			s.cmd.Process.Signal(syscall.SIGTERM)
			done := make(chan error, 1)
			go func() { done <- s.cmd.Wait() }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				s.cmd.Process.Kill()
				<-done
			}
		}
		close(s.stoppedCh)
	})
}

// call 发起 JSON-RPC 调用，等待响应
func (s *MCPServer) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	id := s.rpcID.Add(1)
	ch := make(chan *jsonrpcResponse, 1)

	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
	}()

	req := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	if err := s.writeJSON(req); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("rpc error: %s (code=%d)", resp.Error.Message, resp.Error.Code)
		}
		return resp.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// CallTool 调用指定工具
func (s *MCPServer) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (json.RawMessage, error) {
	if s.state.Load() != int32(stateReady) {
		return nil, fmt.Errorf("server %q not ready", s.Config.Name)
	}
	s.inFlight.Add(1)
	defer s.inFlight.Add(-1)
	s.lastUsedAt.Store(time.Now().UnixNano())

	return s.call(ctx, "tools/call", map[string]interface{}{
		"name":      toolName,
		"arguments": args,
	})
}

func (s *MCPServer) sendNotification(method string, params interface{}) error {
	notif := jsonrpcNotification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	return s.writeJSON(notif)
}

func (s *MCPServer) writeJSON(v interface{}) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.stdin.Write(b)
	return err
}

func (s *MCPServer) readLoop() {
	defer func() { s.state.Store(int32(stateStopped)) }()
	scanner := bufio.NewScanner(s.stdout)
	for scanner.Scan() {
		line := scanner.Bytes()
		var resp jsonrpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			log.Printf("[mcp] %s: parse response error: %v", s.Config.Name, err)
			continue
		}
		if resp.ID == 0 {
			continue // notification, skip
		}
		s.mu.Lock()
		ch, ok := s.pending[resp.ID]
		s.mu.Unlock()
		if ok {
			ch <- &resp
		}
	}
}
