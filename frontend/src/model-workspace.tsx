import React,{useEffect,useRef,useState} from 'react';
import {RefreshCw,Search,ChevronRight,SlidersHorizontal,X} from 'lucide-react';
import {request} from './api';
import {useViewState} from './view-state';
import {hubSourceSelection,hubChannels,hubView,isHubResponse,hubConfiguredDisabled,hubFetchFailures,hubIssue,hubParameterDifference,type HubRow} from './model-hub';
import {ProvenanceBadges,CapabilityBadges,CapabilityEvidence} from './model-badges';
import {ModelMetadata} from './secondary-pages';
type T=(key:string)=>string;
type Props={connected:boolean,busy:boolean,config:HubRow|null,t:T,date:(x:any)=>string,fmt:(x:any)=>string,onError:(e:unknown)=>void,onToggle:(m:HubRow,after:()=>Promise<void>)=>void,refreshRef:React.MutableRefObject<(()=>Promise<void>)|null>};
const tok=(n:any)=>typeof n==='number'&&n>0?(n>=1024?Math.round(n/1024)+'K':String(n)):'—';
function HubDialog({row,t,date,fmt,onClose,config,busy,onToggle,after}:{row:HubRow,t:T,date:(x:any)=>string,fmt:(x:any)=>string,onClose:()=>void,config:HubRow|null,busy:boolean,onToggle:Props['onToggle'],after:()=>Promise<void>}){
 const ref=useRef<HTMLDialogElement>(null);useEffect(()=>{ref.current?.showModal();return()=>ref.current?.close()},[]);
 const disabled=hubConfiguredDisabled(row.id,config,!!row.disabled);
 return <dialog ref={ref} className="modal wide hub-dialog" onCancel={e=>{e.preventDefault();onClose()}} aria-label={row.name||row.id}><div className="modal-head"><h2>{row.name||row.id}</h2><button className="button icon ghost" aria-label={t('close')} onClick={onClose}><X size={18}/></button></div><div className="modal-body"><p className="mono">{row.id}</p><p className="muted">{t('hubEvidenceHint')}</p>{hubParameterDifference(row.variants)&&<p className="hub-detail-difference">{t('hubBasisHint')}</p>}<div className="hub-variants">{row.variants.map((v:HubRow,i:number)=><section className="hub-variant" key={i}><ProvenanceBadges variants={[v]} t={t}/><p>{v.account_name||v.auth_index||v.config_key||'—'}</p><p className="muted">{v.model.name||v.model.id}</p><CapabilityEvidence model={v.model} t={t}/><ModelMetadata model={v.model} t={t} capabilitiesShown/><dl className="runtime"><dt>{t('context')}</dt><dd>{v.model.context_length>0?fmt(v.model.context_length)+' tokens':'—'}</dd><dt>{t('output')}</dt><dd>{v.model.max_completion_tokens>0?fmt(v.model.max_completion_tokens)+' tokens':'—'}</dd><dt>{t('series')}</dt><dd>{v.model.family||v.model.vendor||t('otherFamily')}{v.model.family_derived?' · '+t('idDerived'):''}</dd><dt>{t('source')}</dt><dd>{v.model.sources?.join(' + ')||v.config_key||'—'}</dd><dt>{t('status')}</dt><dd>{t(v.model.disabled?'upstreamDisabled':v.origin==='dynamic'?'directoryOnly':'hubConfigured')}</dd></dl>{v.model.description&&<p>{v.model.description}</p>}{v.model.tags?.length>0&&<p>{t('tags')}: {v.model.tags.join(' · ')}</p>}{v.model.reasoning_summary&&<p>{t('reasoningSummary')}: {v.model.reasoning_summary}</p>}{v.model.image_input_conflict&&<p className="notice warn">{t('imageConflict')}</p>}{Object.entries(v.model.image_input_sources||{}).map(([key,value])=><p key={key}>{key} · {t(value===true?'imageDeclared':value===false?'textDeclared':'unverified')}</p>)}</section>)}</div><div className="hub-config-control"><div><strong>{t('hubConfigSwitch')}</strong><p className="muted">{t('hubConfigHint')}</p><small>{t(disabled?'disabled':'enabled')}</small></div><button className="button" disabled={busy||!config} onClick={()=>{onClose();onToggle({...row,disabled},after)}}>{t(disabled?'enableModel':'disableModel')}</button></div></div></dialog>
}

