package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/kayungou/BatchManagementofCloudServerAccounts/internal/digitalocean"
	"github.com/kayungou/BatchManagementofCloudServerAccounts/internal/model"
	"github.com/kayungou/BatchManagementofCloudServerAccounts/internal/security"
	"github.com/kayungou/BatchManagementofCloudServerAccounts/internal/store"
)

func (s *Server) registerAccountRoutes(r chi.Router) {
	r.Route("/api/v1/cloud-accounts", func(r chi.Router) {
		r.Get("/", s.listCloudAccounts)
		r.Post("/", s.createCloudAccount)
		r.Route("/{accountID}", func(r chi.Router) {
			r.Get("/", s.getCloudAccount)
			r.Put("/token", s.replaceCloudAccountToken)
			r.Post("/sync", s.syncCloudAccount)
			r.Delete("/", s.deleteCloudAccount)
			r.Get("/catalog/{resource}", s.accountCatalog)
			r.Post("/ssh-keys", s.createSSHKey)
			r.Put("/ssh-keys/{keyID}", s.updateSSHKey)
			r.Delete("/ssh-keys/{keyID}", s.deleteSSHKey)
		})
	})
}

func (s *Server) listCloudAccounts(w http.ResponseWriter, r *http.Request) {
	p := currentPrincipal(r.Context())
	accounts, err := s.store.ListCloudAccounts(r.Context(), p.User.ID, p.User.Role == "admin")
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "accounts_failed", "无法读取云账号", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": accounts, "total": len(accounts)})
}

func (s *Server) getCloudAccount(w http.ResponseWriter, r *http.Request) {
	account, ok := s.authorizedAccount(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, account)
}

func (s *Server) createCloudAccount(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name                string `json:"name"`
		Token               string `json:"token"`
		FullAccessConfirmed bool   `json:"full_access_confirmed"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Token = strings.TrimSpace(input.Token)
	if input.Name == "" || len(input.Name) > 100 || input.Token == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_account", "账号名称和 Token 不能为空", nil)
		return
	}
	if !input.FullAccessConfirmed {
		writeError(w, r, http.StatusBadRequest, "full_access_required", "请确认该 Token 已授予 Full Access", nil)
		return
	}
	providerAccount, rate, err := digitalocean.New(input.Token).Account(r.Context())
	if err != nil {
		status := http.StatusBadRequest
		code := "token_invalid"
		if digitalocean.IsStatus(err, http.StatusForbidden) {
			code = "token_insufficient"
		}
		writeError(w, r, status, code, "DigitalOcean Token 无效或缺少 account:read 权限", err.Error())
		return
	}
	ciphertext, nonce, err := s.security.Encrypt([]byte(input.Token), "digitalocean_token")
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "encryption_failed", "Token 加密失败", nil)
		return
	}
	p := currentPrincipal(r.Context())
	account, err := s.store.CreateCloudAccount(r.Context(), p.User.ID, input.Name, ciphertext, nonce, s.security.Fingerprint(input.Token), true)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeError(w, r, http.StatusConflict, "token_exists", "该 Token 已被系统托管", nil)
			return
		}
		writeError(w, r, http.StatusInternalServerError, "account_create_failed", "保存云账号失败", nil)
		return
	}
	providerID := providerAccount.UUID
	if providerAccount.Team != nil && providerAccount.Team.UUID != "" {
		providerID = providerAccount.Team.UUID
	}
	validation := store.AccountValidation{ProviderAccountID: providerID, ProviderEmail: providerAccount.Email,
		ProviderStatus: providerAccount.Status, StatusMessage: providerAccount.StatusMessage, CredentialStatus: "valid",
		AccountLimits: map[string]int{"droplet_limit": providerAccount.DropletLimit, "floating_ip_limit": providerAccount.FloatingIPLimit},
		RateRemaining: rate.Remaining, RateResetAt: rate.ResetAt}
	if err := s.store.UpdateAccountValidation(r.Context(), account.ID, validation); err != nil {
		_ = s.store.DeleteCloudAccount(r.Context(), account.ID)
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeError(w, r, http.StatusConflict, "provider_account_exists", "该 DigitalOcean Team 已归属于其他用户", nil)
			return
		}
		writeError(w, r, http.StatusInternalServerError, "account_validate_failed", "保存 DigitalOcean 账号信息失败", nil)
		return
	}
	job, err := s.store.EnqueueJob(r.Context(), p.User.ID, &account.ID, "sync_account", map[string]any{"account_id": account.ID})
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "sync_enqueue_failed", "账号已保存，但同步任务创建失败", nil)
		return
	}
	s.audit(r, "cloud_account.create", "cloud_account", account.ID.String(), &p.User.ID, map[string]any{"provider_account_id": providerID})
	account, _ = s.store.GetCloudAccount(r.Context(), account.ID)
	writeJSON(w, http.StatusCreated, map[string]any{"account": account, "job": job})
}

func (s *Server) replaceCloudAccountToken(w http.ResponseWriter, r *http.Request) {
	account, ok := s.authorizedAccount(w, r)
	if !ok {
		return
	}
	if !s.requireRecentAuth(w, r) {
		return
	}
	var input struct {
		Token               string `json:"token"`
		FullAccessConfirmed bool   `json:"full_access_confirmed"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Token) == "" || !input.FullAccessConfirmed {
		writeError(w, r, http.StatusBadRequest, "token_required", "请输入并确认 Full Access Token", nil)
		return
	}
	providerAccount, _, err := digitalocean.New(input.Token).Account(r.Context())
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "token_invalid", "新 Token 无效或权限不足", err.Error())
		return
	}
	providerID := providerAccount.UUID
	if providerAccount.Team != nil && providerAccount.Team.UUID != "" {
		providerID = providerAccount.Team.UUID
	}
	if account.ProviderAccountID != nil && *account.ProviderAccountID != providerID {
		writeError(w, r, http.StatusConflict, "provider_account_mismatch", "新 Token 不属于当前 DigitalOcean Team", nil)
		return
	}
	ciphertext, nonce, err := s.security.Encrypt([]byte(input.Token), "digitalocean_token")
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "encryption_failed", "Token 加密失败", nil)
		return
	}
	if err := s.store.ReplaceAccountToken(r.Context(), account.ID, ciphertext, nonce, s.security.Fingerprint(input.Token), true); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeError(w, r, http.StatusConflict, "token_exists", "该 Token 已被系统托管", nil)
			return
		}
		writeError(w, r, http.StatusInternalServerError, "token_replace_failed", "更新 Token 失败", nil)
		return
	}
	p := currentPrincipal(r.Context())
	job, _ := s.store.EnqueueJob(r.Context(), p.User.ID, &account.ID, "sync_account", map[string]any{"account_id": account.ID})
	s.audit(r, "cloud_account.token_replace", "cloud_account", account.ID.String(), &account.UserID, nil)
	writeJSON(w, http.StatusOK, map[string]any{"message": "Token 已更新", "job": job})
}

