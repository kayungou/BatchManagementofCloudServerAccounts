package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID              uuid.UUID  `json:"id"`
	Email           string     `json:"email"`
	PasswordHash    string     `json:"-"`
	Role            string     `json:"role"`
	Status          string     `json:"status"`
	EmailVerifiedAt *time.Time `json:"email_verified_at,omitempty"`
	QuotaDroplets   *int       `json:"quota_droplets"`
	QuotaVCPUs      *int       `json:"quota_vcpus"`
	QuotaMemoryMB   *int       `json:"quota_memory_mb"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type Session struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	RecentAuthAt time.Time
	ExpiresAt    time.Time
}

type CloudAccount struct {
	ID                   uuid.UUID       `json:"id"`
	UserID               uuid.UUID       `json:"user_id"`
	Provider             string          `json:"provider"`
	Name                 string          `json:"name"`
	ProviderAccountID    *string         `json:"provider_account_id,omitempty"`
	ProviderEmail        *string         `json:"provider_email,omitempty"`
	ProviderStatus       *string         `json:"provider_status,omitempty"`
	StatusMessage        *string         `json:"status_message,omitempty"`
	CredentialStatus     string          `json:"credential_status"`
	AccountLimits        json.RawMessage `json:"account_limits"`
	AccountBalance       *float64        `json:"account_balance,omitempty"`
	MonthToDateUsage     *float64        `json:"month_to_date_usage,omitempty"`
	MonthToDateBalance   *float64        `json:"month_to_date_balance,omitempty"`
	Currency             string          `json:"currency"`
	RateLimitRemaining   *int            `json:"rate_limit_remaining,omitempty"`
	RateLimitResetAt     *time.Time      `json:"rate_limit_reset_at,omitempty"`
	LastValidatedAt      *time.Time      `json:"last_validated_at,omitempty"`
	LastSyncedAt         *time.Time      `json:"last_synced_at,omitempty"`
	LastError            *string         `json:"last_error,omitempty"`
	FullAccessConfirmed  bool            `json:"full_access_confirmed"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
	TokenCiphertext      []byte          `json:"-"`
	TokenNonce           []byte          `json:"-"`
	TokenFingerprintHash []byte          `json:"-"`
}

type Droplet struct {
	ID           uuid.UUID       `json:"id"`
	UserID       uuid.UUID       `json:"user_id"`
	AccountID    uuid.UUID       `json:"account_id"`
	ProviderID   int64           `json:"provider_id"`
	Name         string          `json:"name"`
	Status       string          `json:"status"`
	Locked       bool            `json:"locked"`
	RegionSlug   *string         `json:"region_slug,omitempty"`
	SizeSlug     *string         `json:"size_slug,omitempty"`
	VCPUs        int             `json:"vcpus"`
	MemoryMB     int             `json:"memory_mb"`
	DiskGB       int             `json:"disk_gb"`
	PriceHourly  *float64        `json:"price_hourly,omitempty"`
	PriceMonthly *float64        `json:"price_monthly,omitempty"`
	IPv4         *string         `json:"ipv4,omitempty"`
	IPv6         *string         `json:"ipv6,omitempty"`
	Image        json.RawMessage `json:"image"`
	Features     json.RawMessage `json:"features"`
	Tags         []string        `json:"tags"`
	VPCUUID      *string         `json:"vpc_uuid,omitempty"`
	ProjectID    *string         `json:"project_id,omitempty"`
	Raw          json.RawMessage `json:"-"`
	SyncedAt     time.Time       `json:"synced_at"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

type Job struct {
	ID                uuid.UUID       `json:"id"`
	UserID            uuid.UUID       `json:"user_id"`
	AccountID         *uuid.UUID      `json:"account_id,omitempty"`
	Kind              string          `json:"kind"`
	State             string          `json:"state"`
	Payload           json.RawMessage `json:"payload"`
	Result            json.RawMessage `json:"result"`
	ProviderActionIDs []int64         `json:"provider_action_ids"`
	Progress          int             `json:"progress"`
	Attempts          int             `json:"attempts"`
	ErrorMessage      *string         `json:"error_message,omitempty"`
	ScheduledAt       time.Time       `json:"scheduled_at"`
	StartedAt         *time.Time      `json:"started_at,omitempty"`
	FinishedAt        *time.Time      `json:"finished_at,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}