function SourcesDialog({sources,data,loading,error,busy,selection,setSelection,t,date,onRefresh,onClose}:{sources:HubRow[],data:HubRow|null,loading:boolean,error:string,busy:boolean,selection:Record<string,string>,setSelection:(ch:string,id:string)=>void,t:T,date:(x:any)=>string,onRefresh:()=>void,onClose:()=>void}){
 const ref=useRef<HTMLDialogElement>(null);
 useEffect(()=>{ref.current?.showModal();return()=>ref.current?.close()},[]);
 return <dialog ref={ref} className="modal wide hub-source-dialog" onCancel={e=>{e.preventDefault();onClose()}} aria-label={t('hubSources')}>
  <div className="modal-head"><h2>{t('hubSources')}</h2><button className="button icon ghost" aria-label={t('close')} onClick={onClose}><X size={18}/></button></div>
  <div className="modal-body"><p className="hub-source-intro">{t('hubSourceShort')}</p>
   <div className="hub-source-grid">{sources.map(s=><section key={s.channel} className={'hub-source-entry '+s.status}>
    <div className="hub-source-title"><label htmlFor={'hub-source-'+s.channel}>{t('hubChannel_'+s.channel)}</label><span className={'hub-source-state '+s.status}>{loading?t('loading'):error?t('catalogUnavailable'):t('hubStatus_'+s.status)}</span></div>
    {s.status==='unsupported'?<p className="hub-unavailable-note">{t('hubNotSupported')}</p>:<>
     <select id={'hub-source-'+s.channel} value={selection[s.channel]||''} disabled={loading||busy} onChange={e=>setSelection(s.channel,e.target.value)}>
      <option value="">{t('hubAuto')}</option>{s.accounts.map((a:HubRow)=><option key={a.auth_index} value={a.auth_index} disabled={!a.available}>{a.name}{a.reason?' · '+t('hubReason_'+a.reason):''}</option>)}
      {selection[s.channel]&&!s.accounts.some((a:HubRow)=>a.auth_index===selection[s.channel])&&<option value={selection[s.channel]}>{t('hubMissingAccount')}</option>}
     </select>
     <div className="hub-source-byline"><strong>{s.name||t('hubStatus_no_account')}</strong><span>{t('hubBasis_'+s.basis)}</span></div>
     {data&&<p className="hub-source-time">{['ok','partial'].includes(s.status)?s.count+' '+t('hubEntries'):t('hubIssue_'+hubIssue(s))}{s.fetched_at?' · '+date(s.fetched_at):''}{s.cached?' · '+t('cachedDirectory'):''}</p>}
    </>}
    {data&&!!s.endpoints?.length&&<details className="hub-technical"><summary>{t('hubTechnical')}</summary>{s.endpoints.map((e:HubRow)=><div className="hub-endpoint" key={e.source}><strong>{e.source}: {e.status==='ok'?e.count:e.error}</strong>{(e.attempts?.length?e.attempts:[e]).map((a:HubRow,i:number)=><small key={i}><code>{a.path}</code>{a.http_status?' · HTTP '+a.http_status:''}{a.upstream_code!==undefined?' · code '+a.upstream_code:''}{a.error?' · '+a.error:''}</small>)}</div>)}{s.status==='failed'&&<p className="hub-recovery">{t('hubRecovery')}</p>}</details>}
   </section>)}</div>
   {!!data?.account_errors?.length&&<p className="error-text">{t('hubAccountErrors')}: {data.account_errors.length}{data.account_errors.map((a:HubRow,i:number)=><small className="hub-account-error" key={i}>{a.name||a.auth_index}: {t('hubCredentialError')}</small>)}</p>}
   {error&&<p className="error-text" role="alert">{t(error)}</p>}
  </div>
  <div className="modal-footer"><button className="button hub-source-refresh" disabled={loading||busy} onClick={onRefresh}><RefreshCw size={14} className={loading?'spin':''}/>{t('refetchDirectory')}</button><button className="button primary" onClick={onClose}>{t('close')}</button></div>
 </dialog>
}

