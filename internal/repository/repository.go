package repository

import (
	"context"
	"sort"
	"time"

	"deepseek-web-api/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct {
	DB *gorm.DB
}

func New(db *gorm.DB) *Repository { return &Repository{DB: db} }

// ---------- Account ----------

func (r *Repository) ListAccounts(ctx context.Context) ([]model.Account, error) {
	var accs []model.Account
	err := r.DB.WithContext(ctx).Order("id").Find(&accs).Error
	return accs, err
}

func (r *Repository) ListActiveAccounts(ctx context.Context) ([]model.Account, error) {
	var accs []model.Account
	err := r.DB.WithContext(ctx).
		Where("status = ?", "active").
		Order("id").Find(&accs).Error
	return accs, err
}

func (r *Repository) GetAccount(ctx context.Context, id uint) (*model.Account, error) {
	var a model.Account
	err := r.DB.WithContext(ctx).First(&a, id).Error
	return &a, err
}

func (r *Repository) CreateAccount(ctx context.Context, a *model.Account) error {
	return r.DB.WithContext(ctx).Create(a).Error
}

func (r *Repository) UpdateAccount(ctx context.Context, a *model.Account) error {
	return r.DB.WithContext(ctx).Save(a).Error
}

func (r *Repository) DeleteAccount(ctx context.Context, id uint) error {
	return r.DB.WithContext(ctx).Delete(&model.Account{}, id).Error
}

func (r *Repository) MarkAccountStatus(ctx context.Context, id uint, status string) error {
	return r.DB.WithContext(ctx).Model(&model.Account{}).
		Where("id = ?", id).Update("status", status).Error
}

func (r *Repository) TouchAccountUsed(ctx context.Context, id uint) error {
	now := time.Now()
	return r.DB.WithContext(ctx).Model(&model.Account{}).
		Where("id = ?", id).Update("last_used_at", now).Error
}

func (r *Repository) TouchAccountChecked(ctx context.Context, id uint) error {
	now := time.Now()
	return r.DB.WithContext(ctx).Model(&model.Account{}).
		Where("id = ?", id).Update("last_check_at", now).Error
}

// ---------- APIKey ----------

func (r *Repository) FindAPIKeysByPrefix(ctx context.Context, prefix string) ([]model.APIKey, error) {
	var keys []model.APIKey
	err := r.DB.WithContext(ctx).Where("key_prefix = ?", prefix).Find(&keys).Error
	return keys, err
}

func (r *Repository) ListAPIKeys(ctx context.Context) ([]model.APIKey, error) {
	var keys []model.APIKey
	err := r.DB.WithContext(ctx).Order("id").Find(&keys).Error
	return keys, err
}

func (r *Repository) CreateAPIKey(ctx context.Context, k *model.APIKey) error {
	return r.DB.WithContext(ctx).Create(k).Error
}

func (r *Repository) DeleteAPIKey(ctx context.Context, id uint) error {
	return r.DB.WithContext(ctx).Delete(&model.APIKey{}, id).Error
}

func (r *Repository) SetAPIKeyEnabled(ctx context.Context, id uint, enabled bool) error {
	return r.DB.WithContext(ctx).Model(&model.APIKey{}).
		Where("id = ?", id).Update("enabled", enabled).Error
}

// UpdateAPIKeyDefaultModel 修改 API key 绑定的默认模型
func (r *Repository) UpdateAPIKeyDefaultModel(ctx context.Context, id uint, defaultModel string) error {
	return r.DB.WithContext(ctx).Model(&model.APIKey{}).
		Where("id = ?", id).Update("default_model", defaultModel).Error
}

// UpdateAPIKeyQuota 修改 API key 的日配额（0 表示不限）
func (r *Repository) UpdateAPIKeyQuota(ctx context.Context, id uint, quotaPerDay int) error {
	return r.DB.WithContext(ctx).Model(&model.APIKey{}).
		Where("id = ?", id).Update("quota_per_day", quotaPerDay).Error
}

// GetAPIKey 按 id 查询单个 API key
func (r *Repository) GetAPIKey(ctx context.Context, id uint) (*model.APIKey, error) {
	var k model.APIKey
	err := r.DB.WithContext(ctx).First(&k, id).Error
	return &k, err
}

