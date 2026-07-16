package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ikun/cloud-account-manager/internal/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	Pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store { return &Store{Pool: pool} }

const userColumns = `id,email,password_hash,role,status,email_verified_at,quota_droplets,quota_vcpus,quota_memory_mb,created_at,updated_at`

func scanUser(row pgx.Row) (model.User, error) {
	var user model.User
	err := row.Scan(&user.ID, &user.Email, &user.PasswordHash, &user.Role, &user.Status, &user.EmailVerifiedAt,
		&user.QuotaDroplets, &user.QuotaVCPUs, &user.QuotaMemoryMB, &user.CreatedAt, &user.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.User{}, ErrNotFound
	}
	return user, err
}

func (s *Store) CreateUser(ctx context.Context, email, passwordHash, role, status string) (model.User, error) {
	return scanUser(s.Pool.QueryRow(ctx, `INSERT INTO users(email,password_hash,role,status,email_verified_at,quota_droplets,quota_vcpus,quota_memory_mb)
		VALUES($1,$2,$3,$4,CASE WHEN $4='active' THEN now() ELSE NULL END,
			CASE WHEN $3='user' THEN (SELECT (value->>'droplets')::int FROM system_settings WHERE key='default_quota') END,
			CASE WHEN $3='user' THEN (SELECT (value->>'vcpus')::int FROM system_settings WHERE key='default_quota') END,
			CASE WHEN $3='user' THEN (SELECT (value->>'memory_mb')::int FROM system_settings WHERE key='default_quota') END)
		RETURNING `+userColumns,
		strings.ToLower(strings.TrimSpace(email)), passwordHash, role, status))
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (model.User, error) {
	return scanUser(s.Pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE email=$1`, strings.ToLower(strings.TrimSpace(email))))
}

func (s *Store) GetUserByID(ctx context.Context, id uuid.UUID) (model.User, error) {
	return scanUser(s.Pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id=$1`, id))
}

func (s *Store) CreateOneTimeToken(ctx context.Context, userID uuid.UUID, purpose string, tokenHash []byte, ttl time.Duration) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO one_time_tokens(user_id,purpose,token_hash,expires_at)
		VALUES($1,$2,$3,now()+$4::interval)`, userID, purpose, tokenHash, durationInterval(ttl))
	return err
}

func (s *Store) ReplaceOneTimeToken(ctx context.Context, userID uuid.UUID, purpose string, tokenHash []byte, ttl time.Duration) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, userID.String()+":"+purpose); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM one_time_tokens WHERE user_id=$1 AND purpose=$2`, userID, purpose); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO one_time_tokens(user_id,purpose,token_hash,expires_at)
			VALUES($1,$2,$3,now()+$4::interval)`, userID, purpose, tokenHash, durationInterval(ttl))
		return err
	})
}

func (s *Store) ConsumeOneTimeToken(ctx context.Context, tokenHash []byte, purpose string) (uuid.UUID, error) {
	var userID uuid.UUID
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `UPDATE one_time_tokens SET consumed_at=now()
			WHERE token_hash=$1 AND purpose=$2 AND consumed_at IS NULL AND expires_at>now()
			RETURNING user_id`, tokenHash, purpose).Scan(&userID)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return userID, err
}

func (s *Store) ActivateUser(ctx context.Context, userID uuid.UUID) error {
	command, err := s.Pool.Exec(ctx, `UPDATE users SET status='active',email_verified_at=COALESCE(email_verified_at,now()),updated_at=now() WHERE id=$1`, userID)
	if err == nil && command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) UpdatePassword(ctx context.Context, userID uuid.UUID, passwordHash string) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE users SET password_hash=$2,updated_at=now() WHERE id=$1`, userID, passwordHash); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, userID)
		return err
	})
}

