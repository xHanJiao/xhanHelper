package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// BJ 北京时间（Asia/Shanghai），格式化时间前先 .In(BJ) 统一时区
var BJ *time.Location

func init() {
	var err error
	BJ, err = time.LoadLocation("Asia/Shanghai")
	if err != nil {
		panic(fmt.Sprintf("load timezone Asia/Shanghai: %v", err))
	}
}

// Config — 顶层配置
type Config struct {
	Server     ServerConfig     `yaml:"server"`
	DB         DBConfig         `yaml:"db"`
	LLM        LLMConfig        `yaml:"llm"`
	Embedding  EmbeddingConfig  `yaml:"embedding"`
	Reflection ReflectionConfig `yaml:"reflection"`
	Retrieval  RetrievalConfig  `yaml:"retrieval"`
	Search     SearchConfig     `yaml:"search"`
	Chat       ChatConfig       `yaml:"chat"`
	Share      ShareConfig      `yaml:"share"`
	Snowflake  SnowflakeConfig  `yaml:"snowflake"`
	MCP        MCPConfig        `yaml:"mcp"`
}

// ChatConfig — 对话配置
type ChatConfig struct {
	HistoryRounds int `yaml:"history_rounds"` // 加载历史消息轮数，默认 10
}

// ShareConfig — 分享配置
type ShareConfig struct {
	DefaultValidDays int `yaml:"default_valid_days"` // 分享链接默认有效天数，默认 7
}

// MCPServerCfg — 单个 MCP server 配置
type MCPServerCfg struct {
	Name            string            `yaml:"name"`
	Command         string            `yaml:"command"`
	Args            []string          `yaml:"args"`
	Env             map[string]string `yaml:"env"`
	IdleTimeoutSecs int               `yaml:"idle_timeout_secs"`
	Description     string            `yaml:"description"`
}

// MCPConfig — MCP 配置
type MCPConfig struct {
	MaxConcurrent int            `yaml:"max_concurrent"`
	Servers       []MCPServerCfg `yaml:"servers"`
}

// SnowflakeConfig — Snowflake ID 配置
type SnowflakeConfig struct {
	NodeID int64 `yaml:"node_id"`
}

// ServerConfig — HTTP 服务配置
type ServerConfig struct {
	Port                int      `yaml:"port"`
	Mode                string   `yaml:"mode"`                  // debug | release
	RegistrationEnabled *bool    `yaml:"registration_enabled"`  // 是否允许新用户注册
	AllowedUsers        []string `yaml:"allowed_users"`         // 允许注册的用户名列表（空=全部允许）
	MemoryAutoExtract   bool     `yaml:"memory_auto_extract"`   // 是否自动提取记忆
	MemoryDupeThreshold float64  `yaml:"memory_dupe_threshold"` // 记忆去重相似度阈值，默认 0.85
	SidecarURL          string   `yaml:"sidecar_url"`           // Python sidecar MCP HTTP 地址
	TokenTTLHours       int      `yaml:"token_ttl_hours"`       // token 过期时间（小时），默认 720
	AgentRouteTriggers  []int    `yaml:"agent_route_triggers"`  // [已废弃] 异步路由触发消息数
	AgentRouteInterval  int      `yaml:"agent_route_interval"`  // 自动路由间隔：每 N 条用户消息触发一次，0=禁用，默认 5
	MaxTokensPerUser    int      `yaml:"max_tokens_per_user"`   // 同一用户最大 token 数，默认 4
	AdminUsers          []string `yaml:"admin_users"`           // 管理员用户 ID 列表
}

