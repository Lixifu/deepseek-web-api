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
	ID           uint       `gorm:"primaryKey" json:"id"`
	Username     string     `gorm:"size:64;uniqueIndex;not null" json:"username"`
	PasswordHash string     `gorm:"size:128;not null" json:"-"`
	Role         string     `gorm:"size:16;not null;default:superadmin;index" json:"role"`
	Enabled      bool       `gorm:"not null;default:true" json:"enabled"`
	TokenVersion uint       `gorm:"not null;default:1" json:"-"`
	LastLoginAt  *time.Time `json:"last_login_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

func (Admin) TableName() string { return "admins" }

// AuditLog 管理后台操作审计记录，不保存请求体和敏感字段。
type AuditLog struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	AdminID    uint      `gorm:"index;not null" json:"admin_id"`
	AdminName  string    `gorm:"size:64;not null" json:"admin_name"`
	Action     string    `gorm:"size:32;not null;index" json:"action"`
	Resource   string    `gorm:"size:64;not null;index" json:"resource"`
	ResourceID string    `gorm:"size:64" json:"resource_id"`
	Method     string    `gorm:"size:10;not null" json:"method"`
	Path       string    `gorm:"size:255;not null" json:"path"`
	ClientIP   string    `gorm:"size:64" json:"client_ip"`
	Status     int       `gorm:"not null" json:"status"`
	CreatedAt  time.Time `gorm:"index" json:"created_at"`
}

func (AuditLog) TableName() string { return "audit_logs" }

// AuditLogArchive stores immutable audit records moved out of the hot table.
// ID retains the original audit_logs primary key for idempotent archiving.
type AuditLogArchive struct {
	ID         uint      `gorm:"primaryKey;autoIncrement:false" json:"id"`
	AdminID    uint      `gorm:"index;not null" json:"admin_id"`
	AdminName  string    `gorm:"size:64;not null" json:"admin_name"`
	Action     string    `gorm:"size:32;not null;index" json:"action"`
	Resource   string    `gorm:"size:64;not null;index" json:"resource"`
	ResourceID string    `gorm:"size:64" json:"resource_id"`
	Method     string    `gorm:"size:10;not null" json:"method"`
	Path       string    `gorm:"size:255;not null" json:"path"`
	ClientIP   string    `gorm:"size:64" json:"client_ip"`
	Status     int       `gorm:"not null" json:"status"`
	CreatedAt  time.Time `gorm:"index" json:"created_at"`
	ArchivedAt time.Time `gorm:"index;not null" json:"archived_at"`
}

func (AuditLogArchive) TableName() string { return "audit_log_archives" }
