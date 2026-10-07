import React,{useEffect,useRef,useState} from 'react';
import {request} from './api';
import {businessSupported,trialEligibility} from './workspace-data';
type R=Record<string,any>;
export function InternationalBusiness({account,t,disabled,onTrial,onFailure}:{account:R,t:(key:string)=>string,disabled:boolean,onTrial:()=>void,onFailure:(e:unknown)=>string}){
 const [result,setResult]=useState<R|null>(null),[busy,setBusy]=useState(false),[error,setError]=useState('');const seq=useRef(0);
 useEffect(()=>{seq.current++;setResult(null);setError('');setBusy(false);return()=>{seq.current++}},[account.auth_index]);
 const diagnose=async()=>{const id=++seq.current;setBusy(true);setError('');setResult(null);try{const r=await request('/activation/status?auth_index='+encodeURIComponent(account.auth_index));if(!['registered','required','unknown'].includes(r.registration))throw Error('invalidResponse');if(seq.current===id)setResult(r)}catch(e){if(seq.current===id)setError(onFailure(e))}finally{if(seq.current===id)setBusy(false)}};
 if(!businessSupported(account,'trial')&&!businessSupported(account,'activation_status'))return null;
 return <section className="international-business"><h3>{t('internationalBusiness')}</h3><p className="muted">{t(trialEligibility(account)==='already_claimed'?'trialObserved':'trialUnknown')}</p><div className="detail-actions">{businessSupported(account,'activation_status')&&<button className="button activation-read" disabled={disabled||busy} onClick={diagnose}>{t(busy?'loading':'activationRead')}</button>}{businessSupported(account,'trial')&&trialEligibility(account)!=='already_claimed'&&<button className="button trial-request" disabled={disabled||busy} onClick={onTrial}>{t('requestTrial')}</button>}</div>{result&&<div className="notice activation-result"><span>{t('registration_'+result.registration)}{result.http_status?' · HTTP '+result.http_status:''}</span><small>{t('fetched')}: {result.checked_at||'—'}</small></div>}{error&&<p role="alert" className="error-text">{t(error)}</p>}<p className="muted">{t('activationReadOnly')} <a href="https://www.workbuddy.ai/" target="_blank" rel="noopener noreferrer">{t('openOfficial')} ↗</a></p></section>
}
