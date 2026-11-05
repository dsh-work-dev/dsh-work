import {readFileSync,mkdirSync} from 'node:fs';
import path from 'node:path';import {pathToFileURL} from 'node:url';import {createRequire} from 'node:module';
import {randomUUID} from 'node:crypto';import {execFile} from 'node:child_process';import {promisify} from 'node:util';
const finite=n=>typeof n==='number'&&Number.isFinite(n);
const bounds=b=>{if(!b||!['x','y','width','height'].every(k=>finite(b[k]))||b.width<=0||b.height<=0)throw Error('COMPUTER_INVALID_CAPTURE_GEOMETRY');return {x:b.x,y:b.y,width:b.width,height:b.height}};
const same=(a,b)=>Object.keys(a).every(k=>a[k]===b[k]);
export async function createAuvBackend(config){
 const require=createRequire(path.join(config.dependencies,'package.json'));
 for(const pkg of ['@auv-js/sdk','@auv-js/cli-win32-x64-msvc'])if(JSON.parse(readFileSync(require.resolve(pkg+'/package.json'),'utf8')).version!=='0.0.43')throw Error('COMPUTER_AUV_VERSION_MISMATCH');
 const version=await promisify(execFile)(config.driver,['--version'],{timeout:3000,windowsHide:true});if(version.stdout.trim()!=='auv 0.0.43')throw Error('COMPUTER_AUV_BINARY_VERSION_MISMATCH');
 const sdk=await import(pathToFileURL(path.join(path.dirname(require.resolve('@auv-js/sdk/package.json')),'dist/node.js')).href);
 const work=path.join(process.cwd(),'auv');mkdirSync(work,{recursive:true});
 let daemon,connection,last;
 const close=async()=>{last=undefined;try{await connection?.close()}finally{await daemon?.stop()}};
 try{
  daemon=await sdk.startAuv({binaryPath:config.driver,workingDirectory:work,storeRoot:path.join(work,'store'),noRegister:true,local:{auth:{allowedMethods:['launcher-token']}},startupTimeoutMs:15000,shutdownTimeoutMs:2000});
  if(!daemon.connectionOptions.local||daemon.connectionOptions.transport!=='npipe'||!daemon.connectionOptions.credential||daemon.endpoints.some(x=>!x.startsWith('npipe://')))throw Error('COMPUTER_AUV_PRIVATE_TRANSPORT_REQUIRED');
  connection=await daemon.connect({signal:AbortSignal.timeout(5000)});
  const health=await sdk.checkHealth(connection,{signal:AbortSignal.timeout(5000)});if(health.id!==daemon.id)throw Error('COMPUTER_AUV_IDENTITY_MISMATCH');
 }catch(e){await close();throw e}
 const runner=sdk.createAuv(connection).runner({runnerClass:'auv.core.local'});
 const options=()=>({signal:AbortSignal.timeout(20000)});
 const exact=async args=>{const w=await runner.windows.get(String(args.window_id),options());if(w.id!==String(args.window_id)||w.window?.processId!==args.pid)throw Error('COMPUTER_TARGET_CHANGED');return w};
 const result=(text,structuredContent,image)=>({content:[{type:'text',text},...(image?[{type:'image',mimeType:'image/png',data:Buffer.from(image.data).toString('base64')}]:[])],structuredContent});
 return {close,async execute(tool,args){
  if(!['list_windows','get_window_state','click','scroll'].includes(tool))throw Error('COMPUTER_ACTION_UNSUPPORTED: AUV Windows background input supports coordinates and scrolling, not text, keys or UIA indices');
  if(tool==='list_windows'){
   const windows=(await runner.windows.list({excludeOtherDesktops:true,...options()})).filter(w=>w.window?.isVisible).map(w=>{const pid=w.window.processId,window_id=Number(w.id);if(!Number.isSafeInteger(pid)||pid<1||!Number.isSafeInteger(window_id)||window_id<1)throw Error('COMPUTER_INVALID_WINDOW_METADATA');return {pid,window_id,title:w.window.title}});
   return result(JSON.stringify({windows}),{windows});
  }
  if(!Number.isSafeInteger(args.pid)||!Number.isSafeInteger(args.window_id))throw Error('COMPUTER_EXACT_WINDOW_REQUIRED');
  if(tool==='get_window_state'){
   last=undefined;const w=await exact(args),cap=await w.capture(options());
   if(cap.window?.ref?.windowId!==String(args.window_id)||cap.window?.processId!==args.pid||!cap.capture?.ref?.captureId)throw Error('COMPUTER_INVALID_CAPTURE');
   const frame=bounds(cap.capture.bounds),image=await runner.captures.image(cap.capture,{encoding:sdk.ImageEncoding.PNG,...options()});
   if(!Number.isSafeInteger(image.width)||image.width<=0||!Number.isSafeInteger(image.height)||image.height<=0)throw Error('COMPUTER_INVALID_CAPTURE');
   const snapshot_id='auv-'+randomUUID();last={pid:args.pid,hwnd:args.window_id,snapshot_id,bounds:frame,width:image.width,height:image.height,windowFrame:bounds(cap.window.frame),title:cap.window.title};
   return result('AUV Windows screenshot observation. No indexed UIA controls or background text/key input; locate controls in the screenshot.',{snapshot_id,pid:args.pid,window_id:args.window_id,window_title:cap.window.title,window_bounds:frame,screenshot_width:image.width,screenshot_height:image.height,elements:[],elements_complete:false,accessibility_supported:false},image);
  }
  if(args.scope!=='window'||args.delivery_mode!=='background'||args.element_index!==undefined)throw Error('COMPUTER_BACKGROUND_COORDINATES_REQUIRED');
  const observed=last;last=undefined;
  if(!observed||observed.pid!==args.pid||observed.hwnd!==args.window_id||observed.snapshot_id!==args.snapshot_id)throw Error('COMPUTER_STALE_OBSERVATION');
  const w=await exact(args),current=bounds(w.window.frame);if(!same(current,observed.windowFrame)||w.window.title!==observed.title)throw Error('COMPUTER_TARGET_CHANGED: observe again');
  const x=args.x??observed.width/2,y=args.y??observed.height/2;
  if(!finite(x)||!finite(y)||x<0||y<0||x>=observed.width||y>=observed.height)throw Error('COMPUTER_COORDINATES_OUTSIDE_CAPTURE');
  const point={x:observed.bounds.x+x*observed.bounds.width/observed.width-current.x,y:observed.bounds.y+y*observed.bounds.height/observed.height-current.y};
  let receipt;
  if(tool==='click')receipt=await w.click(point,{policy:sdk.InputPolicy.BACKGROUND_ONLY,windowStrategy:sdk.WindowClickStrategy.PID_TARGETED,click:{count:1}},options());
  else{
   if(!['up','down','left','right'].includes(args.direction)||!Number.isSafeInteger(args.amount??3)||(args.amount??3)<1||(args.amount??3)>50||!['line','page'].includes(args.by??'line'))throw Error('COMPUTER_INVALID_SCROLL');
   const horizontal=['left','right'].includes(args.direction),distance=(args.amount??3)*((args.by??'line')==='page'?(horizontal?observed.width:observed.height)*.8:40)*(['up','left'].includes(args.direction)?-1:1);
   receipt=await w.scroll(point,{deltaX:horizontal?distance:0,deltaY:horizontal?0:distance},{policy:sdk.InputPolicy.BACKGROUND_ONLY},options());
  }
  return result('AUV background delivery submitted. Observe again to verify the application result.',{delivery:{mode:'background'},effect:receipt.action?.verified?'confirmed':'unverifiable',receipt:JSON.parse(JSON.stringify(receipt,(_,v)=>typeof v==='bigint'?String(v):v))});
 }};
}