func (s *Store) CreateSession(ctx context.Context, userID uuid.UUID, tokenHash []byte, ip net.IP, userAgent string, ttl time.Duration) (model.Session, error) {
	var session model.Session
	err := s.Pool.QueryRow(ctx, `INSERT INTO sessions(user_id,token_hash,ip_address,user_agent,expires_at)
		VALUES($1,$2,$3,$4,now()+$5::interval) RETURNING id,user_id,recent_auth_at,expires_at`,
		userID, tokenHash, nullableIP(ip), truncate(userAgent, 512), durationInterval(ttl)).Scan(
		&session.ID, &session.UserID, &session.RecentAuthAt, &session.ExpiresAt)
	return session, err
}

func (s *Store) SessionUser(ctx context.Context, tokenHash []byte) (model.Session, model.User, error) {
	var session model.Session
	var user model.User
	err := s.Pool.QueryRow(ctx, `SELECT s.id,s.user_id,s.recent_auth_at,s.expires_at,
		u.id,u.email,u.password_hash,u.role,u.status,u.email_verified_at,u.quota_droplets,u.quota_vcpus,u.quota_memory_mb,u.created_at,u.updated_at
		FROM sessions s JOIN users u ON u.id=s.user_id
		WHERE s.token_hash=$1 AND s.expires_at>now()`, tokenHash).Scan(
		&session.ID, &session.UserID, &session.RecentAuthAt, &session.ExpiresAt,
		&user.ID, &user.Email, &user.PasswordHash, &user.Role, &user.Status, &user.EmailVerifiedAt,
		&user.QuotaDroplets, &user.QuotaVCPUs, &user.QuotaMemoryMB, &user.CreatedAt, &user.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Session{}, model.User{}, ErrNotFound
	}
	return session, user, err
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash []byte) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1`, tokenHash)
	return err
}

func (s *Store) MarkRecentAuth(ctx context.Context, sessionID uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `UPDATE sessions SET recent_auth_at=now() WHERE id=$1`, sessionID)
	return err
}

func (s *Store) ListUsers(ctx context.Context, limit, offset int) ([]model.User, int, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+userColumns+` FROM users ORDER BY created_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	users := make([]model.User, 0)
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, 0, err
		}
		users = append(users, user)
	}
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&total); err != nil {
		return nil, 0, err
	}
	return users, total, rows.Err()
}

type UserAdminUpdate struct {
	Role          *string
	Status        *string
	QuotaDroplets **int
	QuotaVCPUs    **int
	QuotaMemoryMB **int
}

func (s *Store) UpdateUserAdmin(ctx context.Context, id uuid.UUID, update UserAdminUpdate) (model.User, error) {
	return scanUser(s.Pool.QueryRow(ctx, `UPDATE users SET
		role=COALESCE($2,role), status=COALESCE($3,status),
		quota_droplets=CASE WHEN $4::boolean THEN $5 ELSE quota_droplets END,
		quota_vcpus=CASE WHEN $6::boolean THEN $7 ELSE quota_vcpus END,
		quota_memory_mb=CASE WHEN $8::boolean THEN $9 ELSE quota_memory_mb END,
		updated_at=now() WHERE id=$1 RETURNING `+userColumns,
		id, update.Role, update.Status,
		update.QuotaDroplets != nil, derefIntPtr(update.QuotaDroplets),
		update.QuotaVCPUs != nil, derefIntPtr(update.QuotaVCPUs),
		update.QuotaMemoryMB != nil, derefIntPtr(update.QuotaMemoryMB)))
}