// IncrementUsage 累计某 API key 在某小时的调用次数（upsert）。
// success=true 计入 success 列，否则计入 failed 列。
func (r *Repository) IncrementUsage(ctx context.Context, apiKeyID uint, success bool) error {
	hour := time.Now().Truncate(time.Hour)
	u := model.UsageHourly{APIKeyID: apiKeyID, Hour: hour}
	if success {
		u.Success = 1
	} else {
		u.Failed = 1
	}
	// MySQL: ON DUPLICATE KEY UPDATE
	return r.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "api_key_id"}, {Name: "hour"}},
		DoUpdates: clause.Assignments(map[string]any{
			"success": gorm.Expr("success + ?", u.Success),
			"failed":  gorm.Expr("failed + ?", u.Failed),
		}),
	}).Create(&u).Error
}

// TodayUsage 返回某 API key 今日（本地时区）的 (success, failed) 调用次数。
func (r *Repository) TodayUsage(ctx context.Context, apiKeyID uint) (success, failed int64, err error) {
	start := startOfLocalDay(time.Now())
	var res struct {
		S int64
		F int64
	}
	err = r.DB.WithContext(ctx).Model(&model.UsageHourly{}).
		Select("COALESCE(SUM(success),0) AS s, COALESCE(SUM(failed),0) AS f").
		Where("api_key_id = ? AND hour >= ?", apiKeyID, start).
		Scan(&res).Error
	return res.S, res.F, err
}

// UsagePoint 用量时间序列中的一个点
type UsagePoint struct {
	Hour    time.Time `json:"hour"`
	Success int       `json:"success"`
	Failed  int       `json:"failed"`
}

// APIKeyUsageRange 返回某 API key 在 [from, to) 区间内按小时聚合的用量序列。
func (r *Repository) APIKeyUsageRange(ctx context.Context, apiKeyID uint, from, to time.Time) ([]UsagePoint, error) {
	var rows []model.UsageHourly
	err := r.DB.WithContext(ctx).
		Where("api_key_id = ? AND hour >= ? AND hour < ?", apiKeyID, from, to).
		Order("hour ASC").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]UsagePoint, 0, len(rows))
	for _, row := range rows {
		out = append(out, UsagePoint{Hour: row.Hour, Success: row.Success, Failed: row.Failed})
	}
	return out, nil
}

// APIKeyUsageSummary 单个 API key 的用量汇总，附加在列表里给前端展示
type APIKeyUsageSummary struct {
	model.APIKey
	TodayUsed  int64 `json:"today_used"`  // 今日已用（success+failed）
	SuccessCnt int64 `json:"success_cnt"` // 今日成功
	FailedCnt  int64 `json:"failed_cnt"`  // 今日失败
	Remaining  int64 `json:"remaining"`   // 今日剩余（-1 表示不限）
}

// ListAPIKeysWithUsage 列出所有 API key，并附带今日用量。
// quota_per_day = 0 视为不限，Remaining 返回 -1。
func (r *Repository) ListAPIKeysWithUsage(ctx context.Context) ([]APIKeyUsageSummary, error) {
	var keys []model.APIKey
	if err := r.DB.WithContext(ctx).Order("id").Find(&keys).Error; err != nil {
		return nil, err
	}
	start := startOfLocalDay(time.Now())
	// 一次性聚合所有 key 的今日用量
	type aggRow struct {
		APIKeyID uint
		S        int64
		F        int64
	}
	var aggs []aggRow
	if err := r.DB.WithContext(ctx).Model(&model.UsageHourly{}).
		Select("api_key_id, COALESCE(SUM(success),0) AS s, COALESCE(SUM(failed),0) AS f").
		Where("hour >= ?", start).
		Group("api_key_id").Scan(&aggs).Error; err != nil {
		return nil, err
	}
	aggMap := make(map[uint]aggRow, len(aggs))
	for _, a := range aggs {
		aggMap[a.APIKeyID] = a
	}

	out := make([]APIKeyUsageSummary, 0, len(keys))
	for _, k := range keys {
		a := aggMap[k.ID]
		used := a.S + a.F
		rem := int64(-1)
		if k.QuotaPerDay > 0 {
			rem = int64(k.QuotaPerDay) - used
			if rem < 0 {
				rem = 0
			}
		}
		out = append(out, APIKeyUsageSummary{
			APIKey:     k,
			TodayUsed:  used,
			SuccessCnt: a.S,
			FailedCnt:  a.F,
			Remaining:  rem,
		})
	}
	return out, nil
}

