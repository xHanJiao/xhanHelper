package mcp

// ServerConfig — 单个 MCP server 配置
type ServerConfig struct {
	Name            string            `yaml:"name"`
	Command         string            `yaml:"command"`
	Args            []string          `yaml:"args"`
	Env             map[string]string `yaml:"env"`
	IdleTimeoutSecs int               `yaml:"idle_timeout_secs"` // 默认 300
	Description     string            `yaml:"description"`
}

// Config — MCP 总配置
type Config struct {
	MaxConcurrent int            `yaml:"max_concurrent"` // 默认 2
	Servers       []ServerConfig `yaml:"servers"`
}

// Defaults 填充默认值
func (c *Config) Defaults() {
	if c.MaxConcurrent <= 0 {
		c.MaxConcurrent = 2
	}
	for i := range c.Servers {
		if c.Servers[i].IdleTimeoutSecs <= 0 {
			c.Servers[i].IdleTimeoutSecs = 300
		}
	}
}