func (s *Store) ActiveAdminCount(ctx context.Context) (int, error) {
	var count int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE role='admin' AND status='active'`).Scan(&count)
	return count, err
}

const accountColumns = `id,user_id,provider,name,provider_account_id,provider_email,provider_status,status_message,
	credential_status,account_limits,account_balance::float8,month_to_date_usage::float8,month_to_date_balance::float8,currency,
	rate_limit_remaining,rate_limit_reset_at,last_validated_at,last_synced_at,last_error,full_access_confirmed,created_at,updated_at,
	token_ciphertext,token_nonce,token_fingerprint`

func scanAccount(row pgx.Row) (model.CloudAccount, error) {
	var account model.CloudAccount
	err := row.Scan(&account.ID, &account.UserID, &account.Provider, &account.Name, &account.ProviderAccountID,
		&account.ProviderEmail, &account.ProviderStatus, &account.StatusMessage, &account.CredentialStatus,
		&account.AccountLimits, &account.AccountBalance, &account.MonthToDateUsage, &account.MonthToDateBalance,
		&account.Currency, &account.RateLimitRemaining, &account.RateLimitResetAt, &account.LastValidatedAt,
		&account.LastSyncedAt, &account.LastError, &account.FullAccessConfirmed, &account.CreatedAt, &account.UpdatedAt,
		&account.TokenCiphertext, &account.TokenNonce, &account.TokenFingerprintHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.CloudAccount{}, ErrNotFound
	}
	return account, err
}

func (s *Store) CreateCloudAccount(ctx context.Context, userID uuid.UUID, name string, ciphertext, nonce, fingerprint []byte, fullAccess bool) (model.CloudAccount, error) {
	return scanAccount(s.Pool.QueryRow(ctx, `INSERT INTO cloud_accounts(user_id,name,token_ciphertext,token_nonce,token_fingerprint,full_access_confirmed)
		VALUES($1,$2,$3,$4,$5,$6) RETURNING `+accountColumns,
		userID, strings.TrimSpace(name), ciphertext, nonce, fingerprint, fullAccess))
}

func (s *Store) GetCloudAccount(ctx context.Context, id uuid.UUID) (model.CloudAccount, error) {
	return scanAccount(s.Pool.QueryRow(ctx, `SELECT `+accountColumns+` FROM cloud_accounts WHERE id=$1`, id))
}

func (s *Store) ListCloudAccounts(ctx context.Context, userID uuid.UUID, admin bool) ([]model.CloudAccount, error) {
	query := `SELECT ` + accountColumns + ` FROM cloud_accounts`
	args := []any{}
	if !admin {
		query += ` WHERE user_id=$1`
		args = append(args, userID)
	}
	query += ` ORDER BY created_at DESC`
	rows, err := s.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := make([]model.CloudAccount, 0)
	for rows.Next() {
		account, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

type AccountValidation struct {
	ProviderAccountID  string
	ProviderEmail      string
	ProviderStatus     string
	StatusMessage      string
	CredentialStatus   string
	AccountLimits      any
	AccountBalance     *float64
	MonthToDateUsage   *float64
	MonthToDateBalance *float64
	RateRemaining      *int
	RateResetAt        *time.Time
	LastError          *string
}

func (s *Store) UpdateAccountValidation(ctx context.Context, id uuid.UUID, validation AccountValidation) error {
	limits, _ := json.Marshal(validation.AccountLimits)
	_, err := s.Pool.Exec(ctx, `UPDATE cloud_accounts SET provider_account_id=NULLIF($2,''),provider_email=NULLIF($3,''),
		provider_status=NULLIF($4,''),status_message=NULLIF($5,''),credential_status=$6,account_limits=$7,
		account_balance=$8,month_to_date_usage=$9,month_to_date_balance=$10,rate_limit_remaining=$11,
		rate_limit_reset_at=$12,last_error=$13,last_validated_at=now(),
		updated_at=now() WHERE id=$1`, id, validation.ProviderAccountID, validation.ProviderEmail, validation.ProviderStatus,
		validation.StatusMessage, validation.CredentialStatus, limits, validation.AccountBalance, validation.MonthToDateUsage,
		validation.MonthToDateBalance, validation.RateRemaining, validation.RateResetAt, validation.LastError)
	return err
}

func (s *Store) MarkAccountValidationError(ctx context.Context, id uuid.UUID, status, message string) error {
	command, err := s.Pool.Exec(ctx, `UPDATE cloud_accounts SET credential_status=$2,last_error=$3,
		last_validated_at=now(),updated_at=now() WHERE id=$1`, id, status, message)
	if err == nil && command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) MarkAccountSynced(ctx context.Context, id uuid.UUID) error {
	command, err := s.Pool.Exec(ctx, `UPDATE cloud_accounts SET last_synced_at=now(),updated_at=now() WHERE id=$1`, id)
	if err == nil && command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) ReplaceAccountToken(ctx context.Context, id uuid.UUID, ciphertext, nonce, fingerprint []byte, fullAccess bool) error {
	_, err := s.Pool.Exec(ctx, `UPDATE cloud_accounts SET token_ciphertext=$2,token_nonce=$3,token_fingerprint=$4,
		full_access_confirmed=$5,credential_status='unverified',last_error=NULL,updated_at=now() WHERE id=$1`,
		id, ciphertext, nonce, fingerprint, fullAccess)
	return err
}

func (s *Store) DeleteCloudAccount(ctx context.Context, id uuid.UUID) error {
	command, err := s.Pool.Exec(ctx, `DELETE FROM cloud_accounts WHERE id=$1`, id)
	if err == nil && command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) TransferCloudAccount(ctx context.Context, accountID, userID uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE cloud_accounts SET user_id=$2,updated_at=now() WHERE id=$1`, accountID, userID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE droplets SET user_id=$2,updated_at=now() WHERE account_id=$1`, accountID, userID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE managed_credentials mc SET user_id=$2,updated_at=now()
			FROM droplets d WHERE mc.droplet_id=d.id AND d.account_id=$1`, accountID, userID)
		return err
	})
}

