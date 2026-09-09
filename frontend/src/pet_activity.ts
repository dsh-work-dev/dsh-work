import {PetSettingsService} from "../bindings/github.com/local/dsh-work/internal/app";
import {currentLocale as getLocale, subscribeLocale} from "./i18n";

export interface Activity {
  workPhase?: string; toolActivity?: string; sessionId: string; title: string; parentSessionId?: string; state: string; outcome: string;
  summary: string; queued: number; steering: number; unread: boolean;
  interaction?: {kind: string; text: string; count?: number} | null;
  goal?: {phase: string; objective: string; reason: string; rounds: number; maxRounds: number} | null;
  jobs?: {label: string; status: string; detail?: string}[] | null;
}
export interface ActivitySnapshot {connected: boolean; sessions?: Activity[] | null; navigationError?: string; generation?: string}
const labels = {
  "en": {running:"Working", "needs-input":"Needs input", ready:"Completed · unread", blocked:"Needs attention", idle:"Ready", offline:"Connecting to DSH…", list:"Activities", close:"Close", empty:"No active conversations", approval:"Approval needed", question:"Your answer is needed", "plan-review":"Plan ready for review", completed:"Completed", aborted:"Stopped", error:"An error occurred", "max-tokens":"Output limit reached", interrupted:"Execution interrupted", paused:"Paused", active:"Active", complete:"Complete", queued:"queued", steering:"steering", jobs:"background jobs", openError:"Could not open this conversation", goal:"Goal"},
  "zh-CN": {running:"正在处理", "needs-input":"需要你的输入", ready:"已完成 · 未读", blocked:"需要处理", idle:"就绪", offline:"正在连接 DSH…", list:"活动", close:"收起", empty:"暂无活动对话", approval:"需要批准", question:"等你回答", "plan-review":"计划等待确认", completed:"已完成", aborted:"已停止", error:"遇到错误", "max-tokens":"已达输出上限", interrupted:"执行已中断", paused:"已暂停", active:"进行中", complete:"已完成", queued:"项排队", steering:"项补充指令", jobs:"项背景工作", openError:"无法打开此对话", goal:"目标"},
  "ja-JP": {running:"作業中", "needs-input":"入力が必要", ready:"完了・未読", blocked:"確認が必要", idle:"待機中", offline:"DSH に接続中…", list:"アクティビティ", close:"閉じる", empty:"実行中の会話はありません", approval:"承認が必要", question:"回答を待っています", "plan-review":"計画の確認待ち", completed:"完了", aborted:"停止", error:"エラーが発生しました", "max-tokens":"出力上限に到達", interrupted:"実行が中断されました", paused:"一時停止", active:"進行中", complete:"完了", queued:"件待機中", steering:"件の追加指示", jobs:"件のバックグラウンド作業", openError:"会話を開けませんでした", goal:"目標"}
};
const extraLabels: Record<string, Record<string,string>> = {
  "en": {thinking:"Thinking",result:"Organizing results",searching:"Reading and searching",editing:"Editing files",testing:"Checking changes",commanding:"Running a command","using-tool":"Using a tool",stopping:"Stopping", killed:"Stopped", failed:"Failed", questions:"questions"},
  "zh-CN": {thinking:"正在思考",result:"正在整理结果",searching:"正在阅读和搜索",editing:"正在修改文件",testing:"正在检查修改",commanding:"正在执行命令","using-tool":"正在使用工具",stopping:"正在停止", killed:"已停止", failed:"失败", questions:"个问题"},
  "ja-JP": {thinking:"考え中",result:"結果を整理中",searching:"検索・閲覧中",editing:"ファイルを編集中",testing:"変更を確認中",commanding:"コマンドを実行中","using-tool":"ツールを使用中",stopping:"停止中", killed:"停止", failed:"失敗", questions:"件の質問"}
};
function label(key: string): string {const locale=getLocale();return (labels[locale] as Record<string,string>)[key] ?? extraLabels[locale]?.[key] ?? key;}
export function activityText(a: Activity): string {
  if(a.interaction) return `${label(a.interaction.kind)}${(a.interaction.count ?? 0)>1 ? ` · ${a.interaction.count} ${label("questions")}` : ""}${a.interaction.text ? `：${a.interaction.text}` : ""}`;
  if(a.goal?.phase === "blocked") return a.goal.reason || a.goal.objective;
  if(a.outcome && a.state !== "running") return `${label(a.outcome)}${a.summary ? ` · ${a.summary}` : ""}`;
  if(a.state==="running" && a.workPhase) return label(a.workPhase==="working" ? a.toolActivity || "using-tool" : a.workPhase);
  return a.summary || a.goal?.objective || label(a.state);
}
function activityDetails(a:Activity):string {
  const parts:string[]=[];
  if(a.goal)parts.push(`${label("goal")} · ${label(a.goal.phase)} · ${a.goal.rounds}/${a.goal.maxRounds}`);
  if(a.queued)parts.push(`${a.queued} ${label("queued")}`);
  if(a.steering)parts.push(`${a.steering} ${label("steering")}`);
  if(a.jobs?.length)parts.push(a.jobs.map(j=>`${j.label} · ${label(j.status)}${j.detail ? ` · ${j.detail}` : ""}`).join(" / "));
  return parts.join(" · ");
}
export function mountPetActivity() {
  const panel=document.getElementById("pet-activity")!;
  const surface=document.getElementById("pet-surface")!;
  const status=document.getElementById("pet-activity-status")!;
  const primary=document.getElementById("pet-activity-primary") as HTMLButtonElement;
  const title=document.getElementById("pet-activity-title")!;
  const error=document.getElementById("pet-activity-error")!;
  let latest:ActivitySnapshot={connected:false}, signature="", opening=false, openFailed=false;
  let notificationKey="", transient=false, hovered=false, hideTimer:ReturnType<typeof setTimeout>|undefined;
  let idleDismissed=false;
  function syncVisibility() {
    const focused=panel.contains(document.activeElement);
    panel.hidden=idleDismissed || !(transient || openFailed || latest.navigationError || ((hovered || focused) && !!latest.sessions?.length));
  }
  function showBriefly() {
    transient=true;clearTimeout(hideTimer);
    hideTimer=setTimeout(()=>{transient=false;syncVisibility();},8000);
  }
  surface.addEventListener("pointerenter",()=>{hovered=true;idleDismissed=false;syncVisibility();});
  surface.addEventListener("pointerleave",()=>{hovered=false;syncVisibility();});
  panel.addEventListener("focusout",()=>queueMicrotask(syncVisibility));
  surface.addEventListener("keydown",event=>{if(event.key==="Escape"){transient=false;hovered=false;(document.activeElement as HTMLElement)?.blur();signature="";render();}});
  async function open(id:string) {
    if(opening)return;opening=true;openFailed=false;error.hidden=true;
    try {await PetSettingsService.OpenPetActivity(id);} catch {openFailed=true;error.textContent=label("openError");error.hidden=false;}
    finally {opening=false;syncVisibility();}
  }
  primary.addEventListener("click",()=>{const first=latest.sessions?.[0];if(first && latest.connected)void open(first.sessionId);});
  function render() {
    const rows=latest.sessions ?? [], first=rows[0];
    const activityKey=JSON.stringify([latest.connected,first?.sessionId,first?.state,first?.outcome,first?.interaction,first?.summary,first && activityDetails(first)]);
    if(activityKey!==notificationKey){
      notificationKey=activityKey;
      if(latest.connected && first && first.state!=="idle"){
        idleDismissed=false;showBriefly();
      } else {
        transient=false;clearTimeout(hideTimer);
        idleDismissed=true;
        if(panel.contains(document.activeElement))(document.activeElement as HTMLElement)?.blur();
      }
    }
    syncVisibility();
    const next=JSON.stringify([latest,getLocale()]);if(next===signature)return;signature=next;
    status.textContent=latest.connected ? (first ? activityText(first) : label("idle")) : label("offline");
    title.textContent=first?.title ?? label("empty");
    primary.disabled=!latest.connected || !first;
    primary.setAttribute("aria-label",first ? `${first.title} · ${activityText(first)}` : label("empty"));
    primary.setAttribute("aria-description",first ? activityDetails(first) : "");
    error.hidden=!latest.navigationError && !openFailed;
    if(!error.hidden)error.textContent=label("openError");
  }
  const unsubscribe=subscribeLocale(()=>{signature="";render();});
  window.addEventListener("unload",()=>{unsubscribe();clearTimeout(hideTimer);});render();
  return (snapshot:ActivitySnapshot)=>{latest=snapshot;render();};
}
