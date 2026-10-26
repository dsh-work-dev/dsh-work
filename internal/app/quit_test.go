package app

import (
	"context"
	"testing"
)

func TestRequestQuitDefersInteractiveExitAndPreservesDirectQuit(t *testing.T) {
	host := NewHost(Dependencies{}, Config{})
	quits := 0
	host.SetQuitHandler(func() { quits++ })
	s := NewHostService(host, nil)
	var proceed func()
	s.ConfirmQuit = func(next func()) { proceed = next }
	ctx := LocalClientContext(context.Background(), "workspace")
	s.RequestQuit(context.Background())
	if proceed != nil {
		t.Fatal("untrusted request reached confirmation")
	}
	s.RequestQuit(ctx)
	if quits != 0 || proceed == nil {
		t.Fatalf("quits=%d confirmation=%v", quits, proceed != nil)
	}
	proceed()
	if quits != 1 {
		t.Fatal("confirmed request did not quit")
	}
	proceed = nil
	s.Quit(ctx)
	if quits != 2 || proceed != nil {
		t.Fatal("direct Quit was changed to interactive confirmation")
	}
}