func (s *Store) UpsertDroplets(ctx context.Context, account model.CloudAccount, droplets []model.Droplet) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		ids := make([]int64, 0, len(droplets))
		for _, droplet := range droplets {
			ids = append(ids, droplet.ProviderID)
			_, err := tx.Exec(ctx, `INSERT INTO droplets(user_id,account_id,provider_id,name,status,locked,region_slug,size_slug,vcpus,memory_mb,disk_gb,
				price_hourly,price_monthly,ipv4,ipv6,image,features,tags,vpc_uuid,raw,synced_at)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,now())
				ON CONFLICT(account_id,provider_id) DO UPDATE SET user_id=EXCLUDED.user_id,name=EXCLUDED.name,status=EXCLUDED.status,
				locked=EXCLUDED.locked,region_slug=EXCLUDED.region_slug,size_slug=EXCLUDED.size_slug,vcpus=EXCLUDED.vcpus,
				memory_mb=EXCLUDED.memory_mb,disk_gb=EXCLUDED.disk_gb,price_hourly=EXCLUDED.price_hourly,
				price_monthly=EXCLUDED.price_monthly,ipv4=EXCLUDED.ipv4,ipv6=EXCLUDED.ipv6,image=EXCLUDED.image,
				features=EXCLUDED.features,tags=EXCLUDED.tags,vpc_uuid=EXCLUDED.vpc_uuid,raw=EXCLUDED.raw,synced_at=now(),updated_at=now()`,
				account.UserID, account.ID, droplet.ProviderID, droplet.Name, droplet.Status, droplet.Locked,
				droplet.RegionSlug, droplet.SizeSlug, droplet.VCPUs, droplet.MemoryMB, droplet.DiskGB,
				droplet.PriceHourly, droplet.PriceMonthly, droplet.IPv4, droplet.IPv6, droplet.Image,
				droplet.Features, droplet.Tags, droplet.VPCUUID, droplet.Raw)
			if err != nil {
				return err
			}
		}
		if len(ids) == 0 {
			_, err := tx.Exec(ctx, `DELETE FROM droplets WHERE account_id=$1`, account.ID)
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM droplets WHERE account_id=$1 AND NOT(provider_id=ANY($2))`, account.ID, ids)
		return err
	})
}

const dropletColumns = `id,user_id,account_id,provider_id,name,status,locked,region_slug,size_slug,vcpus,memory_mb,disk_gb,
	price_hourly::float8,price_monthly::float8,ipv4,ipv6,image,features,tags,vpc_uuid,project_id,synced_at,created_at,updated_at`

func scanDroplet(row pgx.Row) (model.Droplet, error) {
	var droplet model.Droplet
	err := row.Scan(&droplet.ID, &droplet.UserID, &droplet.AccountID, &droplet.ProviderID, &droplet.Name, &droplet.Status,
		&droplet.Locked, &droplet.RegionSlug, &droplet.SizeSlug, &droplet.VCPUs, &droplet.MemoryMB, &droplet.DiskGB,
		&droplet.PriceHourly, &droplet.PriceMonthly, &droplet.IPv4, &droplet.IPv6, &droplet.Image, &droplet.Features,
		&droplet.Tags, &droplet.VPCUUID, &droplet.ProjectID, &droplet.SyncedAt, &droplet.CreatedAt, &droplet.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Droplet{}, ErrNotFound
	}
	return droplet, err
}

func (s *Store) ListDroplets(ctx context.Context, userID uuid.UUID, admin bool, accountID *uuid.UUID, search string, limit, offset int) ([]model.Droplet, int, error) {
	where := []string{"1=1"}
	args := []any{}
	if !admin {
		args = append(args, userID)
		where = append(where, fmt.Sprintf("user_id=$%d", len(args)))
	}
	if accountID != nil {
		args = append(args, *accountID)
		where = append(where, fmt.Sprintf("account_id=$%d", len(args)))
	}
	if strings.TrimSpace(search) != "" {
		args = append(args, "%"+strings.TrimSpace(search)+"%")
		where = append(where, fmt.Sprintf("(name ILIKE $%d OR ipv4 ILIKE $%d)", len(args), len(args)))
	}
	base := strings.Join(where, " AND ")
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM droplets WHERE `+base, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := s.Pool.Query(ctx, `SELECT `+dropletColumns+` FROM droplets WHERE `+base+
		fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]model.Droplet, 0)
	for rows.Next() {
		item, err := scanDroplet(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (s *Store) GetDroplet(ctx context.Context, id uuid.UUID) (model.Droplet, error) {
	return scanDroplet(s.Pool.QueryRow(ctx, `SELECT `+dropletColumns+` FROM droplets WHERE id=$1`, id))
}

func (s *Store) GetDropletByProviderID(ctx context.Context, accountID uuid.UUID, providerID int64) (model.Droplet, error) {
	return scanDroplet(s.Pool.QueryRow(ctx, `SELECT `+dropletColumns+` FROM droplets WHERE account_id=$1 AND provider_id=$2`, accountID, providerID))
}

func (s *Store) SetDropletProject(ctx context.Context, accountID uuid.UUID, providerID int64, projectID string) error {
	command, err := s.Pool.Exec(ctx, `UPDATE droplets SET project_id=$3,updated_at=now() WHERE account_id=$1 AND provider_id=$2`, accountID, providerID, projectID)
	if err == nil && command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) MarkRootCredentialStale(ctx context.Context, dropletID uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `UPDATE managed_credentials SET status='stale',updated_at=now() WHERE droplet_id=$1 AND kind='root_password'`, dropletID)
	return err
}

func (s *Store) UpsertRootCredential(ctx context.Context, userID, dropletID uuid.UUID, ciphertext, nonce []byte) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO managed_credentials(user_id,droplet_id,kind,ciphertext,nonce,status)
		VALUES($1,$2,'root_password',$3,$4,'active') ON CONFLICT(droplet_id,kind) DO UPDATE SET
		user_id=EXCLUDED.user_id,ciphertext=EXCLUDED.ciphertext,nonce=EXCLUDED.nonce,status='active',updated_at=now()`,
		userID, dropletID, ciphertext, nonce)
	return err
}

func (s *Store) RootCredential(ctx context.Context, dropletID uuid.UUID) (ciphertext, nonce []byte, status string, err error) {
	err = s.Pool.QueryRow(ctx, `SELECT ciphertext,nonce,status FROM managed_credentials WHERE droplet_id=$1 AND kind='root_password'`, dropletID).Scan(&ciphertext, &nonce, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

func (s *Store) UserUsage(ctx context.Context, userID uuid.UUID) (droplets, vcpus, memoryMB int, err error) {
	err = s.Pool.QueryRow(ctx, `SELECT count(*),COALESCE(sum(vcpus),0),COALESCE(sum(memory_mb),0) FROM droplets WHERE user_id=$1`, userID).
		Scan(&droplets, &vcpus, &memoryMB)
	return
}

type UsageSummary struct {
	CloudAccounts     int     `json:"cloud_accounts"`
	UnhealthyAccounts int     `json:"unhealthy_accounts"`
	AccountBalance    float64 `json:"account_balance"`
	Droplets          int     `json:"droplets"`
	ActiveDroplets    int     `json:"active_droplets"`
	VCPUs             int     `json:"vcpus"`
	MemoryMB          int     `json:"memory_mb"`
	MonthlyCost       float64 `json:"monthly_cost"`
}

// UsageSummary aggregates the complete local inventory. A nil userID selects
// the global administrator scope; a non-nil userID restricts every aggregate.
func (s *Store) UsageSummary(ctx context.Context, userID *uuid.UUID) (UsageSummary, error) {
	var scope any
	if userID != nil {
		scope = *userID
	}
	var summary UsageSummary
	err := s.Pool.QueryRow(ctx, `WITH account_totals AS (
			SELECT count(*)::int AS cloud_accounts,
				count(*) FILTER (WHERE credential_status<>'valid')::int AS unhealthy_accounts,
				COALESCE(sum(account_balance),0)::float8 AS account_balance
			FROM cloud_accounts WHERE ($1::uuid IS NULL OR user_id=$1)
		), droplet_totals AS (
			SELECT count(*)::int AS droplets,
				count(*) FILTER (WHERE status='active')::int AS active_droplets,
				COALESCE(sum(vcpus),0)::int AS vcpus,
				COALESCE(sum(memory_mb),0)::int AS memory_mb,
				COALESCE(sum(price_monthly),0)::float8 AS monthly_cost
			FROM droplets WHERE ($1::uuid IS NULL OR user_id=$1)
		)
		SELECT cloud_accounts,unhealthy_accounts,account_balance,droplets,active_droplets,vcpus,memory_mb,monthly_cost
		FROM account_totals CROSS JOIN droplet_totals`, scope).Scan(
		&summary.CloudAccounts, &summary.UnhealthyAccounts, &summary.AccountBalance,
		&summary.Droplets, &summary.ActiveDroplets, &summary.VCPUs, &summary.MemoryMB, &summary.MonthlyCost)
	return summary, err
}

func (s *Store) EnqueueJob(ctx context.Context, userID uuid.UUID, accountID *uuid.UUID, kind string, payload any) (model.Job, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return model.Job{}, err
	}
	return scanJob(s.Pool.QueryRow(ctx, `INSERT INTO jobs(user_id,account_id,kind,payload) VALUES($1,$2,$3,$4) RETURNING `+jobColumns,
		userID, accountID, kind, encoded))
}

const jobColumns = `id,user_id,account_id,kind,state,payload,result,provider_action_ids,progress,attempts,error_message,
	scheduled_at,started_at,finished_at,created_at,updated_at`

func scanJob(row pgx.Row) (model.Job, error) {
	var job model.Job
	err := row.Scan(&job.ID, &job.UserID, &job.AccountID, &job.Kind, &job.State, &job.Payload, &job.Result,
		&job.ProviderActionIDs, &job.Progress, &job.Attempts, &job.ErrorMessage, &job.ScheduledAt, &job.StartedAt,
		&job.FinishedAt, &job.CreatedAt, &job.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Job{}, ErrNotFound
	}
	return job, err
}

func (s *Store) ClaimJob(ctx context.Context) (model.Job, error) {
	var job model.Job
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `SELECT `+jobColumns+` FROM jobs WHERE state='queued' AND scheduled_at<=now()
			ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1`)
		claimed, err := scanJob(row)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE jobs SET state='running',started_at=COALESCE(started_at,now()),attempts=attempts+1,updated_at=now() WHERE id=$1`, claimed.ID); err != nil {
			return err
		}
		claimed.State = "running"
		claimed.Attempts++
		job = claimed
		return nil
	})
	return job, err
}

