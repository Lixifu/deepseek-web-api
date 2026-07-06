package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"deepseek-web-api/internal/api/admin"
	v1 "deepseek-web-api/internal/api/v1"
	"deepseek-web-api/internal/config"
	"deepseek-web-api/internal/core"
	"deepseek-web-api/internal/middleware"
	"deepseek-web-api/internal/model"
	"deepseek-web-api/internal/repository"
)

var installBrowsers = flag.Bool("install-browsers", false, "install Playwright Chromium browsers then exit")

func main() {
	flag.Parse()

	// -install-browsers 子命令：仅安装浏览器
	if *installBrowsers {
		if err := installBrowsersOnly(); err != nil {
			fmt.Println("install browsers failed:", err)
			os.Exit(1)
		}
		fmt.Println("browsers installed")
		return
	}

	// 日志
	logger, _ := zap.NewProduction()
	defer logger.Sync()

	// 配置
	cfg, err := config.Load()
	if err != nil {
		logger.Fatal("load config", zap.Error(err))
	}
	if cfg.AppEnv == "development" {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	// MySQL
	db, err := gorm.Open(mysql.Open(cfg.MysqlDSN), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Warn),
	})
	if err != nil {
		logger.Fatal("connect mysql", zap.Error(err))
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(50)
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(time.Hour)

	// 自动建表（生产建议用 SQL 脚本）
	if err := db.AutoMigrate(
		&model.Account{}, &model.APIKey{}, &model.Conversation{},
		&model.UsageHourly{}, &model.Admin{},
	); err != nil {
		logger.Fatal("auto migrate", zap.Error(err))
	}

	repo := repository.New(db)

	// 初始化默认管理员
	if err := seedAdmin(repo, cfg); err != nil {
		logger.Warn("seed admin", zap.Error(err))
	}

	// Redis
	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, DB: cfg.RedisDB})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		logger.Warn("redis ping failed, rate limiter disabled", zap.Error(err))
	}

	// 加载账号
	accs, err := repo.ListActiveAccounts(context.Background())
	if err != nil {
		logger.Fatal("list accounts", zap.Error(err))
	}
	var accConfigs []core.AccountConfig
	for _, a := range accs {
		if a.StoragePath == "" {
			continue
		}
		accConfigs = append(accConfigs, core.AccountConfig{
			ID: a.ID, Name: a.Name, StoragePath: a.StoragePath,
		})
	}

	// 浏览器池
	pool := core.NewBrowserPool(cfg.Headless, logger)
	if len(accConfigs) > 0 {
		if err := pool.Start(accConfigs); err != nil {
			logger.Fatal("start browser pool", zap.Error(err))
		}
		defer pool.Stop()
	} else {
		logger.Warn("no active accounts configured, browser pool not started")
	}

	// 限流器
	var limiter core.Limiter
	if rdb != nil {
		limiter = core.NewRateLimiter(rdb, cfg.RateLimit)
	}

	orch := &core.Orchestrator{
		Pool:     pool,
		Repo:     repo,
		Limiter:  limiter,
		Selector: core.DefaultSelectors,
		Logger:   logger,
	}

	// Gin
	r := gin.New()
	r.Use(middleware.Recovery(logger), middleware.Logger(logger), cors.Default())

	// 健康检查
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":    "ok",
			"available": pool.Available(),
		})
	})

	// v1 API（OpenAI 兼容）
	v1g := r.Group("/v1", middleware.APIKeyAuth(repo, logger))
	v1.Register(v1g, &v1.Handler{Orch: orch})

	// 管理后台
	adminH := &admin.Handler{
		Repo:       repo,
		Pool:       pool,
		JWTSecret:  cfg.JWTSecret,
		StorageDir: cfg.StorageDir,
		Selectors:  core.DefaultSelectors,
		Logger:     logger,
	}
	admin.Register(r.Group("/admin"), adminH)

	// 启动 HTTP
	srv := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.AppHost, cfg.AppPort),
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 600 * time.Second, // SSE 长连接
	}
	go func() {
		logger.Info("server starting", zap.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("listen", zap.Error(err))
		}
	}()

	// 优雅退出
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("forced shutdown", zap.Error(err))
	}
	logger.Info("server stopped")
}

// seedAdmin 如果不存在管理员，用配置的默认账号创建一个
func seedAdmin(repo *repository.Repository, cfg *config.Config) error {
	if _, err := repo.GetAdminByUsername(context.Background(), cfg.AdminUser); err == nil {
		return nil // 已存在
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(cfg.AdminPass), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return repo.CreateAdmin(context.Background(), &model.Admin{
		Username:     cfg.AdminUser,
		PasswordHash: string(hash),
	})
}

// installBrowsersOnly 安装 Playwright Chromium
func installBrowsersOnly() error {
	return core.InstallBrowsers()
}
