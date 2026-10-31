package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	LLMBg             LLMConfig               `yaml:"llm_bg"` // 后台任务专用 LLM（可选；model 为空则回退主 LLM）
	Embedding         EmbeddingConfig         `yaml:"embedding"`
	Reflection        ReflectionConfig        `yaml:"reflection"`
	Retrieval         RetrievalConfig         `yaml:"retrieval"`
	WebTools          WebToolsConfig          `yaml:"web_tools"`
	SubAgent          SubAgentConfig          `yaml:"subagent"`
	Chat              ChatConfig              `yaml:"chat"`
	Archive           ArchiveConfig           `yaml:"archive"`
	Storage           StorageConfig           `yaml:"storage"`
	Backup            BackupConfig            `yaml:"backup"`
	Snowflake         SnowflakeConfig         `yaml:"snowflake"`
	MCP               MCPConfig               `yaml:"mcp"`
	Vector            VectorConfig            `yaml:"vector"`
	Activity          ActivityConfig          `yaml:"activity"`
	GlobalDefaultRole GlobalDefaultRoleConfig `yaml:"global_default_role"`
}

// ActivityConfig — 活动埋点配置。
// events.db 是自包含 SQLite，可跨形态启用：本地默认与主库同目录，云端（MySQL 主库）用 db_path 显式指定。
type ActivityConfig struct {
	Enabled *bool `yaml:"enabled"` // nil = 未显式配置 → 默认开
	// DBPath — events.db 路径。空时：主库为 sqlite 则与主库同目录（DBPath 推导）；主库非 sqlite 且为空则启动 fatal，
	// 禁止静默把库写到当前目录。
	DBPath string `yaml:"db_path"`
	// DayTimezone — 派生/读取层「一天」边界所用时区（IANA 名，如 Asia/Shanghai）。空 = 沿用主机 Local；
	// 云端主机多为 UTC，须显式设置为 Asia/Shanghai，否则 day_stats/sessions 的日期边界会错 8 小时。
	DayTimezone string `yaml:"day_timezone"`
	// WatermarkMode — 上传接口 POST /api/events/batch 的水位口径（仅服务端决定，客户端按响应 watermark_kind 自适应）：
	//   time（默认）：watermark = 本批最大 occurred_at，客户端删「≤ 水位的本地段」；依赖两端同钟。
	//   segment：watermark = 回显 segment_id，客户端逐段删除；不依赖客户端时钟，供服务端/客户端分离时切换。
	WatermarkMode string `yaml:"watermark_mode"`
}

// DayLocation 解析 DayTimezone；空返回 nil（调用方沿用主机 Local）。
func (a *ActivityConfig) DayLocation() (*time.Location, error) {
	if a == nil || a.DayTimezone == "" {
		return nil, nil
	}
	loc, err := time.LoadLocation(a.DayTimezone)
	if err != nil {
		return nil, fmt.Errorf("activity.day_timezone %q 无效: %w", a.DayTimezone, err)
	}
	return loc, nil
}

// WatermarkModeNormalized 返回归一后的水位模式（空/未配置 = time）；仅接受 time / segment。
func (a *ActivityConfig) WatermarkModeNormalized() string {
	if a == nil || a.WatermarkMode == "" {
		return "time"
	}
	return a.WatermarkMode
}

// VectorConfig — 向量内存治理配置
type VectorConfig struct {
	MessageRetentionDays  int      `yaml:"message_retention_days"`   // message 向量保留天数，<=0 关闭
	SummaryRetentionDays  int      `yaml:"summary_retention_days"`   // summary 向量保留天数，<=0 关闭
	EvictIntervalHours    int      `yaml:"evict_interval_hours"`     // 逐出扫描间隔（小时），<=0 默认 24
	VacuumFreeThresholdMB int      `yaml:"vacuum_free_threshold_mb"` // 删行后空闲页超此 MB 才 VACUUM；<=0 关闭（默认 64）
	ChunkDisabledParents  []string `yaml:"chunk_disabled_parents"`   // 不再建 chunk 的父类型（短文本由 full 向量覆盖）
}

