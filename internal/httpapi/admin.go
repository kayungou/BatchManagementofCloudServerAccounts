package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ikun/cloud-account-manager/internal/buildinfo"
	"github.com/ikun/cloud-account-manager/internal/security"
	"github.com/ikun/cloud-account-manager/internal/store"
)

type optionalNullableInt struct {
	Present bool
	Value   *int
}

func (value *optionalNullableInt) UnmarshalJSON(data []byte) error {
	value.Present = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		value.Value = nil
		return nil
	}
	var decoded int
	if err := json.Unmarshal(data, &decoded); err != nil {
		return fmt.Errorf("must be an integer or null: %w", err)
	}
	value.Value = &decoded
	return nil
}

func quotaUpdate(value *optionalNullableInt) **int {
	if !value.Present {
		return nil
	}
	return &value.Value
}

func (s *Server) registerAdminRoutes(r chi.Router) {
	r.Route("/api/v1/admin", func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler { return s.requireAdmin(next.ServeHTTP) })
		r.Get("/users", s.adminListUsers)
		r.Post("/users", s.adminCreateUser)
		r.Patch("/users/{userID}", s.adminUpdateUser)
		r.Get("/settings", s.adminGetSettings)
		r.Put("/settings", s.adminUpdateSettings)
		r.Post("/settings/smtp/test", s.adminTestSMTP)
		r.Get("/status", s.adminStatus)
		r.Get("/audit-logs", s.adminAuditLogs)
		r.Post("/cloud-accounts/{accountID}/transfer", s.adminTransferAccount)
	})
}

func (s *Server) adminListUsers(w http.ResponseWriter, r *http.Request) {
	limit, offset, page := pagination(r)
	items, total, err := s.store.ListUsers(r.Context(), limit, offset)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "users_failed", "无法读取用户", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "page": page, "per_page": limit})
}

func (s *Server) adminCreateUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireRecentAuth(w, r) {
		return
	}
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if !validEmail(input.Email) || (input.Role != "admin" && input.Role != "user") {
		writeError(w, r, http.StatusBadRequest, "invalid_user", "邮箱或角色无效", nil)
		return
	}
	hash, err := security.HashPassword(input.Password)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_password", "密码长度必须为 12 到 128 个字符", nil)
		return
	}
	user, err := s.store.CreateUser(r.Context(), input.Email, hash, input.Role, "active")
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeError(w, r, http.StatusConflict, "email_exists", "该邮箱已存在", nil)
			return
		}
		writeError(w, r, http.StatusInternalServerError, "user_create_failed", "创建用户失败", nil)
		return
	}
	s.audit(r, "admin.user_create", "user", user.ID.String(), &user.ID, map[string]any{"role": user.Role})
	writeJSON(w, http.StatusCreated, user)
}

