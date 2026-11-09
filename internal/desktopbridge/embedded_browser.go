package desktopbridge

import (
	"errors"
	"strings"
)

const EmbeddedBrowserRuntime = "0.2.2-rc.1"

// The pinned Browser module already separates navigation from presentation.
// Expose only its composition selection and stable occurrence key; default Web
// and Electron factories remain byte-for-byte unchanged when not selected.
func projectEmbeddedBrowser(source string) (string, error) {
	if !strings.Contains(source, `id: "@deepseek-ai/dsh-client-ui-sidebar-browser"`) {
		return source, nil
	}
	toolbar := "className: Browser_module_css_default.toolbar,\n\t\t\t\t\t\tonSubmit: submit,\n\t\t\t\t\t\tchildren: ["

	// Same toolbar tool/Tooltip sizing; pointer geometry comes from the existing
	// pinned Lucide dependency, with its ISC notice retained in the emitted asset.
	pointer := `/* Lucide v1.48.0 MousePointer2
ISC License

Copyright (c) 2026 Lucide Icons and Contributors

Permission to use, copy, modify, and/or distribute this software for any
purpose with or without fee is hereby granted, provided that the above
copyright notice and this permission notice appear in all copies.

THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.

---

The following Lucide icons are derived from the Feather project:

airplay, alert-circle, alert-octagon, alert-triangle, aperture, arrow-down-circle, arrow-down-left, arrow-down-right, arrow-down, arrow-left-circle, arrow-left, arrow-right-circle, arrow-right, arrow-up-circle, arrow-up-left, arrow-up-right, arrow-up, at-sign, calendar, cast, check, chevron-down, chevron-left, chevron-right, chevron-up, chevrons-down, chevrons-left, chevrons-right, chevrons-up, circle, clipboard, clock, code, columns, command, compass, corner-down-left, corner-down-right, corner-left-down, corner-left-up, corner-right-down, corner-right-up, corner-up-left, corner-up-right, crosshair, database, divide-circle, divide-square, dollar-sign, download, external-link, feather, frown, hash, headphones, help-circle, info, italic, key, layout, life-buoy, link-2, link, loader, lock, log-in, log-out, maximize, meh, minimize, minimize-2, minus-circle, minus-square, minus, monitor, moon, more-horizontal, more-vertical, move, music, navigation-2, navigation, octagon, pause-circle, percent, plus-circle, plus-square, plus, power, radio, rss, search, server, share, shopping-bag, sidebar, smartphone, smile, square, table-2, tablet, target, terminal, trash-2, trash, triangle, tv, type, upload, x-circle, x-octagon, x-square, x, zoom-in, zoom-out

The MIT License (MIT) (for the icons listed above)

Copyright (c) 2013-present Cole Bemis

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE. */ (0,react_jsx_runtime.jsx)("svg",{width:16,height:16,viewBox:"0 0 24 24",fill:"none",stroke:"currentColor",strokeWidth:2,strokeLinecap:"round",strokeLinejoin:"round","aria-hidden":true,children:(0,react_jsx_runtime.jsx)("path",{d:"M4.037 4.688a.495.495 0 0 1 .651-.651l16 6.5a.5.5 0 0 1-.063.947l-6.124 1.58a2 2 0 0 0-1.438 1.435l-1.579 6.126a.5.5 0 0 1-.947.063z"})})`
	button := toolbar + `globalThis.__DSH_WORK_EMBEDDED_VERSION__ === '0.2.2-rc.1' && (0,react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tooltip,{label:"Use for this conversation",side:"bottom",delayMs:500,children:(0, react_jsx_runtime.jsx)("button", {type:"button", className:Browser_module_css_default.tool, "aria-label":"Use for this conversation", "aria-busy":controlSelection.busy, disabled:controlSelection.busy||controlPaused, onClick:async()=>{setControlSelection({busy:true,message:"Selecting this page…",error:false});try{const value=await globalThis.__DSH_WORK_BROWSER_PROVIDER__.selectPage(tab.id);setControlSelection({busy:false,error:false,message:value.state==='waiting-for-turn'?"Page selected. Send your next message to continue.":"Page selected for this conversation."})}catch(error){const code=String(error);setControlSelection({busy:false,error:true,message:code.includes('CONTROL_DISABLED')?"Enable Browser use in Settings.":code.includes('COMPONENTS_REQUIRED')?"Install Playwright MCP in Settings > Browser.":code.includes('STOP_BEFORE_SWITCH')?"Stop browser control before choosing another page.":"Couldn't select this page. Try again."})}}, children:` + pointer + `})}),`

	controlHook := "const [mountEpoch, setMountEpoch] = (0, react.useState)(0);"
	controlState := controlHook + `const [controlSelection,setControlSelection]=(0,react.useState)({busy:false,message:"",error:false});`
	feedbackHook := "sandboxed === false && (0, react_jsx_runtime.jsx)(\"div\", {"
	feedback := `globalThis.__DSH_WORK_EMBEDDED_VERSION__ === '0.2.2-rc.1' && !controlPaused && controlSelection.message && (0,react_jsx_runtime.jsx)("div",{className:Browser_module_css_default.sandboxWarning,role:controlSelection.error?"alert":"status",style:{whiteSpace:"normal",overflowWrap:"anywhere"},children:controlSelection.message}),` + feedbackHook

	patches := [][2]string{{toolbar, button}, {controlHook, controlState}, {feedbackHook, feedback},
		{"const error = frame.error;", "const error = frame.error; const controlPaused = globalThis.__DSH_WORK_EMBEDDED_VERSION__ === '0.2.2-rc.1' && error?.code === -32099;"},
		{"loadUrl(tab.id, draft);", "if (!controlPaused) loadUrl(tab.id, draft);"},
		{"disabled: target === void 0 || mountEpoch === 0,", "disabled: controlPaused || target === void 0 || mountEpoch === 0,"},
		{"\"aria-label\": t(\"address.placeholder\"),", "\"aria-label\": t(\"address.placeholder\"), disabled: controlPaused,"},
		{"title: t(\"go\"),", "title: t(\"go\"), disabled: controlPaused,"},
		{"children: error.code !== void 0 && error.description !== void 0 ?", "children: controlPaused ? 'Browser control is paused.' : error.code !== void 0 && error.description !== void 0 ?"},
		{"keepMounted: desktop !== void 0", "keepMounted: desktop !== void 0 || globalThis.__DSH_WORK_EMBEDDED_VERSION__ === '0.2.2-rc.1'"},
		{"this.page = options.createPage({", "this.page = options.createPage({ key: options.tabId,"},
		{"if (desktop === void 0) installFrames(ctx, () => createIframePage);", "if (globalThis.__DSH_WORK_EMBEDDED_VERSION__ === '0.2.2-rc.1') installFrames(ctx, sessionId => options => { const provider = globalThis.__DSH_WORK_BROWSER_PROVIDER__; if (provider?.protocolVersion !== 1) throw Error('Embedded browser is unavailable'); return provider.createPage(sessionId, options); }); else if (desktop === void 0) installFrames(ctx, () => createIframePage);"},
	}
	for _, patch := range patches {
		if strings.Count(source, patch[0]) != 1 {
			return "", errors.New("CEF_DSH_RUNTIME_SEAM_MISMATCH")
		}
		source = strings.Replace(source, patch[0], patch[1], 1)
	}
	return source, nil
}
