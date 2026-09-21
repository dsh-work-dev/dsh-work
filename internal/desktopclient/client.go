package desktopclient

import (
	"context"
	"errors"

	"github.com/local/dsh-work/internal/daemon"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func call(ctx context.Context, client *daemon.Client, service, method string, args []any, result any) error {
	window, err := trustedWindow(ctx)
	if err != nil {
		return err
	}
	return client.Call(ctx, service, method, window.Name(), args, result)
}

func callJSON(ctx context.Context, client *daemon.Client, path string, input, output any) error {
	if _, err := trustedWindow(ctx); err != nil {
		return err
	}
	return client.JSON(ctx, path, input, output)
}

func trustedWindow(ctx context.Context) (application.Window, error) {
	window, ok := ctx.Value(application.WindowKey).(application.Window)
	if !ok || window == nil || (window.Name() != "workspace" && window.Name() != "settings") {
		return nil, errors.New("trusted desktop window required")
	}
	return window, nil
}
