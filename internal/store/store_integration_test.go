package store_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kayungou/BatchManagementofCloudServerAccounts/internal/database"
	"github.com/kayungou/BatchManagementofCloudServerAccounts/internal/store"
)

func TestPostgreSQLStoreCriticalSemantics(t *testing.T) {
	dataStore := newIntegrationStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if _, err := dataStore.Pool.Exec(ctx, `UPDATE system_settings SET value='{"droplets":7,"vcpus":11,"memory_mb":12288}'::jsonb WHERE key='default_quota'`); err != nil {
		t.Fatalf("set default quota: %v", err)
	}
	user, err := dataStore.CreateUser(ctx, "quota@example.com", "test-hash", "user", "pending")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if user.QuotaDroplets == nil || *user.QuotaDroplets != 7 || user.QuotaVCPUs == nil || *user.QuotaVCPUs != 11 || user.QuotaMemoryMB == nil || *user.QuotaMemoryMB != 12288 {
		t.Fatalf("new user quotas = (%v, %v, %v)", user.QuotaDroplets, user.QuotaVCPUs, user.QuotaMemoryMB)
	}
	other, err := dataStore.CreateUser(ctx, "other@example.com", "test-hash", "user", "active")
	if err != nil {
		t.Fatalf("CreateUser(other): %v", err)
	}

	var accountID uuid.UUID
	err = dataStore.Pool.QueryRow(ctx, `INSERT INTO cloud_accounts(
		user_id,name,provider_account_id,token_ciphertext,token_nonce,token_fingerprint,credential_status,account_balance)
		VALUES($1,'primary','team-preserved',$2,$3,$4,'valid',12.5) RETURNING id`,
		user.ID, []byte("cipher"), []byte("nonce"), []byte("fingerprint-primary")).Scan(&accountID)
	if err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err := dataStore.Pool.Exec(ctx, `INSERT INTO cloud_accounts(
		user_id,name,token_ciphertext,token_nonce,token_fingerprint,credential_status)
		VALUES($1,'invalid',$2,$3,$4,'invalid')`,
		user.ID, []byte("cipher"), []byte("nonce"), []byte("fingerprint-invalid")); err != nil {
		t.Fatalf("insert invalid account: %v", err)
	}
	if _, err := dataStore.Pool.Exec(ctx, `INSERT INTO cloud_accounts(
		user_id,name,token_ciphertext,token_nonce,token_fingerprint,credential_status,account_balance)
		VALUES($1,'other',$2,$3,$4,'valid',5)`,
		other.ID, []byte("cipher"), []byte("nonce"), []byte("fingerprint-other")); err != nil {
		t.Fatalf("insert other account: %v", err)
	}

	if err := dataStore.MarkAccountValidationError(ctx, accountID, "invalid", "provider unavailable"); err != nil {
		t.Fatalf("MarkAccountValidationError: %v", err)
	}
	var providerID, lastError string
	if err := dataStore.Pool.QueryRow(ctx, `SELECT provider_account_id,last_error FROM cloud_accounts WHERE id=$1`, accountID).Scan(&providerID, &lastError); err != nil {
		t.Fatalf("read validation state: %v", err)
	}
	if providerID != "team-preserved" || lastError != "provider unavailable" {
		t.Fatalf("validation error changed identity: provider=%q error=%q", providerID, lastError)
	}
	if err := dataStore.MarkAccountSynced(ctx, accountID); err != nil {
		t.Fatalf("MarkAccountSynced: %v", err)
	}
	var synced bool
	if err := dataStore.Pool.QueryRow(ctx, `SELECT last_synced_at IS NOT NULL FROM cloud_accounts WHERE id=$1`, accountID).Scan(&synced); err != nil || !synced {
		t.Fatalf("last_synced_at = %v, %v", synced, err)
	}

	var dropletID uuid.UUID
	err = dataStore.Pool.QueryRow(ctx, `INSERT INTO droplets(user_id,account_id,provider_id,name,status,vcpus,memory_mb,price_monthly)
		VALUES($1,$2,101,'one','active',1,1024,6) RETURNING id`, user.ID, accountID).Scan(&dropletID)
	if err != nil {
		t.Fatalf("insert droplet one: %v", err)
	}
	if _, err := dataStore.Pool.Exec(ctx, `INSERT INTO droplets(user_id,account_id,provider_id,name,status,vcpus,memory_mb,price_monthly)
		VALUES($1,$2,202,'two','off',2,2048,12)`, user.ID, accountID); err != nil {
		t.Fatalf("insert droplet two: %v", err)
	}
	var otherAccountID uuid.UUID
	if err := dataStore.Pool.QueryRow(ctx, `SELECT id FROM cloud_accounts WHERE user_id=$1`, other.ID).Scan(&otherAccountID); err != nil {
		t.Fatalf("read other account: %v", err)
	}
	if _, err := dataStore.Pool.Exec(ctx, `INSERT INTO droplets(user_id,account_id,provider_id,name,status,vcpus,memory_mb,price_monthly)
		VALUES($1,$2,303,'other','active',4,4096,24)`, other.ID, otherAccountID); err != nil {
		t.Fatalf("insert other droplet: %v", err)
	}

	if err := dataStore.SetDropletProject(ctx, accountID, 101, "project-abc"); err != nil {
		t.Fatalf("SetDropletProject: %v", err)
	}
	var projectID string
	if err := dataStore.Pool.QueryRow(ctx, `SELECT project_id FROM droplets WHERE id=$1`, dropletID).Scan(&projectID); err != nil || projectID != "project-abc" {
		t.Fatalf("project_id = %q, %v", projectID, err)
	}
	if err := dataStore.SetDropletProject(ctx, accountID, 999, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing droplet project update error = %v", err)
	}

	self, err := dataStore.UsageSummary(ctx, &user.ID)
	if err != nil {
		t.Fatalf("UsageSummary(self): %v", err)
	}
	if self.CloudAccounts != 2 || self.UnhealthyAccounts != 2 || self.AccountBalance != 12.5 || self.Droplets != 2 || self.ActiveDroplets != 1 || self.VCPUs != 3 || self.MemoryMB != 3072 || self.MonthlyCost != 18 {
		t.Fatalf("self usage summary = %+v", self)
	}
	global, err := dataStore.UsageSummary(ctx, nil)
	if err != nil {
		t.Fatalf("UsageSummary(global): %v", err)
	}
	if global.CloudAccounts != 3 || global.Droplets != 3 || global.ActiveDroplets != 2 || global.VCPUs != 7 || global.MemoryMB != 7168 || global.MonthlyCost != 42 {
		t.Fatalf("global usage summary = %+v", global)
	}

	if err := dataStore.SetSettingWithSecret(ctx, "smtp", map[string]any{"host": "smtp.example.com"}, []byte("secret"), []byte("nonce"), true, user.ID); err != nil {
		t.Fatalf("set SMTP secret: %v", err)
	}
	if err := dataStore.SetSettingWithSecret(ctx, "smtp", map[string]any{"host": "new.example.com"}, nil, nil, false, user.ID); err != nil {
		t.Fatalf("preserve SMTP secret: %v", err)
	}
	_, ciphertext, nonce, err := dataStore.GetSetting(ctx, "smtp")
	if err != nil || !bytes.Equal(ciphertext, []byte("secret")) || !bytes.Equal(nonce, []byte("nonce")) {
		t.Fatalf("preserved SMTP secret = (%q, %q, %v)", ciphertext, nonce, err)
	}
	if err := dataStore.SetSettingWithSecret(ctx, "smtp", map[string]any{"host": "new.example.com"}, nil, nil, true, user.ID); err != nil {
		t.Fatalf("clear SMTP secret: %v", err)
	}
	_, ciphertext, nonce, err = dataStore.GetSetting(ctx, "smtp")
	if err != nil || ciphertext != nil || nonce != nil {
		t.Fatalf("cleared SMTP secret = (%q, %q, %v)", ciphertext, nonce, err)
	}

	firstToken := []byte("first-token-hash")
	secondToken := []byte("second-token-hash")
	if err := dataStore.ReplaceOneTimeToken(ctx, user.ID, "verify_email", firstToken, time.Hour); err != nil {
		t.Fatalf("first ReplaceOneTimeToken: %v", err)
	}
	if err := dataStore.ReplaceOneTimeToken(ctx, user.ID, "verify_email", secondToken, time.Hour); err != nil {
		t.Fatalf("second ReplaceOneTimeToken: %v", err)
	}
	var tokenCount int
	var storedToken []byte
	if err := dataStore.Pool.QueryRow(ctx, `SELECT count(*),
		(SELECT token_hash FROM one_time_tokens WHERE user_id=$1 AND purpose='verify_email' LIMIT 1)
		FROM one_time_tokens WHERE user_id=$1 AND purpose='verify_email'`, user.ID).Scan(&tokenCount, &storedToken); err != nil {
		t.Fatalf("read replacement token: %v", err)
	}
	if tokenCount != 1 || !bytes.Equal(storedToken, secondToken) {
		t.Fatalf("replacement tokens = count %d, value %q", tokenCount, storedToken)
	}

	if _, err := dataStore.Pool.Exec(ctx, `INSERT INTO jobs(user_id,account_id,kind,state) VALUES
		($1,$2,'sync_account','queued'),($1,$2,'sync_account','failed'),($3,$4,'sync_account','failed')`,
		user.ID, accountID, other.ID, otherAccountID); err != nil {
		t.Fatalf("insert jobs: %v", err)
	}
	jobs, total, err := dataStore.ListJobs(ctx, user.ID, false, "failed", 20, 0)
	if err != nil || total != 1 || len(jobs) != 1 || jobs[0].State != "failed" {
		t.Fatalf("filtered user jobs = total %d, jobs %+v, err %v", total, jobs, err)
	}
	jobs, total, err = dataStore.ListJobs(ctx, user.ID, true, "failed", 20, 0)
	if err != nil || total != 2 || len(jobs) != 2 {
		t.Fatalf("filtered admin jobs = total %d, jobs %+v, err %v", total, jobs, err)
	}
}

func newIntegrationStore(t *testing.T) *store.Store {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	bootstrap, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open bootstrap database: %v", err)
	}
	schema := "store_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := bootstrap.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		bootstrap.Close()
		t.Fatalf("create test schema: %v", err)
	}

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		bootstrap.Close()
		t.Fatalf("parse test database URL: %v", err)
	}
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, "SET search_path TO "+identifier+", public")
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		_, _ = bootstrap.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE")
		bootstrap.Close()
		t.Fatalf("open schema pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = bootstrap.Exec(cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE")
		bootstrap.Close()
	})
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate test schema: %v", err)
	}
	return store.New(pool)
}
