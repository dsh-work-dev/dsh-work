package daemon

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"github.com/local/dsh-work/internal/hostevents"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Transport qualification only: native peer admission is exercised by the real UI gate.
func TestAdmittedStreamsReplaceOnlyTheirOwnWindow(t *testing.T) {
	doneA, doneB := make(chan struct{}), make(chan struct{})
	var retiredA, retiredB atomic.Int32
	h := hostevents.New()
	s := &Server{Hub: h, UIStreamScope: func(_ context.Context, key string) (UIStreamAdmission, error) {
		switch key {
		case "a":
			return UIStreamAdmission{Key: key, Windows: []string{"workspace"}, Done: doneA, Retire: func() { retiredA.Add(1) }}, nil
		case "b":
			return UIStreamAdmission{Key: key, Windows: []string{"workspace-2"}, Done: doneB, Retire: func() { retiredB.Add(1) }}, nil
		}
		return UIStreamAdmission{}, errors.New("denied")
	}}
	server := httptest.NewServer(s)
	defer server.Close()
	a, e := server.Client().Get(server.URL + "/stream?windowLease=a")
	if e != nil {
		t.Fatal(e)
	}
	defer a.Body.Close()
	b, e := server.Client().Get(server.URL + "/stream?windowLease=b")
	if e != nil {
		t.Fatal(e)
	}
	defer b.Body.Close()
	a2, e := server.Client().Get(server.URL + "/stream?windowLease=a")
	if e != nil {
		t.Fatal(e)
	}
	defer a2.Body.Close()
	if _, e := bufio.NewReader(a.Body).ReadString('\n'); e == nil {
		t.Fatal("old A remains live")
	}
	close(doneA)
	if _, e := bufio.NewReader(a2.Body).ReadString('\n'); e == nil {
		t.Fatal("closed A lease remains live")
	}
	h.SendCommand("workspace-2", "still-alive", time.Minute, true)
	if line, e := bufio.NewReader(b.Body).ReadString('\n'); e != nil || len(line) == 0 {
		t.Fatalf("B stream lost: %q %v", line, e)
	}
	if retiredB.Load() != 0 {
		t.Fatal("A retirement cleared B")
	}
	close(doneB)
}

func TestWaitingReplacementHasNoEffectsAfterAdmissionExpires(t *testing.T) {
	for _, reason := range []string{"canceled", "retired"} {
		t.Run(reason, func(t *testing.T) {
			done := make(chan struct{})
			entered := make(chan struct{})
			var canceled atomic.Int32
			owner := &uiStreamOwner{cancel: func() { canceled.Add(1) }}
			owner.start.Lock()
			h := hostevents.New()
			h.SendCommand("workspace", "staged", time.Minute, true)
			s := &Server{Hub: h, uiStreams: map[string]*uiStreamOwner{"lease": owner}, UIStreamScope: func(context.Context, string) (UIStreamAdmission, error) {
				close(entered)
				return UIStreamAdmission{Key: "lease", Windows: []string{"workspace"}, Done: done}, nil
			}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			finished := make(chan struct{})
			go func() {
				s.serveUIStream(httptest.NewRecorder(), httptest.NewRequest("GET", "/stream", nil).WithContext(ctx))
				close(finished)
			}()
			<-entered
			if reason == "canceled" {
				cancel()
			} else {
				close(done)
			}
			owner.start.Unlock()
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("replacement remained blocked")
			}
			if canceled.Load() != 0 {
				t.Fatal("invalid replacement canceled valid predecessor")
			}
			sub, initial := h.SubscribeWindows([]string{"workspace"})
			defer sub.Close()
			if len(initial) != 1 || initial[0].Name != "staged" {
				t.Fatalf("invalid replacement consumed staged: %+v", initial)
			}
		})
	}
}

func TestWindowLeaseRetiresOwnerAfterHandlerDisconnected(t *testing.T) {
	for _, early := range []bool{false, true} {
		t.Run(fmt.Sprint(early), func(t *testing.T) {
			done := make(chan struct{})
			retired := make(chan struct{}, 1)
			s := &Server{Hub: hostevents.New(), UIStreamScope: func(context.Context, string) (UIStreamAdmission, error) {
				return UIStreamAdmission{Key: "lease", Windows: []string{"workspace"}, Done: done, Retire: func() { retired <- struct{}{} }}, nil
			}}
			ctx, cancel := context.WithCancel(context.Background())
			if early {
				cancel()
			} else {
				s.Hub.SetTopic("state", true)
			}
			finished := make(chan struct{})
			writer := httptest.NewRecorder()
			go func() {
				s.serveUIStream(writer, httptest.NewRequest("GET", "/stream", nil).WithContext(ctx))
				close(finished)
			}()
			if !early {
				// Retire callback confirms the active handler has relinquished its owner.
				for deadline := time.Now().Add(time.Second); ; {
					s.uiStreamsMu.Lock()
					o := s.uiStreams["lease"]
					s.uiStreamsMu.Unlock()
					active := false
					if o != nil {
						o.mu.Lock()
						active = o.cancel != nil
						o.mu.Unlock()
					}
					if active {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("stream not active")
					}
					time.Sleep(time.Millisecond)
				}
				cancel()
				select {
				case <-retired:
				case <-time.After(time.Second):
					t.Fatal("handler did not retire")
				}
			}
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("handler did not finish")
			}
			s.uiStreamsMu.Lock()
			before := len(s.uiStreams)
			s.uiStreamsMu.Unlock()
			if before != 1 {
				t.Fatal("window owner not retained while live")
			}
			close(done)
			for deadline := time.Now().Add(time.Second); ; {
				s.uiStreamsMu.Lock()
				n := len(s.uiStreams)
				s.uiStreamsMu.Unlock()
				if n == 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("closed lease leaked owner")
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
}
