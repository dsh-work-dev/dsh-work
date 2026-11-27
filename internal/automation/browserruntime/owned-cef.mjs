import {ExtensionProtocolV2} from './cdp-adapter.mjs';
// CEF uses the same maintained browser model, but never claims extension
// admission. Its daemon-owned route has a separate, explicit wire vocabulary.
export class OwnedCEFProtocolV1 extends ExtensionProtocolV2 {
  constructor(send) {
    const names = {
      'chrome.tabs.create': 'tab.create',
      'chrome.tabs.remove': 'tab.close',
      'chrome.debugger.attach': 'devtools.attach',
      'chrome.debugger.sendCommand': 'devtools.dispatch',
    };
    super((method, params) => {
      const owned = names[method];
      if (!owned) throw Error('CEF_ROUTE_METHOD_REJECTED');
      return send(owned, params);
    });
  }
  handleExtensionEvent(method, params) {
    const names = {
      'tab.created': 'chrome.tabs.onCreated',
      'tab.closed': 'chrome.tabs.onRemoved',
      'devtools.event': 'chrome.debugger.onEvent',
      'devtools.detached': 'chrome.debugger.onDetach',
      'route.ready': 'extension.initialized',
    };
    const translated = names[method];
    if (!translated) throw Error('CEF_ROUTE_EVENT_REJECTED');
    super.handleExtensionEvent(translated, params);
  }
}
