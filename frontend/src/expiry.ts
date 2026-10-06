const DAY=86400000;
export function expiryTimestamp(raw:unknown):number{
 if(typeof raw!=='string')return NaN;const text=raw.trim();
 if(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/.test(text)){
  const iso=text.replace(' ','T'),value=Date.parse(iso+'+08:00');
  return Number.isFinite(value)&&new Date(value+8*3600000).toISOString().slice(0,19)===iso?value:NaN;
 }
 return /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(text)?Date.parse(text):NaN;
}
export function earliestCredits(credits:any,now:number){
 let at=Infinity,amount=0,unknown=false,expired=false;
 const packages=Array.isArray(credits?.packages)?credits.packages:null;
 if(packages){for(const p of packages){if(typeof p?.remain!=='number'||!Number.isFinite(p.remain)||p.remain<=0)continue;const end=expiryTimestamp(p.cycle_end);if(!Number.isFinite(end)){unknown=true;continue}if(end<=now){expired=true;continue}if(end<at){at=end;amount=p.remain}else if(end===at)amount+=p.remain}}
 else{const e=credits?.expiry,end=expiryTimestamp(e?.next_at);if(e?.next_amount>0&&Number.isFinite(end)){if(end>now){at=end;amount=e.next_amount}else expired=true}else unknown=true}
 return {at:Number.isFinite(at)?at:null,amount,days:Number.isFinite(at)?(at-now)/DAY:null,unknown,expired};
}
export function expiryDays(days:number,locale:string){return days<0.05?'<'+new Intl.NumberFormat(locale,{minimumFractionDigits:1,maximumFractionDigits:1}).format(0.1):new Intl.NumberFormat(locale,{minimumFractionDigits:1,maximumFractionDigits:1}).format(days)}
