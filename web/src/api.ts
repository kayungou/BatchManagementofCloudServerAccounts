export class ApiError extends Error {
  status: number
  code: string
  details: unknown

  constructor(status: number, code: string, message: string, details?: unknown) {
    super(message)
    this.status = status
    this.code = code
    this.details = details
  }
}

function cookie(name: string) {
  const prefix = `${name}=`
  const item = document.cookie.split('; ').find((value) => value.startsWith(prefix))
  return item ? decodeURIComponent(item.slice(prefix.length)) : ''
}

export async function api<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers = new Headers(options.headers)
  if (options.body && !(options.body instanceof FormData)) headers.set('Content-Type', 'application/json')
  const method = (options.method || 'GET').toUpperCase()
  if (!['GET', 'HEAD', 'OPTIONS'].includes(method)) headers.set('X-CSRF-Token', cookie('csrf_token'))
  const response = await fetch(path, { ...options, headers, credentials: 'same-origin' })
  const text = await response.text()
  let payload: any = null
  if (text) {
    try { payload = JSON.parse(text) } catch { payload = text }
  }
  if (!response.ok) {
    const error = payload?.error
    if (response.status === 401 && ['authentication_required', 'session_invalid'].includes(error?.code)) {
      window.dispatchEvent(new Event('cloud-manager-auth-expired'))
    }
    throw new ApiError(response.status, error?.code || 'request_failed', error?.message || '请求失败', error?.details)
  }
  return payload as T
}

export function json(method: string, body?: unknown): RequestInit {
  return { method, body: body === undefined ? undefined : JSON.stringify(body) }
}

let displayTimeZone = 'Asia/Shanghai'

export function setDisplayTimeZone(value?: string) {
  if (!value) return
  try {
    new Intl.DateTimeFormat('zh-CN', { timeZone: value }).format()
    displayTimeZone = value
  } catch { /* Keep the last valid timezone. */ }
}

export function formatDate(value?: string) {
  return value ? new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short', timeZone: displayTimeZone }).format(new Date(value)) : '未记录'
}

export function formatMoney(value?: number, currency = 'USD') {
  if (value === undefined || value === null) return '无权限或未同步'
  return new Intl.NumberFormat('zh-CN', { style: 'currency', currency }).format(value)
}
