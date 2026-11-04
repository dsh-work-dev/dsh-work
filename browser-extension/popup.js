const $ = id => document.getElementById(id);
const strings = {
 en: {connecting:'Connecting…',bind:'Connect to DSH Work',cancel:'Cancel linking',reconnect:'Reconnect',unbound:'Not linked',connected:'Connected',conflict:'Conflicting identity. Close duplicate connections, then reconnect.',offline:'Not connected',confirm:'Confirm in DSH Work · {code}',update:'Update DSH Work and the extension, then reconnect.',bound:'This profile is already linked.',expired:'Linking request expired. Try again.',timeout:'Open DSH Work, check the connection, then retry.',busy:'Please try again shortly.'},
 'zh-CN': {connecting:'正在连接…',bind:'连接到 DSH Work',cancel:'取消绑定',reconnect:'重新连接',unbound:'尚未绑定',connected:'已连接',conflict:'连接身份冲突，请关闭重复连接后重新连接',offline:'未连接',confirm:'请在 DSH Work 中确认 · {code}',update:'请更新 DSH Work 和扩展后重新连接',bound:'这份资料已绑定',expired:'绑定请求已失效，请重试',timeout:'请打开 DSH Work，重检连接后重试',busy:'请稍后重试'},
 'ja-JP': {connecting:'接続中…',bind:'DSH Work に接続',cancel:'リンクをキャンセル',reconnect:'再接続',unbound:'未リンク',connected:'接続済み',conflict:'接続の識別情報が重複しています。重複した接続を閉じて再接続してください。',offline:'未接続',confirm:'DSH Work で確認 · {code}',update:'DSH Work と拡張機能を更新して再接続してください。',bound:'このプロファイルはリンク済みです。',expired:'リンク要求の期限が切れました。再試行してください。',timeout:'DSH Work を開き、接続を確認して再試行してください。',busy:'しばらくしてから再試行してください。'}
};
const language=chrome.i18n.getUILanguage();const locale=language.startsWith('zh')?'zh-CN':language.startsWith('ja')?'ja-JP':'en';
const t=key=>strings[locale][key];document.documentElement.lang=locale;
for(const id of ['bind','cancel','reconnect'])$(id).textContent=t(id);
$('status').textContent=t('connecting');
const errors = {BROWSER_IDENTITY_CONFLICT:'conflict',BROWSER_UPDATE_REQUIRED:'update',BROWSER_ALREADY_BOUND:'bound',BROWSER_BINDING_REQUEST_INVALID:'expired',BROWSER_HOST_TIMEOUT:'timeout',BROWSER_NATIVE_DISCONNECTED:'timeout',BROWSER_BUSY:'busy'};
async function send(message) { const r = await chrome.runtime.sendMessage(message); if (r?.error) throw Error(errors[r.error] ? t(errors[r.error]) : `${t('timeout')} · ${r.error}`); return r; }
let busy = false;
async function refresh() {
 if (busy) return;
 try {
  const r=await send({kind:'status'}),bound=!!r.status?.id;
  $('status').textContent=bound?`${r.status.name} · ${r.status.id.slice(0,8).toUpperCase()} · ${t(r.status.status)}`:r.candidate?t('confirm').replace('{code}',r.candidate.slice(0,8).toUpperCase()):t(r.status?.status??'offline');
  $('bind-form').hidden=bound||!!r.candidate;$('cancel').hidden=!r.candidate||bound;$('error').textContent='';
 } catch(e){$('status').textContent=t('offline');$('error').textContent=e.message;}
}
async function act(message){busy=true;let succeeded=false;try{await send(message);$('error').textContent='';succeeded=true;}catch(e){$('error').textContent=e.message;}finally{busy=false;}if(succeeded)await refresh();}
$('bind-form').addEventListener('submit',e=>{e.preventDefault();act({kind:'bind'});});
$('cancel').addEventListener('click',()=>act({kind:'cancel'}));$('reconnect').addEventListener('click',()=>act({kind:'reconnect'}));
refresh();setInterval(refresh,1000);
