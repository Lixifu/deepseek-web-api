package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
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
	"deepseek-web-api/internal/maintenance"
	"deepseek-web-api/internal/middleware"
	"deepseek-web-api/internal/model"
	"deepseek-web-api/internal/observability"
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
		&model.UsageHourly{}, &model.Admin{}, &model.AuditLog{}, &model.AuditLogArchive{},
	); err != nil {
		logger.Fatal("auto migrate", zap.Error(err))
	}

	repo := repository.New(db)

	// 初始化默认管理员
	if err := seedAdmin(repo, cfg); err != nil {
		logger.Warn("seed admin", zap.Error(err))
	}

	// Redis
	var rdb *redis.Client
	rdb = redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, DB: cfg.RedisDB})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		logger.Warn("redis ping failed, rate limiter disabled", zap.Error(err))
		_ = rdb.Close()
		rdb = nil
	} else {
		defer rdb.Close()
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
		if cfg.PoolSize > 0 && len(accConfigs) >= cfg.PoolSize {
			break
		}
	}

	// 浏览器池
	pool := core.NewBrowserPool(cfg.Headless, logger)
	pool.Configure(cfg.PoolSize, cfg.QueueMaxSize, time.Duration(cfg.QueueTimeout)*time.Second)
	if cfg.RedisSharedQueue {
		if rdb == nil {
			logger.Fatal("redis shared queue is enabled but redis is unavailable")
		}
		clusterCapacity := cfg.ClusterConcurrency
		if clusterCapacity == 0 {
			clusterCapacity = cfg.PoolSize
		}
		sharedQueue, err := core.NewRedisSharedQueue(rdb, core.RedisSharedQueueConfig{
			KeyPrefix:    cfg.RedisQueuePrefix,
			Capacity:     clusterCapacity,
			MaxQueue:     cfg.QueueMaxSize,
			WaitTimeout:  time.Duration(cfg.QueueTimeout) * time.Second,
			LeaseTTL:     time.Duration(cfg.QueueLeaseTTL) * time.Second,
			PollInterval: time.Duration(cfg.QueuePollMS) * time.Millisecond,
		}, logger)
		if err != nil {
			logger.Fatal("configure redis shared queue", zap.Error(err))
		}
		pool.SetSharedQueue(sharedQueue)
		logger.Info("redis shared browser queue enabled",
			zap.Int("cluster_capacity", clusterCapacity),
			zap.Int("max_waiting", cfg.QueueMaxSize))
	}
	if err := pool.Start(accConfigs); err != nil {
		logger.Fatal("start browser pool", zap.Error(err))
	}
	defer pool.Stop()
	if len(accConfigs) == 0 {
		logger.Warn("no active accounts configured; browser is ready for hot loading")
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
	if err := r.SetTrustedProxies([]string{"127.0.0.1", "::1"}); err != nil {
		logger.Fatal("configure trusted proxies", zap.Error(err))
	}
	r.Use(middleware.Recovery(logger), middleware.Logger(logger))
	if cfg.CORSOrigins != "" {
		origins := strings.Split(cfg.CORSOrigins, ",")
		for i := range origins {
			origins[i] = strings.TrimSpace(origins[i])
		}
		r.Use(cors.New(cors.Config{
			AllowOrigins: origins,
			AllowMethods: []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
			AllowHeaders: []string{"Authorization", "Content-Type", "X-Admin-Token"},
			MaxAge:       12 * time.Hour,
		}))
	}

	// 健康检查
	r.GET("/healthz", func(c *gin.Context) {
		queueCtx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
		defer cancel()
		c.JSON(http.StatusOK, gin.H{
			"status":    "ok",
			"available": pool.Available(),
			"queued":    pool.EffectiveQueueLength(queueCtx),
		})
	})
	r.GET("/metrics", gin.WrapH(observability.Handler()))

	metricsStop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			total, healthy, busy := pool.SessionStats()
			queueCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			queued := pool.EffectiveQueueLength(queueCtx)
			cancel()
			observability.UpdatePool(total, healthy, busy, queued)
			observability.UpdateBrowserMemory(observability.ChromiumMemoryBytes())
			select {
			case <-ticker.C:
			case <-metricsStop:
				return
			}
		}
	}()

	auditArchiver := maintenance.NewAuditArchiver(repo, rdb, maintenance.AuditArchiverConfig{
		ArchiveAfterDays: cfg.AuditArchiveDays,
		RetentionDays:    cfg.AuditArchiveRetention,
		BatchSize:        cfg.AuditArchiveBatch,
		LockKeyPrefix:    cfg.RedisQueuePrefix,
	})
	auditArchiveStop := make(chan struct{})
	go func() {
		run := func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			result, err := auditArchiver.RunOnce(ctx)
			if err != nil {
				logger.Warn("scheduled audit archive failed", zap.Error(err))
				return
			}
			if !result.Skipped && (result.Archived > 0 || result.Deleted > 0) {
				logger.Info("scheduled audit archive completed",
					zap.Int64("archived", result.Archived),
					zap.Int64("deleted", result.Deleted))
			}
		}
		run()
		ticker := time.NewTicker(time.Duration(cfg.AuditArchiveInterval) * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				run()
			case <-auditArchiveStop:
				return
			}
		}
	}()

	// v1 API（OpenAI 兼容）
	v1g := r.Group("/v1", middleware.APIKeyAuth(repo, logger))
	v1.Register(v1g, &v1.Handler{Orch: orch})

	// 管理后台
	adminH := &admin.Handler{
		Repo:               repo,
		Pool:               pool,
		JWTSecret:          cfg.JWTSecret,
		StorageDir:         cfg.StorageDir,
		Selectors:          core.DefaultSelectors,
		Logger:             logger,
		AuditArchiver:      auditArchiver,
		AuditExportMaxRows: cfg.AuditExportMaxRows,
	}
	if rdb != nil {
		adminH.LoginLimiter = core.NewRateLimiter(rdb, cfg.AdminLoginRate)
	}
	admin.Register(r.Group("/admin"), adminH)

	// 启动 HTTP
	srv := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.AppHost, cfg.AppPort),
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      600 * time.Second, // SSE 长连接
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
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
	close(metricsStop)
	close(auditArchiveStop)
	logger.Info("server stopped")
}

// seedAdmin 如果不存在管理员，用配置的默认账号创建一个
func seedAdmin(repo *repository.Repository, cfg *config.Config) error {
	if existing, err := repo.GetAdminByUsername(context.Background(), cfg.AdminUser); err == nil {
		changed := false
		if existing.Role == "" {
			existing.Role = "superadmin"
			changed = true
		}
		if existing.TokenVersion == 0 {
			existing.TokenVersion = 1
			changed = true
		}
		if !existing.Enabled {
			// 不自动重新启用已被明确禁用的管理员。
			return nil
		}
		if changed {
			return repo.UpdateAdmin(context.Background(), existing)
		}
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(cfg.AdminPass), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return repo.CreateAdmin(context.Background(), &model.Admin{
		Username:     cfg.AdminUser,
		PasswordHash: string(hash),
		Role:         "superadmin",
		Enabled:      true,
		TokenVersion: 1,
	})
}

// installBrowsersOnly 安装 Playwright Chromium
func installBrowsersOnly() error {
	return core.InstallBrowsers()
}
