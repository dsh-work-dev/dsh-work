import {createInterface} from 'node:readline/promises';
import {writeFileSync} from 'node:fs';
import {join} from 'node:path';
import {startWindows} from './platforms/windows.mjs';
import {evaluateIME,evaluateMixedDPI} from './lib/manual.mjs';
import {sleep} from './lib/context.mjs';

export async function runManual(options) {
 if(process.platform!=='win32'||!process.stdin.isTTY)throw Error('Manual native verification requires an implemented adapter and an interactive terminal');
 if(!options.operator)throw Error('Provide --operator for manual evidence');
 const a=await startWindows(options),terminal=createInterface({input:process.stdin,output:process.stdout});
 const samples=[];let busy=false,error,timer;
 try{
 await a.main.evaluate(()=>{const label=document.createElement('label');label.textContent='宿主输入区';const field=document.createElement('input');field.id='manual-input';field.setAttribute('aria-label','宿主输入区');field.style.width='210px';label.append(field);document.querySelector('aside').append(label);lab.events=[]});
 await a.call('backend','/manual-reset',{});
 timer=setInterval(async()=>{if(busy)return;busy=true;try{const s=await a.call('control','/state'),d=await a.docs(),v=s.tabs.find(t=>t.name===s.selected);samples.push({time:Date.now(),views:s.tabs.map(t=>({name:t.name,focused:!!t.focus?.focusInRequestedTree})),hostInputFocused:!!s.mainFocus?.focusInDemoTree&&!s.tabs.some(t=>t.focus?.focusInRequestedTree),hostMonitor:s.main.monitor,guestMonitor:v.monitor,hostDpi:s.main.dpi,guestDpi:v.dpi,dpr:d[s.selected]?.dpr})}catch(e){error=String(e.message)}finally{busy=false}},150);
  console.log('只输入测试文字，不要粘贴。A 使用真实中文输入法输入“中文甲”；切 B 并浮动，输入“中文乙”；停靠后在宿主输入区输入“宿主中文”。如有不同缩放的两块显示器，请把主窗实际移过去再移回。');
  await terminal.question('操作完成后回到此终端按 Enter：');
  const candidatePlacement=await terminal.question('候选框是否贴近光标、浮动输入是否正常？请输入 y 或 n：');
  clearInterval(timer);while(busy)await sleep(20);
  const documents=await a.call('backend','/manual-events',{}),host=await a.main.evaluate(()=>({value:document.querySelector('#manual-input').value,events:lab.events.filter(e=>e.target==='manual-input')}));
  const ime=evaluateIME(documents,host,samples),dpi=evaluateMixedDPI(samples),at=new Date().toISOString();
  const native=await a.state();await a.capture('manual-native-input');if(native.floating)await a.capture('manual-native-float','float');
  const output={schemaVersion:1,runId:options.runId,candidate:a.session.build,results:[
   {id:'input.native-ime',status:ime.passed&&candidatePlacement.toLowerCase()==='y'?'pass':'fail',operator:options.operator,at,observation:'Actual composition/focus assessment: '+JSON.stringify(ime)+'; candidate positioning user response='+candidatePlacement},
   {id:'dpi.mixed-monitors',status:dpi.passed?'pass':'blocked',operator:options.operator,at,observation:JSON.stringify(dpi)+(dpi.passed?'':' — no complete physical mixed-DPI observation')}
  ]};
  writeFileSync(join(options.output,'manual-evidence.json'),JSON.stringify(output,null,2));
  writeFileSync(join(options.evidenceRoot,'manual-input-events.json'),JSON.stringify({documents,host,samples,error},null,2));
  return output;
 }finally{clearInterval(timer);terminal.close();await a.close()}
}
