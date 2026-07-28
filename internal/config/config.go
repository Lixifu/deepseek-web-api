package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// Config 全局配置
type Config struct {
	AppEnv                string `mapstructure:"APP_ENV"`
	AppHost               string `mapstructure:"APP_HOST"`
	AppPort               int    `mapstructure:"APP_PORT"`
	LogLevel              string `mapstructure:"APP_LOG_LEVEL"`
	MysqlDSN              string `mapstructure:"MYSQL_DSN"`
	RedisAddr             string `mapstructure:"REDIS_ADDR"`
	RedisDB               int    `mapstructure:"REDIS_DB"`
	RedisSharedQueue      bool   `mapstructure:"REDIS_SHARED_QUEUE_ENABLED"`
	RedisQueuePrefix      string `mapstructure:"REDIS_QUEUE_KEY_PREFIX"`
	PoolSize              int    `mapstructure:"BROWSER_POOL_SIZE"`
	ClusterConcurrency    int    `mapstructure:"BROWSER_CLUSTER_CONCURRENCY"`
	QueueMaxSize          int    `mapstructure:"BROWSER_QUEUE_MAX_SIZE"`
	QueueTimeout          int    `mapstructure:"BROWSER_QUEUE_TIMEOUT_SECONDS"`
	QueueLeaseTTL         int    `mapstructure:"BROWSER_QUEUE_LEASE_TTL_SECONDS"`
	QueuePollMS           int    `mapstructure:"BROWSER_QUEUE_POLL_INTERVAL_MS"`
	Headless              bool   `mapstructure:"BROWSER_HEADLESS"`
	StorageDir            string `mapstructure:"BROWSER_STORAGE_DIR"`
	JWTSecret             string `mapstructure:"ADMIN_JWT_SECRET"`
	AdminUser             string `mapstructure:"ADMIN_DEFAULT_USERNAME"`
	AdminPass             string `mapstructure:"ADMIN_DEFAULT_PASSWORD"`
	RateLimit             int    `mapstructure:"RATE_LIMIT_PER_MINUTE"`
	AdminLoginRate        int    `mapstructure:"ADMIN_LOGIN_RATE_LIMIT_PER_MINUTE"`
	AuditArchiveDays      int    `mapstructure:"AUDIT_ARCHIVE_AFTER_DAYS"`
	AuditArchiveInterval  int    `mapstructure:"AUDIT_ARCHIVE_INTERVAL_HOURS"`
	AuditArchiveRetention int    `mapstructure:"AUDIT_ARCHIVE_RETENTION_DAYS"`
	AuditArchiveBatch     int    `mapstructure:"AUDIT_ARCHIVE_BATCH_SIZE"`
	AuditExportMaxRows    int    `mapstructure:"AUDIT_EXPORT_MAX_ROWS"`
	CORSOrigins           string `mapstructure:"CORS_ALLOWED_ORIGINS"`
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
	viper.SetDefault("BROWSER_CLUSTER_CONCURRENCY", 0)
	viper.SetDefault("BROWSER_QUEUE_MAX_SIZE", 100)
	viper.SetDefault("BROWSER_QUEUE_TIMEOUT_SECONDS", 120)
	viper.SetDefault("BROWSER_QUEUE_LEASE_TTL_SECONDS", 60)
	viper.SetDefault("BROWSER_QUEUE_POLL_INTERVAL_MS", 100)
	viper.SetDefault("BROWSER_HEADLESS", true)
	viper.SetDefault("REDIS_SHARED_QUEUE_ENABLED", false)
	viper.SetDefault("REDIS_QUEUE_KEY_PREFIX", "deepseek_web_api")
	viper.SetDefault("RATE_LIMIT_PER_MINUTE", 60)
	viper.SetDefault("ADMIN_LOGIN_RATE_LIMIT_PER_MINUTE", 5)
	viper.SetDefault("AUDIT_ARCHIVE_AFTER_DAYS", 90)
	viper.SetDefault("AUDIT_ARCHIVE_INTERVAL_HOURS", 24)
	viper.SetDefault("AUDIT_ARCHIVE_RETENTION_DAYS", 0)
	viper.SetDefault("AUDIT_ARCHIVE_BATCH_SIZE", 1000)
	viper.SetDefault("AUDIT_EXPORT_MAX_ROWS", 10000)

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
	if c.AppEnv == "production" && len(c.JWTSecret) < 32 {
		return nil, fmt.Errorf("ADMIN_JWT_SECRET must be at least 32 characters in production")
	}
	if c.AdminUser == "" || c.AdminPass == "" {
		return nil, fmt.Errorf("ADMIN_DEFAULT_USERNAME and ADMIN_DEFAULT_PASSWORD are required")
	}
	if c.PoolSize < 1 {
		return nil, fmt.Errorf("BROWSER_POOL_SIZE must be positive")
	}
	if c.ClusterConcurrency < 0 {
		return nil, fmt.Errorf("BROWSER_CLUSTER_CONCURRENCY must be non-negative")
	}
	if c.QueueMaxSize < 0 || c.QueueTimeout < 0 {
		return nil, fmt.Errorf("browser queue limits must be non-negative")
	}
	if c.QueueLeaseTTL < 1 || c.QueuePollMS < 10 {
		return nil, fmt.Errorf("browser queue lease TTL and poll interval must be positive")
	}
	if c.AuditArchiveDays < 1 || c.AuditArchiveInterval < 1 ||
		c.AuditArchiveRetention < 0 || c.AuditArchiveBatch < 1 ||
		c.AuditExportMaxRows < 1 {
		return nil, fmt.Errorf("audit archive configuration is invalid")
	}
	return &c, nil
}
