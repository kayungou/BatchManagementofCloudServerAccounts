CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email CITEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('admin', 'user')),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'active', 'disabled')),
    email_verified_at TIMESTAMPTZ,
    quota_droplets INTEGER CHECK (quota_droplets IS NULL OR quota_droplets >= 0),
    quota_vcpus INTEGER CHECK (quota_vcpus IS NULL OR quota_vcpus >= 0),
    quota_memory_mb INTEGER CHECK (quota_memory_mb IS NULL OR quota_memory_mb >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash BYTEA NOT NULL UNIQUE,
    ip_address INET,
    user_agent TEXT,
    recent_auth_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX sessions_user_idx ON sessions(user_id);
CREATE INDEX sessions_expires_idx ON sessions(expires_at);

CREATE TABLE one_time_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose TEXT NOT NULL CHECK (purpose IN ('verify_email', 'reset_password')),
    token_hash BYTEA NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX one_time_tokens_lookup_idx ON one_time_tokens(token_hash, purpose);

CREATE TABLE cloud_accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    provider TEXT NOT NULL DEFAULT 'digitalocean' CHECK (provider = 'digitalocean'),
    name TEXT NOT NULL,
    provider_account_id TEXT UNIQUE,
    provider_email TEXT,
    provider_status TEXT,
    status_message TEXT,
    token_ciphertext BYTEA NOT NULL,
    token_nonce BYTEA NOT NULL,
    token_fingerprint BYTEA NOT NULL,
    full_access_confirmed BOOLEAN NOT NULL DEFAULT FALSE,
    credential_status TEXT NOT NULL DEFAULT 'unverified' CHECK (credential_status IN ('unverified', 'valid', 'insufficient', 'invalid')),
    account_limits JSONB NOT NULL DEFAULT '{}'::jsonb,
    account_balance NUMERIC(18,6),
    month_to_date_usage NUMERIC(18,6),
    month_to_date_balance NUMERIC(18,6),
    currency TEXT NOT NULL DEFAULT 'USD',
    rate_limit_remaining INTEGER,
    rate_limit_reset_at TIMESTAMPTZ,
    last_validated_at TIMESTAMPTZ,
    last_synced_at TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX cloud_accounts_user_idx ON cloud_accounts(user_id);
CREATE UNIQUE INDEX cloud_accounts_token_fingerprint_idx ON cloud_accounts(token_fingerprint);

CREATE TABLE droplets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    account_id UUID NOT NULL REFERENCES cloud_accounts(id) ON DELETE CASCADE,
    provider_id BIGINT NOT NULL,
    name TEXT NOT NULL,
    status TEXT NOT NULL,
    locked BOOLEAN NOT NULL DEFAULT FALSE,
    region_slug TEXT,
    size_slug TEXT,
    vcpus INTEGER NOT NULL DEFAULT 0,
    memory_mb INTEGER NOT NULL DEFAULT 0,
    disk_gb INTEGER NOT NULL DEFAULT 0,
    price_hourly NUMERIC(18,6),
    price_monthly NUMERIC(18,6),
    ipv4 TEXT,
    ipv6 TEXT,
    image JSONB NOT NULL DEFAULT '{}'::jsonb,
    features JSONB NOT NULL DEFAULT '[]'::jsonb,
    tags TEXT[] NOT NULL DEFAULT '{}',
    vpc_uuid TEXT,
    project_id TEXT,
    raw JSONB NOT NULL DEFAULT '{}'::jsonb,
    synced_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(account_id, provider_id)
);
CREATE INDEX droplets_user_idx ON droplets(user_id);
CREATE INDEX droplets_account_idx ON droplets(account_id);
CREATE INDEX droplets_status_idx ON droplets(status);

CREATE TABLE managed_credentials (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    droplet_id UUID NOT NULL REFERENCES droplets(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind = 'root_password'),
    ciphertext BYTEA NOT NULL,
    nonce BYTEA NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'stale', 'revoked')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(droplet_id, kind)
);

CREATE TABLE jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    account_id UUID REFERENCES cloud_accounts(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'running', 'succeeded', 'failed', 'partial')),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    result JSONB NOT NULL DEFAULT '{}'::jsonb,
    provider_action_ids BIGINT[] NOT NULL DEFAULT '{}',
    progress INTEGER NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    attempts INTEGER NOT NULL DEFAULT 0,
    error_message TEXT,
    scheduled_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX jobs_queue_idx ON jobs(state, scheduled_at, created_at);
CREATE INDEX jobs_user_idx ON jobs(user_id, created_at DESC);

CREATE TABLE worker_heartbeats (
    worker_id UUID PRIMARY KEY,
    hostname TEXT NOT NULL,
    process_id INTEGER NOT NULL,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX worker_heartbeats_seen_idx ON worker_heartbeats(last_seen_at DESC);

CREATE TABLE system_settings (
    key TEXT PRIMARY KEY,
    value JSONB NOT NULL DEFAULT '{}'::jsonb,
    secret_ciphertext BYTEA,
    secret_nonce BYTEA,
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE audit_logs (
    id BIGSERIAL PRIMARY KEY,
    actor_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    target_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT,
    ip_address INET,
    user_agent TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX audit_logs_created_idx ON audit_logs(created_at DESC);
CREATE INDEX audit_logs_actor_idx ON audit_logs(actor_user_id, created_at DESC);

INSERT INTO system_settings(key, value) VALUES
    ('site', '{"name":"云服务器托管平台","timezone":"Asia/Shanghai"}'::jsonb),
    ('registration', '{"enabled":true}'::jsonb),
    ('maintenance', '{"enabled":false,"message":"系统维护中"}'::jsonb),
    ('session', '{"hours":168}'::jsonb),
    ('default_quota', '{"droplets":null,"vcpus":null,"memory_mb":null}'::jsonb),
    ('smtp', '{"host":"","port":587,"username":"","from":"","starttls":true}'::jsonb)
ON CONFLICT (key) DO NOTHING;
