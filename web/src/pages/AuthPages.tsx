import { FormEvent, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { ArrowRight, KeyRound, LoaderCircle, Mail, ShieldCheck } from 'lucide-react'
import { ApiError, api, json } from '../api'
import type { PublicConfig } from '../types'

function AuthFrame({ title, subtitle, children }: { title: string; subtitle: string; children: React.ReactNode }) {
  const config = useQuery({ queryKey: ['public-config'], queryFn: () => api<PublicConfig>('/api/v1/public/config') })
  return <main className="auth-page"><section className="auth-panel">
    <div className="auth-brand"><span><ShieldCheck size={22}/></span>{config.data?.site?.name || '云服务器托管平台'}</div>
    <div><h1>{title}</h1><p>{subtitle}</p></div>{children}
  </section></main>
}

export function LoginPage() {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [canResend, setCanResend] = useState(false)
  const [resendMessage, setResendMessage] = useState('')
  const navigate = useNavigate()
  const config = useQuery({ queryKey: ['public-config'], queryFn: () => api<PublicConfig>('/api/v1/public/config') })
  const submit = async (event: FormEvent) => {
    event.preventDefault(); setLoading(true); setError(''); setCanResend(false); setResendMessage('')
    try { await api('/api/v1/auth/login', json('POST', { email, password })); navigate('/') }
    catch (err: any) { setError(err.message); setCanResend(err instanceof ApiError && err.code === 'email_unverified') } finally { setLoading(false) }
  }
  const resend = async () => { setLoading(true); setError(''); try { const result=await api<any>('/api/v1/auth/resend-verification',json('POST',{email,password}));setResendMessage(result.dev_token?`${result.message}，开发验证令牌：${result.dev_token}`:result.message) } catch(err:any){setError(err.message)} finally{setLoading(false)} }
  return <AuthFrame title="登录" subtitle="使用已验证的邮箱进入管理后台">
    <form className="stack-form" onSubmit={submit}>
      <label>邮箱<div className="input-with-icon"><Mail size={17}/><input type="email" required value={email} onChange={e => setEmail(e.target.value)} autoComplete="email"/></div></label>
      <label>密码<div className="input-with-icon"><KeyRound size={17}/><input type="password" required value={password} onChange={e => setPassword(e.target.value)} autoComplete="current-password"/></div></label>
      {error && <div className="form-error">{error}</div>}
      {resendMessage && <div className="form-notice">{resendMessage}</div>}
      {canResend && <button type="button" className="button secondary full" onClick={resend} disabled={loading}><Mail size={17}/>重新发送验证邮件</button>}
      <button className="button primary full" disabled={loading}>{loading ? <LoaderCircle className="spin" size={18}/> : <ArrowRight size={18}/>}登录</button>
    </form>
    <div className="auth-links">{config.data?.registration?.enabled!==false?<Link to="/register">注册账号</Link>:<span/>}<Link to="/forgot-password">忘记密码</Link></div>
  </AuthFrame>
}

export function RegisterPage() {
  const [form, setForm] = useState({ email: '', password: '' })
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')
  const submit = async (event: FormEvent) => {
    event.preventDefault(); setError('')
    try { const result = await api<any>('/api/v1/auth/register', json('POST', form)); setMessage(result.dev_token ? `${result.message}，开发验证令牌：${result.dev_token}` : result.message) }
    catch (err: any) { setError(err.message) }
  }
  return <AuthFrame title="注册账号" subtitle="验证邮箱后即可托管 DigitalOcean 账号">
    <form className="stack-form" onSubmit={submit}>
      <label>邮箱<input type="email" required value={form.email} onChange={e => setForm({...form, email:e.target.value})}/></label>
      <label>密码<input type="password" minLength={12} required value={form.password} onChange={e => setForm({...form, password:e.target.value})}/><small>至少 12 个字符</small></label>
      {message && <div className="form-success">{message}</div>}{error && <div className="form-error">{error}</div>}
      <button className="button primary full"><ArrowRight size={18}/>注册</button>
    </form><div className="auth-links"><Link to="/login">返回登录</Link></div>
  </AuthFrame>
}

export function VerifyEmailPage() {
  const [params] = useSearchParams(); const [token, setToken] = useState(params.get('token') || '')
  const [message, setMessage] = useState('')
  const verify = async () => { try { const result = await api<any>('/api/v1/auth/verify-email', json('POST', {token})); setMessage(result.message) } catch (err:any) { setMessage(err.message) } }
  return <AuthFrame title="验证邮箱" subtitle="输入邮件中的验证令牌完成激活"><div className="stack-form"><label>验证令牌<input value={token} onChange={e=>setToken(e.target.value)}/></label><button className="button primary full" onClick={verify}><ShieldCheck size={18}/>验证</button>{message && <div className="form-notice">{message}</div>}<Link to="/login">返回登录</Link></div></AuthFrame>
}

export function ForgotPasswordPage() {
  const [email,setEmail]=useState(''); const [message,setMessage]=useState('');const [error,setError]=useState('');const [loading,setLoading]=useState(false)
  const submit=async(e:FormEvent)=>{e.preventDefault();setLoading(true);setError('');try{const result=await api<any>('/api/v1/auth/forgot-password',json('POST',{email}));setMessage(result.dev_token?`${result.message}，开发令牌：${result.dev_token}`:result.message)}catch(err:any){setError(err.message)}finally{setLoading(false)}}
  return <AuthFrame title="找回密码" subtitle="重置链接将发送到注册邮箱"><form className="stack-form" onSubmit={submit}><label>邮箱<input type="email" value={email} onChange={e=>setEmail(e.target.value)} required/></label><button className="button primary full" disabled={loading}>{loading?<LoaderCircle className="spin" size={18}/>:<Mail size={18}/>}发送重置邮件</button>{message&&<div className="form-notice">{message}</div>}{error&&<div className="form-error">{error}</div>}<Link to="/login">返回登录</Link></form></AuthFrame>
}

export function ResetPasswordPage() {
  const [params]=useSearchParams(); const [token,setToken]=useState(params.get('token')||''); const [password,setPassword]=useState(''); const [message,setMessage]=useState('')
  const submit=async(e:FormEvent)=>{e.preventDefault();try{const result=await api<any>('/api/v1/auth/reset-password',json('POST',{token,password}));setMessage(result.message)}catch(err:any){setMessage(err.message)}}
  return <AuthFrame title="重置密码" subtitle="设置新的登录密码"><form className="stack-form" onSubmit={submit}><label>重置令牌<input value={token} onChange={e=>setToken(e.target.value)} required/></label><label>新密码<input type="password" minLength={12} value={password} onChange={e=>setPassword(e.target.value)} required/></label><button className="button primary full"><KeyRound size={18}/>更新密码</button>{message&&<div className="form-notice">{message}</div>}<Link to="/login">返回登录</Link></form></AuthFrame>
}
