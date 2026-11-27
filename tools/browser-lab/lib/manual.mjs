export function evaluateIME(documents, host, samples) {
 const result={};
 for(const [name,value] of [['A','中文甲'],['B','中文乙'],['host','宿主中文']]) {
  const d=name==='host'?host:documents[name];
  const events=d.events||[];
  const ends=events.filter(e=>e.type==='compositionend'&&e.trusted&&/[\u3400-\u9fff]/u.test(e.data||''));
  const focus=ends.some(e=>samples.some(s=>Math.abs(s.time-e.time)<450&&(name==='host'?s.hostInputFocused:s.views.find(v=>v.name===name)?.focused)));
  result[name]={value:d.value,compositionStart:events.some(e=>e.type==='compositionstart'&&e.trusted),chineseCompositionEnd:ends.length>0,nativeFocusObserved:focus};
  result[name].passed=d.value===value&&result[name].compositionStart&&ends.length>0&&focus;
 }
 return {passed:Object.values(result).every(r=>r.passed),surfaces:result};
}

export function evaluateMixedDPI(samples) {
 const good=samples.filter(s=>s.hostMonitor&&s.guestMonitor===s.hostMonitor&&s.hostDpi===s.guestDpi&&s.dpr===s.hostDpi/96);
 return {passed:new Set(good.map(s=>s.hostMonitor)).size>=2&&new Set(good.map(s=>s.hostDpi)).size>=2,
  monitors:[...new Set(good.map(s=>s.hostMonitor))],dpis:[...new Set(good.map(s=>s.hostDpi))],validSamples:good.length};
}