func (s *Store) UpdateJob(ctx context.Context, id uuid.UUID, state string, progress int, result any, actionIDs []int64, message *string) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `UPDATE jobs SET state=$2,progress=$3,result=$4,provider_action_ids=$5,error_message=$6,
		finished_at=CASE WHEN $2 IN ('succeeded','failed','partial') THEN now() ELSE finished_at END,updated_at=now() WHERE id=$1`,
		id, state, progress, encoded, actionIDs, message)
	return err
}

func (s *Store) ListJobs(ctx context.Context, userID uuid.UUID, admin bool, state string, limit, offset int) ([]model.Job, int, error) {
	where := make([]string, 0, 2)
	args := []any{}
	if !admin {
		args = append(args, userID)
		where = append(where, fmt.Sprintf("user_id=$%d", len(args)))
	}
	if state != "" {
		args = append(args, state)
		where = append(where, fmt.Sprintf("state=$%d", len(args)))
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs`+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := s.Pool.Query(ctx, `SELECT `+jobColumns+` FROM jobs`+clause+fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	jobs := make([]model.Job, 0)
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, 0, err
		}
		jobs = append(jobs, job)
	}
	return jobs, total, rows.Err()
}

func (s *Store) EnqueueStaleSyncs(ctx context.Context, olderThan time.Duration) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO jobs(user_id,account_id,kind,payload)
		SELECT ca.user_id,ca.id,'sync_account',jsonb_build_object('account_id',ca.id)
		FROM cloud_accounts ca
		WHERE (ca.last_synced_at IS NULL OR ca.last_synced_at<now()-$1::interval)
		AND NOT EXISTS(SELECT 1 FROM jobs j WHERE j.account_id=ca.id AND j.kind='sync_account' AND j.state IN ('queued','running'))`,
		durationInterval(olderThan))
	return err
}

