package model

import "time"

// Conversation 一次对话记录
type Conversation struct {
	ID           string    `gorm:"primaryKey;size:36" json:"id"`
	APIKeyID     uint      `gorm:"index" json:"api_key_id"`
	AccountID    uint      `gorm:"index" json:"account_id"`
	Model        string    `gorm:"size:64" json:"model"`
	Prompt       string    `gorm:"type:text;not null" json:"prompt"`
	Reply        string    `gorm:"type:longtext" json:"reply"`
	PromptTokens int       `json:"prompt_tokens"`
	ReplyTokens  int       `json:"reply_tokens"`
	DurationMs   int       `json:"duration_ms"`
	Status       string    `gorm:"size:16" json:"status"` // success / failed / streaming
	Error        string    `gorm:"type:text" json:"error"`
	CreatedAt    time.Time `gorm:"index" json:"created_at"`
}

func (Conversation) TableName() string { return "conversations" }

// UsageHourly 按小时聚合的调用统计，供仪表盘使用
type UsageHourly struct {
	APIKeyID uint      `gorm:"primaryKey" json:"api_key_id"`
	Hour     time.Time `gorm:"primaryKey" json:"hour"`
	Success  int       `gorm:"default:0" json:"success"`
	Failed   int       `gorm:"default:0" json:"failed"`
}

func (UsageHourly) TableName() string { return "usage_hourly" }

// Admin 管理后台账号（仅一条/几条，存表便于改密）
type Admin struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Username     string    `gorm:"size:64;uniqueIndex;not null" json:"username"`
	PasswordHash string    `gorm:"size:128;not null" json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

func (Admin) TableName() string { return "admins" }
