import type { ReactNode } from 'react'
import { AlertTriangle, CheckCircle2, ChevronLeft, ChevronRight, Inbox, LoaderCircle, X } from 'lucide-react'
import { api, json } from '../api'

export function Page({ children }: { children: ReactNode }) {
  return <div className="page">{children}</div>
}

export function PageHeader({ title, subtitle, actions }: { title: string; subtitle?: string; actions?: ReactNode }) {
  return <div className="page-header"><div><h1>{title}</h1>{subtitle && <p>{subtitle}</p>}</div>{actions && <div className="page-actions">{actions}</div>}</div>
}

export function Spinner({ label = '正在加载' }: { label?: string }) {
  return <div className="loading-state"><LoaderCircle className="spin" size={22}/><span>{label}</span></div>
}

export function EmptyState({ title, action }: { title: string; action?: ReactNode }) {
  return <div className="empty-state"><Inbox size={28}/><strong>{title}</strong>{action}</div>
}

export function ErrorNotice({ error }: { error: unknown }) {
  if (!error) return null
  const message = error instanceof Error ? error.message : String(error)
  return <div className="notice error"><AlertTriangle size={18}/><span>{message}</span></div>
}

export function SuccessNotice({ message }: { message?: string }) {
  return message ? <div className="notice success"><CheckCircle2 size={18}/><span>{message}</span></div> : null
}

const statusLabels: Record<string, string> = {
  active: '正常', pending: '待验证', disabled: '已停用', valid: '有效', invalid: '无效', insufficient: '权限不足', unverified: '待验证',
  new: '创建中', off: '已关机', archive: '已归档', queued: '排队中', running: '执行中', succeeded: '已完成', failed: '失败', partial: '部分完成',
  completed: '已完成', errored: '失败', in_progress: '进行中', stale: '需更新', revoked: '已撤销', none: '未托管',
}

export function StatusBadge({ value }: { value?: string }) {
  const normalized = value || 'unknown'
  return <span className={`status-badge status-${normalized.replaceAll('_', '-')}`}><span/>{statusLabels[normalized] || normalized}</span>
}

export function Modal({ title, children, onClose, wide = false }: { title: string; children: ReactNode; onClose: () => void; wide?: boolean }) {
  return <div className="modal-backdrop" role="presentation" onMouseDown={e => e.target === e.currentTarget && onClose()}>
    <section className={`modal ${wide ? 'modal-wide' : ''}`} role="dialog" aria-modal="true" aria-label={title}>
      <header><h2>{title}</h2><button className="icon-button" onClick={onClose} aria-label="关闭"><X size={19}/></button></header>
      <div className="modal-body">{children}</div>
    </section>
  </div>
}

export function ReauthFields({ password, onChange }: { password: string; onChange: (value: string) => void }) {
  return <label>登录密码<input type="password" value={password} onChange={e => onChange(e.target.value)} autoComplete="current-password" required/></label>
}

export async function reauthenticate(password: string) {
  await api('/api/v1/auth/reauth', json('POST', { password }))
}

export function Pager({ page, perPage, total, onChange }: { page: number; perPage: number; total: number; onChange: (page: number) => void }) {
  const pages = Math.max(1, Math.ceil(total / perPage))
  if (pages <= 1) return null
  return <div className="pager"><span>第 {page} / {pages} 页，共 {total} 条</span><div>
    <button className="icon-button bordered" onClick={() => onChange(page - 1)} disabled={page <= 1} aria-label="上一页"><ChevronLeft size={18}/></button>
    <button className="icon-button bordered" onClick={() => onChange(page + 1)} disabled={page >= pages} aria-label="下一页"><ChevronRight size={18}/></button>
  </div></div>
}

export function QuotaBar({ label, used, limit, unit = '' }: { label: string; used: number; limit: number | null; unit?: string }) {
  const percent = limit === null || limit === 0 ? 0 : Math.min(100, Math.round(used / limit * 100))
  return <div className="quota-row"><div><span>{label}</span><strong>{used.toLocaleString('zh-CN')}{unit} / {limit === null ? '不限' : `${limit.toLocaleString('zh-CN')}${unit}`}</strong></div><div className="progress-track"><span style={{ width: `${percent}%` }}/></div></div>
}

export function formatBytesMB(value: number) {
  return value >= 1024 ? `${(value / 1024).toFixed(value % 1024 === 0 ? 0 : 1)} GB` : `${value} MB`
}

export function formatUptime(seconds: number) {
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  return days ? `${days} 天 ${hours} 小时` : hours ? `${hours} 小时 ${minutes} 分钟` : `${minutes} 分钟`
}
