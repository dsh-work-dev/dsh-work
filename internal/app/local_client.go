package app

import "context"

type localClientSurfaceKey struct{}

// LocalClientContext is used only after the native IPC edge has identified a
// current-user client. WebView roles are stamped by the UI process, never JS.
func LocalClientContext(ctx context.Context, surface string) context.Context {
	return context.WithValue(ctx, localClientSurfaceKey{}, surface)
}
