// init_db 初始化数据库表并创建默认管理员。
// 用法: go run ./scripts/init_db
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"deepseek-web-api/internal/config"
	"deepseek-web-api/internal/model"
	"deepseek-web-api/internal/repository"
)

func main() {
	logger, _ := zap.NewProduction()
	defer logger.Sync()

	cfg, err := config.Load()
	if err != nil {
		logger.Fatal("load config", zap.Error(err))
	}

	db, err := gorm.Open(mysql.Open(cfg.MysqlDSN), &gorm.Config{})
	if err != nil {
		logger.Fatal("connect mysql", zap.Error(err))
	}
	if err := db.AutoMigrate(
		&model.Account{}, &model.APIKey{}, &model.Conversation{},
		&model.UsageHourly{}, &model.Admin{}, &model.AuditLog{},
		&model.AuditLogArchive{},
	); err != nil {
		logger.Fatal("auto migrate", zap.Error(err))
	}
	fmt.Println("✓ tables migrated")

	repo := repository.New(db)

	// 创建默认管理员
	if _, err := repo.GetAdminByUsername(context.Background(), cfg.AdminUser); err != nil {
		hash, _ := bcrypt.GenerateFromPassword([]byte(cfg.AdminPass), bcrypt.DefaultCost)
		if err := repo.CreateAdmin(context.Background(), &model.Admin{
			Username:     cfg.AdminUser,
			PasswordHash: string(hash),
			Role:         "superadmin",
			Enabled:      true,
			TokenVersion: 1,
		}); err != nil {
			logger.Fatal("create admin", zap.Error(err))
		}
		fmt.Printf("✓ admin created: %s\n", cfg.AdminUser)
	} else {
		fmt.Printf("→ admin already exists: %s\n", cfg.AdminUser)
	}

	// 探测 Redis（仅提示，不阻塞）
	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, DB: cfg.RedisDB})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		fmt.Println("⚠ redis 不可用:", err)
	} else {
		fmt.Println("✓ redis ok")
	}

	// 提示后续步骤
	fmt.Println("\n下一步：")
	fmt.Println("  1. go run ./scripts/login_capture -out data/storage_states/account_1.json")
	fmt.Println("  2. 启动服务：go run ./cmd/server")
	fmt.Println("  3. 或在管理后台创建账号并上传 storage_state.json")
	_ = os.Stdout.Sync()
	_ = time.Now
}
