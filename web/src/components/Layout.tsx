import { useState } from 'react'
import { NavLink, Outlet, useNavigate } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { Activity, Cloud, Gauge, KeyRound, LayoutDashboard, ListChecks, LogOut, Menu, Server, Settings, ShieldCheck, Users, X } from 'lucide-react'
import { api, json } from '../api'
import type { User } from '../types'

const userLinks = [
  ['/', '概览', LayoutDashboard], ['/accounts', '云账号', Cloud], ['/droplets', '实例', Server], ['/jobs', '任务', ListChecks],
] as const
const adminLinks = [
  ['/admin/users', '用户管理', Users], ['/admin/settings', '系统设置', Settings], ['/admin/status', '系统状态', Activity], ['/admin/audit', '审计日志', ShieldCheck],
] as const

export default function Layout({ user, siteName }: { user: User; siteName: string }) {
  const [open, setOpen] = useState(false)
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const logout = async () => {
    await api('/api/v1/auth/logout', json('POST', {}))
    queryClient.clear()
    navigate('/login')
  }
  return <div className="app-shell">
    <aside className={`sidebar ${open ? 'is-open' : ''}`}>
      <div className="brand"><span className="brand-mark"><Gauge size={20}/></span><span>{siteName}</span></div>
      <button className="icon-button sidebar-close" onClick={() => setOpen(false)} aria-label="关闭导航"><X size={20}/></button>
      <nav className="nav-list">
        {userLinks.map(([to, label, Icon]) => <NavLink key={to} to={to} end={to === '/'} onClick={() => setOpen(false)}><Icon size={18}/><span>{label}</span></NavLink>)}
        {user.role === 'admin' && <>
          <div className="nav-label">管理员</div>
          {adminLinks.map(([to, label, Icon]) => <NavLink key={to} to={to} onClick={() => setOpen(false)}><Icon size={18}/><span>{label}</span></NavLink>)}
        </>}
      </nav>
      <div className="sidebar-user">
        <div><strong>{user.email}</strong><span>{user.role === 'admin' ? '管理员' : '用户'}</span></div>
        <button className="icon-button" onClick={logout} title="退出登录"><LogOut size={18}/></button>
      </div>
    </aside>
    {open && <button className="sidebar-backdrop" onClick={() => setOpen(false)} aria-label="关闭导航"/>}
    <main className="main-area">
      <header className="mobile-header"><button className="icon-button" onClick={() => setOpen(true)} aria-label="打开导航"><Menu size={20}/></button><span>{siteName}</span><KeyRound size={18}/></header>
      <Outlet />
    </main>
  </div>
}

