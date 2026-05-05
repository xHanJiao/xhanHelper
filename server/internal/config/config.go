package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config — 顶层配置
type Config struct {
	Server    ServerConfig    `yaml:"server"`
	DB        DBConfig        `yaml:"db"`
	LLM       LLMConfig       `yaml:"llm"`
	Embedding EmbeddingConfig `yaml:"embedding"`
}

// ServerConfig — HTTP 服务配置
type ServerConfig struct {
	Port                int      `yaml:"port"`
	Mode                string   `yaml:"mode"`                 // debug | release
	RegistrationEnabled *bool    `yaml:"registration_enabled"` // 是否允许新用户注册
	AllowedUsers        []string `yaml:"allowed_users"`        // 允许注册的用户名列表（空=全部允许）
	MemoryAutoExtract   bool     `yaml:"memory_auto_extract"`  // 是否自动提取记忆
	SidecarURL          string   `yaml:"sidecar_url"`          // Python sidecar MCP HTTP 地址
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
type LLMConfig struct {
	Provider      string `yaml:"provider"` // doubao | deepseek
	APIKey        string `yaml:"api_key"`
	Model         string `yaml:"model"`          // 弱模型（日常使用）
	StrongModel   string `yaml:"strong_model"`   // 强模型（深度思考模式，可选，默认同 Model）
	BaseURL       string `yaml:"base_url"`       // LLM API 地址
	MaxIterations int    `yaml:"max_iterations"` // ReAct 最大迭代次数（默认 8）
}

// EmbeddingConfig — 向量化配置
type EmbeddingConfig struct {
	APIKey              string `yaml:"api_key"`
	BaseURL             string `yaml:"base_url"`              // 默认 https://ark.cn-beijing.volces.com
	Model               string `yaml:"model"`                 // 默认 doubao-embedding
	EnableHistorySearch bool   `yaml:"enable_history_search"` // 是否检索历史对话
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
	if src.Embedding.APIKey != "" {
		dst.Embedding.APIKey = src.Embedding.APIKey
	}
	if src.Embedding.BaseURL != "" {
		dst.Embedding.BaseURL = src.Embedding.BaseURL
	}
	if src.Embedding.Model != "" {
		dst.Embedding.Model = src.Embedding.Model
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
	return nil
}
