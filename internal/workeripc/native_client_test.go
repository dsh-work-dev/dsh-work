//go:build !darwin

package workeripc

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeClientUsesOriginalCarrierAndHMAC(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	transport, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	root := t.TempDir()
	if err = transport.PrepareNativeClient(root); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "native-connection.json"))
	var connection map[string]string
	if err != nil || json.Unmarshal(data, &connection) != nil || len(connection) != 1 || connection["pipe"] != transport.path {
		t.Fatal("non-Darwin sidecar changed the carrier or added credentials", err)
	}
	want, _ := hostSource.ReadFile("native-socket.mjs")
	got, err := os.ReadFile(filepath.Join(root, "native-socket.mjs"))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("native client did not reuse the Worker carrier", err)
	}
	const source = `
import {readFileSync} from 'node:fs';
import {createHmac,timingSafeEqual} from 'node:crypto';
import {openPrivateSocket} from './native-socket.mjs';
const config=JSON.parse(readFileSync(new URL('./native-connection.json',import.meta.url)));
const socket=await openPrivateSocket(config);
const token=process.env.DSH_WORK_BRIDGE_TOKEN;
delete process.env.DSH_WORK_BRIDGE_TOKEN;delete process.env.DSH_WORK_BRIDGE_PIPE;
let bytes=Buffer.alloc(0),nonce,authenticated=false;
socket.on('error',()=>{process.exitCode=1});
socket.on('data',chunk=>{
 bytes=Buffer.concat([bytes,chunk]);
 const mac=role=>createHmac('sha256',token).update(role).update(nonce).digest();
 if(!nonce&&bytes.length>=32){nonce=bytes.subarray(0,32);bytes=bytes.subarray(32);socket.write(mac('worker'))}
 if(nonce&&!authenticated&&bytes.length>=32){if(!timingSafeEqual(bytes.subarray(0,32),mac('host')))throw Error('Host HMAC rejected');bytes=bytes.subarray(32);authenticated=true;socket.write('native-client')}
 if(authenticated&&bytes.toString()==='quit')socket.destroy();
});
`
	path := filepath.Join(root, "client.mjs")
	if err = os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, path)
	command.Env = os.Environ()
	for key, value := range transport.ChildEnvironment() {
		command.Env = append(command.Env, key+"="+value)
	}
	var output bytes.Buffer
	command.Stderr = &output
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	joined := false
	defer func() {
		if !joined {
			cancel()
			_ = command.Wait()
		}
	}()
	conn, err := transport.Connect(ctx)
	if err != nil {
		t.Fatal("original authenticated carrier rejected native client", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	proof := make([]byte, len("native-client"))
	if _, err = io.ReadFull(conn, proof); err != nil || string(proof) != "native-client" {
		t.Fatal("native client HMAC/framing changed", err)
	}
	if _, err = conn.Write([]byte("quit")); err != nil {
		t.Fatal(err)
	}
	err = command.Wait()
	joined = true
	if err != nil {
		t.Fatal(err, output.String())
	}
}