func (s *Server) syncCloudAccount(w http.ResponseWriter, r *http.Request) {
	account, ok := s.authorizedAccount(w, r)
	if !ok {
		return
	}
	p := currentPrincipal(r.Context())
	job, err := s.store.EnqueueJob(r.Context(), p.User.ID, &account.ID, "sync_account", map[string]any{"account_id": account.ID})
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "sync_enqueue_failed", "无法创建同步任务", nil)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) deleteCloudAccount(w http.ResponseWriter, r *http.Request) {
	account, ok := s.authorizedAccount(w, r)
	if !ok {
		return
	}
	if !s.requireRecentAuth(w, r) {
		return
	}
	var input struct {
		ConfirmName string `json:"confirm_name"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.ConfirmName != account.Name {
		writeError(w, r, http.StatusBadRequest, "confirmation_mismatch", "请输入完整账号名称确认解绑", nil)
		return
	}
	if err := s.store.DeleteCloudAccount(r.Context(), account.ID); err != nil {
		writeError(w, r, http.StatusInternalServerError, "account_delete_failed", "解绑失败", nil)
		return
	}
	s.audit(r, "cloud_account.delete", "cloud_account", account.ID.String(), &account.UserID, map[string]any{"cloud_resources_deleted": false})
	writeJSON(w, http.StatusOK, map[string]string{"message": "账号已解绑，DigitalOcean 云资源未被删除"})
}

func (s *Server) accountCatalog(w http.ResponseWriter, r *http.Request) {
	account, ok := s.authorizedAccount(w, r)
	if !ok {
		return
	}
	resource := chi.URLParam(r, "resource")
	paths := map[string]string{
		"regions": "/regions", "sizes": "/sizes", "images": "/images", "vpcs": "/vpcs",
		"projects": "/projects", "ssh-keys": "/account/keys", "snapshots": "/snapshots",
	}
	path, exists := paths[resource]
	if !exists {
		writeError(w, r, http.StatusNotFound, "catalog_not_found", "不支持的资源目录", nil)
		return
	}
	query := url.Values{"per_page": []string{boundedQuery(r, "per_page", "200")}, "page": []string{boundedQuery(r, "page", "1")}}
	if resource == "images" {
		query.Set("type", defaultString(r.URL.Query().Get("type"), "distribution"))
	}
	if resource == "snapshots" {
		query.Set("resource_type", "droplet")
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

func (s *Server) createSSHKey(w http.ResponseWriter, r *http.Request) {
	account, ok := s.authorizedAccount(w, r)
	if !ok {
		return
	}
	var input struct {
		Name      string `json:"name"`
		PublicKey string `json:"public_key"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Name) == "" || !strings.HasPrefix(strings.TrimSpace(input.PublicKey), "ssh-") {
		writeError(w, r, http.StatusBadRequest, "invalid_ssh_key", "请输入名称和有效的 SSH 公钥", nil)
		return
	}
	client, err := s.digitalOceanClient(account)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "credential_error", "无法读取云账号凭据", nil)
		return
	}
	raw, _, err := client.PostRaw(r.Context(), "/account/keys", map[string]string{"name": input.Name, "public_key": input.PublicKey})
	if err != nil {
		s.writeDigitalOceanError(w, r, err)
		return
	}
	s.audit(r, "ssh_key.create", "cloud_account", account.ID.String(), &account.UserID, map[string]any{"name": input.Name})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(raw)
}