func (s *Store) GetSetting(ctx context.Context, key string) (json.RawMessage, []byte, []byte, error) {
	var value json.RawMessage
	var ciphertext, nonce []byte
	err := s.Pool.QueryRow(ctx, `SELECT value,secret_ciphertext,secret_nonce FROM system_settings WHERE key=$1`, key).Scan(&value, &ciphertext, &nonce)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil, ErrNotFound
	}
	return value, ciphertext, nonce, err
}

func (s *Store) ListSettings(ctx context.Context) (map[string]json.RawMessage, error) {
	rows, err := s.Pool.Query(ctx, `SELECT key,value FROM system_settings ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	settings := map[string]json.RawMessage{}
	for rows.Next() {
		var key string
		var value json.RawMessage
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		settings[key] = value
	}
	return settings, rows.Err()
}

func (s *Store) SetSetting(ctx context.Context, key string, value any, ciphertext, nonce []byte, actor uuid.UUID) error {
	return s.SetSettingWithSecret(ctx, key, value, ciphertext, nonce, false, actor)
}

// SetSettingWithSecret updates a setting and optionally replaces its secret. When
// updateSecret is true, nil ciphertext and nonce explicitly clear the secret.
func (s *Store) SetSettingWithSecret(ctx context.Context, key string, value any, ciphertext, nonce []byte, updateSecret bool, actor uuid.UUID) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO system_settings(key,value,secret_ciphertext,secret_nonce,updated_by,updated_at)
		VALUES($1,$2,$3,$4,$5,now()) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,
		secret_ciphertext=CASE WHEN $6 THEN EXCLUDED.secret_ciphertext ELSE system_settings.secret_ciphertext END,
		secret_nonce=CASE WHEN $6 THEN EXCLUDED.secret_nonce ELSE system_settings.secret_nonce END,
		updated_by=EXCLUDED.updated_by,updated_at=now()`,
		key, encoded, ciphertext, nonce, actor, updateSecret)
	return err
}

