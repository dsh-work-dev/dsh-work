package app

import "context"

type localClientSurfaceKey struct{}

// LocalClientContext is used only after the native IPC edge has identified a
// current-user client. WebView roles are stamped by the UI process, never JS.
func LocalClientContext(ctx context.Context, surface string) context.Context {
	return context.WithValue(ctx, localClientSurfaceKey{}, surface)
}

// LocalClientSurface returns the trusted surface assigned at the native IPC
// edge. It does not inspect page-controlled message fields.
func LocalClientSurface(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	surface, _ := ctx.Value(localClientSurfaceKey{}).(string)
	return surface
}