export function ModelWorkspace({connected,busy,config,t,date,fmt,onError,onToggle,refreshRef}:Props){
 const [selection,setSelection]=useViewState<Record<string,string>>('hubSources',{}),[query,setQuery]=useViewState('hubQuery',''),[origin,setOrigin]=useViewState('hubOrigin','all'),[channel,setChannel]=useViewState('hubChannel','all'),[sort,setSort]=useViewState('hubSort','sourceOrder');
 const [sourcesOpen,setSourcesOpen]=useState(false),[data,setData]=useState<HubRow|null>(null),[loading,setLoading]=useState(false),[error,setError]=useState(''),[detail,setDetail]=useState<HubRow|null>(null),[sourceSnapshot,setSourceSnapshot]=useState<HubRow[]>([]);
 const sequence=useRef(0),alive=useRef(true),selectionKey=JSON.stringify(hubSourceSelection(selection));
 useEffect(()=>{if(channel==='global')setChannel('intl');if(selection.global)setSelection(hubSourceSelection(selection))},[]);
 async function load(force=false){
  const seq=++sequence.current;setLoading(true);setError('');setData(null);setDetail(null);
  try{const params=new URLSearchParams(Object.entries(hubSourceSelection(selection)).filter(([ch,id])=>hubChannels.includes(ch)&&!!id));const result=await request(force?'/models/hub/refresh':'/models/hub?'+params.toString(),force?'POST':'GET',force?{sources:hubSourceSelection(selection)}:undefined);if(!isHubResponse(result))throw Error('invalidResponse');if(alive.current&&seq===sequence.current){setData(result);setSourceSnapshot(result.sources)}}
  catch(e){if(alive.current&&seq===sequence.current){setError((e as Error).message||'requestFailed');onError(e)}}finally{if(alive.current&&seq===sequence.current)setLoading(false)}
 }
 useEffect(()=>{alive.current=true;return()=>{alive.current=false;sequence.current++}},[]);
 useEffect(()=>{if(connected)void load();else{setData(null);setLoading(false);sequence.current++}},[connected,selectionKey]);
 useEffect(()=>{refreshRef.current=()=>load(true);return()=>{refreshRef.current=null}},[connected,selectionKey]);
 const pool:HubRow[]=data?.models||[],view=hubView(pool,query,origin,channel,sort),failures=hubFetchFailures(data?.sources||[]);
 const active=!!query||origin!=='all'||channel!=='all'||sort!=='sourceOrder',reset=()=>{setQuery('');setOrigin('all');setChannel('all');setSort('sourceOrder')};
 const attention=(data?.sources||[]).filter((s:HubRow)=>['failed','partial','unavailable','invalid_account'].includes(s.status)).length+(data?.account_errors?.length||0);
 const accountNames=new Map((data?.sources||[]).flatMap((s:HubRow)=>(s.accounts||[]).map((a:HubRow)=>[a.auth_index,a.name])));
 const detailRow=(m:HubRow)=>({...m,variants:m.variants.map((v:HubRow)=>({...v,account_name:accountNames.get(v.auth_index)}))});
 const counts:Record<string,number>={all:pool.length,dynamic:pool.filter(m=>m.variants.some((v:HubRow)=>v.origin==='dynamic')).length,custom:pool.filter(m=>m.variants.some((v:HubRow)=>v.origin==='custom')).length};
 return <section className="model-hub" aria-label={t('hubTitle')} aria-busy={loading}>
  <div className="hub-overview"><div className="hub-origin-tabs" role="group" aria-label={t('hubOriginFilter')}>{['all','dynamic','custom'].map(o=><button key={o} data-origin={o} aria-pressed={origin===o} onClick={()=>setOrigin(o)}>{t(o==='all'?'hubAllModels':'hubOrigin_'+o)}<b>{data?counts[o]:'—'}</b></button>)}</div>
   <div className="hub-actions"><button className="button ghost hub-sources-button" aria-label={t('hubSources')} title={t('hubSources')} aria-haspopup="dialog" onClick={()=>setSourcesOpen(true)}><SlidersHorizontal size={14}/><span>{t('hubSources')}</span>{attention>0&&<span className="hub-attention" aria-label={t('needsAttention')}>{attention}</span>}</button><button className="button hub-refresh" disabled={!connected||loading||busy} aria-label={t('refetchDirectory')} title={t('refetchDirectory')} onClick={()=>void load(true)}><RefreshCw size={14} className={loading?'spin':''}/><span>{t('refresh')}</span></button></div>
  </div>
  <div className="hub-toolbar"><label className="search"><Search size={15}/><input value={query} onChange={e=>setQuery(e.target.value)} aria-label={t('searchModels')} placeholder={t('searchModels')}/></label><select aria-label={t('hubChannelFilter')} value={channel} onChange={e=>setChannel(e.target.value)}>{['all',...hubChannels].map(x=><option key={x} value={x}>{t(x==='all'?'hubAllChannels':'hubChannel_'+x)}</option>)}</select><select aria-label={t('modelSort')} value={sort} onChange={e=>setSort(e.target.value)}>{['sourceOrder','nameOrder','contextOrder','creditOrder'].map(x=><option key={x} value={x}>{t(x)}</option>)}</select>{active&&<button className="button icon ghost" onClick={reset} aria-label={t('clearFilters')}><X size={15}/></button>}</div>
  {(failures.length>0||!!data?.account_errors?.length)&&<div className="hub-alert-line" role="status"><span>{failures.map(s=>t('hubChannel_'+s.channel)+'：'+t('hubIssue_'+hubIssue(s))).concat(data?.account_errors?.length?[t('hubAccountErrors')]:[]).join(' · ')}</span><button onClick={()=>setSourcesOpen(true)}>{t('hubResolve')}<ChevronRight size={12}/></button></div>}
  {error&&<div className="notice error" role="alert">{t(error)}<button className="button" onClick={()=>void load(true)} disabled={loading}>{t('retry')}</button></div>}
  <section className="table-card hub-table models-table"><div className="table-scroll"><table><thead><tr><th>{t('hubModel')}</th><th>{t('hubContextLabel')}</th><th>{t('hubOutputLabel')}</th><th>{t('multiplier')} / {t('reasoningTier')}</th><th><span className="sr-only">{t('actions')}</span></th></tr></thead><tbody>{view.map((m:HubRow)=>{
   const model=m.variants[0].model,difference=hubParameterDifference(m.variants);
   return <tr key={m.id}><td className="hub-name"><div className="hub-identity"><strong title={model.description||m.name||m.id}>{m.name||m.id}</strong><ProvenanceBadges variants={m.variants} t={t}/></div>{m.name!==m.id&&<small className="mono">{m.id}</small>}<CapabilityBadges modelID={m.id} variants={detailRow(m).variants} t={t} onDetails={()=>setDetail(detailRow(m))}/></td>
    <td className="hub-limit hub-context" data-label={t('hubContextLabel')}>{tok(model.context_length)}</td><td className="hub-limit hub-output" data-label={t('hubOutputLabel')}>{tok(model.max_completion_tokens)}</td><td className={'hub-meta '+(!model.credits&&!model.efforts?.length?'hub-meta-empty':'')}><b>{model.credits||'—'}</b>{model.efforts?.length>0&&<small>{model.efforts.join(' · ')}</small>}{difference&&<small className="hub-parameter-basis" title={t('hubBasisHint')}>{t('hubShortChannel_'+m.variants[0].channel)} · {t('hubShort_'+m.variants[0].origin)} *</small>}</td>
    <td className="hub-row-action"><button className="button ghost" onClick={()=>setDetail(detailRow(m))} aria-label={t('hubDetails')+' · '+m.id}><span>{t('hubDetails')}</span><ChevronRight size={13}/></button></td></tr>
  })}</tbody></table></div>
  {!view.length&&<div className="empty"><h3>{t(loading?'loading':!connected?'authRequired':error||!data?'catalogUnavailable':pool.length?'noMatches':'hubEmptyShort')}</h3>{!loading&&data&&(pool.length?<button className="button" onClick={reset}>{t('clearFilters')}</button>:<button className="button" onClick={()=>setSourcesOpen(true)}>{t('hubChooseSources')}</button>)}</div>}
  <div className="table-footer"><span>{t('shown')} {view.length} / {data?pool.length:'—'}</span><span title={t('hubEvidenceHint')}>{t('hubBadgeLegend')} · {t('hubBasisFootnote')}</span></div></section>
  {sourcesOpen&&<SourcesDialog sources={data?.sources||sourceSnapshot} data={data} loading={loading} error={error} busy={busy} selection={selection} setSelection={(ch,id)=>{sequence.current++;setData(null);setDetail(null);setSelection(prev=>({...prev,[ch]:id}))}} t={t} date={date} onRefresh={()=>void load(true)} onClose={()=>setSourcesOpen(false)}/>}
  {detail&&<HubDialog row={detail} t={t} date={date} fmt={fmt} onClose={()=>setDetail(null)} config={config} busy={busy} onToggle={onToggle} after={()=>load(false)}/>}</section>
}
