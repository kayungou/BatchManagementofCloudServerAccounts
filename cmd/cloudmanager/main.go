package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ikun/cloud-account-manager/internal/buildinfo"
	"github.com/ikun/cloud-account-manager/internal/config"
	"github.com/ikun/cloud-account-manager/internal/database"
	"github.com/ikun/cloud-account-manager/internal/httpapi"
	"github.com/ikun/cloud-account-manager/internal/security"
	"github.com/ikun/cloud-account-manager/internal/store"
	"github.com/ikun/cloud-account-manager/internal/worker"
)

func main() {
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	if command == "keygen" {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			fatal(err)
		}
		fmt.Println(base64.StdEncoding.EncodeToString(key))
		return
	}
	if command == "version" {
		fmt.Printf("cloudmanager %s\ncommit: %s\nbuilt: %s\n", buildinfo.Version, buildinfo.Commit, buildinfo.BuildTime)
		return
	}

	cfg, err := config.Load()
	if err != nil {
		fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		fatal(err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		fatal(err)
	}
	securityManager, err := security.New(cfg.MasterKey)
	if err != nil {
		fatal(err)
	}
	dataStore := store.New(pool)

	switch command {
	case "serve":
		runServer(ctx, cfg, dataStore, securityManager, logger)
	case "worker":
		runWorker(ctx, cfg, dataStore, securityManager, logger)
	case "migrate":
		fmt.Println("database migrations applied")
	case "admin":
		runAdmin(ctx, dataStore)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", command)
		os.Exit(2)
	}
}

func runServer(ctx context.Context, cfg config.Config, dataStore *store.Store, securityManager *security.Manager, logger *slog.Logger) {
	if cfg.RunWorker {
		backgroundWorker := worker.New(dataStore, securityManager, cfg.WorkerConcurrency, cfg.WorkerPoll, cfg.SyncInterval, logger)
		go func() {
			if err := backgroundWorker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("embedded worker stopped", "error", err)
			}
		}()
	}
	api := httpapi.New(cfg, dataStore, securityManager, logger)
	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           api.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	logger.Info("server started", "address", cfg.ListenAddr, "environment", cfg.Environment)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fatal(err)
	}
}

func runWorker(ctx context.Context, cfg config.Config, dataStore *store.Store, securityManager *security.Manager, logger *slog.Logger) {
	backgroundWorker := worker.New(dataStore, securityManager, cfg.WorkerConcurrency, cfg.WorkerPoll, cfg.SyncInterval, logger)
	logger.Info("worker started", "concurrency", cfg.WorkerConcurrency)
	if err := backgroundWorker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		fatal(err)
	}
}

func runAdmin(ctx context.Context, dataStore *store.Store) {
	adminFlags := flag.NewFlagSet("admin", flag.ExitOnError)
	email := adminFlags.String("email", os.Getenv("ADMIN_EMAIL"), "administrator email")
	password := adminFlags.String("password", os.Getenv("ADMIN_PASSWORD"), "administrator password; omit to read from stdin")
	args := []string{}
	if len(os.Args) > 2 {
		args = os.Args[2:]
	}
	_ = adminFlags.Parse(args)
	if strings.TrimSpace(*email) == "" {
		fmt.Fprint(os.Stderr, "Administrator email: ")
		value, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		*email = strings.TrimSpace(value)
	}
	if *password == "" {
		fmt.Fprint(os.Stderr, "Administrator password (12+ characters): ")
		value, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		*password = strings.TrimSpace(value)
	}
	hash, err := security.HashPassword(*password)
	if err != nil {
		fatal(err)
	}
	user, err := dataStore.CreateUser(ctx, *email, hash, "admin", "active")
	if err != nil {
		fatal(err)
	}
	fmt.Printf("administrator created: %s (%s)\n", user.Email, user.ID)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
