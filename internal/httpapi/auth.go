package httpapi

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/ikun/cloud-account-manager/internal/model"
	"github.com/ikun/cloud-account-manager/internal/security"
	"github.com/ikun/cloud-account-manager/internal/store"
)

var (
	errResendInvalidCredentials = errors.New("invalid resend verification credentials")
	errVerificationNotPending   = errors.New("email verification is not pending")
)

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var registration struct {
		Enabled bool `json:"enabled"`
	}
	if value, _, _, err := s.store.GetSetting(r.Context(), "registration"); err == nil {
		_ = json.Unmarshal(value, &registration)
	}
	if !registration.Enabled {
		writeError(w, r, http.StatusForbidden, "registration_disabled", "系统暂未开放注册", nil)
		return
	}
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if !validEmail(input.Email) {
		writeError(w, r, http.StatusBadRequest, "invalid_email", "请输入有效邮箱", nil)
		return
	}
	passwordHash, err := security.HashPassword(input.Password)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_password", "密码长度必须为 12 到 128 个字符", nil)
		return
	}
	user, err := s.store.CreateUser(r.Context(), input.Email, passwordHash, "user", "pending")
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeError(w, r, http.StatusConflict, "email_exists", "该邮箱已注册", nil)
			return
		}
		writeError(w, r, http.StatusInternalServerError, "registration_failed", "注册失败", nil)
		return
	}
	token, err := security.RandomToken(32)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "token_failed", "无法创建验证邮件", nil)
		return
	}
	if err := s.store.ReplaceOneTimeToken(r.Context(), user.ID, "verify_email", security.HashToken(token), 24*time.Hour); err != nil {
		writeError(w, r, http.StatusInternalServerError, "token_failed", "无法创建验证邮件", nil)
		return
	}
	link := s.cfg.AppBaseURL + "/verify-email?token=" + token
	if err := s.sendEmail(r, user.Email, "验证邮箱", "请访问以下地址完成邮箱验证：\n\n"+link+"\n\n链接 24 小时内有效。"); err != nil && s.cfg.Environment == "production" {
		writeError(w, r, http.StatusServiceUnavailable, "email_unavailable", "验证邮件暂时无法发送，请联系管理员", nil)
		return
	}
	response := map[string]any{"message": "注册成功，请检查邮箱完成验证"}
	if s.cfg.DevExposeTokens {
		response["dev_token"] = token
	}
	writeJSON(w, http.StatusCreated, response)
}

func (s *Server) resendVerification(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	user, err := s.store.GetUserByEmail(r.Context(), input.Email)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "invalid_credentials", "邮箱或密码错误", nil)
		return
	}
	if err := validateResendVerification(user, input.Password); err != nil {
		if errors.Is(err, errResendInvalidCredentials) {
			writeError(w, r, http.StatusUnauthorized, "invalid_credentials", "邮箱或密码错误", nil)
			return
		}
		writeError(w, r, http.StatusConflict, "verification_not_pending", "该账号当前无需邮箱验证", nil)
		return
	}
	token, err := security.RandomToken(32)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "token_failed", "无法创建验证邮件", nil)
		return
	}
	if err := s.store.ReplaceOneTimeToken(r.Context(), user.ID, "verify_email", security.HashToken(token), 24*time.Hour); err != nil {
		writeError(w, r, http.StatusInternalServerError, "token_failed", "无法创建验证邮件", nil)
		return
	}
	link := s.cfg.AppBaseURL + "/verify-email?token=" + token
	if err := s.sendEmail(r, user.Email, "验证邮箱", "请访问以下地址完成邮箱验证：\n\n"+link+"\n\n链接 24 小时内有效。"); err != nil && s.cfg.Environment == "production" {
		writeError(w, r, http.StatusServiceUnavailable, "email_unavailable", "验证邮件暂时无法发送，请稍后重试", nil)
		return
	}
	response := map[string]any{"message": "验证邮件已重新发送"}
	if s.cfg.DevExposeTokens {
		response["dev_token"] = token
	}
	writeJSON(w, http.StatusOK, response)
}

func validateResendVerification(user model.User, password string) error {
	if !security.VerifyPassword(user.PasswordHash, password) {
		return errResendInvalidCredentials
	}
	if user.Status != "pending" || user.EmailVerifiedAt != nil {
		return errVerificationNotPending
	}
	return nil
}