func (s *Server) updateSSHKey(w http.ResponseWriter, r *http.Request) {
	account, ok := s.authorizedAccount(w, r)
	if !ok {
		return
	}
	keyID := chi.URLParam(r, "keyID")
	var input struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Name) == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_ssh_key", "SSH 公钥名称不能为空", nil)
		return
	}
	client, err := s.digitalOceanClient(account)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "credential_error", "无法读取云账号凭据", nil)
		return
	}
	raw, _, err := client.PutRaw(r.Context(), "/account/keys/"+url.PathEscape(keyID), map[string]string{"name": input.Name})
	if err != nil {
		s.writeDigitalOceanError(w, r, err)
		return
	}
	s.audit(r, "ssh_key.update", "ssh_key", keyID, &account.UserID, map[string]any{"name": input.Name})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func (s *Server) deleteSSHKey(w http.ResponseWriter, r *http.Request) {
	account, ok := s.authorizedAccount(w, r)
	if !ok {
		return
	}
	if !s.requireRecentAuth(w, r) {
		return
	}
	keyID := chi.URLParam(r, "keyID")
	client, err := s.digitalOceanClient(account)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "credential_error", "无法读取云账号凭据", nil)
		return
	}
	if _, err := client.DeleteRaw(r.Context(), "/account/keys/"+url.PathEscape(keyID)); err != nil {
		s.writeDigitalOceanError(w, r, err)
		return
	}
	s.audit(r, "ssh_key.delete", "ssh_key", keyID, &account.UserID, nil)
	writeJSON(w, http.StatusOK, map[string]string{"message": "SSH 公钥已删除"})
}

func (s *Server) authorizedAccount(w http.ResponseWriter, r *http.Request) (model.CloudAccount, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "accountID"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_account_id", "云账号 ID 无效", nil)
		return model.CloudAccount{}, false
	}
	account, err := s.store.GetCloudAccount(r.Context(), id)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "account_not_found", "云账号不存在", nil)
		return model.CloudAccount{}, false
	}
	p := currentPrincipal(r.Context())
	if p.User.Role != "admin" && account.UserID != p.User.ID {
		writeError(w, r, http.StatusForbidden, "account_forbidden", "无权访问该云账号", nil)
		return model.CloudAccount{}, false
	}
	return account, true
}

func (s *Server) digitalOceanClient(account model.CloudAccount) (*digitalocean.Client, error) {
	plaintext, err := s.security.Decrypt(account.TokenCiphertext, account.TokenNonce, "digitalocean_token")
	if err != nil {
		return nil, err
	}
	return digitalocean.New(string(plaintext)), nil
}

func (s *Server) writeDigitalOceanError(w http.ResponseWriter, r *http.Request, err error) {
	var apiErr *digitalocean.APIError
	if errors.As(err, &apiErr) {
		status := apiErr.StatusCode
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		writeError(w, r, status, "digitalocean_"+defaultString(apiErr.ID, "error"), apiErr.Message, map[string]any{"provider_request_id": apiErr.RequestID})
		return
	}
	writeError(w, r, http.StatusBadGateway, "digitalocean_unavailable", "DigitalOcean API 暂时不可用", err.Error())
}

func boundedQuery(r *http.Request, key, fallback string) string {
	value := r.URL.Query().Get(key)
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return fallback
	}
	if key == "per_page" && parsed > 200 {
		parsed = 200
	}
	return strconv.Itoa(parsed)
}

var _ = fmt.Sprintf
var _ = security.HashToken
