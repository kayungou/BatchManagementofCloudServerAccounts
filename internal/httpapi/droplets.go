package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/kayungou/BatchManagementofCloudServerAccounts/internal/model"
	"github.com/kayungou/BatchManagementofCloudServerAccounts/internal/store"
)

var dropletNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]{0,62}$`)

func (s *Server) registerDropletRoutes(r chi.Router) {
	r.Route("/api/v1/droplets", func(r chi.Router) {
		r.Get("/", s.listDroplets)
		r.Post("/", s.createDroplets)
		r.Post("/actions", s.enqueueDropletAction)
		r.Post("/delete", s.enqueueDropletDelete)
		r.Route("/{dropletID}", func(r chi.Router) {
			r.Get("/", s.getDroplet)
			r.Get("/remote/{resource}", s.dropletRemoteResource)
			r.Get("/root-credential", s.revealRootCredential)
			r.Put("/root-credential", s.updateRootCredential)
		})
	})
	r.Get("/api/v1/jobs", s.listJobs)
	r.Get("/api/v1/usage-summary", s.usageSummary)
}

func (s *Server) listDroplets(w http.ResponseWriter, r *http.Request) {
	p := currentPrincipal(r.Context())
	limit, offset, page := pagination(r)
	var accountID *uuid.UUID
	if raw := r.URL.Query().Get("account_id"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_account_id", "云账号 ID 无效", nil)
			return
		}
		accountID = &parsed
	}
	items, total, err := s.store.ListDroplets(r.Context(), p.User.ID, p.User.Role == "admin", accountID, r.URL.Query().Get("search"), limit, offset)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "droplets_failed", "无法读取实例列表", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "page": page, "per_page": limit})
}

func (s *Server) getDroplet(w http.ResponseWriter, r *http.Request) {
	droplet, ok := s.authorizedDroplet(w, r)
	if !ok {
		return
	}
	credentialStatus := "none"
	if _, _, status, err := s.store.RootCredential(r.Context(), droplet.ID); err == nil {
		credentialStatus = status
	}
	writeJSON(w, http.StatusOK, map[string]any{"droplet": droplet, "root_credential_status": credentialStatus})
}

func (s *Server) createDroplets(w http.ResponseWriter, r *http.Request) {
	var input struct {
		AccountID        uuid.UUID      `json:"account_id"`
		Names            []string       `json:"names"`
		Region           string         `json:"region"`
		Size             string         `json:"size"`
		Image            any            `json:"image"`
		SSHKeys          []any          `json:"ssh_keys"`
		Backups          bool           `json:"backups"`
		BackupPolicy     map[string]any `json:"backup_policy"`
		IPv6             bool           `json:"ipv6"`
		Monitoring       bool           `json:"monitoring"`
		Tags             []string       `json:"tags"`
		VPCUUID          string         `json:"vpc_uuid"`
		ProjectID        string         `json:"project_id"`
		AuthMode         string         `json:"auth_mode"`
		PublicNetworking *bool          `json:"public_networking"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	account, ok := s.accountByIDForUser(w, r, input.AccountID)
	if !ok {
		return
	}
	input.Names = normalizeNames(input.Names)
	if len(input.Names) < 1 || len(input.Names) > 10 {
		writeError(w, r, http.StatusBadRequest, "invalid_names", "每次需要提供 1 到 10 个不重复实例名称", nil)
		return
	}
	for _, name := range input.Names {
		if !dropletNamePattern.MatchString(name) {
			writeError(w, r, http.StatusBadRequest, "invalid_name", "实例名称只能包含字母、数字、点和连字符，且最长 63 字符", map[string]string{"name": name})
			return
		}
	}
	if input.Size == "" || input.Image == nil {
		writeError(w, r, http.StatusBadRequest, "create_fields_required", "规格和镜像不能为空", nil)
		return
	}
	if input.AuthMode == "" {
		input.AuthMode = "ssh_keys"
	}
	if input.AuthMode != "ssh_keys" && input.AuthMode != "root_password" {
		writeError(w, r, http.StatusBadRequest, "invalid_auth_mode", "登录方式无效", nil)
		return
	}
	if input.AuthMode == "ssh_keys" && len(input.SSHKeys) == 0 {
		writeError(w, r, http.StatusBadRequest, "ssh_key_required", "SSH 公钥模式至少选择一个公钥", nil)
		return
	}
	if input.AuthMode == "root_password" && !cloudInitCompatibleImage(input.Image) {
		writeError(w, r, http.StatusBadRequest, "cloud_init_required", "root 密码模式仅支持带 cloud-init 的官方 Linux 镜像", nil)
		return
	}
	p := currentPrincipal(r.Context())
	payload := map[string]any{"names": input.Names, "region": input.Region, "size": input.Size, "image": input.Image,
		"ssh_keys": input.SSHKeys, "backups": input.Backups, "backup_policy": input.BackupPolicy, "ipv6": input.IPv6,
		"monitoring": input.Monitoring, "tags": input.Tags, "vpc_uuid": input.VPCUUID, "project_id": input.ProjectID,
		"auth_mode": input.AuthMode, "public_networking": input.PublicNetworking}
	job, err := s.store.EnqueueJob(r.Context(), p.User.ID, &account.ID, "create_droplets", payload)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "create_enqueue_failed", "无法创建实例任务", nil)
		return
	}
	s.audit(r, "droplet.create_enqueue", "cloud_account", account.ID.String(), &account.UserID, map[string]any{"names": input.Names, "size": input.Size})
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) enqueueDropletAction(w http.ResponseWriter, r *http.Request) {
	var input struct {
		AccountID  uuid.UUID      `json:"account_id"`
		DropletIDs []int64        `json:"droplet_ids"`
		Action     string         `json:"action"`
		Parameters map[string]any `json:"parameters"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.DropletIDs = uniqueInt64(input.DropletIDs)
	if len(input.DropletIDs) == 0 || len(input.DropletIDs) > 100 {
		writeError(w, r, http.StatusBadRequest, "invalid_selection", "请选择 1 到 100 台实例", nil)
		return
	}
	input.Action = strings.TrimSpace(input.Action)
	if !supportedDropletAction(input.Action) {
		writeError(w, r, http.StatusBadRequest, "unsupported_action", "不支持的实例操作", nil)
		return
	}
	if singleDropletAction(input.Action) && len(input.DropletIDs) != 1 {
		writeError(w, r, http.StatusBadRequest, "single_droplet_required", "该操作每次只能选择一台实例", nil)
		return
	}
	account, ok := s.accountByIDForUser(w, r, input.AccountID)
	if !ok {
		return
	}
	if !s.verifyProviderDroplets(w, r, account, input.DropletIDs) {
		return
	}
	dangerous := map[string]bool{"password_reset": true, "resize": true, "rebuild": true, "restore": true}
	if dangerous[input.Action] && !s.requireRecentAuth(w, r) {
		return
	}
	p := currentPrincipal(r.Context())
	job, err := s.store.EnqueueJob(r.Context(), p.User.ID, &account.ID, "droplet_action", map[string]any{
		"droplet_ids": input.DropletIDs, "action": input.Action, "parameters": input.Parameters,
	})
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "action_enqueue_failed", "无法创建实例操作任务", nil)
		return
	}
	s.audit(r, "droplet.action_enqueue", "cloud_account", account.ID.String(), &account.UserID, map[string]any{"action": input.Action, "droplet_ids": input.DropletIDs})
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) enqueueDropletDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireRecentAuth(w, r) {
		return
	}
	var input struct {
		AccountID   uuid.UUID         `json:"account_id"`
		DropletIDs  []int64           `json:"droplet_ids"`
		ConfirmName map[string]string `json:"confirm_names"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.DropletIDs = uniqueInt64(input.DropletIDs)
	if len(input.DropletIDs) == 0 || len(input.DropletIDs) > 100 {
		writeError(w, r, http.StatusBadRequest, "invalid_selection", "请选择 1 到 100 台实例", nil)
		return
	}
	account, ok := s.accountByIDForUser(w, r, input.AccountID)
	if !ok {
		return
	}
	for _, providerID := range input.DropletIDs {
		droplet, err := s.store.GetDropletByProviderID(r.Context(), account.ID, providerID)
		if err != nil {
			writeError(w, r, http.StatusNotFound, "droplet_not_found", "实例不存在或未同步", map[string]int64{"droplet_id": providerID})
			return
		}
		if input.ConfirmName[strconv.FormatInt(providerID, 10)] != droplet.Name {
			writeError(w, r, http.StatusBadRequest, "confirmation_mismatch", "销毁确认名称不匹配", map[string]string{"name": droplet.Name})
			return
		}
	}
	p := currentPrincipal(r.Context())
	job, err := s.store.EnqueueJob(r.Context(), p.User.ID, &account.ID, "delete_droplets", map[string]any{"droplet_ids": input.DropletIDs})
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "delete_enqueue_failed", "无法创建销毁任务", nil)
		return
	}
	s.audit(r, "droplet.delete_enqueue", "cloud_account", account.ID.String(), &account.UserID, map[string]any{"droplet_ids": input.DropletIDs})
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) dropletRemoteResource(w http.ResponseWriter, r *http.Request) {
	droplet, ok := s.authorizedDroplet(w, r)
	if !ok {
		return
	}
	account, err := s.store.GetCloudAccount(r.Context(), droplet.AccountID)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "account_not_found", "云账号不存在", nil)
		return
	}
	resource := chi.URLParam(r, "resource")
	paths := map[string]string{
		"backups":       fmt.Sprintf("/droplets/%d/backups", droplet.ProviderID),
		"snapshots":     fmt.Sprintf("/droplets/%d/snapshots", droplet.ProviderID),
		"actions":       fmt.Sprintf("/droplets/%d/actions", droplet.ProviderID),
		"backup-policy": fmt.Sprintf("/droplets/%d/backups/policy", droplet.ProviderID),
	}
	path, exists := paths[resource]
	if !exists {
		writeError(w, r, http.StatusNotFound, "resource_not_found", "不支持的实例资源", nil)
		return
	}
	query := url.Values{}
	if resource == "actions" || resource == "backups" || resource == "snapshots" {
		query.Set("per_page", boundedQuery(r, "per_page", "50"))
		query.Set("page", boundedQuery(r, "page", "1"))
	}
	client, err := s.digitalOceanClient(account)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "credential_error", "无法读取云账号凭据", nil)
		return
	}
	raw, _, err := client.GetRaw(r.Context(), path, query)
	if err != nil {
		s.writeDigitalOceanError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func (s *Server) revealRootCredential(w http.ResponseWriter, r *http.Request) {
	if !s.requireRecentAuth(w, r) {
		return
	}
	droplet, ok := s.authorizedDroplet(w, r)
	if !ok {
		return
	}
	ciphertext, nonce, status, err := s.store.RootCredential(r.Context(), droplet.ID)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "credential_not_found", "该实例没有托管 root 密码", nil)
		return
	}
	plaintext, err := s.security.Decrypt(ciphertext, nonce, "root_password:"+droplet.ID.String())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "credential_decrypt_failed", "root 密码解密失败", nil)
		return
	}
	s.audit(r, "root_credential.reveal", "droplet", droplet.ID.String(), &droplet.UserID, map[string]any{"provider_id": droplet.ProviderID})
	writeJSON(w, http.StatusOK, map[string]any{"password": string(plaintext), "status": status})
}

