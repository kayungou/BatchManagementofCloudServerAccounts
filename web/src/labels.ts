const statusLabels: Record<string, string> = {
  active: '正常',
  pending: '待验证',
  disabled: '已停用',
  valid: '有效',
  invalid: '无效',
  insufficient: '权限不足',
  unverified: '待验证',
  warning: '警告',
  locked: '已锁定',
  unknown: '未知',
  new: '创建中',
  off: '已关机',
  archive: '已归档',
  queued: '排队中',
  running: '执行中',
  succeeded: '已完成',
  failed: '失败',
  partial: '部分完成',
  completed: '已完成',
  errored: '失败',
  in_progress: '进行中',
  stale: '需更新',
  revoked: '已撤销',
  none: '未托管',
}

const auditActionLabels: Record<string, string> = {
  'auth.login': '登录',
  'auth.logout': '退出登录',
  'cloud_account.create': '绑定云账号',
  'cloud_account.token_replace': '替换云账号 Token',
  'cloud_account.delete': '解绑云账号',
  'ssh_key.create': '添加 SSH 公钥',
  'ssh_key.update': '更新 SSH 公钥',
  'ssh_key.delete': '删除 SSH 公钥',
  'droplet.create_enqueue': '提交创建实例任务',
  'droplet.action_enqueue': '提交实例操作任务',
  'droplet.delete_enqueue': '提交销毁实例任务',
  'root_credential.reveal': '查看 root 凭据',
  'root_credential.update': '更新 root 凭据',
  'admin.user_create': '创建用户',
  'admin.user_update': '更新用户',
  'admin.settings_update': '更新系统设置',
  'admin.smtp_test': '发送 SMTP 测试邮件',
  'admin.account_transfer': '转移云账号归属',
}

const auditResourceLabels: Record<string, string> = {
  session: '会话',
  user: '用户',
  system: '系统',
  cloud_account: '云账号',
  droplet: '实例',
  ssh_key: 'SSH 公钥',
}

const digitalOceanStatusMessageLabels: Record<string, string> = {
  'Good Standing': '账号状态正常',
  'Your account is active and in good standing.': '账号已启用且状态正常。',
  'Your team has created the maximum allowed number of Droplets. Please resolve this on the control panel.': '团队已达到 Droplet 实例数量上限，请前往 DigitalOcean 控制面板处理。',
}

export function statusLabel(value?: string) {
  const normalized = value || 'unknown'
  return statusLabels[normalized] || normalized
}

export function auditActionLabel(value: string) {
  return auditActionLabels[value] || value
}

export function auditResourceLabel(value: string) {
  return auditResourceLabels[value] || value
}

export function digitalOceanStatusMessageLabel(value?: string) {
  if (!value) return ''
  return digitalOceanStatusMessageLabels[value] || value
}
