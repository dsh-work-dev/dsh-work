package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/dshmanager"
)

type onlineManagerTestService struct {
	called string
}

func (s *onlineManagerTestService) GetSnapshot(context.Context) (dshmanager.Snapshot, error) {
	s.called = "GetSnapshot"
	return dshmanager.Snapshot{}, nil
}

func (s *onlineManagerTestService) InstallRuntime(context.Context, string) (dshmanager.Snapshot, error) {
	s.called = "InstallRuntime"
	return dshmanager.Snapshot{}, nil
}

func (s *onlineManagerTestService) RegisterRuntime(context.Context, dshmanager.RuntimeInfo) (dshmanager.Snapshot, error) {
	s.called = "RegisterRuntime"
	return dshmanager.Snapshot{}, nil
}

func (s *onlineManagerTestService) RemoveRuntime(context.Context, string) (dshmanager.Snapshot, error) {
	s.called = "RemoveRuntime"
	return dshmanager.Snapshot{}, nil
}

func (s *onlineManagerTestService) RegisterDataDirectory(context.Context, dshmanager.DataDirectoryInfo) (dshmanager.Snapshot, error) {
	s.called = "RegisterDataDirectory"
	return dshmanager.Snapshot{}, nil
}

func (s *onlineManagerTestService) RemoveDataDirectory(context.Context, string) (dshmanager.Snapshot, error) {
	s.called = "RemoveDataDirectory"
	return dshmanager.Snapshot{}, nil
}

func (s *onlineManagerTestService) SetRunContext(context.Context, dshmanager.RunContext) (dshmanager.Snapshot, error) {
	s.called = "SetRunContext"
	return dshmanager.Snapshot{}, nil
}

func (s *onlineManagerTestService) ListPlugins(context.Context, dshmanager.PluginListRequest) ([]dshmanager.PluginInfo, error) {
	s.called = "ListPlugins"
	return nil, nil
}

func (s *onlineManagerTestService) InstallPlugin(context.Context, dshmanager.PluginInstallRequest) (dshmanager.PluginResult, error) {
	s.called = "InstallPlugin"
	return dshmanager.PluginResult{}, nil
}

func (s *onlineManagerTestService) RemovePlugin(context.Context, dshmanager.PluginRemoveRequest) (dshmanager.PluginResult, error) {
	s.called = "RemovePlugin"
	return dshmanager.PluginResult{}, nil
}

type daemonHandlerRoundTripper struct {
	handler http.Handler
}

func (t daemonHandlerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	t.handler.ServeHTTP(recorder, request)
	response := recorder.Result()
	response.Request = request
	return response, nil
}

func TestOnlineManagerCallsAreAcceptedByDaemonInvoke(t *testing.T) {
	type invokeCall struct {
		name string
		run  func(onlineManager) error
	}

	ctx := context.Background()
	calls := []invokeCall{
		{name: "GetSnapshot", run: func(manager onlineManager) error { _, err := manager.Snapshot(ctx); return err }},
		{name: "InstallRuntime", run: func(manager onlineManager) error { _, err := manager.InstallRuntime(ctx, ""); return err }},
		{name: "RegisterRuntime", run: func(manager onlineManager) error {
			_, err := manager.RegisterRuntime(ctx, dshmanager.RuntimeInfo{})
			return err
		}},
		{name: "RemoveRuntime", run: func(manager onlineManager) error { _, err := manager.RemoveRuntime(ctx, ""); return err }},
		{name: "RegisterDataDirectory", run: func(manager onlineManager) error {
			_, err := manager.RegisterDataDirectory(ctx, dshmanager.DataDirectoryInfo{})
			return err
		}},
		{name: "RemoveDataDirectory", run: func(manager onlineManager) error { _, err := manager.RemoveDataDirectory(ctx, ""); return err }},
		{name: "SetRunContext", run: func(manager onlineManager) error {
			_, err := manager.SetConfigured(ctx, dshmanager.RunContext{})
			return err
		}},
		{name: "ListPlugins", run: func(manager onlineManager) error {
			_, err := manager.ListPlugins(ctx, dshmanager.PluginListRequest{})
			return err
		}},
		{name: "InstallPlugin", run: func(manager onlineManager) error {
			_, err := manager.InstallPlugin(ctx, dshmanager.PluginInstallRequest{})
			return err
		}},
		{name: "RemovePlugin", run: func(manager onlineManager) error {
			_, err := manager.RemovePlugin(ctx, dshmanager.PluginRemoveRequest{})
			return err
		}},
	}

	for _, call := range calls {
		t.Run(call.name, func(t *testing.T) {
			service := &onlineManagerTestService{}
			server := &daemon.Server{Services: map[string]any{"ManagerService": service}}
			client := &daemon.Client{HTTP: &http.Client{Transport: daemonHandlerRoundTripper{handler: server}}}
			if err := call.run(onlineManager{client: client}); err != nil {
				t.Fatalf("online call was rejected: %v", err)
			}
			if service.called != call.name {
				t.Fatalf("invoked method = %q, want %q", service.called, call.name)
			}
		})
	}
}