func (s *Server) updateRootCredential(w http.ResponseWriter, r *http.Request) {
	if !s.requireRecentAuth(w, r) {
		return
	}
	droplet, ok := s.authorizedDroplet(w, r)
	if !ok {
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if len(input.Password) < 12 || len(input.Password) > 128 {
		writeError(w, r, http.StatusBadRequest, "invalid_password", "root 密码长度必须为 12 到 128 个字符", nil)
		return
	}
	ciphertext, nonce, err := s.security.Encrypt([]byte(input.Password), "root_password:"+droplet.ID.String())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "encryption_failed", "root 密码加密失败", nil)
		return
	}
	if err := s.store.UpsertRootCredential(r.Context(), droplet.UserID, droplet.ID, ciphertext, nonce); err != nil {
		writeError(w, r, http.StatusInternalServerError, "credential_update_failed", "root 密码更新失败", nil)
		return
	}
	s.audit(r, "root_credential.update", "droplet", droplet.ID.String(), &droplet.UserID, nil)
	writeJSON(w, http.StatusOK, map[string]string{"message": "root 密码已加密保存"})
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	p := currentPrincipal(r.Context())
	limit, offset, page := pagination(r)
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if state != "" && !validJobState(state) {
		writeError(w, r, http.StatusBadRequest, "invalid_job_state", "任务状态无效", nil)
		return
	}
	items, total, err := s.store.ListJobs(r.Context(), p.User.ID, p.User.Role == "admin", state, limit, offset)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "jobs_failed", "无法读取任务记录", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "page": page, "per_page": limit})
}

