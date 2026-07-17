package httpapi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/kayungou/BatchManagementofCloudServerAccounts/internal/model"
	"github.com/kayungou/BatchManagementofCloudServerAccounts/internal/security"
	"github.com/kayungou/BatchManagementofCloudServerAccounts/internal/store"
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

const (
	smtpConnectTimeout   = 10 * time.Second
	smtpIOTimeout        = 15 * time.Second
	smtpOperationTimeout = 45 * time.Second
)

type smtpTransport struct {
	address        string
	host           string
	implicitTLS    bool
	startTLS       bool
	tlsConfig      *tls.Config
	connectTimeout time.Duration
	ioTimeout      time.Duration
}

func smtpTransportFor(setting smtpSetting) smtpTransport {
	implicitTLS := setting.Port == 465
	return smtpTransport{
		address:     net.JoinHostPort(setting.Host, strconv.Itoa(setting.Port)),
		host:        setting.Host,
		implicitTLS: implicitTLS,
		startTLS:    setting.StartTLS && !implicitTLS,
		tlsConfig: &tls.Config{
			ServerName: setting.Host,
			MinVersion: tls.VersionTLS12,
		},
		connectTimeout: smtpConnectTimeout,
		ioTimeout:      smtpIOTimeout,
	}
}

func (s *Server) sendEmail(r *http.Request, to, subject, body string) error {
	if !validEmail(to) {
		return errors.New("invalid recipient email")
	}
	value, ciphertext, nonce, err := s.store.GetSetting(r.Context(), "smtp")
	if err != nil {
		return fmt.Errorf("读取 SMTP 配置失败: %w", err)
	}
	var setting smtpSetting
	if err := json.Unmarshal(value, &setting); err != nil {
		return fmt.Errorf("解析 SMTP 配置失败: %w", err)
	}
	if setting.Host == "" || setting.From == "" {
		s.logger.Info("development email", "to", to, "subject", subject, "body", body)
		return errors.New("SMTP is not configured")
	}
	password := ""
	if len(ciphertext) > 0 {
		plaintext, decryptErr := s.security.Decrypt(ciphertext, nonce, "smtp_password")
		if decryptErr != nil {
			return fmt.Errorf("解密 SMTP 密码失败: %w", decryptErr)
		}
		password = string(plaintext)
	}
	message := []byte("From: " + setting.From + "\r\n" + "To: " + to + "\r\n" + "Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + body)
	ctx, cancel := context.WithTimeout(r.Context(), smtpOperationTimeout)
	defer cancel()
	return deliverSMTP(ctx, smtpTransportFor(setting), setting, password, to, message)
}

func deliverSMTP(ctx context.Context, transport smtpTransport, setting smtpSetting, password, to string, message []byte) error {
	dialer := net.Dialer{Timeout: transport.connectTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", transport.address)
	if err != nil {
		return fmt.Errorf("SMTP 连接失败: %w", err)
	}
	defer conn.Close()
	stopContextClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopContextClose()

	setDeadline := func(stage string) error {
		deadline := time.Now().Add(transport.ioTimeout)
		if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
			deadline = contextDeadline
		}
		if err := conn.SetDeadline(deadline); err != nil {
			return fmt.Errorf("SMTP %s超时设置失败: %w", stage, err)
		}
		return nil
	}
	stageError := func(stage string, err error) error {
		if contextErr := ctx.Err(); contextErr != nil {
			return fmt.Errorf("SMTP %s失败: %w", stage, contextErr)
		}
		return fmt.Errorf("SMTP %s失败: %w", stage, err)
	}

	if transport.implicitTLS {
		if err := setDeadline("TLS 握手"); err != nil {
			return err
		}
		tlsConn := tls.Client(conn, transport.tlsConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return stageError("TLS 握手", err)
		}
		conn = tlsConn
	}
	if err := setDeadline("服务问候"); err != nil {
		return err
	}
	client, err := smtp.NewClient(conn, transport.host)
	if err != nil {
		return stageError("服务问候", err)
	}
	defer client.Close()

	if transport.startTLS {
		if err := setDeadline("STARTTLS 升级"); err != nil {
			return err
		}
		if err := client.StartTLS(transport.tlsConfig); err != nil {
			return stageError("STARTTLS 升级", err)
		}
	}
	if setting.Username != "" {
		if err := setDeadline("认证"); err != nil {
			return err
		}
		auth := smtp.PlainAuth("", setting.Username, password, transport.host)
		if err := client.Auth(auth); err != nil {
			return stageError("认证", err)
		}
	}
	if err := setDeadline("发件人校验"); err != nil {
		return err
	}
	if err := client.Mail(setting.From); err != nil {
		return stageError("发件人校验", err)
	}
	if err := setDeadline("收件人校验"); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return stageError("收件人校验", err)
	}
	if err := setDeadline("DATA 命令"); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return stageError("DATA 命令", err)
	}
	if err := setDeadline("正文写入"); err != nil {
		_ = writer.Close()
		return err
	}
	if _, err := writer.Write(message); err != nil {
		return stageError("正文写入", err)
	}
	if err := setDeadline("投递确认"); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return stageError("投递确认", err)
	}
	return nil
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

var _ = store.ErrNotFound
