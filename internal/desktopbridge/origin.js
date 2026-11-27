// URL normalization for dsh-work's configured WebView authorities. This is not
// browser-origin evidence or an authorization grant. Message consumers still
// compare the browser's actual event.origin and reject opaque "null" origins.
(() => {
 if(globalThis.__DSH_WORK_ORIGINS__)return;
 const parse=(raw,base)=>{try{const u=new URL(raw,base);if(u.username||u.password)return;return u}catch{}};
 const origin=(raw,base)=>{
  const u=parse(raw,base);if(!u)return;
  if(u.protocol==='http:'||u.protocol==='https:')return u.origin;
  // The Linux shell and Worker use the single Wails-registered URI scheme,
  // with separate exact authorities. Other custom schemes/hosts stay opaque.
  if(u.protocol==='wails:'&&(u.host==='localhost'||u.host==='wails.localhost:48217'))return 'wails://'+u.host;
 };
 const same=(raw,base)=>{const a=origin(raw,base),b=origin(base);return a!==undefined&&b!==undefined&&a===b};
 const gateway=(raw,base)=>{
  const page=parse(base),target=parse(raw,base);if(!page||!target||target.pathname!=='/api/remote.mux')return false;
  if(page.protocol==='wails:')return page.host==='wails.localhost:48217'&&target.host===page.host&&(target.protocol==='wails:'||target.protocol==='ws:');
  if(!['http:','https:'].includes(page.protocol))return false;
  const protocol=target.protocol==='ws:'?'http:':target.protocol==='wss:'?'https:':target.protocol;
  return protocol===page.protocol&&target.host===page.host;
 };
 Object.defineProperty(globalThis,'__DSH_WORK_ORIGINS__',{value:Object.freeze({origin,same,gateway})});
})();
