package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"deepseek-web-api/internal/model"
)

// Repository 数据访问接口（由 repository 包实现，避免循环依赖）
type Repository interface {
	SaveConversation(ctx context.Context, c *model.Conversation) error
	UpdateReply(ctx context.Context, id, reply, status string) error
	MarkFailed(ctx context.Context, id, errMsg string) error
	TouchAccountUsed(ctx context.Context, accountID uint) error
	// 配额与用量
	TodayUsage(ctx context.Context, apiKeyID uint) (success, failed int64, err error)
	IncrementUsage(ctx context.Context, apiKeyID uint, success bool) error
}

// Limiter 限流接口
type Limiter interface {
	Allow(ctx context.Context, key string) (bool, int, error)
}

// ErrQuotaExceeded 日配额已用尽
var ErrQuotaExceeded = errors.New("daily quota exceeded")

// Orchestrator 对话编排器
type Orchestrator struct {
	Pool     *BrowserPool
	Repo     Repository
	Limiter  Limiter
	Selector Selectors
	Logger   *zap.Logger
}

// Chat 执行一次对话。
// 流式：返回 (nil, streamCh, nil)，streamCh 关闭表示结束。
// 非流式：返回 (result, nil, nil)。
func (o *Orchestrator) Chat(ctx context.Context, req ChatRequest) (map[string]any, <-chan string, error) {
	prompt := BuildPrompt(req.Messages)

	// 限流
	limiterKey := fmt.Sprintf("rate:%d", req.APIKeyID)
	if o.Limiter != nil {
		allowed, _, err := o.Limiter.Allow(ctx, limiterKey)
		if err != nil {
			o.Logger.Warn("rate limiter error", zap.Error(err))
		} else if !allowed {
			return nil, nil, errors.New("rate limit exceeded")
		}
	}

	// 日配额校验：quota_per_day = 0 表示不限
	if req.QuotaPerDay > 0 && o.Repo != nil {
		succ, fail, err := o.Repo.TodayUsage(ctx, req.APIKeyID)
		if err != nil {
			o.Logger.Warn("query today usage failed", zap.Error(err))
		} else if succ+fail >= int64(req.QuotaPerDay) {
			return nil, nil, ErrQuotaExceeded
		}
	}

	// 解析 model → ModelConfig
	mc := ParseModelName(req.Model)

	tried := map[uint]bool{}
	var lastErr error
	restartTried := false

	for attempt := 0; attempt < 3; attempt++ {
		sess, err := o.Pool.Acquire(ctx, tried)
		if err != nil {
			// 池里没有可用会话，可能是浏览器崩溃后所有会话被标记为 unhealthy，
			// 尝试重启一次浏览器池
			if errors.Is(err, ErrNoSession) && !restartTried {
				restartTried = true
				o.Logger.Warn("no session available, attempting browser pool restart")
				if rerr := o.Pool.Restart(); rerr != nil {
					o.Logger.Error("pool restart failed", zap.Error(rerr))
					return nil, nil, fmt.Errorf("%w: %v", ErrAllSessionsDown, rerr)
				}
				// 重启后清空 tried，重新尝试
				tried = map[uint]bool{}
				lastErr = err
				continue
			}
			break
		}
		tried[sess.AccountID] = true

		driver := NewDeepSeekDriver(sess, o.Selector, o.Logger)
		// 设置模式与开关（SendMessage 内部 ensurePage 后才真正切换）
		driver.SetModelConfig(mc)
		deltaCh, err := driver.SendMessage(ctx, prompt)
		if err != nil {
			if IsSessionExpired(err) {
				sess.MarkUnhealthy()
				o.Logger.Warn("session expired, will retry with another",
					zap.Uint("account_id", sess.AccountID))
				lastErr = err
				sess.Release()
				continue
			}
			if IsBrowserClosed(err) {
				// 浏览器/上下文已崩溃：标记会话不可用并尝试重启池
				sess.MarkUnhealthy()
				sess.Release()
				o.Logger.Warn("browser closed, will restart pool and retry",
					zap.Uint("account_id", sess.AccountID), zap.Error(err))
				if !restartTried {
					restartTried = true
					if rerr := o.Pool.Restart(); rerr != nil {
						o.Logger.Error("pool restart failed", zap.Error(rerr))
						return nil, nil, fmt.Errorf("%w: %v", ErrAllSessionsDown, rerr)
					}
					tried = map[uint]bool{}
					lastErr = err
					continue
				}
				lastErr = err
				continue
			}
			sess.Release()
			return nil, nil, err
		}

		convID := uuid.NewString()
		startedAt := time.Now()
		_ = o.Repo.SaveConversation(ctx, &model.Conversation{
			ID:        convID,
			APIKeyID:  req.APIKeyID,
			AccountID: sess.AccountID,
			Model:     req.Model,
			Prompt:    prompt,
			Status:    "streaming",
		})
		_ = o.Repo.TouchAccountUsed(ctx, sess.AccountID)

		if req.Stream {
			// 持久化与释放由 wrapWithRelease 统一处理
			rawCh := ConvertStream(deltaCh, req.Model, convID, nil, req.Tools)
			wrapped := wrapWithRelease(rawCh, sess, o.Logger, convID, req.APIKeyID, o.Repo, startedAt)
			return nil, wrapped, nil
		}

		// 非流式：聚合完整文本
		fullReasoning, fullContent := consumeAllChunks(ctx, deltaCh)
		sess.Release()
		dur := int(time.Since(startedAt) / time.Millisecond)
		_ = o.Repo.UpdateReply(ctx, convID, fullContent, "success")
		if err := o.Repo.IncrementUsage(ctx, req.APIKeyID, true); err != nil {
			o.Logger.Warn("increment usage failed", zap.Error(err))
		}
		o.Logger.Info("chat completed",
			zap.String("conv_id", convID),
			zap.Uint("account_id", sess.AccountID),
			zap.String("model", req.Model),
			zap.Int("duration_ms", dur))
		return BuildCompletion(convID, req.Model, fullReasoning, fullContent, req.Tools), nil, nil
	}

	if lastErr != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrAllSessionsDown, lastErr)
	}
	return nil, nil, ErrAllSessionsDown
}

