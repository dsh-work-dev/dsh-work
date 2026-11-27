package app

import (
	"context"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/dshmanager"
)

func TestManagerOverviewEventsFollowTerminalPluginAndRestoreResults(t *testing.T) {
	f := newRunContextSwitchFixture(t)
	defer f.close()
	f.startReady(t)
	manager := &acceptingPluginManager{Manager: f.manager}
	f.host.deps.Manager = manager
	service := NewManagerService(f.manager, f.host)
	var events []ManagerOverviewEvent
	SetManagerOverviewEvent(service, func(event ManagerOverviewEvent) { events = append(events, event) })
	ctx := LocalClientContext(context.Background(), "workspace")
	request := dshmanager.PluginUpgradeRequest{Target: dshmanager.PluginTarget{Profile: f.target("alpha").Profile}, Package: "@scope/pkg"}
	if _, err := service.UpgradePlugin(context.Background(), request); err == nil || len(events) != 0 {
		t.Fatal("unauthorized attempt produced an operation event")
	}
	if _, err := service.UpgradePlugin(ctx, request); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != "plugin-update" || events[0].State != "completed" {
		t.Fatalf("plugin success event = %+v", events)
	}
	if _, err := service.UpgradePlugins(ctx, dshmanager.PluginUpgradeAllRequest{Target: request.Target, Packages: []string{" "}}); err == nil {
		t.Fatal("invalid upgrade batch unexpectedly succeeded")
	}
	if len(events) != 2 || events[1].Kind != "plugin-update" || events[1].State != "failed" {
		t.Fatalf("plugin failure event = %+v", events)
	}
	if _, err := service.RestorePoint(ctx, "missing-point"); err == nil {
		t.Fatal("missing restore point unexpectedly succeeded")
	}
	if len(events) != 3 || events[2].Kind != "restore" || events[2].State != "failed" {
		t.Fatalf("restore failure event = %+v", events)
	}
	ids := map[string]bool{}
	for _, event := range events {
		if event.ID == "" || ids[event.ID] {
			t.Fatalf("operation identity missing or reused: %+v", event)
		}
		ids[event.ID] = true
		if _, err := time.Parse(time.RFC3339Nano, event.At); err != nil {
			t.Fatal(err)
		}
	}
}