func validJobState(state string) bool {
	switch state {
	case "queued", "running", "succeeded", "failed", "partial":
		return true
	default:
		return false
	}
}

func supportedDropletAction(action string) bool {
	switch action {
	case "power_on", "power_off", "power_cycle", "shutdown", "reboot", "password_reset", "resize", "rebuild", "rename", "snapshot", "enable_backups", "disable_backups", "restore":
		return true
	default:
		return false
	}
}

func singleDropletAction(action string) bool {
	switch action {
	case "rename", "resize", "rebuild", "restore":
		return true
	default:
		return false
	}
}

func (s *Server) usageSummary(w http.ResponseWriter, r *http.Request) {
	p := currentPrincipal(r.Context())
	scope := defaultString(r.URL.Query().Get("scope"), "self")
	var userID *uuid.UUID
	switch scope {
	case "self":
		userID = &p.User.ID
	case "all":
		if p.User.Role != "admin" {
			writeError(w, r, http.StatusForbidden, "admin_required", "全局用量汇总需要管理员权限", nil)
			return
		}
	default:
		writeError(w, r, http.StatusBadRequest, "invalid_scope", "用量汇总范围无效", nil)
		return
	}
	summary, err := s.store.UsageSummary(r.Context(), userID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "usage_summary_failed", "无法汇总资源用量", nil)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Scope string `json:"scope"`
		store.UsageSummary
	}{Scope: scope, UsageSummary: summary})
}