// ChatConfig — 对话配置
type ChatConfig struct {
	HistoryRounds             int      `yaml:"history_rounds"`              // 加载历史消息轮数，默认 10
	RecentUserMsgs            int      `yaml:"recent_user_msgs"`            // 新窗口注入资源区的最近轮数（user + AI 最终回复），默认 3
	MaxWindowRounds           int      `yaml:"max_window_rounds"`           // 扩张窗口上限，默认 100
	MaxSummaryRounds          int      `yaml:"max_summary_rounds"`          // 单次摘要覆盖上限，0=等于 history_rounds
	SessionGapMinutes         int      `yaml:"session_gap_minutes"`         // session 间隔阈值（分钟），默认 30；超过则重置窗口并触发 session_end 提取
	SummaryAttachCount        int      `yaml:"summary_attach_count"`        // 摘要附着条数上限，默认 1
	SummaryAttachMaxDays      int      `yaml:"summary_attach_max_days"`     // 摘要附着时间窗口（天），默认 3
	SummaryRelevanceThreshold float64  `yaml:"summary_relevance_threshold"` // 摘要相关性余弦相似度阈值，默认 0.5
	SummaryRelevanceHistoryN  int      `yaml:"summary_relevance_history_n"` // 相关性检查的历史消息窗口大小，默认 5
	SummaryDedupThreshold     float64  `yaml:"summary_dedup_threshold"`     // 摘要附着去重余弦相似度阈值（相邻摘要更相似则丢弃较旧者），默认 0.85
	ToolExcludes              []string `yaml:"tool_excludes"`               // 对 LLM 隐藏的工具名列表（控 token 成本），默认空
	SubAgentOnlyClientTools   []string `yaml:"subagent_only_client_tools"`  // 仅 subAgent 可用的客户端工具组（主循环对 LLM 隐藏），默认 ["fs","shell"]（browser 已放回主循环）
	ImageReplayRounds         int      `yaml:"image_replay_rounds"`         // 历史消息重放图片的最近轮数，默认 10（图片 token 烧钱，不随文本缓存复用）
}

// ArchiveConfig — 会话自动归档配置
type ArchiveConfig struct {
	AfterDays       int `yaml:"after_days"`       // 无活动天数超过则自动归档；<=0 关闭
	IntervalMinutes int `yaml:"interval_minutes"` // 归档扫描间隔（分钟），<=0 默认 1440（每日一次）
}

// StorageConfig — 图片存储配置（图床：文档配图 + 聊天图片输入共用）
type StorageConfig struct {
	ImageDir                string `yaml:"image_dir"`                      // 图片根目录（相对 server 运行目录），默认 data/images
	MaxImageBytes           int64  `yaml:"max_image_bytes"`                // 单图大小上限，默认 32 MiB（对齐 DeepSeek 单图上限）
	ImageTTLDays            int    `yaml:"image_ttl_days"`                 // 被消息引用过的图保留天数；0=默认 15，<0=关闭该类过期清理（文件缺失自愈恒开）
	ImageOrphanTTLDays      int    `yaml:"image_orphan_ttl_days"`          // 从未被引用的图保留天数；0=默认 3，<0=关闭该类过期清理（文件缺失自愈恒开）
	ImageCleanupIntervalMin int    `yaml:"image_cleanup_interval_minutes"` // 清理扫描间隔（分钟），<=0 默认 720（12 小时）
}

// BackupConfig — 本地数据备份配置（仅本地模式生效；云端忽略）
type BackupConfig struct {
	Enabled      bool   `yaml:"enabled"`       // 总开关（默认关，由 Swift 设置页经 env 注入）
	Dest         string `yaml:"dest"`          // 备份目标目录（可为 iCloud Drive 路径）
	IntervalDays int    `yaml:"interval_days"` // 备份间隔天数，<=0 视为 1
	DailyKeep    int    `yaml:"daily_keep"`    // 保留最近几次，<=0 视为 3
	WeeklyKeep   int    `yaml:"weekly_keep"`   // 额外保留几份「≥7 天前」的，<=0 视为 1
}

// WebToolsConfig — 联网工具（DeepSeek 官方 Search / Fetch）配置。
// 设计见 docs/design/web-tools/design.md。key 缺省回退 llm.api_key；enabled 但没有可用 key
// 时启动只告警、不注册（可选能力，不 panic）。
type WebToolsConfig struct {
	Enabled         bool   `yaml:"enabled"`           // 是否注册 web 工具组（默认 false）
	APIKey          string `yaml:"api_key"`           // 空则回退 llm.api_key（仅 provider=deepseek 时）
	BaseURL         string `yaml:"base_url"`          // 默认 https://api.deepseek.com
	TimeoutSeconds  int    `yaml:"timeout_seconds"`   // 单次 HTTP 超时秒数，默认 30
	SearchCount     int    `yaml:"search_count"`      // 模型不传 count 时的条数，默认 8；模型传值夹在 1..20
	MaxContentChars int    `yaml:"max_content_chars"` // fetch 正文 / search 单条正文的字符上限，默认 8000
	SearchMaxChars  int    `yaml:"search_max_chars"`  // search 结果总预算（字符），默认 16000；超出则截条并在 note 说明
	DailyLimit      int    `yaml:"daily_limit"`       // 每日 search+fetch 合计上限，默认 1000；<0 不限制
}

// SubAgentConfig — 子 agent（task 工具）配置
type SubAgentConfig struct {
	ExploreMaxIterations  int   `yaml:"explore_max_iterations"`  // explore 子 agent 最大轮次（含强制收敛轮），默认 20（防死循环兜底，timeout 才是真实预算）
	ExploreTimeoutSeconds int   `yaml:"explore_timeout_seconds"` // explore 子 agent 执行超时秒数，默认 600
	UserContextEnabled    *bool `yaml:"user_context_enabled"`    // 是否向主 LLM 注入 user_context 触发规则（纯 prompt 引导的保险丝）；nil=未显式配置，沿用 base
}

