import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import {readFileSync} from 'node:fs';
const tick=async()=>{for(let i=0;i<15;i++)await new Promise(r=>setImmediate(r))};

// Only the DOM/IPC boundary is simulated here. Actual DSH tab creation is an
// integration acceptance item, not established by these provider guard tests.
async function fixture(session='source-session'){
 const listeners=new Map(),calls=[],opens=[],elements=[],intervals=[];let visible=true,holdAllow=false,held;
 const node=(tag='div')=>{const n={tag,style:{},dataset:{},children:[],isConnected:true,offsetHeight:0,
  setAttribute(k,v){this[k]=v},removeAttribute(k){delete this[k]},addEventListener(){},append(...items){for(const i of items){i.parent=this;this.children.push(i)}},
  remove(){this.isConnected=false;if(this.parent)this.parent.children=this.parent.children.filter(n=>n!==this)},closest(){return null},getClientRects(){return visible?[{}]:[]},getBoundingClientRect(){return{x:0,y:0,left:0,top:0,right:700,bottom:450,width:700,height:450}}};elements.push(n);return n};
 const container=node(),popup={id:'native/'+session+'/1',context:'native-document-1',frameDocument:'4',browser:'4',popup:'7',frame:'cef-source',document:'12',view:'view-lease',presentation:'3',origin:'https://source.test',sourceURL:'https://source.test/source',url:'https://target.test/same',userGesture:true};
 let state={state:'ready',url:popup.sourceURL,title:'Source page',lease:popup.view,document:popup.document,visible:true,popup,popupVisible:false};
 const reply=(m,value)=>{for(const fn of listeners.get('message')||[])fn({source:parent,origin:'http://shell',data:{version:1,type:'dsh-work/browser-result',id:m.id,value}})};
 const parent={postMessage(m){calls.push(m);if(m.action==='popup'&&m.command==='allow'&&holdAllow){held=m;return}queueMicrotask(()=>respond(m))}};
 const respond=m=>{let value=true;if(m.action==='state'||m.action==='update')value=structuredClone(state);else if(m.action==='popup'){
  if(m.command==='ready')state.popupVisible=true;
  if(['allow','deny'].includes(m.command)){delete state.popup;state.popupVisible=false;value=m.command==='allow'?popup.url:''}else value='';
 }reply(m,value)};
 const context=vm.createContext({window:{},console,URL,location:{ancestorOrigins:['http://shell'],hash:''},parent,crypto:{randomUUID:()=>String(calls.length+1)},innerWidth:900,innerHeight:700,
  setTimeout:()=>1,clearTimeout(){},setInterval:fn=>{intervals.push(fn);return intervals.length},clearInterval(){},requestAnimationFrame:fn=>queueMicrotask(fn),
  ResizeObserver:class{observe(){}disconnect(){}},IntersectionObserver:class{constructor(fn){this.fn=fn}observe(target){this.fn([{target,isIntersecting:true,intersectionRatio:1}])}disconnect(){}},MutationObserver:class{observe(){}},
  getComputedStyle:()=>({visibility:visible?'visible':'hidden',position:'relative'}),document:{head:{append(){}},documentElement:{},createElement:node,querySelectorAll:()=>[],getElementById:()=>container},
  addEventListener(type,fn){listeners.set(type,[...(listeners.get(type)||[]),fn])},removeEventListener(){}});
 vm.runInContext(readFileSync(new URL('./embedded_browser.js',import.meta.url),'utf8'),context);
 const page=context.__DSH_WORK_BROWSER_PROVIDER__.createPage(session,{key:'source-tab',persist(){},openRequested:url=>opens.push({session,url})});
 const unmount=page.presentation.mount('viewport');await tick();
 const button=text=>elements.findLast(e=>e.tag==='button'&&e.textContent===text&&e.isConnected);
 return{calls,opens,popup,page,button,unmount,hide(){visible=false;unmount()},setState(v){state={...state,...v}},hold(){holdAllow=true},release(){respond(held)},replay(){reply(held,popup.url)},poll:async()=>{for(const fn of intervals)fn();await tick()}};
}

test('popup uses its source-session callback once, after native suspension and source recheck',async()=>{
 const a=await fixture('A'),b=await fixture('B');
 for(const f of[a,b]){
  assert.deepEqual(f.calls.filter(c=>c.action==='popup').map(c=>c.command),['suspend','ready']);
  const allow=f.button('鎵撳紑鏂版爣绛鹃〉');assert.ok(allow&&!allow.disabled);
  f.hold();allow.onclick();allow.onclick();await tick();
  assert.equal(f.calls.filter(c=>c.command==='allow').length,1);assert.equal(f.opens.length,0);
  f.release();await tick();f.replay();await tick();
  assert.deepEqual(f.opens,[{session:f===a?'A':'B',url:f.popup.url}]);
  assert.equal(f.calls.some(c=>c.action==='select-control'),false);
  assert.equal(f.calls.some(c=>c.action==='update'&&c.address===f.popup.url),false,'source must never navigate to popup target');
  await f.page.frame.dispose();
 }
});

test('late popup allow cannot open after source hide, disposal, or native document change',async()=>{
 for(const reason of['hide','dispose','navigation']){
  const f=await fixture(reason);f.hold();f.button('鎵撳紑鏂版爣绛鹃〉').onclick();await tick();
  let disposal;if(reason==='hide')f.hide();else if(reason==='dispose')disposal=f.page.frame.dispose();else f.setState({document:'13',url:'https://source.test/next'});
  f.release();await tick();if(disposal)await disposal;
  assert.equal(f.opens.length,0,reason);if(!disposal)await f.page.frame.dispose();
 }
});

test('popup decline consumes only the request and a visible resize re-read keeps approval usable',async()=>{
 const f=await fixture();f.setState({popup:{...f.popup,presentation:'4'},popupVisible:false});await f.poll();
 assert.ok(f.button('鎵撳紑鏂版爣绛鹃〉')&&!f.button('鎵撳紑鏂版爣绛鹃〉').disabled);
 f.button('鎷掔粷').onclick();await tick();assert.equal(f.opens.length,0);
 assert.equal(f.calls.filter(c=>c.command==='deny').length,1);await f.page.frame.dispose();
});
