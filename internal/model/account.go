package model

import "time"

// Account DeepSeek 网页账号（对应一个 storage_state 登录态）
type Account struct {
	ID            uint       `gorm:"primaryKey" json:"id"`
	Name          string     `gorm:"size:64;uniqueIndex;not null" json:"name"`
	StoragePath   string     `gorm:"size:256;not null" json:"storage_path"`
	Status        string     `gorm:"size:16;default:active" json:"status"` // active / disabled / expired
	DefaultModel  string     `gorm:"size:64;default:deepseek-chat" json:"default_model"`
	LastUsedAt    *time.Time `json:"last_used_at"`
	LastCheckAt   *time.Time `json:"last_check_at"`
	Note          string     `gorm:"type:text" json:"note"`
	CreatedAt     time.Time  `json:"created_at"`
}

func (Account) TableName() string { return "accounts" }
