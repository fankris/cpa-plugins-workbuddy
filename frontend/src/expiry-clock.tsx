import React,{createContext,useContext,useEffect,useRef,useState} from 'react';
import {earliestCredits,expiryDays} from './expiry';
import {useLocale} from './i18n';
const Clock=createContext(Date.now());
export function ExpiryClock({serverTime,children}:{serverTime?:string,children:React.ReactNode}){
 const anchor=useRef({epoch:Date.now(),mono:performance.now()}),[now,setNow]=useState(Date.now());
 useEffect(()=>{const parsed=serverTime?Date.parse(serverTime):NaN;anchor.current={epoch:Number.isFinite(parsed)?parsed:Date.now(),mono:performance.now()};const tick=()=>setNow(anchor.current.epoch+performance.now()-anchor.current.mono);tick();const timer=setInterval(tick,30000);document.addEventListener('visibilitychange',tick);window.addEventListener('focus',tick);return()=>{clearInterval(timer);document.removeEventListener('visibilitychange',tick);window.removeEventListener('focus',tick)}},[serverTime]);
 return <Clock.Provider value={now}>{children}</Clock.Provider>
}
export function NextExpiry({credits,compact=false}:{credits?:any,compact?:boolean}){
 const {locale,t}=useLocale(),now=useContext(Clock),e=earliestCredits(credits,now);
 const amount=new Intl.NumberFormat(locale,{maximumFractionDigits:0}).format(e.amount);
 return <small className="credit-expiring" aria-label={t('nextExpiryShort')} title={e.at?t('nextExpiryShort')+' · '+new Intl.DateTimeFormat(locale,{dateStyle:'medium',timeStyle:'short'}).format(e.at):t('expiryUnknown')}>
 {compact&&<span className="expiry-mobile-label">{t('nextExpiryShort')} </span>}{e.at?<>{!compact&&t('nextExpiryShort')+' '}{amount} · <span className="expiry-days">{expiryDays(e.days!,locale)}{t('daysUnit')}</span>{e.unknown?' *':''}</>:compact?(e.expired?t('expiredShort'):'—'):t(e.expired?'expiryElapsed':e.unknown?'expiryUnknown':'noDatedCredits')}
 </small>
}