type AuditEntry struct {
	ActorUserID  *uuid.UUID
	TargetUserID *uuid.UUID
	Action       string
	ResourceType string
	ResourceID   string
	IP           net.IP
	UserAgent    string
	Metadata     any
}

func (s *Store) Audit(ctx context.Context, entry AuditEntry) error {
	metadata, _ := json.Marshal(entry.Metadata)
	_, err := s.Pool.Exec(ctx, `INSERT INTO audit_logs(actor_user_id,target_user_id,action,resource_type,resource_id,ip_address,user_agent,metadata)
		VALUES($1,$2,$3,$4,NULLIF($5,''),$6,$7,$8)`, entry.ActorUserID, entry.TargetUserID, entry.Action,
		entry.ResourceType, entry.ResourceID, nullableIP(entry.IP), truncate(entry.UserAgent, 512), metadata)
	return err
}

func (s *Store) ListAudit(ctx context.Context, limit, offset int) ([]map[string]any, int, error) {
	rows, err := s.Pool.Query(ctx, `SELECT a.id,a.actor_user_id,u.email,a.target_user_id,a.action,a.resource_type,a.resource_id,
		a.ip_address::text,a.user_agent,a.metadata,a.created_at FROM audit_logs a LEFT JOIN users u ON u.id=a.actor_user_id
		ORDER BY a.created_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var actorID, targetID *uuid.UUID
		var actorEmail, resourceID, ip, userAgent *string
		var action, resourceType string
		var metadata json.RawMessage
		var created time.Time
		if err := rows.Scan(&id, &actorID, &actorEmail, &targetID, &action, &resourceType, &resourceID, &ip, &userAgent, &metadata, &created); err != nil {
			return nil, 0, err
		}
		items = append(items, map[string]any{"id": id, "actor_user_id": actorID, "actor_email": actorEmail,
			"target_user_id": targetID, "action": action, "resource_type": resourceType, "resource_id": resourceID,
			"ip_address": ip, "user_agent": userAgent, "metadata": metadata, "created_at": created})
	}
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs`).Scan(&total); err != nil {
		return nil, 0, err
	}
	return items, total, rows.Err()
}

