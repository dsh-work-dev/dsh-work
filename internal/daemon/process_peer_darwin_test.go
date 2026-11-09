//go:build darwin && cgo

package daemon

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/localipc"
)

func TestDarwinProcessClientAndPeerContext(t *testing.T) {
	identity := fmt.Sprintf("mac-test-%d-%d", os.Getpid(), time.Now().UnixNano())
	l, err := Listen(identity)
	if err != nil {
		t.Fatal(err)
	}
	path, _, _ := endpoint(identity)
	defer os.Remove(path + ".lock")
	defer l.Close()
	seen := make(chan ProcessPeer, 2)
	server := &http.Server{ConnContext: nativePeerContext, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := ProcessPeerFrom(r.Context())
		if !ok {
			http.Error(w, "missing peer", 403)
			return
		}
		seen <- p
		w.Write([]byte("{}"))
	})}
	go server.Serve(l)
	defer server.Close()
	started, err := localipc.ProcessStarted(uint32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	expected := ProcessPeer{PID: uint32(os.Getpid()), Started: started}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := NewProcessClient(identity, expected)
	defer client.Close()
	if err = client.JSON(ctx, "/probe", nil, nil); err != nil {
		t.Fatal(err)
	}
	if p := <-seen; p != expected {
		t.Fatalf("wrong peer %+v", p)
	}
	wrong := NewProcessClient(identity, ProcessPeer{PID: expected.PID, Started: expected.Started + 1})
	defer wrong.Close()
	if err = wrong.JSON(ctx, "/probe", nil, nil); err == nil {
		t.Fatal("wrong creation accepted")
	}
	select {
	case <-seen:
		t.Fatal("rejected peer reached handler")
	default:
	}
	plain := NewClient(identity)
	defer plain.Close()
	if err = plain.JSON(ctx, "/probe", nil, nil); err != nil {
		t.Fatal(err)
	}
}
