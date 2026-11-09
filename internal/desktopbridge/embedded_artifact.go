package desktopbridge

import (
	"errors"
	"runtime"
	"strings"
)

// The pinned HTML renderer already owns the real deliverables/files/document
// occurrence. Replace only its body on the explicitly enabled Windows provider;
// the unprojected runtime and other platforms keep their original renderer.
func projectEmbeddedArtifact(source string) (string, error) {
	if runtime.GOOS != "windows" || !strings.Contains(source, `id: "@deepseek-ai/dsh-client-ui-sidebar-documentpreview"`) {
		return source, nil
	}
	seam := "function HtmlBody({ content, resourceAddress, readRelated, useTabInfo, useInteractivePreview, addResource, setResources, t }) {"
	if strings.Count(source, seam) != 1 {
		return "", errors.New("CEF_DSH_ARTIFACT_SEAM_MISMATCH")
	}
	body := `function HtmlBody({ sessionId, useTabInfo, t }) {
 const {tab}=useTabInfo();
 const pair=(0,react.useMemo)(()=>globalThis.__DSH_WORK_BROWSER_PROVIDER__.createArtifactPage(sessionId,tab.id,tab.signal),[sessionId,tab.id,tab.signal]);
 const id=(0,react.useId)();
 const state=(0,react.useSyncExternalStore)(pair.frame.subscribe,pair.frame.getSnapshot);
 const copy=state.artifactLabels;
 const [status,setStatus]=(0,react.useState)("");const [busy,setBusy]=(0,react.useState)(false);
 (0,react.useEffect)(()=>pair.presentation.mount(id),[pair,id]);
 const run=async task=>{setBusy(true);setStatus("");try{await task()}catch(error){setStatus(String(error).includes("SELECTION_CANCELED")?"canceled":"failed")}finally{setBusy(false)}};
 return (0,react_jsx_runtime.jsxs)("section",{"data-dsh-artifact":true,style:{display:"flex",flexDirection:"column",height:"100%",minHeight:0,minWidth:0},children:[
 (0,react_jsx_runtime.jsxs)("div",{style:{display:"flex",gap:"var(--dsh-work-control-gap-size)",alignItems:"center",flexWrap:"wrap",padding:"var(--dsh-work-control-gap-size)"},children:[
 (0,react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button,{size:"sm",disabled:busy||!copy,onClick:()=>run(()=>pair.frame.openArtifact()),children:copy?(state.needsReload?copy.reselect:copy.choose):t("loading")}),
 (0,react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button,{size:"sm",disabled:busy||!copy||!state.target,onClick:()=>run(async()=>{const value=await globalThis.__DSH_WORK_BROWSER_PROVIDER__.selectPage('artifact/'+tab.id);setStatus(value.state==='waiting-for-turn'?"waiting":"selected")}),children:copy?.select||t("loading")}),
 (0,react_jsx_runtime.jsx)("span",{children:copy?(state.artifactName?copy.local+" · "+state.artifactName:copy.scope):t("loading")})]}),
 (status||state.error||state.needsReload)&&(0,react_jsx_runtime.jsx)("p",{role:"status",style:{margin:"0 var(--dsh-work-control-gap-size) var(--dsh-work-control-gap-size)",overflowWrap:"anywhere"},children:(status?copy?.[status]:undefined)||(state.error?(state.error.code===-32099?copy?.paused:copy?.failed):copy?.recover)||t("loading")}),
 (0,react_jsx_runtime.jsx)("div",{id,style:{flex:1,minHeight:0,minWidth:0,position:"relative"},"data-dsh-artifact-viewport":true})]});
 }
 function LegacyHtmlBody({ content, resourceAddress, readRelated, useTabInfo, useInteractivePreview, addResource, setResources, t }) {`
	return strings.Replace(source, seam, body, 1), nil
}
