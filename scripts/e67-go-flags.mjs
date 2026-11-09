// Pure normalization for this candidate's historical GOFLAGS. This module never
// writes Go configuration, caches or the caller's environment.
import {resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
export function normalizeCandidateFlags(raw,modfile){
 const canonical=value=>resolve(value.replaceAll('\\','/')).replaceAll('\\','/').toLowerCase();
 let removed=0;
 const flags=raw.replace(/(^|\s)("-modfile=[^"]*"|'-modfile=[^']*'|-modfile=\S+|-tags=e67presenter)(?=\s|$)/g,(match,space,token)=>{
  const unquoted=/^["']/.test(token)?token.slice(1,-1):token;
  if(unquoted==='-tags=e67presenter'||canonical(unquoted.slice('-modfile='.length))===canonical(modfile)){removed++;return space}
  return match;
 });
 // A foreign modfile is not ours to discard. Fail before launching Wails rather
 // than changing a user's build meaning or letting the probe fail obscurely.
 if(/(^|\s)["']?-modfile(?:=|\s|$)/.test(flags))throw Error('Effective GOFLAGS contains a non-candidate -modfile; preserved unchanged. Remove it explicitly for this candidate launch or choose its intended build.');
 return {flags:flags.trim()||' ',removed}; // nonempty whitespace overrides a persistent GOENV fallback
}
export function inspectCandidateFlags(modfile){
 const effective=JSON.parse(execFileSync('go',['env','-json','GOFLAGS','GOENV'],{encoding:'utf8',windowsHide:true}));
 const source=process.env.GOFLAGS?'process environment':'Go effective environment (GOENV/default)';
 try{return {...normalizeCandidateFlags(effective.GOFLAGS||'',modfile),source,goenv:effective.GOENV}}
 catch(error){throw Error(`${source}; GOENV=${effective.GOENV}: ${error.message}`)}
}
if(process.argv[1]&&resolve(process.argv[1])===fileURLToPath(import.meta.url)){
 if(process.argv.length!==3)throw Error('Expected candidate modfile path');
 console.log(JSON.stringify(inspectCandidateFlags(process.argv[2])));
}
