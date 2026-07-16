import { Navigate, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'
import { ApiError, api, setDisplayTimeZone } from './api'
import type { MeResponse, PublicConfig } from './types'
import Layout from './components/Layout'
import { Spinner } from './components/UI'
import { ForgotPasswordPage, LoginPage, RegisterPage, ResetPasswordPage, VerifyEmailPage } from './pages/AuthPages'
import DashboardPage from './pages/DashboardPage'
import AccountsPage, { AccountDetailPage } from './pages/AccountsPage'
import DropletsPage, { CreateDropletPage, DropletDetailPage } from './pages/DropletsPage'
import JobsPage from './pages/JobsPage'
import { AdminAuditPage, AdminSettingsPage, AdminStatusPage, AdminUsersPage } from './pages/AdminPages'

function ProtectedShell({ siteName }: { siteName: string }) {
  const location = useLocation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const me = useQuery({ queryKey: ['me'], queryFn: () => api<MeResponse>('/api/v1/me'), retry: false })
  useEffect(() => {
    const expired = () => { queryClient.clear(); navigate('/login', { replace: true }) }
    window.addEventListener('cloud-manager-auth-expired', expired)
    return () => window.removeEventListener('cloud-manager-auth-expired', expired)
  }, [navigate, queryClient])
  if (me.isLoading) return <main className="full-loading"><Spinner label="正在读取登录状态"/></main>
  if (me.error instanceof ApiError && me.error.status === 401) return <Navigate to="/login" replace state={{ from: location.pathname }}/>
  if (me.error || !me.data) return <main className="full-loading"><div className="notice error">{me.error instanceof Error ? me.error.message : '无法读取登录状态'}</div></main>
  return <Layout user={me.data.user} siteName={siteName}/>
}

function AdminOnly({ children }: { children: React.ReactNode }) {
  const me = useQuery({ queryKey: ['me'], queryFn: () => api<MeResponse>('/api/v1/me'), retry: false })
  return me.data?.user.role === 'admin' ? children : <Navigate to="/" replace/>
}

export default function App() {
  const publicConfig = useQuery({ queryKey: ['public-config'], queryFn: () => api<PublicConfig>('/api/v1/public/config') })
  const siteName = publicConfig.data?.site?.name || '云服务器托管平台'
  const registrationEnabled = publicConfig.data?.registration?.enabled !== false
  setDisplayTimeZone(publicConfig.data?.site?.timezone)
  useEffect(() => { document.title = siteName }, [siteName])
  return <Routes>
    <Route path="/login" element={<LoginPage/>}/>
    <Route path="/register" element={registrationEnabled ? <RegisterPage/> : <Navigate to="/login" replace/>}/>
    <Route path="/verify-email" element={<VerifyEmailPage/>}/>
    <Route path="/forgot-password" element={<ForgotPasswordPage/>}/>
    <Route path="/reset-password" element={<ResetPasswordPage/>}/>
    <Route element={<ProtectedShell siteName={siteName}/> }>
      <Route index element={<DashboardPage/>}/>
      <Route path="accounts" element={<AccountsPage/>}/>
      <Route path="accounts/:accountId" element={<AccountDetailPage/>}/>
      <Route path="droplets" element={<DropletsPage/>}/>
      <Route path="droplets/new" element={<CreateDropletPage/>}/>
      <Route path="droplets/:dropletId" element={<DropletDetailPage/>}/>
      <Route path="jobs" element={<JobsPage/>}/>
      <Route path="admin/users" element={<AdminOnly><AdminUsersPage/></AdminOnly>}/>
      <Route path="admin/settings" element={<AdminOnly><AdminSettingsPage/></AdminOnly>}/>
      <Route path="admin/status" element={<AdminOnly><AdminStatusPage/></AdminOnly>}/>
      <Route path="admin/audit" element={<AdminOnly><AdminAuditPage/></AdminOnly>}/>
    </Route>
    <Route path="*" element={<Navigate to="/" replace/>}/>
  </Routes>
}
