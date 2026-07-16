package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/kayungou/BatchManagementofCloudServerAccounts/internal/config"
	"github.com/kayungou/BatchManagementofCloudServerAccounts/internal/model"
	"github.com/kayungou/BatchManagementofCloudServerAccounts/internal/security"
	"github.com/kayungou/BatchManagementofCloudServerAccounts/internal/store"
)

type Server struct {
	cfg       config.Config
	store     *store.Store
	security  *security.Manager
	logger    *slog.Logger
	startedAt time.Time
	limiter   *rateLimiter
}

type principal struct {
	User    model.User
	Session model.Session
	Token   string
}

type contextKey string

const (
	principalKey contextKey = "principal"
	requestIDKey contextKey = "request_id"
)

func New(cfg config.Config, dataStore *store.Store, securityManager *security.Manager, logger *slog.Logger) *Server {
	return &Server{cfg: cfg, store: dataStore, security: securityManager, logger: logger, startedAt: time.Now(), limiter: newRateLimiter()}
}

func (s *Server) Router() http.Handler {
	router := chi.NewRouter()
	router.Use(s.requestIDMiddleware)
	router.Use(s.recoverMiddleware)
	router.Use(s.securityHeadersMiddleware)
	router.Use(s.loggingMiddleware)

	router.Get("/healthz", s.health)
	router.Get("/readyz", s.ready)
	router.Get("/api/v1/public/config", s.publicConfig)
	router.Route("/api/v1/auth", func(r chi.Router) {
		r.Post("/register", s.rateLimit("register", 5, time.Hour, s.register))
		r.Post("/resend-verification", s.rateLimit("resend_verification", 5, time.Hour, s.resendVerification))
		r.Post("/verify-email", s.verifyEmail)
		r.Post("/login", s.rateLimit("login", 10, 15*time.Minute, s.login))
		r.Post("/forgot-password", s.rateLimit("forgot", 5, time.Hour, s.forgotPassword))
		r.Post("/reset-password", s.resetPassword)
	})

	router.Group(func(r chi.Router) {
		r.Use(s.authMiddleware)
		r.Use(s.csrfMiddleware)
		r.Post("/api/v1/auth/logout", s.logout)
		r.Post("/api/v1/auth/reauth", s.reauth)
		r.Get("/api/v1/me", s.me)
		s.registerAccountRoutes(r)
		s.registerDropletRoutes(r)
		s.registerAdminRoutes(r)
	})

	router.NotFound(s.serveFrontend)
	return router
}

func (s *Server) requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if requestID == "" || len(requestID) > 128 {
			requestID = uuid.NewString()
		}
		w.Header().Set("X-Request-ID", requestID)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, requestID)))
	})
}

func (s *Server) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("request panic", "panic", recovered, "stack", string(debug.Stack()), "request_id", requestID(r.Context()))
				writeError(w, r, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		s.logger.Info("http request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started), "request_id", requestID(r.Context()))
	})
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(s.cfg.CookieName)
		if err != nil || cookie.Value == "" {
			writeError(w, r, http.StatusUnauthorized, "authentication_required", "请先登录", nil)
			return
		}
		session, user, err := s.store.SessionUser(r.Context(), security.HashToken(cookie.Value))
		if err != nil || user.Status != "active" {
			s.clearSessionCookies(w)
			writeError(w, r, http.StatusUnauthorized, "session_invalid", "登录状态已失效", nil)
			return
		}
		if user.Role != "admin" {
			value, _, _, settingErr := s.store.GetSetting(r.Context(), "maintenance")
			if settingErr == nil {
				var maintenance struct {
					Enabled bool   `json:"enabled"`
					Message string `json:"message"`
				}
				_ = json.Unmarshal(value, &maintenance)
				if maintenance.Enabled {
					writeError(w, r, http.StatusServiceUnavailable, "maintenance", defaultString(maintenance.Message, "系统维护中"), nil)
					return
				}
			}
		}
		p := principal{User: user, Session: session, Token: cookie.Value}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, p)))
	})
}

func (s *Server) csrfMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie("csrf_token")
		header := r.Header.Get("X-CSRF-Token")
		if err != nil || cookie.Value == "" || header == "" || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(header)) != 1 {
			writeError(w, r, http.StatusForbidden, "csrf_invalid", "请求安全令牌无效，请刷新页面后重试", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if currentPrincipal(r.Context()).User.Role != "admin" {
			writeError(w, r, http.StatusForbidden, "admin_required", "需要管理员权限", nil)
			return
		}
		next(w, r)
	}
}

