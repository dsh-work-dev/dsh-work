import {chromium} from 'playwright-core';
// Playwright's HTTP discovery honors HTTP_PROXY, including for 127.0.0.1.
// Native fetch is direct here; pass the discovered loopback websocket to Playwright.
export async function attach(port) {
 const info=await (await fetch(`http://127.0.0.1:${port}/json/version`,{signal:AbortSignal.timeout(5000)})).json();
 const url=new URL(info.webSocketDebuggerUrl);
 if(url.protocol!=='ws:'||url.hostname!=='127.0.0.1'||Number(url.port)!==port)throw Error('Unexpected CDP endpoint');
 return chromium.connectOverCDP(url.href);
}
