import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ChevronDown, ChevronRight, Clock3, ListChecks, RefreshCw } from 'lucide-react'
import { api, formatDate } from '../api'
import type { Job, ListResponse } from '../types'
import { EmptyState, ErrorNotice, Page, PageHeader, Pager, Spinner, StatusBadge } from '../components/UI'
import { jobKindLabel } from './DashboardPage'

export default function JobsPage(){
  const [page,setPage]=useState(1);const [expanded,setExpanded]=useState<string|null>(null);const [stateFilter,setStateFilter]=useState('')
  const stateQuery=stateFilter?`&state=${stateFilter}`:''
  const jobs=useQuery({queryKey:['jobs',page,20,stateFilter],queryFn:()=>api<ListResponse<Job>>(`/api/v1/jobs?page=${page}&per_page=20${stateQuery}`),refetchInterval:query=>query.state.data?.items.some(job=>['queued','running'].includes(job.state))?2500:false})
  const items=jobs.data?.items||[]
  return <Page><PageHeader title="任务中心" subtitle="异步操作进度与 DigitalOcean Action 结果" actions={<button className="button secondary" onClick={()=>jobs.refetch()}><RefreshCw className={jobs.isFetching?'spin':''} size={17}/>刷新</button>}/>
    <div className="filter-bar"><select value={stateFilter} onChange={e=>{setStateFilter(e.target.value);setPage(1)}}><option value="">全部状态</option><option value="queued">排队中</option><option value="running">执行中</option><option value="succeeded">已完成</option><option value="partial">部分完成</option><option value="failed">失败</option></select></div><ErrorNotice error={jobs.error}/>
    {jobs.isLoading?<Spinner/>:!items.length?<EmptyState title="暂无任务记录"/>:<section className="panel table-panel"><div className="table-wrap"><table><thead><tr><th/><th>任务</th><th>状态</th><th>进度</th><th>尝试</th><th>DigitalOcean Action</th><th>创建时间</th><th>完成时间</th></tr></thead><tbody>{items.map(job=><JobRow key={job.id} job={job} open={expanded===job.id} onToggle={()=>setExpanded(expanded===job.id?null:job.id)}/>)}</tbody></table></div><Pager page={page} perPage={20} total={jobs.data?.total||0} onChange={setPage}/></section>}
  </Page>
}

function JobRow({job,open,onToggle}:{job:Job;open:boolean;onToggle:()=>void}){
  return <><tr className={open?'expanded-row':''}><td className="check-cell"><button className="icon-button" onClick={onToggle} aria-label={open?'收起详情':'展开详情'}>{open?<ChevronDown size={17}/>:<ChevronRight size={17}/>}</button></td><td><div className="cell-primary"><span className="resource-icon"><ListChecks size={17}/></span><span><strong>{jobKindLabel(job.kind)}</strong><small className="mono">{job.id.slice(0,8)}</small></span></div></td><td><StatusBadge value={job.state}/>{job.error_message&&<small className="danger-text block">{job.error_message}</small>}</td><td><div className="progress-cell"><div className="inline-progress"><span style={{width:`${job.progress}%`}}/></div><strong>{job.progress}%</strong></div></td><td>{job.attempts}</td><td>{job.provider_action_ids?.length?<span className="mono">{job.provider_action_ids.join(', ')}</span>:'--'}</td><td>{formatDate(job.created_at)}</td><td>{formatDate(job.finished_at)}</td></tr>{open&&<tr className="job-detail-row"><td colSpan={8}><div className="job-detail-grid"><div><h3>请求参数</h3><pre>{JSON.stringify(job.payload||{},null,2)}</pre></div><div><h3>执行结果</h3><pre>{JSON.stringify(job.result||{},null,2)}</pre></div></div><div className="job-timeline"><Clock3 size={16}/><span>计划 {formatDate(job.scheduled_at)}</span><span>开始 {formatDate(job.started_at)}</span><span>更新 {formatDate(job.updated_at)}</span></div></td></tr>}</>
}