// AutoExtractMemory 是否自动提取记忆（nil=未显式配置 → false）。
func (s *ServerConfig) AutoExtractMemory() bool {
	return s.MemoryAutoExtract != nil && *s.MemoryAutoExtract
}

// BackgroundTasksDisabled 是否禁用全部后台任务（nil=未显式配置 → false）。
func (s *ServerConfig) BackgroundTasksDisabled() bool {
	return s.DisableBackgroundTasks != nil && *s.DisableBackgroundTasks
}

// IsEnabled 活动埋点是否开启（nil=未显式配置 → 默认开）。
func (a *ActivityConfig) IsEnabled() bool {
	return a.Enabled == nil || *a.Enabled
}

// PromptUserContext 是否注入 user_context 触发规则（nil=未显式配置 → false）。
func (s *SubAgentConfig) PromptUserContext() bool {
	return s.UserContextEnabled != nil && *s.UserContextEnabled
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
	Host                    string   `yaml:"host"` // 监听地址；空=所有网卡（云端），本地写 127.0.0.1
	Port                    int      `yaml:"port"`
	Mode                    string   `yaml:"mode"`                      // debug | release
	RegistrationEnabled     *bool    `yaml:"registration_enabled"`      // 是否允许新用户注册
	AllowedUsers            []string `yaml:"allowed_users"`             // 允许注册的用户名列表（空=全部允许）
	MemoryAutoExtract       *bool    `yaml:"memory_auto_extract"`       // 是否自动提取记忆；nil=未显式配置，沿用 base
	ProfileStartupMigration bool     `yaml:"profile_startup_migration"` // 启动时异步迁移旧画像（facts 空才迁，单用户场景用）
	MemoryDupeThreshold     float64  `yaml:"memory_dupe_threshold"`     // 记忆去重相似度阈值，默认 0.85（旧单条提取路径遗留）
	// —— 记录循环（记忆两段式：抽取 + 逐候选决策循环）——
	RecordLoopMaxIterations   int     `yaml:"record_loop_max_iterations"`   // 决策循环轮数上限，默认 6
	MemoryMaxPerSession       int     `yaml:"memory_max_per_session"`       // 单次会话最多新增几条记忆（抽取段上限），默认 2
	MemoryNeighborTopK        int     `yaml:"memory_neighbor_top_k"`        // 预检索近邻条数（每域），默认 5
	MemoryNeighborThreshold   float64 `yaml:"memory_neighbor_threshold"`    // 预检索近邻相似度下限，默认 0.5
	MemoryConfirmThreshold    float64 `yaml:"memory_confirm_threshold"`     // NONE 时「再次确认」的近邻相似度下限，默认 0.80
	MemoryTopicReuseThreshold float64 `yaml:"memory_topic_reuse_threshold"` // topic 复用阈值，默认 0.80（与 topic-merge 一致）
	// MemoryExtractTemperature — 抽取段的采样温度。抽取要的是「逐字抄录用户原话」，
	// 温度越低越少改写；nil = 不干预，走厂商默认（见 docs/design/memory-facts-verbatim）。
	// 指针类型：0 是合法取值（贪心），不能用零值表示「未配置」。
	MemoryExtractTemperature *float64 `yaml:"memory_extract_temperature"`
	DisableBackgroundTasks   *bool    `yaml:"disable_background_tasks"` // 禁用所有后台任务（记忆提取/摘要/主题分析/反思等）；nil=未显式配置，沿用 base
	SidecarURL               string   `yaml:"sidecar_url"`              // Python sidecar MCP HTTP 地址
	TokenTTLMinutes          int      `yaml:"token_ttl_minutes"`        // token 过期时间（分钟），默认 5
	MaxTokensPerUser         int      `yaml:"max_tokens_per_user"`      // 同一用户最大 token 数，默认 4
	AdminUsers               []string `yaml:"admin_users"`              // 管理员用户 ID 列表
	// —— 本地单用户模式（Mac 本地部署）——
	LocalSingleUser bool   `yaml:"local_single_user"` // 免账号密码：凭 local_token 直接进
	LocalUsername   string `yaml:"local_username"`    // 本地单用户的用户名（按它解析/创建，自动 admin）
	LocalToken      string `yaml:"local_token"`       // 本地访问令牌（app 生成，经 ${XHAN_LOCAL_TOKEN} 注入）
	LocalEnv        string `yaml:"local_env"`         // dev | prod：本环境标签，与 APP_ENV / 数据根 / token 前缀交叉断言（第 4 期）
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
	Provider              string             `yaml:"provider"` // doubao | deepseek | ark
	APIKey                string             `yaml:"api_key"`
	Model                 string             `yaml:"model"`                    // 弱模型（日常使用）
	StrongModel           string             `yaml:"strong_model"`             // 强模型（深度思考模式，可选，默认同 Model）
	BaseURL               string             `yaml:"base_url"`                 // LLM API 地址
	HTTPTimeoutSecs       int                `yaml:"http_timeout_secs"`        // Flash LLM HTTP 请求超时秒数，默认 120
	StrongHTTPTimeoutSecs int                `yaml:"strong_http_timeout_secs"` // Pro LLM HTTP 请求超时秒数，默认 600
	MaxIterations         int                `yaml:"max_iterations"`           // ReAct 最大迭代次数（默认 8）
	DefaultTemperature    *float64           `yaml:"default_temperature"`      // 默认采样温度，nil 则用 LLM 自身默认
	DefaultMaxTokens      *int               `yaml:"default_max_tokens"`       // 默认最大输出 token，nil 则用 LLM 自身默认
	ContextWindow         int                `yaml:"context_window"`           // 弱模型（model）上下文窗口 token 上限，用于 UI 展示占用比
	StrongContextWindow   int                `yaml:"strong_context_window"`    // 强模型（strong_model）上下文窗口 token 上限
	MaxToolResultChars    int                `yaml:"max_tool_result_chars"`    // 工具结果传入 LLM 的最大字符数，超出截断，默认 2000
	ToolMaxConcurrency    int                `yaml:"tool_max_concurrency"`     // 工具并发执行上限，0=不限制，默认 0
	Verification          VerificationConfig `yaml:"verification"`             // 响应检验配置
	ExtractMsgLimit       int                `yaml:"extract_msg_limit"`        // 记忆提取每次最多取的消息数，默认 50
	ClientToolTimeoutSecs int                `yaml:"client_tool_timeout_secs"` // 客户端工具执行超时秒数，默认 240
	DebugPersistJSON      *bool              `yaml:"debug_persist_json"`       // 是否持久化完整 LLM 请求 JSON 到 DB（异步限频写入，配 debug_json 控制频率与保留）；nil=未显式配置
	DebugJSON             DebugJSONConfig    `yaml:"debug_json"`               // 调试 JSON 异步持久化参数
}

