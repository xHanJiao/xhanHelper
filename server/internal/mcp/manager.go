package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"
)

// StatusCallback — MCP server 状态变更回调（Orchestrator 注册，转 SSE 事件）
type StatusCallback func(serverName, eventType, msg string)

// OnReadyFunc — server 就绪回调（Registry 注册工具用）
type OnReadyFunc func(serverName string, tools []ToolDef)

// Manager — MCP server 生命周期管理器
type Manager struct {
	config   Config
	servers  map[string]*MCPServer
	mu       sync.Mutex
	callback StatusCallback
	onReady  OnReadyFunc

	stopCh chan struct{}
}

// NewManager 创建 Manager
func NewManager(cfg Config) *Manager {
	cfg.Defaults()
	return &Manager{
		config:  cfg,
		servers: make(map[string]*MCPServer),
		stopCh:  make(chan struct{}),
	}
}

// SetStatusCallback 注册状态变更回调
func (m *Manager) SetStatusCallback(cb StatusCallback) {
	m.callback = cb
}

// SetOnReady 注册 server 就绪回调（Registry 调用以注册工具）
func (m *Manager) SetOnReady(fn OnReadyFunc) {
	m.onReady = fn
}

func (m *Manager) emitStatus(name, eventType, msg string) {
	if m.callback != nil {
		m.callback(name, eventType, msg)
	}
}

// EnsureRunning 懒启动语义：server 已就绪直接返回；启动中等待；未启动则启动
func (m *Manager) EnsureRunning(ctx context.Context, name string) (*MCPServer, error) {
	m.mu.Lock()

	s := m.servers[name]
	if s != nil {
		state := serverState(s.state.Load())
		m.mu.Unlock()
		if state == stateReady {
			return s, nil
		}
		if state == stateStarting {
			return nil, fmt.Errorf("server %q is starting", name)
		}
		// 其他状态视为需要重启
		s.Stop()
		m.mu.Lock()
		delete(m.servers, name)
	} else {
		m.mu.Unlock()
	}

	// 查找配置
	cfg, ok := m.findConfig(name)
	if !ok {
		return nil, fmt.Errorf("unknown mcp server: %s", name)
	}

	// 检查并发上限，必要时 LRU 抢占
	m.mu.Lock()
	if err := m.ensureCapacity(name); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	m.mu.Unlock()

	s = NewMCPServer(cfg)

	m.emitStatus(name, "starting", fmt.Sprintf("正在启动 %s 服务（首次使用，约需 3 秒）...", name))
	if err := s.Start(ctx); err != nil {
		log.Printf("[mcp] start %s error: %v", name, err)
		m.emitStatus(name, "error", fmt.Sprintf("无法启动 %s 服务：%v", name, err))
		return nil, err
	}

	m.mu.Lock()
	m.servers[name] = s
	m.mu.Unlock()

	m.emitStatus(name, "ready", fmt.Sprintf("%s 服务已就绪", name))
	if m.onReady != nil {
		m.onReady(name, s.ToolsCache)
	}
	return s, nil
}

// CallTool 调用 MCP 工具，自动触发懒启动
func (m *Manager) CallTool(ctx context.Context, serverName, toolName string, args map[string]interface{}) (json.RawMessage, error) {
	s, err := m.EnsureRunning(ctx, serverName)
	if err != nil {
		return nil, err
	}
	return s.CallTool(ctx, toolName, args)
}

// GetToolsSnapshot 获取 server 的工具缓存（用于 tool_retriever），未启动返回 nil
func (m *Manager) GetToolsSnapshot(name string) []ToolDef {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.servers[name]
	if s == nil || s.state.Load() != int32(stateReady) {
		return nil
	}
	return s.ToolsCache
}

// GetConfiguredServers 返回所有配置的 MCP server 元信息（用于 tool_retriever 发现）
func (m *Manager) GetConfiguredServers() []ServerConfig {
	return m.config.Servers
}