// startOfLocalDay 返回传入时间所在时区的本地零点。
// time.Truncate(24h) 按绝对时长截断，在 UTC+8 会得到当天 08:00。
func startOfLocalDay(t time.Time) time.Time {
	year, month, day := t.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, t.Location())
}

// ---------- Conversation ----------

func (r *Repository) SaveConversation(ctx context.Context, c *model.Conversation) error {
	return r.DB.WithContext(ctx).Create(c).Error
}

func (r *Repository) UpdateReply(ctx context.Context, id, reply, status string) error {
	return r.DB.WithContext(ctx).Model(&model.Conversation{}).
		Where("id = ?", id).
		Updates(map[string]any{"reply": reply, "status": status}).Error
}

func (r *Repository) MarkFailed(ctx context.Context, id, errMsg string) error {
	return r.DB.WithContext(ctx).Model(&model.Conversation{}).
		Where("id = ?", id).
		Updates(map[string]any{"status": "failed", "error": errMsg}).Error
}

func (r *Repository) GetConversation(ctx context.Context, id string) (*model.Conversation, error) {
	var c model.Conversation
	err := r.DB.WithContext(ctx).First(&c, "id = ?", id).Error
	return &c, err
}

func (r *Repository) ListConversations(ctx context.Context, q ConvQuery) ([]model.Conversation, int64, error) {
	var items []model.Conversation
	var total int64
	tx := r.DB.WithContext(ctx).Model(&model.Conversation{})
	if q.APIKeyID != 0 {
		tx = tx.Where("api_key_id = ?", q.APIKeyID)
	}
	if q.AccountID != 0 {
		tx = tx.Where("account_id = ?", q.AccountID)
	}
	if !q.From.IsZero() {
		tx = tx.Where("created_at >= ?", q.From)
	}
	if !q.To.IsZero() {
		tx = tx.Where("created_at < ?", q.To)
	}
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Size < 1 || q.Size > 200 {
		q.Size = 20
	}
	err := tx.Order("created_at DESC").
		Offset((q.Page - 1) * q.Size).Limit(q.Size).Find(&items).Error
	return items, total, err
}

type ConvQuery struct {
	APIKeyID  uint
	AccountID uint
	From      time.Time
	To        time.Time
	Page      int
	Size      int
}

// ---------- Admin ----------

func (r *Repository) GetAdminByUsername(ctx context.Context, username string) (*model.Admin, error) {
	var a model.Admin
	err := r.DB.WithContext(ctx).Where("username = ?", username).First(&a).Error
	return &a, err
}

func (r *Repository) GetAdmin(ctx context.Context, id uint) (*model.Admin, error) {
	var admin model.Admin
	err := r.DB.WithContext(ctx).First(&admin, id).Error
	return &admin, err
}

func (r *Repository) ListAdmins(ctx context.Context) ([]model.Admin, error) {
	var admins []model.Admin
	err := r.DB.WithContext(ctx).Order("id ASC").Find(&admins).Error
	return admins, err
}

func (r *Repository) CreateAdmin(ctx context.Context, a *model.Admin) error {
	return r.DB.WithContext(ctx).Create(a).Error
}

func (r *Repository) UpdateAdmin(ctx context.Context, admin *model.Admin) error {
	return r.DB.WithContext(ctx).Save(admin).Error
}

func (r *Repository) TouchAdminLogin(ctx context.Context, id uint) error {
	now := time.Now()
	return r.DB.WithContext(ctx).Model(&model.Admin{}).
		Where("id = ?", id).Update("last_login_at", now).Error
}

func (r *Repository) CountEnabledSuperadmins(ctx context.Context) (int64, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&model.Admin{}).
		Where("role = ? AND enabled = ?", "superadmin", true).
		Count(&count).Error
	return count, err
}