func (s *Server) requireRecentAuth(w http.ResponseWriter, r *http.Request) bool {
	if time.Since(currentPrincipal(r.Context()).Session.RecentAuthAt) > 5*time.Minute {
		writeError(w, r, http.StatusForbidden, "recent_auth_required", "请重新输入登录密码后再执行此操作", nil)
		return false
	}
	return true
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "uptime_seconds": int(time.Since(s.startedAt).Seconds())})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Pool.Ping(ctx); err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "database_unavailable", "数据库不可用", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) publicConfig(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.ListSettings(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "settings_error", "无法读取系统设置", nil)
		return
	}
	var site map[string]any
	var registration map[string]any
	var maintenance map[string]any
	_ = json.Unmarshal(settings["site"], &site)
	_ = json.Unmarshal(settings["registration"], &registration)
	_ = json.Unmarshal(settings["maintenance"], &maintenance)
	writeJSON(w, http.StatusOK, map[string]any{"site": site, "registration": registration, "maintenance": maintenance})
}

func (s *Server) serveFrontend(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, r, http.StatusNotFound, "not_found", "接口不存在", nil)
		return
	}
	root := filepath.Clean(s.cfg.FrontendDir)
	path := filepath.Join(root, filepath.Clean("/"+r.URL.Path))
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		http.ServeFile(w, r, path)
		return
	}
	index := filepath.Join(root, "index.html")
	if _, err := os.Stat(index); err == nil {
		http.ServeFile(w, r, index)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": "云服务器托管平台", "message": "前端尚未构建，请在 web 目录运行 npm run build"})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string, details any) {
	writeJSON(w, status, map[string]any{"error": map[string]any{
		"code": code, "message": message, "details": details, "request_id": requestID(r.Context()),
	}})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_json", "请求内容格式错误", err.Error())
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, r, http.StatusBadRequest, "invalid_json", "请求内容只能包含一个 JSON 对象", nil)
		return false
	}
	return true
}

func currentPrincipal(ctx context.Context) principal {
	value, _ := ctx.Value(principalKey).(principal)
	return value
}

func requestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

func clientIP(r *http.Request) net.IP {
	if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); forwarded != "" {
		if parsed := net.ParseIP(forwarded); parsed != nil {
			return parsed
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return net.ParseIP(host)
	}
	return net.ParseIP(r.RemoteAddr)
}

func pagination(r *http.Request) (limit, offset, page int) {
	page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ = strconv.Atoi(r.URL.Query().Get("per_page"))
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	offset = (page - 1) * limit
	return
}

func (s *Server) audit(r *http.Request, action, resourceType, resourceID string, target *uuid.UUID, metadata any) {
	p := currentPrincipal(r.Context())
	actor := p.User.ID
	if p.User.ID == uuid.Nil {
		actor = uuid.Nil
	}
	var actorPtr *uuid.UUID
	if actor != uuid.Nil {
		actorPtr = &actor
	}
	if err := s.store.Audit(r.Context(), store.AuditEntry{ActorUserID: actorPtr, TargetUserID: target, Action: action,
		ResourceType: resourceType, ResourceID: resourceID, IP: clientIP(r), UserAgent: r.UserAgent(), Metadata: metadata}); err != nil {
		s.logger.Error("write audit log", "error", err)
	}
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

type rateLimiter struct {
	mu      sync.Mutex
	entries map[string]rateEntry
}

type rateEntry struct {
	Count int
	Reset time.Time
}

func newRateLimiter() *rateLimiter { return &rateLimiter{entries: map[string]rateEntry{}} }

func (s *Server) rateLimit(bucket string, max int, window time.Duration, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := bucket + ":" + clientIP(r).String()
		now := time.Now()
		s.limiter.mu.Lock()
		entry := s.limiter.entries[key]
		if now.After(entry.Reset) {
			entry = rateEntry{Reset: now.Add(window)}
		}
		entry.Count++
		s.limiter.entries[key] = entry
		s.limiter.mu.Unlock()
		if entry.Count > max {
			w.Header().Set("Retry-After", fmt.Sprintf("%d", int(time.Until(entry.Reset).Seconds())))
			writeError(w, r, http.StatusTooManyRequests, "rate_limited", "请求过于频繁，请稍后重试", nil)
			return
		}
		next(w, r)
	}
}

func (s *Server) clearSessionCookies(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: s.cfg.CookieName, Value: "", Path: "/", HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	http.SetCookie(w, &http.Cookie{Name: "csrf_token", Value: "", Path: "/", HttpOnly: false, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

var _ fs.FileInfo
var _ = errors.Is