// GetAllSnapshots 返回所有已就绪 server 的工具快照（用于 tool_retriever 检索）
func (m *Manager) GetAllSnapshots() map[string][]ToolDef {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[string][]ToolDef)
	for name, s := range m.servers {
		if s.state.Load() == int32(stateReady) && len(s.ToolsCache) > 0 {
			result[name] = s.ToolsCache
		}
	}
	return result
}

// StartIdleReaper 启动空闲回收协程
func (m *Manager) StartIdleReaper(ctx context.Context) {
	go m.idleReaper(ctx)
}

// Shutdown 关闭所有 server
func (m *Manager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, s := range m.servers {
		log.Printf("[mcp] shutdown: stopping %s", name)
		s.Stop()
	}
	m.servers = make(map[string]*MCPServer)
}

func (m *Manager) findConfig(name string) (ServerConfig, bool) {
	for _, c := range m.config.Servers {
		if c.Name == name {
			return c, true
		}
	}
	return ServerConfig{}, false
}

// ensureCapacity 调用者需持有 m.mu。并发上限满时 LRU 抢占，无法抢占则返 error
func (m *Manager) ensureCapacity(requestingName string) error {
	active := 0
	for _, s := range m.servers {
		if s.state.Load() == int32(stateReady) || s.state.Load() == int32(stateStarting) {
			active++
		}
	}
	if active < m.config.MaxConcurrent {
		return nil
	}

	// 找 idle server（空闲超时）
	for name, s := range m.servers {
		if s.state.Load() != int32(stateReady) {
			continue
		}
		if s.inFlight.Load() > 0 {
			continue
		}
		idleSecs := m.config.Servers[0].IdleTimeoutSecs
		for _, c := range m.config.Servers {
			if c.Name == name {
				idleSecs = c.IdleTimeoutSecs
				break
			}
		}
		if time.Duration(time.Now().UnixNano()-s.lastUsedAt.Load())*time.Nanosecond > time.Duration(idleSecs)*time.Second {
			log.Printf("[mcp] LRU: stopping idle server %s for %s", name, requestingName)
			m.emitStatus(name, "stopped", fmt.Sprintf("%s 已关闭（空闲），正在启动 %s...", name, requestingName))
			s.Stop()
			delete(m.servers, name)
			return nil
		}
	}

	// 找最久未用且无 inFlight 的 server（LRU）
	var oldestName string
	var oldestTime int64
	for name, s := range m.servers {
		if s.state.Load() != int32(stateReady) {
			continue
		}
		if s.inFlight.Load() > 0 {
			continue
		}
		lu := s.lastUsedAt.Load()
		if oldestName == "" || lu < oldestTime {
			oldestName = name
			oldestTime = lu
		}
	}
	if oldestName != "" {
		log.Printf("[mcp] LRU: stopping server %s for %s", oldestName, requestingName)
		m.emitStatus(oldestName, "preempt", fmt.Sprintf("正在关闭 %s 服务以释放资源，启动 %s 服务...", oldestName, requestingName))
		m.servers[oldestName].Stop()
		delete(m.servers, oldestName)
		return nil
	}

	return fmt.Errorf("all mcp servers busy (inFlight > 0)")
}

func (m *Manager) idleReaper(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.reapIdle()
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		}
	}
}

func (m *Manager) reapIdle() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, s := range m.servers {
		if s.state.Load() != int32(stateReady) {
			continue
		}
		if s.inFlight.Load() > 0 {
			continue
		}
		var idleSecs int
		for _, c := range m.config.Servers {
			if c.Name == name {
				idleSecs = c.IdleTimeoutSecs
				break
			}
		}
		if idleSecs <= 0 {
			continue
		}
		idle := time.Duration(time.Now().UnixNano()-s.lastUsedAt.Load()) * time.Nanosecond
		if idle > time.Duration(idleSecs)*time.Second {
			log.Printf("[mcp] idle reaper: stopping %s (idle %v)", name, idle)
			s.Stop()
			delete(m.servers, name)
			// 空闲回收不回调——对话已结束，无 SSE 消费者
		}
	}
}
