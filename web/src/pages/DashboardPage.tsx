import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { AlertTriangle, ArrowRight, Cloud, Cpu, HardDrive, ListChecks, Plus, Server, Wallet } from 'lucide-react'
import { api, formatMoney } from '../api'
import type { CloudAccount, Job, ListResponse, MeResponse, UsageSummary } from '../types'
import { EmptyState, ErrorNotice, Page, PageHeader, QuotaBar, Spinner, StatusBadge, formatBytesMB } from '../components/UI'

export default function DashboardPage() {
  const me = useQuery({ queryKey: ['me'], queryFn: () => api<MeResponse>('/api/v1/me') })
  const accounts = useQuery({ queryKey: ['accounts'], queryFn: () => api<ListResponse<CloudAccount>>('/api/v1/cloud-accounts/') })
  const scope = me.data?.user.role === 'admin' ? 'all' : 'self'
  const usage = useQuery({ queryKey: ['usage-summary', scope], queryFn: () => api<UsageSummary>(`/api/v1/usage-summary?scope=${scope}`), enabled: !!me.data })
  const jobs = useQuery({ queryKey: ['jobs', 1, 8], queryFn: () => api<ListResponse<Job>>('/api/v1/jobs?page=1&per_page=8'), refetchInterval: query => query.state.data?.items.some(job => ['queued', 'running'].includes(job.state)) ? 3000 : false })
  if (me.isLoading || accounts.isLoading || usage.isLoading || jobs.isLoading) return <Page><Spinner label="正在汇总资源"/></Page>
  const error = me.error || accounts.error || usage.error || jobs.error
  const accountItems = accounts.data?.items || []
  const jobItems = jobs.data?.items || []
  const totalVCPUs = usage.data?.vcpus || 0
  const totalMemory = usage.data?.memory_mb || 0
  const dropletCount = usage.data?.droplets || 0
  const online = usage.data?.active_droplets || 0
  const balance = usage.data?.account_balance || 0
  const unhealthy = accountItems.filter(item => item.credential_status !== 'valid')
  const user = me.data?.user

  return <Page>
    <PageHeader title="概览" subtitle="DigitalOcean 账号与实例运行情况" actions={<Link className="button primary" to="/droplets/new"><Plus size={18}/>创建实例</Link>}/>
    <ErrorNotice error={error}/>
    {unhealthy.length > 0 && <div className="notice warning"><AlertTriangle size={18}/><span>{unhealthy.length} 个云账号凭据需要处理</span><Link to="/accounts">查看账号</Link></div>}
    <section className="metric-grid">
      <div className="metric"><span className="metric-icon"><Cloud size={20}/></span><div><span>托管账号</span><strong>{usage.data?.cloud_accounts || 0}</strong></div></div>
      <div className="metric"><span className="metric-icon"><Server size={20}/></span><div><span>实例</span><strong>{dropletCount}</strong><small>{online} 台运行中</small></div></div>
      <div className="metric"><span className="metric-icon"><Cpu size={20}/></span><div><span>总计算资源</span><strong>{totalVCPUs} vCPU</strong><small>{formatBytesMB(totalMemory)} 内存</small></div></div>
      <div className="metric"><span className="metric-icon"><Wallet size={20}/></span><div><span>账号余额</span><strong>{accountItems.length ? formatMoney(balance) : '--'}</strong><small>已同步账号合计</small></div></div>
    </section>

    <div className="dashboard-grid">
      <section className="panel">
        <div className="section-heading"><div><h2>{scope==='all'?'全局资源':'本地配额'}</h2><span>{scope==='all'?'所有用户的已同步实例':'当前用户全部托管实例'}</span></div></div>
        {scope==='all'?<dl className="detail-list"><div><dt>实例</dt><dd>{dropletCount}</dd></div><div><dt>vCPU</dt><dd>{totalVCPUs}</dd></div><div><dt>内存</dt><dd>{formatBytesMB(totalMemory)}</dd></div><div><dt>预计月费</dt><dd>{formatMoney(usage.data?.monthly_cost)}</dd></div></dl>:<div className="quota-list">
          <QuotaBar label="实例" used={dropletCount} limit={user?.quota_droplets ?? null}/>
          <QuotaBar label="vCPU" used={totalVCPUs} limit={user?.quota_vcpus ?? null}/>
          <QuotaBar label="内存" used={totalMemory} limit={user?.quota_memory_mb ?? null} unit=" MB"/>
        </div>}
      </section>
      <section className="panel">
        <div className="section-heading"><div><h2>账号状态</h2><span>{accountItems.length} 个 DigitalOcean Team</span></div><Link className="text-link" to="/accounts">全部账号<ArrowRight size={15}/></Link></div>
        {accountItems.length === 0 ? <EmptyState title="尚未绑定云账号" action={<Link className="button secondary small" to="/accounts"><Plus size={16}/>绑定账号</Link>}/> : <div className="compact-list">
          {accountItems.slice(0, 5).map(account => <Link to={`/accounts/${account.id}`} key={account.id} className="compact-row"><span className="provider-mark">DO</span><div><strong>{account.name}</strong><small>{account.provider_email || '等待同步'}</small></div><StatusBadge value={account.credential_status}/></Link>)}
        </div>}
      </section>
    </div>

    <section className="panel">
      <div className="section-heading"><div><h2>最近任务</h2><span>创建、同步和实例操作</span></div><Link className="text-link" to="/jobs">任务中心<ArrowRight size={15}/></Link></div>
      {jobItems.length === 0 ? <EmptyState title="暂无任务记录"/> : <div className="table-wrap"><table><thead><tr><th>任务</th><th>状态</th><th>进度</th><th>更新时间</th></tr></thead><tbody>
        {jobItems.map(job => <tr key={job.id}><td><div className="cell-primary"><ListChecks size={17}/><span><strong>{jobKindLabel(job.kind)}</strong><small>{job.id.slice(0, 8)}</small></span></div></td><td><StatusBadge value={job.state}/></td><td><div className="inline-progress"><span style={{width:`${job.progress}%`}}/></div><small>{job.progress}%</small></td><td>{new Intl.DateTimeFormat('zh-CN',{dateStyle:'short',timeStyle:'short'}).format(new Date(job.updated_at))}</td></tr>)}
      </tbody></table></div>}
    </section>
  </Page>
}

export function jobKindLabel(kind: string) {
  const labels: Record<string, string> = { sync_account: '同步账号', create_droplets: '创建实例', droplet_action: '实例操作', delete_droplets: '销毁实例' }
  return labels[kind] || kind
}