func (s *Server) verifyEmail(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token string `json:"token"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	userID, err := s.store.ConsumeOneTimeToken(r.Context(), security.HashToken(input.Token), "verify_email")
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "token_invalid", "验证链接无效或已过期", nil)
		return
	}
	if err := s.store.ActivateUser(r.Context(), userID); err != nil {
		writeError(w, r, http.StatusInternalServerError, "verification_failed", "邮箱验证失败", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "邮箱验证成功"})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	user, err := s.store.GetUserByEmail(r.Context(), input.Email)
	if err != nil || !security.VerifyPassword(user.PasswordHash, input.Password) {
		writeError(w, r, http.StatusUnauthorized, "invalid_credentials", "邮箱或密码错误", nil)
		return
	}
	if user.Status == "pending" {
		writeError(w, r, http.StatusForbidden, "email_unverified", "请先完成邮箱验证", nil)
		return
	}
	if user.Status != "active" {
		writeError(w, r, http.StatusForbidden, "account_disabled", "账号已被停用", nil)
		return
	}
	token, err := security.RandomToken(32)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "session_failed", "无法创建登录会话", nil)
		return
	}
	session, err := s.store.CreateSession(r.Context(), user.ID, security.HashToken(token), clientIP(r), r.UserAgent(), s.sessionTTL(r))
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "session_failed", "无法创建登录会话", nil)
		return
	}
	csrfToken, err := security.RandomToken(24)
	if err != nil {
		_ = s.store.DeleteSession(r.Context(), security.HashToken(token))
		writeError(w, r, http.StatusInternalServerError, "session_failed", "无法创建登录会话", nil)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: s.cfg.CookieName, Value: token, Path: "/", HttpOnly: true, Secure: s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode, Expires: session.ExpiresAt})
	http.SetCookie(w, &http.Cookie{Name: "csrf_token", Value: csrfToken, Path: "/", HttpOnly: false, Secure: s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode, Expires: session.ExpiresAt})
	s.audit(r, "auth.login", "session", session.ID.String(), &user.ID, nil)
	writeJSON(w, http.StatusOK, map[string]any{"user": user, "csrf_token": csrfToken})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	p := currentPrincipal(r.Context())
	_ = s.store.DeleteSession(r.Context(), security.HashToken(p.Token))
	s.clearSessionCookies(w)
	s.audit(r, "auth.logout", "session", p.Session.ID.String(), &p.User.ID, nil)
	writeJSON(w, http.StatusOK, map[string]string{"message": "已退出登录"})
}

func (s *Server) reauth(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	p := currentPrincipal(r.Context())
	if !security.VerifyPassword(p.User.PasswordHash, input.Password) {
		writeError(w, r, http.StatusUnauthorized, "invalid_password", "密码错误", nil)
		return
	}
	if err := s.store.MarkRecentAuth(r.Context(), p.Session.ID); err != nil {
		writeError(w, r, http.StatusInternalServerError, "reauth_failed", "重新验证失败", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "验证成功"})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	p := currentPrincipal(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"user": p.User, "recent_auth_at": p.Session.RecentAuthAt, "expires_at": p.Session.ExpiresAt})
}

func (s *Server) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email string `json:"email"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	response := map[string]any{"message": "如果邮箱存在，重置邮件已发送"}
	user, err := s.store.GetUserByEmail(r.Context(), input.Email)
	if err == nil && user.Status != "disabled" {
		token, tokenErr := security.RandomToken(32)
		if tokenErr == nil {
			tokenErr = s.store.ReplaceOneTimeToken(r.Context(), user.ID, "reset_password", security.HashToken(token), time.Hour)
		}
		if tokenErr == nil {
			link := s.cfg.AppBaseURL + "/reset-password?token=" + token
			_ = s.sendEmail(r, user.Email, "重置密码", "请访问以下地址重置密码：\n\n"+link+"\n\n链接 1 小时内有效。")
			if s.cfg.DevExposeTokens {
				response["dev_token"] = token
			}
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	passwordHash, err := security.HashPassword(input.Password)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_password", "密码长度必须为 12 到 128 个字符", nil)
		return
	}
	userID, err := s.store.ConsumeOneTimeToken(r.Context(), security.HashToken(input.Token), "reset_password")
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "token_invalid", "重置链接无效或已过期", nil)
		return
	}
	if err := s.store.UpdatePassword(r.Context(), userID, passwordHash); err != nil {
		writeError(w, r, http.StatusInternalServerError, "reset_failed", "密码重置失败", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "密码已重置，请重新登录"})
}

type smtpSetting struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	From     string `json:"from"`
	StartTLS bool   `json:"starttls"`
}

func (s *Server) sendEmail(r *http.Request, to, subject, body string) error {
	if !validEmail(to) {
		return errors.New("invalid recipient email")
	}
	value, ciphertext, nonce, err := s.store.GetSetting(r.Context(), "smtp")
	if err != nil {
		return err
	}
	var setting smtpSetting
	if err := json.Unmarshal(value, &setting); err != nil {
		return err
	}
	if setting.Host == "" || setting.From == "" {
		s.logger.Info("development email", "to", to, "subject", subject, "body", body)
		return errors.New("SMTP is not configured")
	}
	password := ""
	if len(ciphertext) > 0 {
		plaintext, decryptErr := s.security.Decrypt(ciphertext, nonce, "smtp_password")
		if decryptErr != nil {
			return decryptErr
		}
		password = string(plaintext)
	}
	address := setting.Host + ":" + strconv.Itoa(setting.Port)
	message := []byte("From: " + setting.From + "\r\n" + "To: " + to + "\r\n" + "Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + body)
	auth := smtp.PlainAuth("", setting.Username, password, setting.Host)
	client, err := smtp.Dial(address)
	if err != nil {
		return err
	}
	defer client.Close()
	if setting.StartTLS {
		if err := client.StartTLS(&tls.Config{ServerName: setting.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if setting.Username != "" {
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(setting.From); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(message); err != nil {
		return err
	}
	return writer.Close()
}

func validEmail(email string) bool {
	email = strings.TrimSpace(email)
	if email == "" || len(email) > 254 || strings.ContainsAny(email, "\r\n") {
		return false
	}
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		return false
	}
	local, domain, ok := strings.Cut(email, "@")
	return ok && local != "" && strings.Contains(domain, ".")
}

func (s *Server) sessionTTL(r *http.Request) time.Duration {
	value, _, _, err := s.store.GetSetting(r.Context(), "session")
	if err != nil {
		return s.cfg.SessionTTL
	}
	return sessionTTLFromSetting(value, s.cfg.SessionTTL)
}

func sessionTTLFromSetting(value []byte, fallback time.Duration) time.Duration {
	var setting struct {
		Hours int `json:"hours"`
	}
	if json.Unmarshal(value, &setting) != nil || setting.Hours < 1 || setting.Hours > 24*90 {
		return fallback
	}
	return time.Duration(setting.Hours) * time.Hour
}

var _ = fmt.Sprintf
var _ = store.ErrNotFound
