package desktopapp

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/local/dsh-work/internal/dshactivity"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type quitConfirmation struct {
	busy    atomic.Bool
	inspect func(context.Context) (dshactivity.QuitInspection, error)
	ask     func(dshactivity.QuitInspection, func(bool))
}

func inspectQuitForHost(ctx context.Context, status func() lifecycle.Status, inspect func(context.Context, string) (dshactivity.QuitInspection, error)) (dshactivity.QuitInspection, error) {
	before := status()
	var inspection dshactivity.QuitInspection
	var err error
	// Stopped is published only after cleanup (or before the first launch).
	// Failed and transitional states do not prove that no Worker remains.
	if before.State != lifecycle.StateStopped {
		inspection, err = inspect(ctx, before.GenerationID)
	}
	after := status()
	if before.State != after.State || before.GenerationID != after.GenerationID {
		return dshactivity.QuitInspection{}, errors.New("Worker changed during quit inspection")
	}
	return inspection, err
}

func (q *quitConfirmation) confirm(proceed func()) {
	if !q.busy.CompareAndSwap(false, true) {
		return
	}
	go func() {
		inspection, err := q.inspect(context.Background())
		if err != nil {
			inspection.ActiveTasks = true
		}
		var once sync.Once
		complete := func(yes bool) {
			once.Do(func() {
				defer q.busy.Store(false)
				if yes {
					proceed()
				}
			})
		}
		if !inspection.ActiveTasks && !inspection.ScheduledTasks {
			complete(true)
			return
		}
		q.ask(inspection, complete)
	}()
}

func showQuitQuestion(dialog *application.MessageDialog, complete func(bool)) {
	dialog.AddButton("Yes").SetAsDefault().OnClick(func() { complete(true) })
	dialog.AddButton("No").SetAsCancel().OnClick(func() { complete(false) })
	dialog.Show()
	// Windows Show blocks; dismissing the system dialog may have no button callback.
	// Other desktop backends deliver their button callbacks asynchronously.
	if runtime.GOOS == "windows" {
		complete(false)
	}
}
