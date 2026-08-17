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
	Server            ServerConfig            `yaml:"server"`
	DB                DBConfig                `yaml:"db"`
	LLM               LLMConfig               `yaml:"llm"`
	Embedding         EmbeddingConfig         `yaml:"embedding"`
	Reflection        ReflectionConfig        `yaml:"reflection"`
	Retrieval         RetrievalConfig         `yaml:"retrieval"`
	WebFetch          WebFetchConfig          `yaml:"webfetch"`
	SubAgent          SubAgentConfig          `yaml:"subagent"`
	Chat              ChatConfig              `yaml:"chat"`
	Share             ShareConfig             `yaml:"share"`
	Snowflake         SnowflakeConfig         `yaml:"snowflake"`
	MCP               MCPConfig               `yaml:"mcp"`
	GlobalDefaultRole GlobalDefaultRoleConfig `yaml:"global_default_role"`
}

// ChatConfig — 对话配置
type ChatConfig struct {
	HistoryRounds             int      `yaml:"history_rounds"`              // 加载历史消息轮数，默认 10
	MaxWindowRounds           int      `yaml:"max_window_rounds"`           // 扩张窗口上限，默认 100
	MaxSummaryRounds          int      `yaml:"max_summary_rounds"`          // 单次摘要覆盖上限，0=等于 history_rounds
	SessionGapMinutes         int      `yaml:"session_gap_minutes"`         // session 间隔阈值（分钟），默认 30
	SummaryAttachCount        int      `yaml:"summary_attach_count"`        // 摘要附着条数上限，默认 10
	SummaryAttachMaxDays      int      `yaml:"summary_attach_max_days"`     // 摘要附着时间窗口（天），默认 3
	SummaryRelevanceThreshold float64  `yaml:"summary_relevance_threshold"` // 摘要相关性余弦相似度阈值，默认 0.5
	SummaryRelevanceHistoryN  int      `yaml:"summary_relevance_history_n"` // 相关性检查的历史消息窗口大小，默认 5
	SummaryDedupThreshold     float64  `yaml:"summary_dedup_threshold"`     // 摘要附着去重余弦相似度阈值（相邻摘要更相似则丢弃较旧者），默认 0.85
	ToolExcludes              []string `yaml:"tool_excludes"`               // 对 LLM 隐藏的工具名列表（控 token 成本），默认空
}

// ShareConfig — 分享配置
type ShareConfig struct {
	DefaultValidDays int `yaml:"default_valid_days"` // 分享链接默认有效天数，默认 7
}

// WebFetchConfig — webfetch 工具配置
type WebFetchConfig struct {
	Enabled        bool `yaml:"enabled"`         // 是否启用 webfetch（默认 false 关闭；启用后仅 subAgent 可见）
	TimeoutSeconds int  `yaml:"timeout_seconds"` // 抓取超时秒数，默认 15
	MaxBytes       int  `yaml:"max_bytes"`       // 正文大小上限（字节），默认 51200
}

