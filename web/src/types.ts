export interface User {
  id: string
  email: string
  role: 'admin' | 'user'
  status: 'pending' | 'active' | 'disabled'
  email_verified_at?: string
  quota_droplets: number | null
  quota_vcpus: number | null
  quota_memory_mb: number | null
  created_at: string
  updated_at?: string
}

export interface CloudAccount {
  id: string
  user_id: string
  provider: 'digitalocean'
  name: string
  provider_account_id?: string
  provider_email?: string
  provider_status?: string
  status_message?: string
  credential_status: 'unverified' | 'valid' | 'insufficient' | 'invalid'
  account_limits: { droplet_limit?: number; floating_ip_limit?: number }
  account_balance?: number
  month_to_date_usage?: number
  month_to_date_balance?: number
  currency: string
  rate_limit_remaining?: number
  last_synced_at?: string
  last_error?: string
  full_access_confirmed: boolean
  created_at?: string
  updated_at?: string
}

export interface Droplet {
  id: string
  user_id: string
  account_id: string
  provider_id: number
  name: string
  status: string
  locked: boolean
  region_slug?: string
  size_slug?: string
  vcpus: number
  memory_mb: number
  disk_gb: number
  price_hourly?: number
  price_monthly?: number
  ipv4?: string
  ipv6?: string
  image: { name?: string; distribution?: string; slug?: string }
  features: string[]
  tags: string[]
  vpc_uuid?: string
  project_id?: string
  synced_at: string
}

export interface Job {
  id: string
  user_id: string
  account_id?: string
  kind: string
  state: 'queued' | 'running' | 'succeeded' | 'failed' | 'partial'
  payload: Record<string, unknown>
  result: Record<string, unknown>
  provider_action_ids: number[]
  progress: number
  attempts: number
  error_message?: string
  scheduled_at: string
  started_at?: string
  finished_at?: string
  created_at: string
  updated_at: string
}

export interface ListResponse<T> {
  items: T[]
  total: number
  page?: number
  per_page?: number
}

export interface MeResponse {
  user: User
  recent_auth_at: string
  expires_at: string
}

export interface PublicConfig {
  site: { name?: string; timezone?: string }
  registration: { enabled?: boolean }
  maintenance: { enabled?: boolean; message?: string }
}

export interface Region {
  slug: string
  name: string
  available: boolean
  features: string[]
  sizes: string[]
}

export interface Size {
  slug: string
  memory: number
  vcpus: number
  disk: number
  transfer: number
  price_monthly: number
  price_hourly: number
  regions: string[]
  available: boolean
  description?: string
}

export interface Image {
  id: number
  name: string
  distribution: string
  slug?: string
  public: boolean
  regions: string[]
  type: string
  min_disk_size: number
  status: string
}

export interface Project {
  id: string
  name: string
  description?: string
  purpose?: string
  environment?: string
  is_default?: boolean
}

export interface VPC {
  id: string
  urn: string
  name: string
  region: string
  ip_range: string
  default?: boolean
}

export interface SSHKey {
  id: number
  fingerprint: string
  public_key: string
  name: string
}

export interface AuditLog {
  id: number
  actor_user_id?: string
  actor_email?: string
  target_user_id?: string
  action: string
  resource_type: string
  resource_id?: string
  ip_address?: string
  user_agent?: string
  metadata: Record<string, unknown>
  created_at: string
}

export interface SystemSettings {
  site: { name: string; timezone: string }
  registration: { enabled: boolean }
  maintenance: { enabled: boolean; message: string }
  session: { hours: number }
  default_quota: { droplets: number | null; vcpus: number | null; memory_mb: number | null }
  smtp: {
    host: string
    port: number
    username: string
    from: string
    starttls: boolean
    password_configured?: boolean
  }
}

export interface SystemStatus {
  version: string
  commit: string
  build_time: string
  go_version: string
  environment: string
  uptime_seconds: number
  database: { ok: boolean; latency_ms: number; migration_version: number }
  counts: Record<string, number>
  worker_ok: boolean
}

export interface UsageSummary {
  scope: 'self' | 'all'
  cloud_accounts: number
  unhealthy_accounts: number
  account_balance: number
  droplets: number
  active_droplets: number
  vcpus: number
  memory_mb: number
  monthly_cost: number
}
