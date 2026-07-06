package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// Config 全局配置
type Config struct {
	AppEnv     string `mapstructure:"APP_ENV"`
	AppHost    string `mapstructure:"APP_HOST"`
	AppPort    int    `mapstructure:"APP_PORT"`
	LogLevel   string `mapstructure:"APP_LOG_LEVEL"`
	MysqlDSN   string `mapstructure:"MYSQL_DSN"`
	RedisAddr  string `mapstructure:"REDIS_ADDR"`
	RedisDB    int    `mapstructure:"REDIS_DB"`
	PoolSize   int    `mapstructure:"BROWSER_POOL_SIZE"`
	Headless   bool   `mapstructure:"BROWSER_HEADLESS"`
	StorageDir string `mapstructure:"BROWSER_STORAGE_DIR"`
	JWTSecret  string `mapstructure:"ADMIN_JWT_SECRET"`
	AdminUser  string `mapstructure:"ADMIN_DEFAULT_USERNAME"`
	AdminPass  string `mapstructure:"ADMIN_DEFAULT_PASSWORD"`
	RateLimit  int    `mapstructure:"RATE_LIMIT_PER_MINUTE"`
}

// Load 从环境变量（可选 .env 文件）加载配置
func Load() (*Config, error) {
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))

	// 可选：读取 .env 文件（开发环境用）
	viper.SetConfigName(".env")
	viper.SetConfigType("env")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./config")
	if err := viper.ReadInConfig(); err != nil {
		// 找不到 .env 不算错误，用纯环境变量
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("read config: %w", err)
		}
	}

	// 默认值
	viper.SetDefault("APP_PORT", 8000)
	viper.SetDefault("BROWSER_POOL_SIZE", 4)
	viper.SetDefault("BROWSER_HEADLESS", true)
	viper.SetDefault("RATE_LIMIT_PER_MINUTE", 60)

	var c Config
	if err := viper.Unmarshal(&c); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	if c.MysqlDSN == "" {
		return nil, fmt.Errorf("MYSQL_DSN is required")
	}
	if c.JWTSecret == "" {
		return nil, fmt.Errorf("ADMIN_JWT_SECRET is required")
	}
	return &c, nil
}