// wrapWithRelease 包装流：流结束时释放会话、持久化、记录失败与用量
func wrapWithRelease(in <-chan string, sess *BrowserSession, logger *zap.Logger,
	convID string, apiKeyID uint, repo Repository, startedAt time.Time) <-chan string {
	out := make(chan string, 32)
	go func() {
		defer close(out)
		var buf string
		failed := false
	loop:
		for s := range in {
			buf += s
			select {
			case out <- s:
			case <-time.After(30 * time.Second):
				// 下游消费过慢，强制结束
				failed = true
				break loop
			}
		}
		dur := int(time.Since(startedAt) / time.Millisecond)
		if failed {
			_ = repo.MarkFailed(context.Background(), convID, "downstream timeout")
			// 排空剩余输入，避免上游 goroutine 阻塞
			for range in {
			}
		} else {
			_ = repo.UpdateReply(context.Background(), convID, buf, "success")
		}
		// 记录用量（失败也计入，便于风控）
		if err := repo.IncrementUsage(context.Background(), apiKeyID, !failed); err != nil {
			logger.Warn("increment usage failed", zap.Error(err))
		}
		logger.Info("stream completed",
			zap.String("conv_id", convID),
			zap.Int("duration_ms", dur),
			zap.Bool("failed", failed))
		sess.Release()
	}()
	return out
}

// consumeAllChunks 消费全部 StreamChunk 增量，返回 (reasoning, content)
func consumeAllChunks(ctx context.Context, in <-chan StreamChunk) (string, string) {
	var reasoning, content string
	for chunk := range in {
		reasoning += chunk.Reasoning
		content += chunk.Content
		select {
		default:
		case <-ctx.Done():
			return reasoning, content
		}
	}
	return reasoning, content
}
