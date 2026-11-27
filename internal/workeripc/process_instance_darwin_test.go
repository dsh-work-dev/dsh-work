//go:build darwin && cgo

package workeripc

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/localipc"
)

func TestDarwinNodeProcessAdmission(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := bundledMacBridge()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("real Node same-process bridge: %s", bridge)
	transport, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	root := t.TempDir()
	if _, err = transport.Prepare(root, node, ""); err != nil {
		t.Fatal(err)
	}
	source := `
import {readFileSync} from 'node:fs';
import {createHmac,timingSafeEqual} from 'node:crypto';
import {openPrivateSocket} from './native-socket.mjs';
import {spawn} from 'node:child_process';
import {once} from 'node:events';
const config=JSON.parse(readFileSync(new URL('./connection.json',import.meta.url)));
const socket=await openPrivateSocket(config);
// The bridge's external owner is now unreachable. Force its real N-API
// finalizer, then exercise the cold-child-spawn path before the live echo.
for(let i=0;i<3;i++){global.gc();await new Promise(setImmediate)}
const child=spawn(process.execPath,['-e','process.exit(0)'],{stdio:'ignore'});
const [code]=await once(child,'exit');if(code!==0)throw Error('cold child failed');
global.gc();await new Promise(setImmediate);
socket.on('error',error=>{ console.error(error.code); process.exitCode=1; });
let bytes=Buffer.alloc(0),nonce,done=false;
socket.on('data',chunk=>{
 bytes=Buffer.concat([bytes,chunk]);
 const mac=role=>createHmac('sha256',config.token).update(role).update(nonce).digest();
 if(!nonce && bytes.length>=32) { nonce=bytes.subarray(0,32); bytes=bytes.subarray(32); socket.write(mac('worker')); }
 if(nonce && !done && bytes.length>=32) {
   if(!timingSafeEqual(bytes.subarray(0,32),mac('host'))) throw Error('HMAC rejected');
   done=true; bytes=bytes.subarray(32); console.log('HMAC_OK pid='+process.pid); socket.write('node-proof');
 }
 if(done && bytes.toString()==='quit') socket.destroy();
});
`
	fixture := filepath.Join(root, "node-peer.mjs")
	if err = os.WriteFile(fixture, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--expose-gc", fixture)
	var output strings.Builder
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	started, err := localipc.ProcessStarted(uint32(cmd.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	c, err := transport.ConnectProcessInstance(ctx, uint32(cmd.Process.Pid), started)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	proof := make([]byte, len("node-proof"))
	if _, err = io.ReadFull(c, proof); err != nil || string(proof) != "node-proof" {
		t.Fatalf("Node proof %q: %v", proof, err)
	}
	if _, err = c.Write([]byte("quit")); err != nil {
		t.Fatal(err)
	}
	if err = cmd.Wait(); err != nil {
		t.Fatalf("Node: %v: %s", err, output.String())
	}
	if !strings.Contains(output.String(), "HMAC_OK") {
		t.Fatalf("missing real HMAC completion: %s", output.String())
	}
	t.Log(output.String())
}

func TestDarwinMissingBridgeIsTyped(t *testing.T) {
	path, err := macBridgeAt(filepath.Join(t.TempDir(), "missing.node"))
	if path != "" || !errors.Is(err, ErrMacBridgeMissing) {
		t.Fatalf("missing asset: %q %v", path, err)
	}
}
