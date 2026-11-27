// Compiled only into the explicit isolated preview consumer candidate.
(async()=>{
 const cfg=__MAC_PREVIEW_CONFIG__;
 if(parent===window||globalThis.__MAC_PREVIEW_CONSUMER__)return;
 globalThis.__MAC_PREVIEW_CONSUMER__=true;
 const shell=location.ancestorOrigins?.[0];
 if(!shell||shell==='null')return;
 let step='provider';
 const report=(stage,extra={})=>parent.postMessage({version:1,type:'dsh-work/mac-preview-report',nonce:cfg.nonce,stage,...extra},shell);
 const until=async(label,fn)=>{step=label;const end=performance.now()+30000;while(performance.now()<end){const v=await fn();if(v)return v;await new Promise(r=>setTimeout(r,100))}throw Error(label)};
 const click=pattern=>{const r=new RegExp(pattern,'i');const e=[...document.querySelectorAll('button,[role="button"]')].find(e=>!e.disabled&&e.getClientRects().length&&r.test(e.getAttribute('aria-label')||e.getAttribute('title')||e.textContent.trim()));if(!e)return false;e.click();return true};
 try{
  await until('provider',()=>globalThis.__DSH_WORK_BROWSER_PROVIDER__?.protocolVersion===1);
  let reached=false;try{void parent.document.body;reached=true}catch{}if(reached)throw Error('origin-isolation');
  await until('choose-workspace',()=>click('^(Choose workspace|选择工作区)$'));
  await until('edit-workspace-path',()=>click('^(Edit path|编辑路径)$'));
  const edit=document.querySelector('input[aria-label="Edit path"],input[aria-label="编辑路径"]');
  if(!edit)throw Error('path-input');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(edit,cfg.workspace);edit.dispatchEvent(new Event('input',{bubbles:true}));edit.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true}));
  await until('workspace-navigation',()=>{const e=[...document.querySelectorAll('[role="dialog"] button')].find(e=>/^(Open|打开)$/.test(e.textContent.trim()));return e&&!e.disabled&&!e.closest('[role="dialog"]').querySelector('[role="alert"]')});
  await until('workspace-readback',()=>click('^(Edit path|编辑路径)$'));
  const landed=document.querySelector('input[aria-label="Edit path"],input[aria-label="编辑路径"]');if(landed?.value.replace(/\/$/,'')!==cfg.workspace)throw Error('workspace-readback');landed.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true}));
  await until('open-workspace',()=>click('^(Open|打开)$'));
  await until('real-session',()=>{const rows=[...document.querySelectorAll('[role="treeitem"][data-row-key^="session:"]')].filter(e=>e.getClientRects().length&&e.getAttribute('aria-selected')==='true');return rows.length===1});
  const surface=await until('browser-sidebar',()=>{const address=document.querySelector('input[aria-label="Enter an HTTP(S) address"],input[aria-label="输入 HTTP(S) 地址"]');if(address?.getClientRects().length)return 'page';const guide=[...document.querySelectorAll('button[data-sidebar-right-guide-entry="browser"]')].filter(e=>!e.disabled&&e.getClientRects().length);if(guide.length===1)return 'guide';const expand=document.querySelector('button[data-sidebar-right-expand]');if(expand&&!expand.disabled&&expand.getClientRects().length)expand.click();return false});
  if(surface==='guide')await until('open-browser',()=>{const rows=[...document.querySelectorAll('button[data-sidebar-right-guide-entry="browser"]')].filter(e=>!e.disabled&&e.getClientRects().length);if(rows.length!==1)return false;rows[0].click();return true});
  const address=await until('address-toolbar',()=>document.querySelector('input[aria-label="Enter an HTTP(S) address"],input[aria-label="输入 HTTP(S) 地址"]'));
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(address,cfg.url);address.dispatchEvent(new Event('input',{bubbles:true}));address.dispatchEvent(new Event('change',{bubbles:true}));address.form.requestSubmit();
  report('slot-requested',{session:[...document.querySelectorAll('[role="treeitem"][data-row-key^="session:"]')].find(e=>e.getAttribute('aria-selected')==='true')?.getAttribute('data-row-key')});
  addEventListener('message',event=>{if(event.source!==parent||event.origin!==shell||event.data?.version!==1||event.data?.type!=='dsh-work/mac-preview-command'||event.data.nonce!==cfg.nonce)return;
   const command=event.data.command;
   if(command==='hide')void until('collapse-sidebar',()=>click('^(Collapse right sidebar|收起右侧边栏)$')).then(()=>report('hidden-requested')).catch(()=>report('failure',{step}));
   if(command==='show')void until('expand-sidebar',()=>{const e=document.querySelector('button[data-sidebar-right-expand]');if(!e||e.disabled||!e.getClientRects().length)return false;e.click();return true}).then(()=>report('reopened-requested')).catch(()=>report('failure',{step}));
  });
 }catch{report('failure',{step})}
})();
