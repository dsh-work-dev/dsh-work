package splash

import (
	"context"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

type testWindow struct {
	application.Window
	name string
}

func (w testWindow) Name() string { return w.name }

func windowContext(name string) context.Context {
	return context.WithValue(context.Background(), application.WindowKey, testWindow{name: name})
}

func TestServiceRequiresTheWorkspaceWindow(t *testing.T) {
	store, _ := newTestStore(t)
	service := &SplashService{Store: store}
	if _, err := service.List(context.Background()); err == nil {
		t.Fatal("no window must be refused")
	}
	if _, err := service.List(windowContext("pet")); err == nil {
		t.Fatal("pet window must be refused")
	}
	if items, err := service.List(windowContext("workspace")); err != nil || len(items) != 1 {
		t.Fatalf("workspace: %v %v", items, err)
	}
}

func TestServiceImportCancelAndChoose(t *testing.T) {
	store, src := newTestStore(t)
	path := writeFile(t, src, "a.png", 1)
	choice := ""
	service := &SplashService{Store: store, ChooseFile: func() (string, error) { return choice, nil }}
	ctx := windowContext("workspace")
	if got, err := service.Import(ctx); err != nil || got != nil {
		t.Fatalf("cancel: %v %v", got, err)
	}
	choice = path
	got, err := service.Import(ctx)
	if err != nil || got == nil || got.Kind != "image" {
		t.Fatalf("import: %+v %v", got, err)
	}
	if current, _ := service.Current(ctx); current.ID != BuiltinWhale {
		t.Fatal("import must not select")
	}
}
