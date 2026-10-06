type Model=Record<string,any>;
export function modelMultiplier(raw:unknown):number|null{
 if(typeof raw!=='string')return null;
 const match=raw.trim().match(/^[x×]\s*(\d+(?:\.\d+)?)(?:\s+credits)?$/i);
 if(!match)return null;const value=Number(match[1]);return Number.isFinite(value)?value:null;
}
export function filterModels(pool:Model[],query:string,status:string,family:string,capability:string,sort:string){
 const needle=query.trim().toLocaleLowerCase();
 const result=pool.filter(m=>[m.id,m.name,m.family,m.vendor,m.credits,...(Array.isArray(m.efforts)?m.efforts:[])].join(' ').toLocaleLowerCase().includes(needle)
 &&(status==='all'||(status==='enabled'?!m.disabled:!!m.disabled))&&(!family||(m.family||m.vendor)===family)
 &&(capability==='all'||(capability==='imageDeclared'&&m.supports_images===true)||(capability==='textDeclared'&&m.supports_images===false)||(capability==='reasoningDeclared'&&(m.supports_reasoning===true||m.only_reasoning===true||Array.isArray(m.efforts)&&m.efforts.length>0))||(capability==='largeContext'&&m.context_length>=128*1024)));
 if(sort==='sourceOrder')return result;
 const value=(m:Model)=>sort==='creditOrder'?modelMultiplier(m.credits):typeof m.context_length==='number'&&m.context_length>0?m.context_length:null;
 return result.sort((a,b)=>{if(sort==='nameOrder')return String(a.name||a.id).localeCompare(String(b.name||b.id))||String(a.id).localeCompare(String(b.id));const av=value(a),bv=value(b);if(av===null&&bv!==null)return 1;if(bv===null&&av!==null)return -1;return (av!==null&&bv!==null?(sort==='creditOrder'?av-bv:bv-av):0)||String(a.id).localeCompare(String(b.id))});
}

export function isDirectoryResponse(value:any):boolean{return !!value&&Array.isArray(value.models)&&Array.isArray(value.sources)&&['ok','partial','failed','unsupported'].includes(value.status)}