// DebugPersist 返回 debug_persist_json 是否开启（nil=未显式配置时按 base 值生效）
func (l *LLMConfig) DebugPersist() bool {
	return l.DebugPersistJSON != nil && *l.DebugPersistJSON
}

// ContextWindowFor 返回指定模型的上下文窗口 token 上限。
// 按档位匹配：model == StrongModel 用强档上限，其余用弱档上限（0 表示未配置）。
// 比较配置里的模型名而非硬编码，换厂家只需改 model/strong_model。
func (l *LLMConfig) ContextWindowFor(model string) int {
	if model != "" && model == l.StrongModel && l.StrongContextWindow > 0 {
		return l.StrongContextWindow
	}
	return l.ContextWindow
}

// DebugJSONConfig — 调试 JSON 异步持久化配置
type DebugJSONConfig struct {
	RetentionDays           int   `yaml:"retention_days"`             // request_json 保留天数，默认 3
	FlushIntervalMs         int   `yaml:"flush_interval_ms"`          // 异步刷盘间隔毫秒，默认 1000
	MaxBytesPerFlush        int64 `yaml:"max_bytes_per_flush"`        // 每批字节预算（限频核心），默认 2MB
	MaxPayloadBytes         int64 `yaml:"max_payload_bytes"`          // 单条 payload 上限，超限丢弃，默认 1MB
	QueueMaxEntries         int   `yaml:"queue_max_entries"`          // 排队条数上限，默认 2048
	QueueMaxBytes           int64 `yaml:"queue_max_bytes"`            // 排队字节上限，默认 64MB
	CleanupIntervalHours    int   `yaml:"cleanup_interval_hours"`     // 保留清理周期（小时），默认 6
	CleanupBatchRows        int   `yaml:"cleanup_batch_rows"`         // 清理每批行数，默认 500
	ExtraTraceRetentionDays int   `yaml:"extra_trace_retention_days"` // message.extra 检索 trace 保留天数，默认 14，<=0 关闭
}

// EmbeddingConfig — 向量化配置
type EmbeddingConfig struct {
	APIKey  string `yaml:"api_key"`
	BaseURL string `yaml:"base_url"` // 默认 https://ark.cn-beijing.volces.com
	Model   string `yaml:"model"`    // 默认 doubao-embedding
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
	QuotaSummary  int  `yaml:"quota_summary"`  // 摘要配额，默认 1
	QuotaMemory   int  `yaml:"quota_memory"`   // 记忆配额，默认 8
	QuotaNote     int  `yaml:"quota_note"`     // 笔记配额，默认 10
	QuotaTopic    int  `yaml:"quota_topic"`    // 主题配额（只收 matched），默认 5
}

// ReflectionConfig — 反思系统配置
type ReflectionConfig struct {
	LaunchMaxIterations   int            `yaml:"launch_max_iterations"`   // 启动 Agent 最大轮次，默认 8
	AnalystMaxIterations  int            `yaml:"analyst_max_iterations"`  // ThemeAnalyst 内部最大轮次，默认 3
	DiscoverMaxIterations int            `yaml:"discover_max_iterations"` // ThemeDiscoverer 内部最大轮次，默认 3
	ClusterThreshold      float64        `yaml:"cluster_threshold"`       // topic 聚类余弦相似度阈值，默认 0.70
	DailySchedules        []string       `yaml:"daily_schedules"`         // 主题发现定时触发时间列表，默认 ["03:00"]（每天一次）
	ActiveDurationDays    map[string]int `yaml:"active_duration_days"`    // active 各 duration 的衰减天数，默认 long:90 medium:30 short:14 ephemeral:7
	MinActiveDays         int            `yaml:"min_active_days"`         // 反思门控：用户最近 N 天内有聊天才跑，默认 3；<0 表示不过滤
}