// ---------- Audit log ----------

func (r *Repository) CreateAuditLog(ctx context.Context, entry *model.AuditLog) error {
	return r.DB.WithContext(ctx).Create(entry).Error
}

type AuditQuery struct {
	AdminID  uint
	Action   string
	Resource string
	From     time.Time
	To       time.Time
	Page     int
	Size     int
}

type AuditRecord struct {
	ID         uint       `json:"id"`
	AdminID    uint       `json:"admin_id"`
	AdminName  string     `json:"admin_name"`
	Action     string     `json:"action"`
	Resource   string     `json:"resource"`
	ResourceID string     `json:"resource_id"`
	Method     string     `json:"method"`
	Path       string     `json:"path"`
	ClientIP   string     `json:"client_ip"`
	Status     int        `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	Archived   bool       `json:"archived"`
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
}

func applyAuditFilters(tx *gorm.DB, query AuditQuery) *gorm.DB {
	if query.AdminID != 0 {
		tx = tx.Where("admin_id = ?", query.AdminID)
	}
	if query.Action != "" {
		tx = tx.Where("action = ?", query.Action)
	}
	if query.Resource != "" {
		tx = tx.Where("resource = ?", query.Resource)
	}
	if !query.From.IsZero() {
		tx = tx.Where("created_at >= ?", query.From)
	}
	if !query.To.IsZero() {
		tx = tx.Where("created_at < ?", query.To)
	}
	return tx
}

// QueryAuditRecords reads hot records, archived records, or a merged view.
func (r *Repository) QueryAuditRecords(
	ctx context.Context,
	query AuditQuery,
	scope string,
	offset, limit int,
) ([]AuditRecord, int64, error) {
	if offset < 0 {
		offset = 0
	}
	if limit < 1 {
		limit = 50
	}
	switch scope {
	case "active":
		return r.queryActiveAuditRecords(ctx, query, offset, limit)
	case "archive":
		return r.queryArchivedAuditRecords(ctx, query, offset, limit)
	case "all", "":
	default:
		return nil, 0, gorm.ErrInvalidData
	}

	fetchLimit := offset + limit
	active, activeTotal, err := r.queryActiveAuditRecords(ctx, query, 0, fetchLimit)
	if err != nil {
		return nil, 0, err
	}
	archived, archivedTotal, err := r.queryArchivedAuditRecords(ctx, query, 0, fetchLimit)
	if err != nil {
		return nil, 0, err
	}
	records := append(active, archived...)
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].ID > records[j].ID
		}
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})
	if offset >= len(records) {
		return []AuditRecord{}, activeTotal + archivedTotal, nil
	}
	end := offset + limit
	if end > len(records) {
		end = len(records)
	}
	return records[offset:end], activeTotal + archivedTotal, nil
}

func (r *Repository) queryActiveAuditRecords(
	ctx context.Context,
	query AuditQuery,
	offset, limit int,
) ([]AuditRecord, int64, error) {
	tx := applyAuditFilters(r.DB.WithContext(ctx).Model(&model.AuditLog{}), query)
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var entries []model.AuditLog
	if err := tx.Order("created_at DESC, id DESC").Offset(offset).Limit(limit).Find(&entries).Error; err != nil {
		return nil, 0, err
	}
	records := make([]AuditRecord, 0, len(entries))
	for _, entry := range entries {
		records = append(records, auditRecordFromActive(entry))
	}
	return records, total, nil
}

func (r *Repository) queryArchivedAuditRecords(
	ctx context.Context,
	query AuditQuery,
	offset, limit int,
) ([]AuditRecord, int64, error) {
	tx := applyAuditFilters(r.DB.WithContext(ctx).Model(&model.AuditLogArchive{}), query)
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var entries []model.AuditLogArchive
	if err := tx.Order("created_at DESC, id DESC").Offset(offset).Limit(limit).Find(&entries).Error; err != nil {
		return nil, 0, err
	}
	records := make([]AuditRecord, 0, len(entries))
	for _, entry := range entries {
		archivedAt := entry.ArchivedAt
		records = append(records, AuditRecord{
			ID:         entry.ID,
			AdminID:    entry.AdminID,
			AdminName:  entry.AdminName,
			Action:     entry.Action,
			Resource:   entry.Resource,
			ResourceID: entry.ResourceID,
			Method:     entry.Method,
			Path:       entry.Path,
			ClientIP:   entry.ClientIP,
			Status:     entry.Status,
			CreatedAt:  entry.CreatedAt,
			Archived:   true,
			ArchivedAt: &archivedAt,
		})
	}
	return records, total, nil
}

func auditRecordFromActive(entry model.AuditLog) AuditRecord {
	return AuditRecord{
		ID:         entry.ID,
		AdminID:    entry.AdminID,
		AdminName:  entry.AdminName,
		Action:     entry.Action,
		Resource:   entry.Resource,
		ResourceID: entry.ResourceID,
		Method:     entry.Method,
		Path:       entry.Path,
		ClientIP:   entry.ClientIP,
		Status:     entry.Status,
		CreatedAt:  entry.CreatedAt,
	}
}

// ArchiveAuditLogs moves one idempotent batch into the archive table.
func (r *Repository) ArchiveAuditLogs(ctx context.Context, before time.Time, batchSize int) (int, error) {
	if batchSize < 1 {
		batchSize = 1000
	}
	archived := 0
	err := r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var entries []model.AuditLog
		if err := tx.Where("created_at < ?", before).
			Order("id ASC").
			Limit(batchSize).
			Find(&entries).Error; err != nil {
			return err
		}
		if len(entries) == 0 {
			return nil
		}
		now := time.Now()
		archives := make([]model.AuditLogArchive, 0, len(entries))
		ids := make([]uint, 0, len(entries))
		for _, entry := range entries {
			archives = append(archives, model.AuditLogArchive{
				ID:         entry.ID,
				AdminID:    entry.AdminID,
				AdminName:  entry.AdminName,
				Action:     entry.Action,
				Resource:   entry.Resource,
				ResourceID: entry.ResourceID,
				Method:     entry.Method,
				Path:       entry.Path,
				ClientIP:   entry.ClientIP,
				Status:     entry.Status,
				CreatedAt:  entry.CreatedAt,
				ArchivedAt: now,
			})
			ids = append(ids, entry.ID)
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&archives).Error; err != nil {
			return err
		}
		if err := tx.Where("id IN ?", ids).Delete(&model.AuditLog{}).Error; err != nil {
			return err
		}
		archived = len(entries)
		return nil
	})
	return archived, err
}

func (r *Repository) DeleteArchivedAuditLogs(ctx context.Context, before time.Time, batchSize int) (int64, error) {
	if batchSize < 1 {
		batchSize = 1000
	}
	var ids []uint
	if err := r.DB.WithContext(ctx).Model(&model.AuditLogArchive{}).
		Where("created_at < ?", before).
		Order("id ASC").
		Limit(batchSize).
		Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	result := r.DB.WithContext(ctx).Where("id IN ?", ids).Delete(&model.AuditLogArchive{})
	return result.RowsAffected, result.Error
}

// ---------- Dashboard ----------

type DashboardStat struct {
	TotalCalls     int64 `json:"total_calls"`
	SuccessCalls   int64 `json:"success_calls"`
	FailedCalls    int64 `json:"failed_calls"`
	ActiveAccounts int64 `json:"active_accounts"`
	TotalAccounts  int64 `json:"total_accounts"`
}

func (r *Repository) Dashboard(ctx context.Context) (*DashboardStat, error) {
	var s DashboardStat
	if err := r.DB.WithContext(ctx).Model(&model.Conversation{}).Count(&s.TotalCalls).Error; err != nil {
		return nil, err
	}
	r.DB.WithContext(ctx).Model(&model.Conversation{}).Where("status = ?", "success").Count(&s.SuccessCalls)
	r.DB.WithContext(ctx).Model(&model.Conversation{}).Where("status = ?", "failed").Count(&s.FailedCalls)
	r.DB.WithContext(ctx).Model(&model.Account{}).Where("status = ?", "active").Count(&s.ActiveAccounts)
	r.DB.WithContext(ctx).Model(&model.Account{}).Count(&s.TotalAccounts)
	return &s, nil
}
