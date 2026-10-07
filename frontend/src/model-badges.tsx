import React from 'react';
import {Eye,Wrench,BrainCircuit,Radio,Braces} from 'lucide-react';
import {hubCapabilityBadges,hubProvenanceBadges,modelCapabilities,modelCapabilityState,type HubRow,type ModelCapability} from './model-hub';
type T=(key:string)=>string;
const labels:Record<ModelCapability,string>={vision:'Vision',tools:'Tools',reasoning:'Reasoning'};
const icons={vision:Eye,tools:Wrench,reasoning:BrainCircuit};
export function ProvenanceBadges({variants,t}:{variants:HubRow[],t:T}){
 return <div className="hub-row-origins" aria-label={t('source')}>{hubProvenanceBadges(variants).map(b=>{const Icon=b.origin==='dynamic'?Radio:Braces;return <span key={b.channel+b.origin} className={'model-badge hub-provenance '+b.origin+(b.disabled?' upstream-disabled':'')} data-channel={b.channel} data-origin={b.origin} title={t(b.origin==='dynamic'?'hubBadgeDynamicHint':'hubBadgeCustomHint')+(b.disabled?' · '+t('upstreamDisabled'):'')}><span className="hub-channel">{t('hubShortChannel_'+b.channel)}</span><span className="badge-divider"/><Icon size={11} aria-hidden="true"/><span className={'hub-origin '+b.origin}>{t('hubOrigin_'+b.origin)}</span>{b.disabled&&<small>{t('disabled')}</small>}</span>})}</div>
}
export function CapabilityBadges({variants,t,onDetails,modelID}:{variants:HubRow[],t:T,onDetails:()=>void,modelID:string}){
 const badges=hubCapabilityBadges(variants);if(!badges.length)return null;
 return <div className="hub-capabilities" aria-label={t('hubCapabilityDeclarations')}>{badges.map(b=>{const Icon=icons[b.capability];const scope=b.supported.map(v=>t('hubShortChannel_'+v.channel)+' · '+t('hubOrigin_'+v.origin)+(v.account_name?' · '+v.account_name:'')).join('; ');const description=modelID+' · '+labels[b.capability]+' · '+t(b.partial?'hubCapabilityPartial':'hubCapabilityAll')+' ('+b.supported.length+'/'+b.total+') · '+scope;return <button type="button" key={b.capability} data-capability={b.capability} data-scope={b.partial?'partial':'all'} className={'model-badge hub-capability '+b.capability+(b.partial?' partial':'')} title={description} aria-label={description+' · '+t('details')} onClick={onDetails}><Icon size={12} aria-hidden="true"/>{labels[b.capability]}{b.partial&&<small>{b.supported.length}/{b.total}</small>}</button>})}</div>
}
export function CapabilityEvidence({model,t}:{model:HubRow,t:T}){
 return <dl className="hub-capability-evidence">{modelCapabilities.map(capability=>{const state=modelCapabilityState(model,capability),Icon=icons[capability];return <div key={capability} data-capability={capability} data-state={state}><dt><Icon size={13} aria-hidden="true"/>{labels[capability]}</dt><dd>{t('hubCapability_'+state)}</dd></div>})}</dl>
}
