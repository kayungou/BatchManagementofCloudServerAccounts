package config

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Environment       string
	ListenAddr        string
	DatabaseURL       string
	AppBaseURL        string
	MasterKey         []byte
	CookieName        string
	CookieSecure      bool
	SessionTTL        time.Duration
	WorkerConcurrency int
	WorkerPoll        time.Duration
	SyncInterval      time.Duration
	FrontendDir       string
	DevExposeTokens   bool
	RunWorker         bool
}

func Load() (Config, error) {
	_ = LoadEnvFile(".env")
	_ = LoadEnvFile(".env.local")

	cfg := Config{
		Environment:       env("APP_ENV", "development"),
		ListenAddr:        env("LISTEN_ADDR", "127.0.0.1:8080"),
		DatabaseURL:       env("DATABASE_URL", "postgres://ikun:ServBay.dev@127.0.0.1:5432/cloud_account_manager?sslmode=disable"),
		AppBaseURL:        strings.TrimRight(env("APP_BASE_URL", "http://127.0.0.1:8080"), "/"),
		CookieName:        env("COOKIE_NAME", "cloud_manager_session"),
		CookieSecure:      envBool("COOKIE_SECURE", env("APP_ENV", "development") == "production"),
		SessionTTL:        envDuration("SESSION_TTL", 7*24*time.Hour),
		WorkerConcurrency: envInt("WORKER_CONCURRENCY", 4),
		WorkerPoll:        envDuration("WORKER_POLL_INTERVAL", 2*time.Second),
		SyncInterval:      envDuration("SYNC_INTERVAL", 5*time.Minute),
		FrontendDir:       env("FRONTEND_DIR", "web/dist"),
		DevExposeTokens:   envBool("DEV_EXPOSE_TOKENS", env("APP_ENV", "development") != "production"),
		RunWorker:         envBool("RUN_WORKER", env("APP_ENV", "development") != "production"),
	}

	encodedKey := strings.TrimSpace(os.Getenv("MASTER_KEY"))
	if encodedKey == "" {
		return Config{}, errors.New("MASTER_KEY is required (base64-encoded 32-byte key)")
	}
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil || len(key) != 32 {
		return Config{}, errors.New("MASTER_KEY must be a base64-encoded 32-byte key")
	}
	cfg.MasterKey = key

	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if cfg.WorkerConcurrency < 1 || cfg.WorkerConcurrency > 32 {
		return Config{}, fmt.Errorf("WORKER_CONCURRENCY must be between 1 and 32")
	}
	return cfg, nil
}

func LoadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.TrimPrefix(key, "export "))
		value = strings.Trim(strings.TrimSpace(value), "\"'")
		if key != "" {
			if _, exists := os.LookupEnv(key); !exists {
				_ = os.Setenv(key, value)
			}
		}
	}
	return scanner.Err()
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}