// SubAgentConfig — 子 agent（task 工具）配置
type SubAgentConfig struct {
	ExploreMaxIterations  int `yaml:"explore_max_iterations"`  // explore 子 agent 最大轮次（含强制收敛轮），默认 20（防死循环兜底，timeout 才是真实预算）
	ExploreTimeoutSeconds int `yaml:"explore_timeout_seconds"` // explore 子 agent 执行超时秒数，默认 600
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

// GlobalDefaultRoleConfig — 全局默认角色（无 agent_id 时生效）
type GlobalDefaultRoleConfig struct {
	SystemPrompt string `yaml:"system_prompt"` // 全局默认角色 system prompt（Markdown）
}

// SnowflakeConfig — Snowflake ID 配置
type SnowflakeConfig struct {
	NodeID int64 `yaml:"node_id"`
}

// ServerConfig — HTTP 服务配置
type ServerConfig struct {
	Port                    int      `yaml:"port"`
	Mode                    string   `yaml:"mode"`                      // debug | release
	RegistrationEnabled     *bool    `yaml:"registration_enabled"`      // 是否允许新用户注册
	AllowedUsers            []string `yaml:"allowed_users"`             // 允许注册的用户名列表（空=全部允许）
	MemoryAutoExtract       bool     `yaml:"memory_auto_extract"`       // 是否自动提取记忆
	ProfileStartupMigration bool     `yaml:"profile_startup_migration"` // 启动时异步迁移旧画像（facts 空才迁，单用户场景用）
	MemoryDupeThreshold     float64  `yaml:"memory_dupe_threshold"`     // 记忆去重相似度阈值，默认 0.85
	DisableBackgroundTasks  bool     `yaml:"disable_background_tasks"`  // 禁用所有后台任务（记忆提取/摘要/主题分析/反思等），dev 环境建议开启
	SidecarURL              string   `yaml:"sidecar_url"`               // Python sidecar MCP HTTP 地址
	TokenTTLMinutes         int      `yaml:"token_ttl_minutes"`         // token 过期时间（分钟），默认 5
	MaxTokensPerUser        int      `yaml:"max_tokens_per_user"`       // 同一用户最大 token 数，默认 4
	AdminUsers              []string `yaml:"admin_users"`               // 管理员用户 ID 列表
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
	Provider              string             `yaml:"provider"` // doubao | deepseek
	APIKey                string             `yaml:"api_key"`
	Model                 string             `yaml:"model"`                    // 弱模型（日常使用）
	StrongModel           string             `yaml:"strong_model"`             // 强模型（深度思考模式，可选，默认同 Model）
	BaseURL               string             `yaml:"base_url"`                 // LLM API 地址
	MaxIterations         int                `yaml:"max_iterations"`           // ReAct 最大迭代次数（默认 8）
	DefaultTemperature    *float64           `yaml:"default_temperature"`      // 默认采样温度，nil 则用 LLM 自身默认
	DefaultMaxTokens      *int               `yaml:"default_max_tokens"`       // 默认最大输出 token，nil 则用 LLM 自身默认
	MaxToolResultChars    int                `yaml:"max_tool_result_chars"`    // 工具结果传入 LLM 的最大字符数，超出截断，默认 2000
	ToolMaxConcurrency    int                `yaml:"tool_max_concurrency"`     // 工具并发执行上限，0=不限制，默认 0
	Verification          VerificationConfig `yaml:"verification"`             // 响应检验配置
	ExtractMsgLimit       int                `yaml:"extract_msg_limit"`        // 记忆提取每次最多取的消息数，默认 50
	UseNativeFC           bool               `yaml:"use_native_fc"`            // 是否使用原生 function calling（默认 true）
	ClientToolTimeoutSecs int                `yaml:"client_tool_timeout_secs"` // 客户端工具执行超时秒数，默认 60
	DebugPersistJSON      bool               `yaml:"debug_persist_json"`       // 是否持久化完整 LLM 请求 JSON 到 DB（异步限频写入，配 debug_json 控制频率与保留）
	DebugJSON             DebugJSONConfig    `yaml:"debug_json"`               // 调试 JSON 异步持久化参数
}

// DebugJSONConfig — 调试 JSON 异步持久化配置
type DebugJSONConfig struct {
	RetentionDays        int   `yaml:"retention_days"`         // request_json 保留天数，默认 3
	FlushIntervalMs      int   `yaml:"flush_interval_ms"`      // 异步刷盘间隔毫秒，默认 1000
	MaxBytesPerFlush     int64 `yaml:"max_bytes_per_flush"`    // 每批字节预算（限频核心），默认 2MB
	MaxPayloadBytes      int64 `yaml:"max_payload_bytes"`      // 单条 payload 上限，超限丢弃，默认 1MB
	QueueMaxEntries      int   `yaml:"queue_max_entries"`      // 排队条数上限，默认 2048
	QueueMaxBytes        int64 `yaml:"queue_max_bytes"`        // 排队字节上限，默认 64MB
	CleanupIntervalHours int   `yaml:"cleanup_interval_hours"` // 保留清理周期（小时），默认 6
	CleanupBatchRows     int   `yaml:"cleanup_batch_rows"`     // 清理每批行数，默认 500
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
	FreshnessDecay float64            `yaml:"freshness_decay"` // 时间衰减系数，保留作为默认值
	Memory         SourceWeights      `yaml:"memory"`
	Note           SourceWeights      `yaml:"note"`
	History        SourceWeights      `yaml:"history"`
	Theme          SourceWeights      `yaml:"theme"`
	Summary        SourceWeights      `yaml:"summary"`
	ResourceZone   ResourceZoneConfig `yaml:"resource_zone"` // 公共资源区（phase-10 任务 B）
}

// ResourceZoneConfig — 公共资源区配置
type ResourceZoneConfig struct {
	Enabled       bool `yaml:"enabled"`        // 是否启用资源区（历史消息不再重放 XML）
	BudgetChars   int  `yaml:"budget_chars"`   // 总预算帽（UTF-8 字符数，中文 1 字=1；7000 字符 ≈ 5500-7000 tokens）
	BudgetProfile int  `yaml:"budget_profile"` // profile 每类型预算（chars），默认 1600
	BudgetSummary int  `yaml:"budget_summary"` // summary 预算，默认 800
	BudgetMemory  int  `yaml:"budget_memory"`  // memory 预算，默认 600
	BudgetNote    int  `yaml:"budget_note"`    // note 预算，默认 3000
	BudgetTopic   int  `yaml:"budget_topic"`   // topic 预算，默认 200
	QuotaSummary  int  `yaml:"quota_summary"`  // 摘要配额，默认 2
	QuotaMemory   int  `yaml:"quota_memory"`   // 记忆配额，默认 8
	QuotaNote     int  `yaml:"quota_note"`     // 笔记配额，默认 10
	QuotaTopic    int  `yaml:"quota_topic"`    // 主题配额（只收 matched），默认 5
}

// ReflectionConfig — 反思系统配置
type ReflectionConfig struct {
	LaunchMaxIterations         int            `yaml:"launch_max_iterations"`          // 启动 Agent 最大轮次，默认 8
	AnalystMaxIterations        int            `yaml:"analyst_max_iterations"`         // ThemeAnalyst 内部最大轮次，默认 3
	DiscoverMaxIterations       int            `yaml:"discover_max_iterations"`        // ThemeDiscoverer 内部最大轮次，默认 3
	ClusterThreshold            float64        `yaml:"cluster_threshold"`              // topic 聚类余弦相似度阈值，默认 0.70
	DailySchedules              []string       `yaml:"daily_schedules"`                // 主题发现定时触发时间列表，默认 ["03:00"]（每天一次）
	DeliverIntervalMinutes      int            `yaml:"deliver_interval_minutes"`       // 投递窗口间隔分钟，默认 60；<=0 关闭投递（pending findings 会积压，恢复后补投）
	MaxDeliveryRetries          int            `yaml:"max_delivery_retries"`           // 投递重试上限，默认 3
	ActiveDurationDays          map[string]int `yaml:"active_duration_days"`           // active 各 duration 的衰减天数，默认 long:90 medium:30 short:14 ephemeral:7
	MissionCheckIntervalMinutes int            `yaml:"mission_check_interval_minutes"` // Mission 检查间隔，默认 30 分钟；<=0 表示关闭定时检查
	MinActiveDays               int            `yaml:"min_active_days"`                // 反思门控：用户最近 N 天内有聊天才跑，默认 3；<0 表示不过滤
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
	dst.Server.DisableBackgroundTasks = src.Server.DisableBackgroundTasks
	if src.Server.SidecarURL != "" {
		dst.Server.SidecarURL = src.Server.SidecarURL
	}
	if src.Server.TokenTTLMinutes != 0 {
		dst.Server.TokenTTLMinutes = src.Server.TokenTTLMinutes
	}
	if src.Server.MaxTokensPerUser != 0 {
		dst.Server.MaxTokensPerUser = src.Server.MaxTokensPerUser
	}
	if len(src.Server.AdminUsers) > 0 {
		dst.Server.AdminUsers = src.Server.AdminUsers
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
	dst.LLM.DebugPersistJSON = src.LLM.DebugPersistJSON
	mergeDebugJSON(&dst.LLM.DebugJSON, &src.LLM.DebugJSON)
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
	mergeSourceWeights(&dst.Retrieval.Theme, &src.Retrieval.Theme)
	mergeSourceWeights(&dst.Retrieval.Summary, &src.Retrieval.Summary)
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
	if src.Reflection.MissionCheckIntervalMinutes != 0 {
		dst.Reflection.MissionCheckIntervalMinutes = src.Reflection.MissionCheckIntervalMinutes
	}
	if src.Reflection.MinActiveDays != 0 {
		dst.Reflection.MinActiveDays = src.Reflection.MinActiveDays
	}
	if src.Chat.HistoryRounds != 0 {
		dst.Chat.HistoryRounds = src.Chat.HistoryRounds
	}
	if src.Chat.MaxWindowRounds != 0 {
		dst.Chat.MaxWindowRounds = src.Chat.MaxWindowRounds
	}
	if src.Chat.MaxSummaryRounds != 0 {
		dst.Chat.MaxSummaryRounds = src.Chat.MaxSummaryRounds
	}
	if src.Chat.SessionGapMinutes != 0 {
		dst.Chat.SessionGapMinutes = src.Chat.SessionGapMinutes
	}
	if src.Chat.SummaryAttachCount != 0 {
		dst.Chat.SummaryAttachCount = src.Chat.SummaryAttachCount
	}
	if src.Chat.SummaryAttachMaxDays != 0 {
		dst.Chat.SummaryAttachMaxDays = src.Chat.SummaryAttachMaxDays
	}
	if src.Chat.SummaryRelevanceThreshold > 0 {
		dst.Chat.SummaryRelevanceThreshold = src.Chat.SummaryRelevanceThreshold
	}
	if src.Chat.SummaryRelevanceHistoryN != 0 {
		dst.Chat.SummaryRelevanceHistoryN = src.Chat.SummaryRelevanceHistoryN
	}
	if src.Chat.SummaryDedupThreshold > 0 {
		dst.Chat.SummaryDedupThreshold = src.Chat.SummaryDedupThreshold
	}
	if src.Chat.ToolExcludes != nil {
		dst.Chat.ToolExcludes = src.Chat.ToolExcludes
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

// mergeDebugJSON 按字段合并调试 JSON 配置，非零值覆盖
func mergeDebugJSON(dst, src *DebugJSONConfig) {
	if src.RetentionDays != 0 {
		dst.RetentionDays = src.RetentionDays
	}
	if src.FlushIntervalMs != 0 {
		dst.FlushIntervalMs = src.FlushIntervalMs
	}
	if src.MaxBytesPerFlush != 0 {
		dst.MaxBytesPerFlush = src.MaxBytesPerFlush
	}
	if src.MaxPayloadBytes != 0 {
		dst.MaxPayloadBytes = src.MaxPayloadBytes
	}
	if src.QueueMaxEntries != 0 {
		dst.QueueMaxEntries = src.QueueMaxEntries
	}
	if src.QueueMaxBytes != 0 {
		dst.QueueMaxBytes = src.QueueMaxBytes
	}
	if src.CleanupIntervalHours != 0 {
		dst.CleanupIntervalHours = src.CleanupIntervalHours
	}
	if src.CleanupBatchRows != 0 {
		dst.CleanupBatchRows = src.CleanupBatchRows
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
	setSourceWeightsDefault(&cfg.Retrieval.Theme, 0, 0.6, 10)
	setSourceWeightsDefault(&cfg.Retrieval.Summary, 0.02, 1.0, 0)
	if cfg.Retrieval.ResourceZone.BudgetChars == 0 {
		cfg.Retrieval.ResourceZone.BudgetChars = 7000
	}
	if cfg.Retrieval.ResourceZone.BudgetProfile == 0 {
		cfg.Retrieval.ResourceZone.BudgetProfile = 1600
	}
	if cfg.Retrieval.ResourceZone.BudgetSummary == 0 {
		cfg.Retrieval.ResourceZone.BudgetSummary = 800
	}
	if cfg.Retrieval.ResourceZone.BudgetMemory == 0 {
		cfg.Retrieval.ResourceZone.BudgetMemory = 600
	}
	if cfg.Retrieval.ResourceZone.BudgetNote == 0 {
		cfg.Retrieval.ResourceZone.BudgetNote = 3000
	}
	if cfg.Retrieval.ResourceZone.BudgetTopic == 0 {
		cfg.Retrieval.ResourceZone.BudgetTopic = 200
	}
	if cfg.Retrieval.ResourceZone.QuotaSummary == 0 {
		cfg.Retrieval.ResourceZone.QuotaSummary = 2
	}
	if cfg.Retrieval.ResourceZone.QuotaMemory == 0 {
		cfg.Retrieval.ResourceZone.QuotaMemory = 8
	}
	if cfg.Retrieval.ResourceZone.QuotaNote == 0 {
		cfg.Retrieval.ResourceZone.QuotaNote = 10
	}
	if cfg.Retrieval.ResourceZone.QuotaTopic == 0 {
		cfg.Retrieval.ResourceZone.QuotaTopic = 5
	}
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
	if len(cfg.Reflection.DailySchedules) == 0 {
		cfg.Reflection.DailySchedules = []string{"09:00", "20:00"}
	}
	if cfg.Reflection.DeliverIntervalMinutes < 0 {
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
	if cfg.Reflection.MinActiveDays == 0 {
		cfg.Reflection.MinActiveDays = 3
	}
	if cfg.LLM.MaxToolResultChars == 0 {
		cfg.LLM.MaxToolResultChars = 2000
	}
	if cfg.LLM.ExtractMsgLimit == 0 {
		cfg.LLM.ExtractMsgLimit = 50
	}
	if cfg.Chat.HistoryRounds == 0 {
		cfg.Chat.HistoryRounds = 10
	}
	if cfg.Chat.MaxWindowRounds == 0 {
		cfg.Chat.MaxWindowRounds = 100
	}
	if cfg.Chat.SessionGapMinutes == 0 {
		cfg.Chat.SessionGapMinutes = 30
	}
	if cfg.Chat.SummaryAttachCount == 0 {
		cfg.Chat.SummaryAttachCount = 10
	}
	if cfg.Chat.SummaryAttachMaxDays == 0 {
		cfg.Chat.SummaryAttachMaxDays = 3
	}
	if cfg.Chat.SummaryRelevanceThreshold == 0 {
		cfg.Chat.SummaryRelevanceThreshold = 0.5
	}
	if cfg.Chat.SummaryRelevanceHistoryN == 0 {
		cfg.Chat.SummaryRelevanceHistoryN = 5
	}
	if cfg.Chat.SummaryDedupThreshold == 0 {
		cfg.Chat.SummaryDedupThreshold = 0.85
	}
	if cfg.Share.DefaultValidDays == 0 {
		cfg.Share.DefaultValidDays = 7
	}
	if cfg.WebFetch.TimeoutSeconds == 0 {
		cfg.WebFetch.TimeoutSeconds = 15
	}
	if cfg.WebFetch.MaxBytes == 0 {
		cfg.WebFetch.MaxBytes = 51200
	}
	// Debug JSON 默认值（配 debug_persist_json 开关使用）
	d := &cfg.LLM.DebugJSON
	if d.RetentionDays == 0 {
		d.RetentionDays = 3
	}
	if d.FlushIntervalMs == 0 {
		d.FlushIntervalMs = 1000
	}
	if d.MaxBytesPerFlush == 0 {
		d.MaxBytesPerFlush = 2 << 20
	}
	if d.MaxPayloadBytes == 0 {
		d.MaxPayloadBytes = 1 << 20
	}
	if d.QueueMaxEntries == 0 {
		d.QueueMaxEntries = 2048
	}
	if d.QueueMaxBytes == 0 {
		d.QueueMaxBytes = 64 << 20
	}
	if d.CleanupIntervalHours == 0 {
		d.CleanupIntervalHours = 6
	}
	if d.CleanupBatchRows == 0 {
		d.CleanupBatchRows = 500
	}
	return nil
}
