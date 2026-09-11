import {once} from 'node:events';
export const inject=['webServer','typertGateway','connection'];
export function apply(ctx){
      ctx.effect(() => ctx.webServer.register({kind:'exact',path:'/.dsh/remote-stream',handler:async(req,res)=>{
        const rejection=ctx.connection.requestRejection(req);
        if(rejection!==undefined){res.writeHead(rejection).end();return;}
        if(req.method!=='POST'){res.writeHead(405).end();return;}
        const abort=new AbortController();
        res.once('close',()=>abort.abort());
        try {
          const chunks=[];let size=0;
          for await(const chunk of req){size+=chunk.length;if(size>1024*1024){res.writeHead(413).end();return;}chunks.push(chunk);}
          const body=JSON.parse(Buffer.concat(chunks).toString());
          if(typeof body.endpoint!=='string'){res.writeHead(400).end();return;}
          const values=await ctx.typertGateway.wireStream.open(body.endpoint,body.payload,abort.signal);
          res.writeHead(200,{'content-type':'application/x-ndjson'});res.flushHeaders();
          for await(const value of values){
            if(!res.write(JSON.stringify(value)+'\n'))await once(res,'drain',{signal:abort.signal});
          }
          res.end();
        }catch{if(res.headersSent)res.destroy();else res.writeHead(400).end();}
      }}));
}
