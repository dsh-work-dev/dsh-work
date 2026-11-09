// Fixed read-only policy observation. Never exported as an Agent tool, and no
// caller supplies JavaScript. Input values, cookies and page bodies are omitted.
const observe = `(element) => {
 const d=element?.ownerDocument||document;let e=element||d.activeElement;
 if(!element)while(e?.shadowRoot?.activeElement)e=e.shadowRoot.activeElement;
 const label=node=>{if(!node)return '';const labelled=(node.getAttribute('aria-labelledby')||'').split(/\\s+/).map(id=>d.getElementById(id)?.textContent||'').join(' ');return (node.getAttribute('aria-label')||labelled.trim()||[...(node.labels||[])].map(n=>n.textContent).join(' ')||(['BUTTON','A','OPTION'].includes(node.tagName)?node.textContent:'')||node.getAttribute('placeholder')||'').replace(/\\s+/g,' ').trim().slice(0,160)};
 const form=e?.form||e?.closest('form'), submit=form?.querySelector('button[type=submit],input[type=submit],button:not([type])');
 let geometry=null;try{const w=d.defaultView,r=e&&w===w.top?e.getBoundingClientRect():null;if(r)geometry={x:r.x,y:r.y,width:r.width,height:r.height}}catch{}
 return {url:d.location.href,geometry,element:{name:label(e),tag:(e?.tagName||'').toLowerCase(),role:e?.getAttribute('role')||'',type:e?.getAttribute('type')||'',autocomplete:e?.getAttribute('autocomplete')||'',multiline:e?.tagName==='TEXTAREA'||e?.isContentEditable||e?.getAttribute('aria-multiline')==='true',inForm:!!form,submit:e?.tagName==='BUTTON'&&e.type==='submit'||e?.tagName==='INPUT'&&e.type==='submit',submitName:label(submit)}};
}`;
function resultJSON(result) {
 if(result.isError)throw Error('BROWSER_POLICY_INSPECTION_FAILED');
 const text=result.content?.filter(v=>v.type==='text').map(v=>v.text).join('\n')??'';
 const match=/(?:^|\r?\n)### Result\r?\n([\s\S]*?)(?=\r?\n### |$)/.exec(text);
 if(!match)throw Error('BROWSER_POLICY_INSPECTION_FAILED');
 try{return JSON.parse(match[1].trim())}catch{throw Error('BROWSER_POLICY_INSPECTION_FAILED')}
}
export async function inspectBrowser(client, refs, args, observeGeometry) {
 // MCP disallows tab tools (including snapshot/evaluate) while its file chooser
 // is pending. The context-level tabs list still reports URL and modal state.
 // Require that state; never substitute cached page evidence for an upload.
 if(args.modal==='file-upload'||args.modal==='dialog'){
  const result=await client.callTool({name:'browser_tabs',arguments:{action:'list'}},undefined,{timeout:5000});
  if(result.isError)throw Error('BROWSER_POLICY_INSPECTION_FAILED');
  const text=(result.content??[]).filter(v=>v.type==='text').map(v=>v.text).join('\n').split(/\r?\n### Snapshot(?:\r?\n|$)/)[0];
  const listing=/(?:^|\r?\n)### Result\r?\n([\s\S]*?)(?=\r?\n### |$)/.exec(text);
  const page=listing&&/^- \d+: \(current\) \[.*\]\((.*)\)\s*$/m.exec(listing[1]);
  const modal=/(?:^|\r?\n)### Modal state\r?\n([\s\S]*?)(?=\r?\n### |$)/.exec(text);
  const required=args.modal==='dialog'?/^\- \["(?:alert|confirm|prompt|beforeunload)" dialog with message /m:/^\- \[File chooser\]: can be handled by browser_file_upload\s*$/m;
  if(!page||!modal||!required.test(modal[1]))throw Error(args.modal==='dialog'?'BROWSER_DIALOG_REQUIRED':'BROWSER_UPLOAD_CHOOSER_REQUIRED');
  return {url:page[1],elements:[],tabs:[]};
 }
 const targets=Array.isArray(args.targets)&&args.targets.length?args.targets:[undefined];
 const elements=[];let url='';
 for(const target of targets){
  const parameters={function:observe};
  if(target!==undefined){parameters.target=refs.argumentsForMCP({target}).target;parameters.element='Browser access check';}
  const value=resultJSON(await client.callTool({name:'browser_evaluate',arguments:parameters},undefined,{timeout:5000}));
  observeGeometry?.(target,value.geometry);
  url=value.url;elements.push(value.element);
 }
 const tabs=[];
 if(args.tabs){
  const result=await client.callTool({name:'browser_tabs',arguments:{action:'list'}},undefined,{timeout:5000});
  if(result.isError)throw Error('BROWSER_POLICY_INSPECTION_FAILED');
  for(const item of result.content??[]){if(item.type!=='text')continue;for(const line of item.text.split('\n')){const match=/^- (\d+): .*\((.*)\)\s*$/.exec(line);if(match)tabs.push({index:Number(match[1]),url:match[2]});}}
 }
 return {url,elements,tabs};
}