// Load 加载配置：base YAML + env YAML 合并 → 环境变量替换
func Load() (*Config, error) {
	env := os.Getenv("APP_ENV")
	if env == "" {
		env = "dev"
	}

	cfg, err := loadFile("config.yaml")
	if err != nil {
		return nil, fmt.Errorf("load config.yaml: %w", err)
	}

	envPath := fmt.Sprintf("config.%s.yaml", env)
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

// confDir 返回配置文件目录，默认 conf；app 拉起子进程时用 XHAN_CONF_DIR 指到 Contents/Resources/conf
func confDir() string {
	if d := os.Getenv("XHAN_CONF_DIR"); d != "" {
		return d
	}
	return "conf"
}

// loadFile 读取 YAML 并展开 ${VAR} 占位符
func loadFile(name string) (*Config, error) {
	data, err := os.ReadFile(filepath.Join(confDir(), name))
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
	if src.Server.Host != "" {
		dst.Server.Host = src.Server.Host
	}
	if src.Server.LocalUsername != "" {
		dst.Server.LocalUsername = src.Server.LocalUsername
	}
	if src.Server.LocalToken != "" {
		dst.Server.LocalToken = src.Server.LocalToken
	}
	if src.Server.LocalEnv != "" {
		dst.Server.LocalEnv = src.Server.LocalEnv
	}
	if src.Server.LocalSingleUser {
		dst.Server.LocalSingleUser = true
	}
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
	if src.Server.MemoryAutoExtract != nil {
		dst.Server.MemoryAutoExtract = src.Server.MemoryAutoExtract
	}
	if src.Server.MemoryDupeThreshold > 0 {
		dst.Server.MemoryDupeThreshold = src.Server.MemoryDupeThreshold
	}
	if src.Server.RecordLoopMaxIterations > 0 {
		dst.Server.RecordLoopMaxIterations = src.Server.RecordLoopMaxIterations
	}
	if src.Server.MemoryMaxPerSession > 0 {
		dst.Server.MemoryMaxPerSession = src.Server.MemoryMaxPerSession
	}
	if src.Server.MemoryNeighborTopK > 0 {
		dst.Server.MemoryNeighborTopK = src.Server.MemoryNeighborTopK
	}
	if src.Server.MemoryNeighborThreshold > 0 {
		dst.Server.MemoryNeighborThreshold = src.Server.MemoryNeighborThreshold
	}
	if src.Server.MemoryConfirmThreshold > 0 {
		dst.Server.MemoryConfirmThreshold = src.Server.MemoryConfirmThreshold
	}
	if src.Server.MemoryTopicReuseThreshold > 0 {
		dst.Server.MemoryTopicReuseThreshold = src.Server.MemoryTopicReuseThreshold
	}
	// 温度用 != nil 判据：0 是合法取值（贪心），不能用 > 0。
	if src.Server.MemoryExtractTemperature != nil {
		dst.Server.MemoryExtractTemperature = src.Server.MemoryExtractTemperature
	}
	if src.Server.DisableBackgroundTasks != nil {
		dst.Server.DisableBackgroundTasks = src.Server.DisableBackgroundTasks
	}
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
	if src.LLM.ContextWindow != 0 {
		dst.LLM.ContextWindow = src.LLM.ContextWindow
	}
	if src.LLM.StrongContextWindow != 0 {
		dst.LLM.StrongContextWindow = src.LLM.StrongContextWindow
	}
	if src.LLM.MaxToolResultChars != 0 {
		dst.LLM.MaxToolResultChars = src.LLM.MaxToolResultChars
	}
	if src.LLM.HTTPTimeoutSecs != 0 {
		dst.LLM.HTTPTimeoutSecs = src.LLM.HTTPTimeoutSecs
	}
	if src.LLM.StrongHTTPTimeoutSecs != 0 {
		dst.LLM.StrongHTTPTimeoutSecs = src.LLM.StrongHTTPTimeoutSecs
	}
	if src.LLM.ExtractMsgLimit != 0 {
		dst.LLM.ExtractMsgLimit = src.LLM.ExtractMsgLimit
	}
	if src.LLM.DebugPersistJSON != nil {
		dst.LLM.DebugPersistJSON = src.LLM.DebugPersistJSON
	}
	mergeDebugJSON(&dst.LLM.DebugJSON, &src.LLM.DebugJSON)
	if src.LLMBg.Provider != "" {
		dst.LLMBg.Provider = src.LLMBg.Provider
	}
	if src.LLMBg.APIKey != "" {
		dst.LLMBg.APIKey = src.LLMBg.APIKey
	}
	if src.LLMBg.Model != "" {
		dst.LLMBg.Model = src.LLMBg.Model
	}
	if src.LLMBg.StrongModel != "" {
		dst.LLMBg.StrongModel = src.LLMBg.StrongModel
	}
	if src.LLMBg.BaseURL != "" {
		dst.LLMBg.BaseURL = src.LLMBg.BaseURL
	}
	if src.LLMBg.HTTPTimeoutSecs != 0 {
		dst.LLMBg.HTTPTimeoutSecs = src.LLMBg.HTTPTimeoutSecs
	}
	if src.LLMBg.StrongHTTPTimeoutSecs != 0 {
		dst.LLMBg.StrongHTTPTimeoutSecs = src.LLMBg.StrongHTTPTimeoutSecs
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
	mergeSourceWeights(&dst.Retrieval.Theme, &src.Retrieval.Theme)
	mergeSourceWeights(&dst.Retrieval.Summary, &src.Retrieval.Summary)
	if src.Vector.MessageRetentionDays != 0 {
		dst.Vector.MessageRetentionDays = src.Vector.MessageRetentionDays
	}
	if src.Vector.SummaryRetentionDays != 0 {
		dst.Vector.SummaryRetentionDays = src.Vector.SummaryRetentionDays
	}
	if src.Vector.EvictIntervalHours != 0 {
		dst.Vector.EvictIntervalHours = src.Vector.EvictIntervalHours
	}
	if src.Vector.VacuumFreeThresholdMB != 0 {
		dst.Vector.VacuumFreeThresholdMB = src.Vector.VacuumFreeThresholdMB
	}
	if src.Vector.ChunkDisabledParents != nil {
		dst.Vector.ChunkDisabledParents = src.Vector.ChunkDisabledParents
	}
	if src.Activity.Enabled != nil {
		dst.Activity.Enabled = src.Activity.Enabled
	}
	if src.Activity.DBPath != "" {
		dst.Activity.DBPath = src.Activity.DBPath
	}
	if src.Activity.DayTimezone != "" {
		dst.Activity.DayTimezone = src.Activity.DayTimezone
	}
	if src.Activity.WatermarkMode != "" {
		dst.Activity.WatermarkMode = src.Activity.WatermarkMode
	}
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
	if len(src.Reflection.ActiveDurationDays) > 0 {
		dst.Reflection.ActiveDurationDays = src.Reflection.ActiveDurationDays
	}
	if src.Reflection.MinActiveDays != 0 {
		dst.Reflection.MinActiveDays = src.Reflection.MinActiveDays
	}
	if src.Chat.HistoryRounds != 0 {
		dst.Chat.HistoryRounds = src.Chat.HistoryRounds
	}
	if src.Chat.RecentUserMsgs != 0 {
		dst.Chat.RecentUserMsgs = src.Chat.RecentUserMsgs
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
	if src.Chat.SubAgentOnlyClientTools != nil {
		dst.Chat.SubAgentOnlyClientTools = src.Chat.SubAgentOnlyClientTools
	}
	if src.Chat.ImageReplayRounds != 0 {
		dst.Chat.ImageReplayRounds = src.Chat.ImageReplayRounds
	}
	if src.SubAgent.ExploreMaxIterations != 0 {
		dst.SubAgent.ExploreMaxIterations = src.SubAgent.ExploreMaxIterations
	}
	if src.SubAgent.ExploreTimeoutSeconds != 0 {
		dst.SubAgent.ExploreTimeoutSeconds = src.SubAgent.ExploreTimeoutSeconds
	}
	if src.SubAgent.UserContextEnabled != nil {
		dst.SubAgent.UserContextEnabled = src.SubAgent.UserContextEnabled
	}
	if src.Archive.AfterDays != 0 {
		dst.Archive.AfterDays = src.Archive.AfterDays
	}
	if src.Archive.IntervalMinutes != 0 {
		dst.Archive.IntervalMinutes = src.Archive.IntervalMinutes
	}
	if src.Storage.ImageDir != "" {
		dst.Storage.ImageDir = src.Storage.ImageDir
	}
	if src.Storage.MaxImageBytes != 0 {
		dst.Storage.MaxImageBytes = src.Storage.MaxImageBytes
	}
	if src.Storage.ImageTTLDays != 0 {
		dst.Storage.ImageTTLDays = src.Storage.ImageTTLDays
	}
	if src.Storage.ImageOrphanTTLDays != 0 {
		dst.Storage.ImageOrphanTTLDays = src.Storage.ImageOrphanTTLDays
	}
	if src.Storage.ImageCleanupIntervalMin != 0 {
		dst.Storage.ImageCleanupIntervalMin = src.Storage.ImageCleanupIntervalMin
	}
	// backup：Enabled 只允许 env 覆盖文件「打开」（bool 零值分不清未配置）；开关真正来源是 env
	if src.Backup.Enabled {
		dst.Backup.Enabled = true
	}
	if src.Backup.Dest != "" {
		dst.Backup.Dest = src.Backup.Dest
	}
	if src.Backup.IntervalDays != 0 {
		dst.Backup.IntervalDays = src.Backup.IntervalDays
	}
	if src.Backup.DailyKeep != 0 {
		dst.Backup.DailyKeep = src.Backup.DailyKeep
	}
	if src.Backup.WeeklyKeep != 0 {
		dst.Backup.WeeklyKeep = src.Backup.WeeklyKeep
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
	if src.ExtraTraceRetentionDays != 0 {
		dst.ExtraTraceRetentionDays = src.ExtraTraceRetentionDays
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
	cfg.LLMBg.APIKey = os.ExpandEnv(cfg.LLMBg.APIKey)
	cfg.Embedding.APIKey = os.ExpandEnv(cfg.Embedding.APIKey)
	cfg.Server.LocalToken = os.ExpandEnv(cfg.Server.LocalToken)
	applyLLMEnvOverrides(cfg)
	applyBackupEnvOverrides(cfg)
}

// applyBackupEnvOverrides 应用 Swift 设置页传入的备份配置。
// 走「非空才覆盖」——与 applyLLMEnvOverrides 同款：未设置时不冲掉 YAML 默认值。
// 解析失败只记日志并跳过（Swift UI 送的是它自己的数值，正常不会走到这里）。
func applyBackupEnvOverrides(cfg *Config) {
	if v := os.Getenv("XHAN_BACKUP_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Backup.Enabled = b
		} else {
			log.Printf("[config] XHAN_BACKUP_ENABLED 解析失败（%q），沿用默认：%v", v, err)
		}
	}
	if v := os.Getenv("XHAN_BACKUP_DEST"); v != "" {
		cfg.Backup.Dest = v
	}
	applyBackupInt := func(key string, dst *int) {
		v := os.Getenv(key)
		if v == "" {
			return
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			log.Printf("[config] %s 解析失败（%q），沿用默认：%v", key, v, err)
			return
		}
		*dst = n
	}
	applyBackupInt("XHAN_BACKUP_INTERVAL_DAYS", &cfg.Backup.IntervalDays)
	applyBackupInt("XHAN_BACKUP_DAILY_KEEP", &cfg.Backup.DailyKeep)
	applyBackupInt("XHAN_BACKUP_WEEKLY_KEEP", &cfg.Backup.WeeklyKeep)
}

// applyLLMEnvOverrides 应用本地 app 传入的可选覆盖项。
// 走「非空才覆盖」——不能用 ${VAR} 占位符：os.Expand 对未设置的变量展开成空串，
// 会把 YAML 里的默认 base_url / model 冲掉。
//
// 同时覆盖 llm_bg：`conf/config.yaml` 里 llm_bg 自带非空 model/base_url（不回落主 LLM），
// 只改主 LLM 的话后台任务（记忆/摘要/主题）仍会打默认地址、用默认模型名 —— 换了供应商/网关
// 就会 400/404。UI 只暴露一对 model/base_url，两套必须一起走。
func applyLLMEnvOverrides(cfg *Config) {
	if v := os.Getenv("XHAN_LLM_BASE_URL"); v != "" {
		cfg.LLM.BaseURL = v
		cfg.LLMBg.BaseURL = v
	}
	if v := os.Getenv("XHAN_LLM_MODEL"); v != "" {
		cfg.LLM.Model = v
		cfg.LLMBg.Model = v
	}
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
	// Activity（events.db）水位口径：只允许 time / segment（空 = time）。
	// 字面量与 activity.WatermarkTime / WatermarkSegment 同步（config 是最底层，不反向依赖 activity/model）。
	if m := cfg.Activity.WatermarkModeNormalized(); m != "time" && m != "segment" {
		return fmt.Errorf("activity.watermark_mode 必须是 time 或 segment（当前 %q）", cfg.Activity.WatermarkMode)
	}
	// Activity 日期边界时区：空 = 沿用主机 Local；非空必须是可加载的 IANA 名。
	if _, err := cfg.Activity.DayLocation(); err != nil {
		return err
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
	// Vector 默认值
	if cfg.Vector.MessageRetentionDays == 0 {
		cfg.Vector.MessageRetentionDays = 14
	}
	if cfg.Vector.SummaryRetentionDays == 0 {
		cfg.Vector.SummaryRetentionDays = 30
	}
	if cfg.Vector.EvictIntervalHours == 0 {
		cfg.Vector.EvictIntervalHours = 24
	}
	if cfg.Vector.VacuumFreeThresholdMB == 0 {
		cfg.Vector.VacuumFreeThresholdMB = 64
	}
	if cfg.Vector.ChunkDisabledParents == nil {
		// 值必须与 model.EntityType* 一致（由 TestChunkDisabledParentsDefaultMatchesModel 钉住）。
		// 这里刻意用字面量：config 是最底层，不反向依赖 model。
		cfg.Vector.ChunkDisabledParents = []string{"memory", "note", "summary"}
	}
	// 记录循环默认值（记忆两段式）
	if cfg.Server.RecordLoopMaxIterations == 0 {
		cfg.Server.RecordLoopMaxIterations = 8
	}
	if cfg.Server.MemoryMaxPerSession == 0 {
		cfg.Server.MemoryMaxPerSession = 2
	}
	if cfg.Server.MemoryNeighborTopK == 0 {
		cfg.Server.MemoryNeighborTopK = 5
	}
	if cfg.Server.MemoryNeighborThreshold == 0 {
		cfg.Server.MemoryNeighborThreshold = 0.5
	}
	if cfg.Server.MemoryConfirmThreshold == 0 {
		cfg.Server.MemoryConfirmThreshold = 0.80
	}
	if cfg.Server.MemoryTopicReuseThreshold == 0 {
		cfg.Server.MemoryTopicReuseThreshold = 0.80
	}
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
		cfg.Retrieval.ResourceZone.QuotaSummary = 1
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
	if cfg.LLM.HTTPTimeoutSecs == 0 {
		cfg.LLM.HTTPTimeoutSecs = 120
	}
	if cfg.LLM.StrongHTTPTimeoutSecs == 0 {
		cfg.LLM.StrongHTTPTimeoutSecs = 600
	}
	if cfg.LLMBg.Model != "" {
		if cfg.LLMBg.HTTPTimeoutSecs == 0 {
			cfg.LLMBg.HTTPTimeoutSecs = 120
		}
		if cfg.LLMBg.StrongHTTPTimeoutSecs == 0 {
			cfg.LLMBg.StrongHTTPTimeoutSecs = 600
		}
		if cfg.LLMBg.APIKey == "" || strings.Contains(cfg.LLMBg.APIKey, "${") {
			return fmt.Errorf("llm_bg.model 已配置但 LLM_BG_API_KEY 缺失或未展开（环境变量未设置）")
		}
	}
	if cfg.LLM.ExtractMsgLimit == 0 {
		cfg.LLM.ExtractMsgLimit = 50
	}
	if cfg.Chat.HistoryRounds == 0 {
		cfg.Chat.HistoryRounds = 10
	}
	if cfg.Chat.RecentUserMsgs == 0 {
		cfg.Chat.RecentUserMsgs = 3
	}
	if cfg.Chat.MaxWindowRounds == 0 {
		cfg.Chat.MaxWindowRounds = 100
	}
	if cfg.Chat.SessionGapMinutes == 0 {
		cfg.Chat.SessionGapMinutes = 30
	}
	if cfg.Chat.SummaryAttachCount == 0 {
		cfg.Chat.SummaryAttachCount = 1
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
	if cfg.Chat.SubAgentOnlyClientTools == nil {
		cfg.Chat.SubAgentOnlyClientTools = []string{"fs", "shell"}
	}
	if cfg.Chat.ImageReplayRounds == 0 {
		cfg.Chat.ImageReplayRounds = 10
	}
	// 图片存储默认值
	if cfg.Storage.ImageDir == "" {
		cfg.Storage.ImageDir = "data/images"
	}
	if cfg.Storage.MaxImageBytes == 0 {
		cfg.Storage.MaxImageBytes = 32 << 20
	}
	if cfg.Storage.ImageTTLDays == 0 {
		cfg.Storage.ImageTTLDays = 15
	}
	if cfg.Storage.ImageOrphanTTLDays == 0 {
		cfg.Storage.ImageOrphanTTLDays = 3
	}
	if cfg.Storage.ImageCleanupIntervalMin == 0 {
		cfg.Storage.ImageCleanupIntervalMin = 720
	}
	// 备份默认值（0 视为未配置；不允许用 0 关闭保留）
	if cfg.Backup.IntervalDays <= 0 {
		cfg.Backup.IntervalDays = 1
	}
	if cfg.Backup.DailyKeep <= 0 {
		cfg.Backup.DailyKeep = 3
	}
	if cfg.Backup.WeeklyKeep <= 0 {
		cfg.Backup.WeeklyKeep = 1
	}
	// 联网工具（DeepSeek 官方 Search / Fetch）默认值
	if cfg.WebTools.BaseURL == "" {
		cfg.WebTools.BaseURL = "https://api.deepseek.com"
	}
	if cfg.WebTools.TimeoutSeconds == 0 {
		cfg.WebTools.TimeoutSeconds = 30
	}
	if cfg.WebTools.SearchCount == 0 {
		cfg.WebTools.SearchCount = 8
	}
	if cfg.WebTools.MaxContentChars == 0 {
		cfg.WebTools.MaxContentChars = 8000
	}
	if cfg.WebTools.SearchMaxChars == 0 {
		cfg.WebTools.SearchMaxChars = 16000
	}
	if cfg.WebTools.DailyLimit == 0 {
		cfg.WebTools.DailyLimit = 1000
	}
	// key 回退：官方 Search/Fetch 与 DeepSeek 大模型同一个 key，非 deepseek provider 需单独配
	if cfg.WebTools.APIKey == "" && cfg.LLM.Provider == "deepseek" {
		cfg.WebTools.APIKey = cfg.LLM.APIKey
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
	if d.ExtraTraceRetentionDays == 0 {
		d.ExtraTraceRetentionDays = 14
	}
	return nil
}