// DBConfig — 数据库配置
type DBConfig struct {
	Driver   string `yaml:"driver"` // mysql | sqlite
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Database string `yaml:"database"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Charset  string `yaml:"charset"`
}

// DSN 返回数据库连接串
func (d DBConfig) DSN() string {
	if d.Driver == "sqlite" {
		return d.Database // SQLite 直接返回文件路径
	}
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=%s&parseTime=true",
		d.User, d.Password, d.Host, d.Port, d.Database, d.Charset)
}

// LLMConfig — LLM 厂商配置
type VerificationConfig struct {
	Enabled    bool   `yaml:"enabled"`     // 是否开启响应检验
	Model      string `yaml:"model"`       // 检验用模型：flash | pro，默认 flash
	MaxRetries int    `yaml:"max_retries"` // 最大重检验次数，默认 1
}

type LLMConfig struct {
	Provider           string             `yaml:"provider"` // doubao | deepseek
	APIKey             string             `yaml:"api_key"`
	Model              string             `yaml:"model"`                 // 弱模型（日常使用）
	StrongModel        string             `yaml:"strong_model"`          // 强模型（深度思考模式，可选，默认同 Model）
	BaseURL            string             `yaml:"base_url"`              // LLM API 地址
	MaxIterations      int                `yaml:"max_iterations"`        // ReAct 最大迭代次数（默认 8）
	DefaultTemperature *float64           `yaml:"default_temperature"`   // 默认采样温度，nil 则用 LLM 自身默认
	DefaultMaxTokens   *int               `yaml:"default_max_tokens"`    // 默认最大输出 token，nil 则用 LLM 自身默认
	MaxToolResultChars int                `yaml:"max_tool_result_chars"` // 工具结果传入 LLM 的最大字符数，超出截断，默认 2000
	ToolMaxConcurrency int                `yaml:"tool_max_concurrency"`  // 工具并发执行上限，0=不限制，默认 0
	Verification       VerificationConfig `yaml:"verification"`          // 响应检验配置
	ExtractMsgLimit    int                `yaml:"extract_msg_limit"`     // 记忆提取每次最多取的消息数，默认 50
	UseNativeFC        bool               `yaml:"use_native_fc"`         // 是否使用原生 function calling（默认 true）
}

// EmbeddingConfig — 向量化配置
type EmbeddingConfig struct {
	APIKey              string `yaml:"api_key"`
	BaseURL             string `yaml:"base_url"`              // 默认 https://ark.cn-beijing.volces.com
	Model               string `yaml:"model"`                 // 默认 doubao-embedding
	EnableHistorySearch bool   `yaml:"enable_history_search"` // 是否检索历史对话
}

// SourceWeights — 单个检索源的配置
type SourceWeights struct {
	FreshnessDecay float64 `yaml:"freshness_decay"`
	Threshold      float64 `yaml:"threshold"`
	Limit          int     `yaml:"limit"`
}

// RetrievalConfig — 记忆检索配置
type RetrievalConfig struct {
	FreshnessDecay float64       `yaml:"freshness_decay"` // 时间衰减系数，保留作为默认值
	Memory         SourceWeights `yaml:"memory"`
	Note           SourceWeights `yaml:"note"`
	History        SourceWeights `yaml:"history"`
}

// SearchConfig — 搜索工具配置（通用，支持多 provider）
type SearchConfig struct {
	Provider   string `yaml:"provider"`
	APIKey     string `yaml:"api_key"`
	BaseURL    string `yaml:"base_url"`
	DailyLimit int    `yaml:"daily_limit"`
}

// ReflectionConfig — 反思系统配置
type ReflectionConfig struct {
	LaunchMaxIterations         int            `yaml:"launch_max_iterations"`          // 启动 Agent 最大轮次，默认 8
	AnalystMaxIterations        int            `yaml:"analyst_max_iterations"`         // ThemeAnalyst 内部最大轮次，默认 3
	DiscoverMaxIterations       int            `yaml:"discover_max_iterations"`        // ThemeDiscoverer 内部最大轮次，默认 3
	ClusterThreshold            float64        `yaml:"cluster_threshold"`              // topic 聚类余弦相似度阈值，默认 0.70
	PostChatDelaySeconds        int            `yaml:"post_chat_delay_seconds"`        // 对话后延迟触发秒数，默认 1800（30分钟）
	DailySchedules              []string       `yaml:"daily_schedules"`                // 早晚回顾定时触发时间列表，默认 ["09:00","20:00"]
	DeliverIntervalMinutes      int            `yaml:"deliver_interval_minutes"`       // 投递窗口间隔分钟，默认 60
	MaxDeliveryRetries          int            `yaml:"max_delivery_retries"`           // 投递重试上限，默认 3
	ActiveDurationDays          map[string]int `yaml:"active_duration_days"`           // active 各 duration 的衰减天数，默认 long:90 medium:30 short:14 ephemeral:7
	MissionCheckIntervalMinutes int            `yaml:"mission_check_interval_minutes"` // Mission 检查间隔，默认 30 分钟
}

// Load 加载配置：base YAML + env YAML 合并 → 环境变量替换
func Load() (*Config, error) {
	env := os.Getenv("APP_ENV")
	if env == "" {
		env = "dev"
	}

	cfg, err := loadFile("conf/config.yaml")
	if err != nil {
		return nil, fmt.Errorf("load config.yaml: %w", err)
	}

	envPath := fmt.Sprintf("conf/config.%s.yaml", env)
	if envCfg, err := loadFile(envPath); err == nil {
		merge(cfg, envCfg)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("load %s: %w", envPath, err)
	}

	resolveEnvVars(cfg)

	if err := validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// loadFile 读取 YAML 并展开 ${VAR} 占位符
func loadFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	expanded := os.Expand(string(data), func(key string) string {
		return os.Getenv(key)
	})
	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// merge 将 src 中的非零值覆盖到 dst
func merge(dst, src *Config) {
	if src.Server.Port != 0 {
		dst.Server.Port = src.Server.Port
	}
	if src.Server.Mode != "" {
		dst.Server.Mode = src.Server.Mode
	}
	if src.Server.RegistrationEnabled != nil {
		dst.Server.RegistrationEnabled = src.Server.RegistrationEnabled
	}
	if len(src.Server.AllowedUsers) > 0 {
		dst.Server.AllowedUsers = src.Server.AllowedUsers
	}
	dst.Server.MemoryAutoExtract = src.Server.MemoryAutoExtract
	if src.Server.MemoryDupeThreshold > 0 {
		dst.Server.MemoryDupeThreshold = src.Server.MemoryDupeThreshold
	}
	if src.Server.SidecarURL != "" {
		dst.Server.SidecarURL = src.Server.SidecarURL
	}
	if src.Server.TokenTTLHours != 0 {
		dst.Server.TokenTTLHours = src.Server.TokenTTLHours
	}
	if src.Server.MaxTokensPerUser != 0 {
		dst.Server.MaxTokensPerUser = src.Server.MaxTokensPerUser
	}
	if len(src.Server.AdminUsers) > 0 {
		dst.Server.AdminUsers = src.Server.AdminUsers
	}
	if len(src.Server.AgentRouteTriggers) > 0 {
		dst.Server.AgentRouteTriggers = src.Server.AgentRouteTriggers
	}
	if src.Server.AgentRouteInterval != 0 {
		dst.Server.AgentRouteInterval = src.Server.AgentRouteInterval
	}
	if src.DB.Driver != "" {
		dst.DB.Driver = src.DB.Driver
	}
	if src.DB.Host != "" {
		dst.DB.Host = src.DB.Host
	}
	if src.DB.Port != 0 {
		dst.DB.Port = src.DB.Port
	}
	if src.DB.Database != "" {
		dst.DB.Database = src.DB.Database
	}
	if src.DB.User != "" {
		dst.DB.User = src.DB.User
	}
	if src.DB.Password != "" {
		dst.DB.Password = src.DB.Password
	}
	if src.DB.Charset != "" {
		dst.DB.Charset = src.DB.Charset
	}
	if src.LLM.Provider != "" {
		dst.LLM.Provider = src.LLM.Provider
	}
	if src.LLM.APIKey != "" {
		dst.LLM.APIKey = src.LLM.APIKey
	}
	if src.LLM.Model != "" {
		dst.LLM.Model = src.LLM.Model
	}
	if src.LLM.BaseURL != "" {
		dst.LLM.BaseURL = src.LLM.BaseURL
	}
	if src.LLM.MaxIterations != 0 {
		dst.LLM.MaxIterations = src.LLM.MaxIterations
	}
	if src.LLM.StrongModel != "" {
		dst.LLM.StrongModel = src.LLM.StrongModel
	}
	if src.LLM.DefaultTemperature != nil {
		dst.LLM.DefaultTemperature = src.LLM.DefaultTemperature
	}
	if src.LLM.DefaultMaxTokens != nil {
		dst.LLM.DefaultMaxTokens = src.LLM.DefaultMaxTokens
	}
	if src.LLM.MaxToolResultChars != 0 {
		dst.LLM.MaxToolResultChars = src.LLM.MaxToolResultChars
	}
	if src.LLM.ExtractMsgLimit != 0 {
		dst.LLM.ExtractMsgLimit = src.LLM.ExtractMsgLimit
	}
	if src.Embedding.APIKey != "" {
		dst.Embedding.APIKey = src.Embedding.APIKey
	}
	if src.Embedding.BaseURL != "" {
		dst.Embedding.BaseURL = src.Embedding.BaseURL
	}
	if src.Embedding.Model != "" {
		dst.Embedding.Model = src.Embedding.Model
	}
	if src.Snowflake.NodeID != 0 {
		dst.Snowflake.NodeID = src.Snowflake.NodeID
	}
	if src.Retrieval.FreshnessDecay != 0 {
		dst.Retrieval.FreshnessDecay = src.Retrieval.FreshnessDecay
	}
	mergeSourceWeights(&dst.Retrieval.Memory, &src.Retrieval.Memory)
	mergeSourceWeights(&dst.Retrieval.Note, &src.Retrieval.Note)
	mergeSourceWeights(&dst.Retrieval.History, &src.Retrieval.History)
	if src.Reflection.LaunchMaxIterations != 0 {
		dst.Reflection.LaunchMaxIterations = src.Reflection.LaunchMaxIterations
	}
	if src.Reflection.AnalystMaxIterations != 0 {
		dst.Reflection.AnalystMaxIterations = src.Reflection.AnalystMaxIterations
	}
	if src.Reflection.DiscoverMaxIterations != 0 {
		dst.Reflection.DiscoverMaxIterations = src.Reflection.DiscoverMaxIterations
	}
	if src.Reflection.ClusterThreshold != 0 {
		dst.Reflection.ClusterThreshold = src.Reflection.ClusterThreshold
	}
	if src.Reflection.PostChatDelaySeconds != 0 {
		dst.Reflection.PostChatDelaySeconds = src.Reflection.PostChatDelaySeconds
	}
	if len(src.Reflection.DailySchedules) > 0 {
		dst.Reflection.DailySchedules = src.Reflection.DailySchedules
	}
	if src.Reflection.DeliverIntervalMinutes != 0 {
		dst.Reflection.DeliverIntervalMinutes = src.Reflection.DeliverIntervalMinutes
	}
	if src.Reflection.MaxDeliveryRetries != 0 {
		dst.Reflection.MaxDeliveryRetries = src.Reflection.MaxDeliveryRetries
	}
	if len(src.Reflection.ActiveDurationDays) > 0 {
		dst.Reflection.ActiveDurationDays = src.Reflection.ActiveDurationDays
	}
	if src.Search.Provider != "" {
		dst.Search.Provider = src.Search.Provider
	}
	if src.Search.APIKey != "" {
		dst.Search.APIKey = src.Search.APIKey
	}
	if src.Search.BaseURL != "" {
		dst.Search.BaseURL = src.Search.BaseURL
	}
	if src.Search.DailyLimit != 0 {
		dst.Search.DailyLimit = src.Search.DailyLimit
	}
	if src.Chat.HistoryRounds != 0 {
		dst.Chat.HistoryRounds = src.Chat.HistoryRounds
	}
}

// mergeSourceWeights 按字段合并检索源配置，非零值覆盖
func mergeSourceWeights(dst, src *SourceWeights) {
	if src.FreshnessDecay != 0 {
		dst.FreshnessDecay = src.FreshnessDecay
	}
	if src.Threshold != 0 {
		dst.Threshold = src.Threshold
	}
	if src.Limit != 0 {
		dst.Limit = src.Limit
	}
}

// setSourceWeightsDefault 为源配置设置默认值（仅当当前值为零时）
func setSourceWeightsDefault(w *SourceWeights, decay, threshold float64, limit int) {
	if w.FreshnessDecay == 0 {
		w.FreshnessDecay = decay
	}
	if w.Threshold == 0 {
		w.Threshold = threshold
	}
	if w.Limit == 0 {
		w.Limit = limit
	}
}

// resolveEnvVars 对字符串字段再做一次 ${VAR} 展开（处理 env YAML 覆盖引入的新占位符）
func resolveEnvVars(cfg *Config) {
	cfg.DB.Password = os.ExpandEnv(cfg.DB.Password)
	cfg.LLM.APIKey = os.ExpandEnv(cfg.LLM.APIKey)
	cfg.Embedding.APIKey = os.ExpandEnv(cfg.Embedding.APIKey)
	cfg.Search.APIKey = os.ExpandEnv(cfg.Search.APIKey)
}

func validate(cfg *Config) error {
	if cfg.DB.Driver == "sqlite" {
		if cfg.DB.Database == "" {
			return fmt.Errorf("db.database (sqlite file path) is required")
		}
	} else {
		if cfg.DB.Host == "" {
			return fmt.Errorf("db.host is required")
		}
	}
	if cfg.LLM.APIKey == "" {
		return fmt.Errorf("llm.api_key is required (set LLM_API_KEY env var)")
	}
	// Snowflake 默认值
	if cfg.Snowflake.NodeID == 0 {
		cfg.Snowflake.NodeID = 1
	}
	// Reflection 默认值
	// Retrieval 默认值
	if cfg.Retrieval.FreshnessDecay == 0 {
		cfg.Retrieval.FreshnessDecay = 0.02
	}
	setSourceWeightsDefault(&cfg.Retrieval.Memory, cfg.Retrieval.FreshnessDecay, 0.5, 8)
	setSourceWeightsDefault(&cfg.Retrieval.Note, 0, 0.5, 5)
	setSourceWeightsDefault(&cfg.Retrieval.History, 0.005, 0.5, 5)
	if cfg.Reflection.LaunchMaxIterations == 0 {
		cfg.Reflection.LaunchMaxIterations = 8
	}
	if cfg.Reflection.AnalystMaxIterations == 0 {
		cfg.Reflection.AnalystMaxIterations = 3
	}
	if cfg.Reflection.DiscoverMaxIterations == 0 {
		cfg.Reflection.DiscoverMaxIterations = 3
	}
	if cfg.Reflection.ClusterThreshold == 0 {
		cfg.Reflection.ClusterThreshold = 0.70
	}
	if cfg.Reflection.PostChatDelaySeconds == 0 {
		cfg.Reflection.PostChatDelaySeconds = 1800
	}
	if len(cfg.Reflection.DailySchedules) == 0 {
		cfg.Reflection.DailySchedules = []string{"09:00", "20:00"}
	}
	if cfg.Reflection.DeliverIntervalMinutes == 0 {
		cfg.Reflection.DeliverIntervalMinutes = 60
	}
	if cfg.Reflection.MaxDeliveryRetries == 0 {
		cfg.Reflection.MaxDeliveryRetries = 3
	}
	if len(cfg.Reflection.ActiveDurationDays) == 0 {
		cfg.Reflection.ActiveDurationDays = map[string]int{
			"long": 90, "medium": 30, "short": 14, "ephemeral": 7,
		}
	}
	if cfg.LLM.MaxToolResultChars == 0 {
		cfg.LLM.MaxToolResultChars = 2000
	}
	if cfg.LLM.ExtractMsgLimit == 0 {
		cfg.LLM.ExtractMsgLimit = 50
	}
	if cfg.Search.Provider == "" {
		cfg.Search.Provider = "anysearch"
	}
	if cfg.Search.BaseURL == "" {
		cfg.Search.BaseURL = "https://api.anysearch.com/v1"
	}
	if cfg.Search.DailyLimit == 0 {
		cfg.Search.DailyLimit = 100
	}
	if cfg.Chat.HistoryRounds == 0 {
		cfg.Chat.HistoryRounds = 10
	}
	if cfg.Share.DefaultValidDays == 0 {
		cfg.Share.DefaultValidDays = 7
	}
	return nil
}