func (s *Server) adminUpdateUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireRecentAuth(w, r) {
		return
	}
	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_user_id", "用户 ID 无效", nil)
		return
	}
	current, err := s.store.GetUserByID(r.Context(), userID)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "user_not_found", "用户不存在", nil)
		return
	}
	var input struct {
		Role          *string             `json:"role"`
		Status        *string             `json:"status"`
		QuotaDroplets optionalNullableInt `json:"quota_droplets"`
		QuotaVCPUs    optionalNullableInt `json:"quota_vcpus"`
		QuotaMemoryMB optionalNullableInt `json:"quota_memory_mb"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Role != nil && *input.Role != "admin" && *input.Role != "user" {
		writeError(w, r, http.StatusBadRequest, "invalid_role", "角色无效", nil)
		return
	}
	if input.Status != nil && *input.Status != "active" && *input.Status != "disabled" && *input.Status != "pending" {
		writeError(w, r, http.StatusBadRequest, "invalid_status", "用户状态无效", nil)
		return
	}
	for name, quota := range map[string]optionalNullableInt{
		"quota_droplets": input.QuotaDroplets, "quota_vcpus": input.QuotaVCPUs, "quota_memory_mb": input.QuotaMemoryMB,
	} {
		if quota.Present && quota.Value != nil && *quota.Value < 0 {
			writeError(w, r, http.StatusBadRequest, "invalid_quota", "配额不能小于 0", map[string]string{"field": name})
			return
		}
	}
	removingActiveAdmin := current.Role == "admin" && current.Status == "active" &&
		((input.Role != nil && *input.Role != "admin") || (input.Status != nil && *input.Status != "active"))
	if removingActiveAdmin {
		count, _ := s.store.ActiveAdminCount(r.Context())
		if count <= 1 {
			writeError(w, r, http.StatusConflict, "last_admin", "不能停用或降级最后一个管理员", nil)
			return
		}
	}
	updated, err := s.store.UpdateUserAdmin(r.Context(), userID, store.UserAdminUpdate{
		Role: input.Role, Status: input.Status,
		QuotaDroplets: quotaUpdate(&input.QuotaDroplets), QuotaVCPUs: quotaUpdate(&input.QuotaVCPUs), QuotaMemoryMB: quotaUpdate(&input.QuotaMemoryMB),
	})
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "user_update_failed", "更新用户失败", nil)
		return
	}
	s.audit(r, "admin.user_update", "user", userID.String(), &userID, map[string]any{"role": input.Role, "status": input.Status})
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) adminGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.ListSettings(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "settings_failed", "无法读取系统设置", nil)
		return
	}
	var smtp map[string]any
	_ = json.Unmarshal(settings["smtp"], &smtp)
	_, ciphertext, _, _ := s.store.GetSetting(r.Context(), "smtp")
	smtp["password_configured"] = len(ciphertext) > 0
	settings["smtp"], _ = json.Marshal(smtp)
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) adminUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireRecentAuth(w, r) {
		return
	}
	var input struct {
		Site struct {
			Name     string `json:"name"`
			Timezone string `json:"timezone"`
		} `json:"site"`
		Registration struct {
			Enabled bool `json:"enabled"`
		} `json:"registration"`
		Maintenance struct {
			Enabled bool   `json:"enabled"`
			Message string `json:"message"`
		} `json:"maintenance"`
		Session struct {
			Hours int `json:"hours"`
		} `json:"session"`
		DefaultQuota struct {
			Droplets *int `json:"droplets"`
			VCPUs    *int `json:"vcpus"`
			MemoryMB *int `json:"memory_mb"`
		} `json:"default_quota"`
		SMTP struct {
			Host          string `json:"host"`
			Port          int    `json:"port"`
			Username      string `json:"username"`
			Password      string `json:"password"`
			ClearPassword bool   `json:"clear_password"`
			From          string `json:"from"`
			StartTLS      bool   `json:"starttls"`
		} `json:"smtp"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Site.Name) == "" || len(input.Site.Name) > 100 || input.Session.Hours < 1 || input.Session.Hours > 24*90 || input.SMTP.Port < 1 || input.SMTP.Port > 65535 {
		writeError(w, r, http.StatusBadRequest, "invalid_settings", "系统设置参数无效", nil)
		return
	}
	if _, err := time.LoadLocation(input.Site.Timezone); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_timezone", "时区无效", nil)
		return
	}
	for name, quota := range map[string]*int{"droplets": input.DefaultQuota.Droplets, "vcpus": input.DefaultQuota.VCPUs, "memory_mb": input.DefaultQuota.MemoryMB} {
		if quota != nil && *quota < 0 {
			writeError(w, r, http.StatusBadRequest, "invalid_quota", "默认配额不能小于 0", map[string]string{"field": name})
			return
		}
	}
	input.SMTP.Host = strings.TrimSpace(input.SMTP.Host)
	input.SMTP.Username = strings.TrimSpace(input.SMTP.Username)
	input.SMTP.From = strings.TrimSpace(input.SMTP.From)
	if input.SMTP.Host != "" && !validEmail(input.SMTP.From) {
		writeError(w, r, http.StatusBadRequest, "invalid_smtp_from", "SMTP 发件邮箱无效", nil)
		return
	}
	if input.SMTP.ClearPassword && input.SMTP.Password != "" {
		writeError(w, r, http.StatusBadRequest, "smtp_password_conflict", "不能同时设置并清除 SMTP 密码", nil)
		return
	}
	p := currentPrincipal(r.Context())
	settings := []struct {
		key   string
		value any
	}{
		{"site", input.Site}, {"registration", input.Registration}, {"maintenance", input.Maintenance},
		{"session", input.Session}, {"default_quota", input.DefaultQuota},
	}
	for _, setting := range settings {
		if err := s.store.SetSetting(r.Context(), setting.key, setting.value, nil, nil, p.User.ID); err != nil {
			writeError(w, r, http.StatusInternalServerError, "settings_update_failed", "系统设置保存失败", map[string]string{"key": setting.key})
			return
		}
	}
	ciphertext, nonce, updateSecret, err := s.smtpSecretUpdate(input.SMTP.Password, input.SMTP.ClearPassword)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "smtp_encryption_failed", "SMTP 密码加密失败", nil)
		return
	}
	smtpValue := map[string]any{"host": input.SMTP.Host, "port": input.SMTP.Port, "username": input.SMTP.Username,
		"from": input.SMTP.From, "starttls": input.SMTP.StartTLS}
	if err := s.store.SetSettingWithSecret(r.Context(), "smtp", smtpValue, ciphertext, nonce, updateSecret, p.User.ID); err != nil {
		writeError(w, r, http.StatusInternalServerError, "smtp_update_failed", "SMTP 设置保存失败", nil)
		return
	}
	s.audit(r, "admin.settings_update", "system", "settings", nil, map[string]any{"maintenance": input.Maintenance.Enabled, "registration": input.Registration.Enabled})
	writeJSON(w, http.StatusOK, map[string]string{"message": "系统设置已保存"})
}

