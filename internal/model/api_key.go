package model

import "time"

// APIKey 客户端调用 /v1/* 时使用的密钥
type APIKey struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	Name          string    `gorm:"size:64;not null" json:"name"`
	KeyPrefix     string    `gorm:"size:16;index;not null" json:"key_prefix"` // 明文 key 前 8 位，用于定位
	KeyHash       string    `gorm:"size:128;uniqueIndex;not null" json:"-"`   // bcrypt 哈希
	QuotaPerDay   int       `gorm:"default:1000" json:"quota_per_day"`
	AllowedModels string    `gorm:"type:json" json:"allowed_models"`         // ["deepseek-chat","deepseek-reasoner"]
	DefaultModel  string    `gorm:"size:64;default:''" json:"default_model"` // 绑定模型：非空时强制使用此模型，忽略请求的 model
	Enabled       bool      `gorm:"default:true" json:"enabled"`
	CreatedAt     time.Time `json:"created_at"`
}

func (APIKey) TableName() string { return "api_keys" }
