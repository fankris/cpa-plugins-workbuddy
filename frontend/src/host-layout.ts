import {useLayoutEffect} from 'react';

// Read-only, same-origin geometry. Never remove/style parent controls or read
// credentials. A title bar outside the iframe already consumes host space.
export function useHostLayout(){
 useLayoutEffect(()=>{
  const root=document.documentElement;
  const set=(name:string,value:string)=>{if(root.style.getPropertyValue(name)!==value)root.style.setProperty(name,value)};
  if(parent===window){root.dataset.hostLayout='standalone';return}
  let outer:Document,frame:HTMLElement;
  try{outer=parent.document;frame=window.frameElement as HTMLElement;if(!frame)throw Error('inaccessible frame')}
  catch{root.dataset.hostLayout='unmeasurable';set('--host-top-gap','80px');set('--host-overlay-bottom','80px');return()=>{root.removeAttribute('data-host-layout');root.style.removeProperty('--host-top-gap');root.style.removeProperty('--host-overlay-bottom')}}
  let pending=0,stopped=false;
  const observed=new Set<Element>();
  const schedule=()=>{if(!pending&&!stopped)pending=requestAnimationFrame(measure)};
  const resize=new ResizeObserver(schedule);
  const watch=(node:Element)=>{if(!observed.has(node)){observed.add(node);resize.observe(node)}};
  const measure=()=>{
   pending=0;if(stopped)return;
   const main=document.querySelector('main'),nav=document.querySelector('.section-nav');if(!main||!nav)return;
   const f=frame.getBoundingClientRect();if(!f.width||!f.height)return;
   const sx=innerWidth/f.width,sy=innerHeight/f.height;
   const official=!!outer.querySelector('.main-header .header-actions.floating-actions .language-menu')&&!!outer.querySelector('.main-header .header-actions.floating-actions .theme-menu');
   if(official)root.dataset.hostUi='cpamc';else root.removeAttribute('data-host-ui');
   const overlays:{left:number,right:number,bottom:number}[]=[];
   const nodes=outer.querySelectorAll('.floating-actions,.mobile-sidebar-actions,[data-workbuddy-overlay],[role="toolbar"],header,[role="banner"],button');
   const current=new Set<Element>([frame]);
   for(const node of Array.from(nodes)){
    if(node.contains(frame))continue;
    const style=parent.getComputedStyle(node);
    if(style.display==='none'||style.visibility==='hidden'||Number(style.opacity)===0||style.pointerEvents==='none')continue;
    if(node.tagName==='BUTTON'&&!['fixed','absolute'].includes(style.position))continue;
    // The CPAMC header shell may span the viewport; its floating children,
    // rather than the transparent shell, define the actual occupied area.
    if(node.matches('header,[role="banner"]')&&node.querySelector('.floating-actions,.mobile-sidebar-actions'))continue;
    const b=node.getBoundingClientRect();current.add(node);watch(node);
    if(b.width<=0||b.height<=0||b.bottom<=f.top||b.top>=f.top+Math.min(160,f.height/3)||b.right<=f.left||b.left>=f.right)continue;
    const left=Math.max(b.left,f.left),right=Math.min(b.right,f.right),top=Math.max(b.top,f.top),bottom=Math.min(b.bottom,f.bottom);
    const hit=outer.elementFromPoint((left+right)/2,(top+bottom)/2);
    if(!hit||!(node===hit||node.contains(hit)))continue;
    overlays.push({left:(left-f.left)*sx,right:(right-f.left)*sx,bottom:(bottom-f.top)*sy});
   }
   for(const node of observed){if(!current.has(node)&&(!node.isConnected||!node.matches('.language-menu-popover,.theme-menu-popover'))){resize.unobserve(node);observed.delete(node)}}
   const box=main.getBoundingClientRect(),css=getComputedStyle(main);
   const left=box.left+parseFloat(css.paddingLeft),right=box.right-parseFloat(css.paddingRight);
   let l=0,r=0,bottom=0,wide=false;
   for(const o of overlays){bottom=Math.max(bottom,o.bottom+8);if(o.left<=left+8&&o.right>=right-8)wide=true;else if((o.left+o.right)/2>=(left+right)/2)r=Math.max(r,right-o.left+8);else l=Math.max(l,o.right-left+8)}
   l=Math.max(0,l);r=Math.max(0,r);
   const compact=innerWidth<=640;
   const floatingHost=!!outer.querySelector('.floating-actions,.mobile-sidebar-actions');
   const mode=!bottom&&!floatingHost?'external-header':compact?'overlay-mobile':wide?'overlay-wide':'overlay-band';
   root.dataset.hostLayout=mode;
   // A real workspace header owns this row. No whole-page padding or shifting
   // navigation between top/side positions as toolbar width changes.
   set('--host-top-gap','0px');
   set('--host-nav-left',Math.ceil(l)+'px');
   set('--host-nav-right',Math.ceil(r)+'px');
   set('--host-nav-height',Math.max(44,Math.ceil(bottom-parseFloat(css.paddingTop)))+'px');
   // Official popovers temporarily overlay the workspace, as designed by
   // CPAMC. Only modal safety changes; never reflow the underlying page.
   let dialogBottom=bottom;
   if(official)for(const node of Array.from(outer.querySelectorAll('.language-menu-popover,.theme-menu-popover'))){
    const b=node.getBoundingClientRect(),style=parent.getComputedStyle(node);
    if(style.display!=='none'&&style.visibility!=='hidden'&&b.width&&b.height&&b.bottom>f.top&&b.left<f.right&&b.right>f.left){dialogBottom=Math.max(dialogBottom,(b.bottom-f.top)*sy+8);watch(node)}
   }
   set('--host-overlay-bottom',Math.ceil(dialogBottom)+'px');
  };
  watch(frame);
  const mutation=new MutationObserver(schedule);
  mutation.observe(outer.body,{childList:true,subtree:true,attributes:true,attributeFilter:['class','style','hidden']});
  parent.addEventListener('animationend',schedule,true);parent.addEventListener('transitionend',schedule,true);
  parent.addEventListener('resize',schedule);parent.addEventListener('scroll',schedule,true);
  window.addEventListener('resize',schedule);window.visualViewport?.addEventListener('resize',schedule);
  document.fonts.ready.then(schedule);measure();
  const dispose=()=>{if(stopped)return;stopped=true;cancelAnimationFrame(pending);resize.disconnect();mutation.disconnect();parent.removeEventListener('animationend',schedule,true);parent.removeEventListener('transitionend',schedule,true);parent.removeEventListener('resize',schedule);parent.removeEventListener('scroll',schedule,true);window.removeEventListener('resize',schedule);window.visualViewport?.removeEventListener('resize',schedule);root.removeAttribute('data-host-layout');root.removeAttribute('data-host-ui');for(const n of ['top-gap','nav-left','nav-right','nav-height','overlay-bottom'])root.style.removeProperty('--host-'+n)};
  // Host locale/route changes can remove the iframe without React unmounting.
  const onPageHide=(event:PageTransitionEvent)=>{if(!event.persisted){window.removeEventListener('pagehide',onPageHide);dispose()}};
  window.addEventListener('pagehide',onPageHide);
  return()=>{window.removeEventListener('pagehide',onPageHide);dispose()};
 },[]);
}