func (s *Store) StatusCounts(ctx context.Context) (map[string]int64, error) {
	counts := map[string]int64{}
	queries := map[string]string{
		"users":          `SELECT count(*) FROM users`,
		"accounts":       `SELECT count(*) FROM cloud_accounts`,
		"droplets":       `SELECT count(*) FROM droplets`,
		"queued_jobs":    `SELECT count(*) FROM jobs WHERE state='queued'`,
		"running_jobs":   `SELECT count(*) FROM jobs WHERE state='running'`,
		"failed_jobs":    `SELECT count(*) FROM jobs WHERE state='failed' AND created_at>now()-interval '24 hours'`,
		"active_workers": `SELECT count(*) FROM worker_heartbeats WHERE last_seen_at>now()-interval '30 seconds'`,
	}
	for key, query := range queries {
		var value int64
		if err := s.Pool.QueryRow(ctx, query).Scan(&value); err != nil {
			return nil, err
		}
		counts[key] = value
	}
	return counts, nil
}

func (s *Store) WorkerHeartbeat(ctx context.Context, workerID uuid.UUID, hostname string, processID int) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO worker_heartbeats(worker_id,hostname,process_id) VALUES($1,$2,$3)
		ON CONFLICT(worker_id) DO UPDATE SET hostname=EXCLUDED.hostname,process_id=EXCLUDED.process_id,last_seen_at=now()`,
		workerID, hostname, processID)
	return err
}

func durationInterval(duration time.Duration) string {
	return fmt.Sprintf("%f seconds", duration.Seconds())
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}

func nullableIP(ip net.IP) any {
	if ip == nil {
		return nil
	}
	return ip.String()
}

func derefIntPtr(value **int) any {
	if value == nil || *value == nil {
		return nil
	}
	return **value
}