func (s *Server) smtpSecretUpdate(password string, clear bool) (ciphertext, nonce []byte, update bool, err error) {
	if password == "" {
		return nil, nil, clear, nil
	}
	ciphertext, nonce, err = s.security.Encrypt([]byte(password), "smtp_password")
	return ciphertext, nonce, true, err
}

func (s *Server) adminTestSMTP(w http.ResponseWriter, r *http.Request) {
	if !s.requireRecentAuth(w, r) {
		return
	}
	var input struct {
		To string `json:"to"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if !validEmail(input.To) {
		writeError(w, r, http.StatusBadRequest, "invalid_email", "收件邮箱无效", nil)
		return
	}
	if err := s.sendEmail(r, input.To, "SMTP 测试", "云服务器托管平台 SMTP 配置测试成功。"); err != nil {
		writeError(w, r, http.StatusBadGateway, "smtp_test_failed", "测试邮件发送失败", err.Error())
		return
	}
	s.audit(r, "admin.smtp_test", "system", "smtp", nil, map[string]any{"to": input.To})
	writeJSON(w, http.StatusOK, map[string]string{"message": "测试邮件已发送"})
}

func (s *Server) adminStatus(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	dbErr := s.store.Pool.Ping(ctx)
	latency := time.Since(started)
	counts, countErr := s.store.StatusCounts(r.Context())
	if countErr != nil {
		writeError(w, r, http.StatusInternalServerError, "status_failed", "无法读取系统状态", nil)
		return
	}
	var migration int64
	_ = s.store.Pool.QueryRow(r.Context(), `SELECT COALESCE(max(version),0) FROM schema_migrations`).Scan(&migration)
	writeJSON(w, http.StatusOK, map[string]any{
		"version": buildinfo.Version, "commit": buildinfo.Commit, "build_time": buildinfo.BuildTime,
		"go_version": runtime.Version(), "environment": s.cfg.Environment,
		"uptime_seconds": int(time.Since(s.startedAt).Seconds()), "database": map[string]any{"ok": dbErr == nil, "latency_ms": latency.Milliseconds(), "migration_version": migration},
		"counts": counts, "worker_ok": counts["active_workers"] > 0,
	})
}

func (s *Server) adminAuditLogs(w http.ResponseWriter, r *http.Request) {
	limit, offset, page := pagination(r)
	items, total, err := s.store.ListAudit(r.Context(), limit, offset)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "audit_failed", "无法读取审计日志", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "page": page, "per_page": limit})
}

func (s *Server) adminTransferAccount(w http.ResponseWriter, r *http.Request) {
	if !s.requireRecentAuth(w, r) {
		return
	}
	accountID, err := uuid.Parse(chi.URLParam(r, "accountID"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_account_id", "云账号 ID 无效", nil)
		return
	}
	account, err := s.store.GetCloudAccount(r.Context(), accountID)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "account_not_found", "云账号不存在", nil)
		return
	}
	var input struct {
		UserID uuid.UUID `json:"user_id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	user, err := s.store.GetUserByID(r.Context(), input.UserID)
	if err != nil || user.Status != "active" {
		writeError(w, r, http.StatusBadRequest, "target_user_invalid", "目标用户不存在或未激活", nil)
		return
	}
	if err := s.store.TransferCloudAccount(r.Context(), account.ID, user.ID); err != nil {
		writeError(w, r, http.StatusInternalServerError, "transfer_failed", "账号归属转移失败", nil)
		return
	}
	s.audit(r, "admin.account_transfer", "cloud_account", account.ID.String(), &user.ID, map[string]any{"from_user_id": account.UserID})
	writeJSON(w, http.StatusOK, map[string]string{"message": "云账号归属已转移"})
}