func (s *Server) authorizedDroplet(w http.ResponseWriter, r *http.Request) (model.Droplet, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "dropletID"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_droplet_id", "实例 ID 无效", nil)
		return model.Droplet{}, false
	}
	droplet, err := s.store.GetDroplet(r.Context(), id)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "droplet_not_found", "实例不存在", nil)
		return model.Droplet{}, false
	}
	p := currentPrincipal(r.Context())
	if p.User.Role != "admin" && droplet.UserID != p.User.ID {
		writeError(w, r, http.StatusForbidden, "droplet_forbidden", "无权访问该实例", nil)
		return model.Droplet{}, false
	}
	return droplet, true
}

func (s *Server) accountByIDForUser(w http.ResponseWriter, r *http.Request, id uuid.UUID) (model.CloudAccount, bool) {
	account, err := s.store.GetCloudAccount(r.Context(), id)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "account_not_found", "云账号不存在", nil)
		return model.CloudAccount{}, false
	}
	p := currentPrincipal(r.Context())
	if p.User.Role != "admin" && account.UserID != p.User.ID {
		writeError(w, r, http.StatusForbidden, "account_forbidden", "无权使用该云账号", nil)
		return model.CloudAccount{}, false
	}
	return account, true
}

func (s *Server) verifyProviderDroplets(w http.ResponseWriter, r *http.Request, account model.CloudAccount, ids []int64) bool {
	for _, id := range ids {
		if _, err := s.store.GetDropletByProviderID(r.Context(), account.ID, id); err != nil {
			writeError(w, r, http.StatusNotFound, "droplet_not_found", "实例不存在或不属于所选账号", map[string]int64{"droplet_id": id})
			return false
		}
	}
	return true
}

func normalizeNames(names []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" && !seen[name] {
			seen[name] = true
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result
}

func uniqueInt64(values []int64) []int64 {
	seen := map[int64]bool{}
	result := make([]int64, 0, len(values))
	for _, value := range values {
		if value > 0 && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func cloudInitCompatibleImage(image any) bool {
	value, ok := image.(string)
	if !ok {
		return false
	}
	value = strings.ToLower(value)
	for _, prefix := range []string{"ubuntu-", "debian-", "fedora-", "centos-", "rocky-", "almalinux-"} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

var _ = json.RawMessage{}
var _ = store.ErrNotFound
