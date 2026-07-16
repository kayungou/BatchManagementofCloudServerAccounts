import { FormEvent, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { ArrowLeft, ArrowRight, Cloud, Copy, KeyRound, Link2, Pencil, Plus, RefreshCw, ShieldCheck, Trash2, UserRoundCog } from 'lucide-react'
import { api, formatDate, formatMoney, json } from '../api'
import type { CloudAccount, ListResponse, MeResponse, SSHKey, User } from '../types'
import { EmptyState, ErrorNotice, Modal, Page, PageHeader, ReauthFields, Spinner, StatusBadge, SuccessNotice, reauthenticate } from '../components/UI'

export default function AccountsPage() {
  const queryClient = useQueryClient()
  const [showBind, setShowBind] = useState(false)
  const [form, setForm] = useState({ name: '', token: '', full_access_confirmed: false })
  const accounts = useQuery({ queryKey: ['accounts'], queryFn: () => api<ListResponse<CloudAccount>>('/api/v1/cloud-accounts/') })
  const create = useMutation({
    mutationFn: () => api('/api/v1/cloud-accounts/', json('POST', form)),
    onSuccess: () => { queryClient.invalidateQueries({queryKey:['accounts']}); queryClient.invalidateQueries({queryKey:['jobs']}); setShowBind(false); setForm({name:'',token:'',full_access_confirmed:false}) },
  })
  const submit = (event: FormEvent) => { event.preventDefault(); create.mutate() }
  return <Page>
    <PageHeader title="云账号" subtitle="托管 DigitalOcean Personal Access Token" actions={<button className="button primary" onClick={() => setShowBind(true)}><Plus size={18}/>绑定账号</button>}/>
    <ErrorNotice error={accounts.error}/>
    {accounts.isLoading ? <Spinner/> : !accounts.data?.items.length ? <EmptyState title="尚未绑定 DigitalOcean 账号" action={<button className="button primary" onClick={() => setShowBind(true)}><Link2 size={17}/>绑定账号</button>}/> : <section className="panel table-panel"><div className="table-wrap"><table>
      <thead><tr><th>账号</th><th>凭据</th><th>DigitalOcean 状态</th><th>余额 / 本月用量</th><th>实例上限</th><th>最后同步</th><th/></tr></thead>
      <tbody>{accounts.data.items.map(account => <tr key={account.id}>
        <td><div className="cell-primary"><span className="provider-mark">DO</span><span><strong>{account.name}</strong><small>{account.provider_email || account.provider_account_id || account.id.slice(0,8)}</small></span></div></td>
        <td><StatusBadge value={account.credential_status}/></td>
        <td><StatusBadge value={account.provider_status}/>{account.last_error && <small className="danger-text block">{account.last_error}</small>}</td>
        <td><strong>{formatMoney(account.account_balance, account.currency)}</strong><small className="block">用量 {formatMoney(account.month_to_date_usage, account.currency)}</small></td>
        <td>{account.account_limits?.droplet_limit ?? '未同步'}</td>
        <td>{formatDate(account.last_synced_at)}</td>
        <td className="align-right"><Link className="icon-button bordered" to={`/accounts/${account.id}`} aria-label="查看账号"><ArrowRight size={18}/></Link></td>
      </tr>)}</tbody>
    </table></div></section>}
    {showBind && <Modal title="绑定 DigitalOcean 账号" onClose={() => setShowBind(false)}><form className="stack-form" onSubmit={submit}>
      <label>显示名称<input required maxLength={100} value={form.name} onChange={e=>setForm({...form,name:e.target.value})} placeholder="例如：生产环境"/></label>
      <label>Personal Access Token<textarea required rows={4} value={form.token} onChange={e=>setForm({...form,token:e.target.value})} autoComplete="off"/></label>
      <label className="check-row"><input type="checkbox" checked={form.full_access_confirmed} onChange={e=>setForm({...form,full_access_confirmed:e.target.checked})}/><span>确认 Token 已授予 Full Access</span></label>
      <ErrorNotice error={create.error}/>
      <div className="modal-actions"><button type="button" className="button secondary" onClick={()=>setShowBind(false)}>取消</button><button className="button primary" disabled={create.isPending || !form.full_access_confirmed}><ShieldCheck size={17}/>验证并绑定</button></div>
    </form></Modal>}
  </Page>
}

type SensitiveMode = 'replace' | 'delete-account' | 'delete-key' | 'transfer' | null

export function AccountDetailPage() {
  const { accountId = '' } = useParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [message,setMessage]=useState('')
  const [actionError,setActionError]=useState<unknown>(null)
  const [sensitive,setSensitive]=useState<SensitiveMode>(null)
  const [password,setPassword]=useState('')
  const [token,setToken]=useState('')
  const [confirmed,setConfirmed]=useState(false)
  const [confirmName,setConfirmName]=useState('')
  const [deleteKey,setDeleteKey]=useState<SSHKey|null>(null)
  const [transferUser,setTransferUser]=useState('')
  const [keyModal,setKeyModal]=useState<SSHKey | 'new' | null>(null)
  const [keyForm,setKeyForm]=useState({name:'',public_key:''})
  const account = useQuery({ queryKey:['account',accountId], queryFn:()=>api<CloudAccount>(`/api/v1/cloud-accounts/${accountId}`), enabled:!!accountId })
  const me = useQuery({ queryKey:['me'], queryFn:()=>api<MeResponse>('/api/v1/me') })
  const sshKeys = useQuery({ queryKey:['account-catalog',accountId,'ssh-keys'], queryFn:()=>api<{ssh_keys:SSHKey[]}>(`/api/v1/cloud-accounts/${accountId}/catalog/ssh-keys?per_page=200`), enabled:!!accountId && account.data?.credential_status==='valid' })
  const users = useQuery({ queryKey:['admin-users','all'], queryFn:()=>api<ListResponse<User>>('/api/v1/admin/users?per_page=100'), enabled:sensitive==='transfer' })
  const sync = useMutation({ mutationFn:()=>api(`/api/v1/cloud-accounts/${accountId}/sync`,json('POST',{})), onSuccess:()=>{setMessage('同步任务已创建');queryClient.invalidateQueries({queryKey:['jobs']})} })

  const refresh = () => { queryClient.invalidateQueries({queryKey:['account',accountId]}); queryClient.invalidateQueries({queryKey:['accounts']}); queryClient.invalidateQueries({queryKey:['account-catalog',accountId]}) }
  const closeSensitive=()=>{setSensitive(null);setPassword('');setToken('');setConfirmed(false);setConfirmName('');setDeleteKey(null);setTransferUser('');setActionError(null)}
  const runSensitive=async(event:FormEvent)=>{
    event.preventDefault(); setActionError(null); setMessage('')
    try {
      await reauthenticate(password)
      if(sensitive==='replace') await api(`/api/v1/cloud-accounts/${accountId}/token`,json('PUT',{token,full_access_confirmed:confirmed}))
      if(sensitive==='delete-account') {
        await api(`/api/v1/cloud-accounts/${accountId}`,json('DELETE',{confirm_name:confirmName}))
        queryClient.removeQueries({queryKey:['account',accountId]})
        await Promise.all([queryClient.invalidateQueries({queryKey:['accounts']}),queryClient.invalidateQueries({queryKey:['droplets']}),queryClient.invalidateQueries({queryKey:['usage-summary']})])
        navigate('/accounts'); return
      }
      if(sensitive==='delete-key'&&deleteKey) await api(`/api/v1/cloud-accounts/${accountId}/ssh-keys/${deleteKey.id}`,json('DELETE',{}))
      if(sensitive==='transfer') await api(`/api/v1/admin/cloud-accounts/${accountId}/transfer`,json('POST',{user_id:transferUser}))
      setMessage(sensitive==='replace'?'Token 已更新并等待同步':sensitive==='transfer'?'账号归属已转移':'SSH 公钥已删除')
      closeSensitive(); refresh()
    } catch(error){setActionError(error)}
  }
  const openKey=(key:SSHKey|'new')=>{setKeyModal(key);setKeyForm(key==='new'?{name:'',public_key:''}:{name:key.name,public_key:key.public_key})}
  const saveKey=async(event:FormEvent)=>{
    event.preventDefault();setActionError(null)
    try{
      if(keyModal==='new') await api(`/api/v1/cloud-accounts/${accountId}/ssh-keys`,json('POST',keyForm))
      else if(keyModal) await api(`/api/v1/cloud-accounts/${accountId}/ssh-keys/${keyModal.id}`,json('PUT',{name:keyForm.name}))
      setKeyModal(null);setMessage(keyModal==='new'?'SSH 公钥已添加':'SSH 公钥名称已更新');refresh()
    }catch(error){setActionError(error)}
  }
  if(account.isLoading) return <Page><Spinner/></Page>
  if(account.error||!account.data) return <Page><PageHeader title="云账号"/><ErrorNotice error={account.error||new Error('账号不存在')}/></Page>
  const item=account.data
  return <Page>
    <div className="breadcrumb"><Link to="/accounts"><ArrowLeft size={16}/>云账号</Link></div>
    <PageHeader title={item.name} subtitle={item.provider_email || item.provider_account_id || 'DigitalOcean'} actions={<>
      <button className="button secondary" onClick={()=>sync.mutate()} disabled={sync.isPending}><RefreshCw className={sync.isPending?'spin':''} size={17}/>同步</button>
      <button className="button secondary" onClick={()=>setSensitive('replace')}><KeyRound size={17}/>替换 Token</button>
    </>}/>
    <SuccessNotice message={message}/><ErrorNotice error={actionError||sync.error}/>
    <section className="detail-grid account-detail-grid">
      <div className="panel"><div className="section-heading"><h2>账号状态</h2><StatusBadge value={item.credential_status}/></div><dl className="detail-list">
        <div><dt>DigitalOcean 状态</dt><dd><StatusBadge value={item.provider_status}/></dd></div><div><dt>Team ID</dt><dd className="mono">{item.provider_account_id||'未同步'}</dd></div><div><dt>账号邮箱</dt><dd>{item.provider_email||'未同步'}</dd></div><div><dt>API 剩余额度</dt><dd>{item.rate_limit_remaining??'未同步'}</dd></div><div><dt>最后同步</dt><dd>{formatDate(item.last_synced_at)}</dd></div>
      </dl>{item.last_error&&<div className="notice error compact">{item.last_error}</div>}</div>
      <div className="panel"><div className="section-heading"><h2>账单与配额</h2></div><dl className="detail-list">
        <div><dt>余额</dt><dd>{formatMoney(item.account_balance,item.currency)}</dd></div><div><dt>本月用量</dt><dd>{formatMoney(item.month_to_date_usage,item.currency)}</dd></div><div><dt>本月至今余额</dt><dd>{formatMoney(item.month_to_date_balance,item.currency)}</dd></div><div><dt>Droplet 上限</dt><dd>{item.account_limits?.droplet_limit??'未同步'}</dd></div><div><dt>Reserved IP 上限</dt><dd>{item.account_limits?.floating_ip_limit??'未同步'}</dd></div>
      </dl></div>
    </section>

    <section className="panel">
      <div className="section-heading"><div><h2>SSH 公钥</h2><span>DigitalOcean 账号中的公钥</span></div><button className="button secondary small" onClick={()=>openKey('new')}><Plus size={16}/>添加公钥</button></div>
      {sshKeys.isLoading?<Spinner label="正在读取公钥"/>:<ErrorNotice error={sshKeys.error}/>} 
      {sshKeys.data && (sshKeys.data.ssh_keys?.length ? <div className="table-wrap"><table><thead><tr><th>名称</th><th>指纹</th><th>公钥</th><th/></tr></thead><tbody>{sshKeys.data.ssh_keys.map(key=><tr key={key.id}><td><strong>{key.name}</strong></td><td className="mono">{key.fingerprint}</td><td><button className="copy-value" onClick={()=>navigator.clipboard.writeText(key.public_key)} title="复制公钥"><span className="mono">{key.public_key.slice(0,38)}...</span><Copy size={15}/></button></td><td><div className="row-actions"><button className="icon-button bordered" onClick={()=>openKey(key)} title="重命名"><Pencil size={16}/></button><button className="icon-button bordered danger" onClick={()=>{setDeleteKey(key);setSensitive('delete-key')}} title="删除"><Trash2 size={16}/></button></div></td></tr>)}</tbody></table></div>:<EmptyState title="该账号没有 SSH 公钥"/>)}
    </section>

    <section className="danger-zone"><div><h2>账号管理</h2><p>高风险操作需要重新验证登录密码</p></div><div>{me.data?.user.role==='admin' && <button className="button secondary" onClick={()=>setSensitive('transfer')}><UserRoundCog size={17}/>转移归属</button>}<button className="button danger" onClick={()=>setSensitive('delete-account')}><Trash2 size={17}/>解绑账号</button></div></section>

    {keyModal&&<Modal title={keyModal==='new'?'添加 SSH 公钥':'重命名 SSH 公钥'} onClose={()=>setKeyModal(null)}><form className="stack-form" onSubmit={saveKey}><label>名称<input value={keyForm.name} onChange={e=>setKeyForm({...keyForm,name:e.target.value})} required/></label>{keyModal==='new'&&<label>SSH 公钥<textarea rows={5} value={keyForm.public_key} onChange={e=>setKeyForm({...keyForm,public_key:e.target.value})} required placeholder="ssh-ed25519 ..."/></label>}<ErrorNotice error={actionError}/><div className="modal-actions"><button type="button" className="button secondary" onClick={()=>setKeyModal(null)}>取消</button><button className="button primary">保存</button></div></form></Modal>}
    {sensitive&&<Modal title={sensitive==='replace'?'替换 Token':sensitive==='delete-account'?'解绑云账号':sensitive==='delete-key'?'删除 SSH 公钥':'转移账号归属'} onClose={closeSensitive}><form className="stack-form" onSubmit={runSensitive}>
      {sensitive==='replace'&&<><label>新 Personal Access Token<textarea rows={4} value={token} onChange={e=>setToken(e.target.value)} required/></label><label className="check-row"><input type="checkbox" checked={confirmed} onChange={e=>setConfirmed(e.target.checked)}/><span>确认新 Token 已授予 Full Access</span></label></>}
      {sensitive==='delete-account'&&<><div className="notice warning compact"><Trash2 size={17}/><span>DigitalOcean 远程资源不会被删除；本地同步实例记录和托管 root 密码将一并移除。</span></div><label>输入账号名称 <strong>{item.name}</strong><input value={confirmName} onChange={e=>setConfirmName(e.target.value)} required/></label></>}
      {sensitive==='delete-key'&&<div className="confirm-summary">将删除 SSH 公钥 <strong>{deleteKey?.name}</strong></div>}
      {sensitive==='transfer'&&<><label>目标用户<select value={transferUser} onChange={e=>setTransferUser(e.target.value)} required><option value="">请选择</option>{users.data?.items.filter(user=>user.status==='active'&&user.id!==item.user_id).map(user=><option value={user.id} key={user.id}>{user.email}（{user.role==='admin'?'管理员':'用户'}）</option>)}</select></label><ErrorNotice error={users.error}/></>}
      <ReauthFields password={password} onChange={setPassword}/><ErrorNotice error={actionError}/><div className="modal-actions"><button type="button" className="button secondary" onClick={closeSensitive}>取消</button><button className={`button ${sensitive==='delete-account'||sensitive==='delete-key'?'danger':'primary'}`} disabled={(sensitive==='replace'&&!confirmed)||(sensitive==='transfer'&&!transferUser)}>确认</button></div>
    </form></Modal>}
  </Page>
}
