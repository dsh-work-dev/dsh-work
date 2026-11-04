// The MCP target field is opaque. Scope snapshot references without changing
// schemas or interpreting ordinary input text as a route/selector.
const rawRef=/^(f\d+)?e\d+$/;
export function createRefScope(scope){
 const prefix=`dsh-${scope}:`;
 function argumentsForMCP(args){
  function target(value){
   if(typeof value!=='string')return value;
   if(value.startsWith(prefix)){
    if(rawRef.test(value.slice(prefix.length)))return value.slice(prefix.length);
    throw Error('BROWSER_FOREIGN_OR_STALE_REFERENCE: take a fresh snapshot in this conversation');
   }
   if(/^dsh-[A-Za-z0-9_-]+:(?:f\d+)?e\d+$/.test(value)||rawRef.test(value))throw Error('BROWSER_FOREIGN_OR_STALE_REFERENCE: take a fresh snapshot in this conversation');
   return value;
  }
  // Only actual locked-schema reference slots are interpreted. Drop MIME data,
  // ordinary input values and CSS custom elements/pseudo-classes stay literal.
  function fields(value){const result={...value};for(const key of ['target','startTarget','endTarget'])if(Object.hasOwn(result,key))result[key]=target(result[key]);return result;}
  const result=fields(args);if(Array.isArray(result.fields))result.fields=result.fields.map(fields);return result;
 }
 function resultForAgent(result){
  function snapshotText(text){
   let active=false,fence;
   return text.split('\n').map(line=>{
    if(!fence&&/^### /.test(line))active=/^### (Snapshot|Find results)$/.test(line);
    const mark=/^```([^`]*)$/.exec(line);if(mark){fence=fence?undefined:mark[1]||'plain';return line;}
    if(!active||fence&&fence!=='yaml'||!/^\s*-\s/.test(line))return line;
    let quoted=false,escaped=false,out='';
    for(let i=0;i<line.length;){
     if(!quoted){const match=/^\[ref=((?:f\d+)?e\d+)\]/.exec(line.slice(i));if(match){out+=`[ref=${prefix}${match[1]}]`;i+=match[0].length;continue;}}
     const char=line[i++];out+=char;
     if(escaped){escaped=false;continue;}if(char==='\\'&&quoted){escaped=true;continue;}if(char==='"')quoted=!quoted;
    }
    return out;
   }).join('\n');
  }
  function visit(value){
   if(typeof value==='string')return snapshotText(value);
   if(Array.isArray(value))return value.map(visit);
   if(value&&typeof value==='object')return Object.fromEntries(Object.entries(value).map(([k,v])=>[k,visit(v)]));
   return value;
  }
  return visit(result);
 }
 return {argumentsForMCP,resultForAgent};
}
