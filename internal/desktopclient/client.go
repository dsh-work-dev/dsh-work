package desktopclient

import (
	"context"
	"errors"

	"github.com/local/dsh-work/internal/daemon"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func call(ctx context.Context, client *daemon.Client, service, method string, args []any, result any) error {
	window, ok := ctx.Value(application.WindowKey).(application.Window)
	if !ok || window == nil || (window.Name() != "workspace" && window.Name() != "settings") {
		return errors.New("trusted desktop window required")
	}
	return client.Call(ctx, service, method, window.Name(), args, result)
}
