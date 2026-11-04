package desktopapp

import (
	"context"
	"sync"

	"github.com/local/dsh-work/internal/browserhost"
)

// The registration layer remains platform-neutral at the desktop boundary.
// Windows is the implemented backend; extracting the development module is an
// explicit configuration action, independent of execution authorization.
type developmentBrowserRegistrar struct {
	mu sync.Mutex
	browserhost.Registrar
	config      browserhost.Config
	maintenance func() (func(), error)
}

func (r *developmentBrowserRegistrar) Ensure(ctx context.Context) []browserhost.Registration {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.maintenance != nil {
		release, err := r.maintenance()
		if err != nil {
			return []browserhost.Registration{{Browser: "chrome", Failure: "update-busy"}, {Browser: "edge", Failure: "update-busy"}}
		}
		defer release()
	}
	result := r.Registrar.Ensure(ctx)
	ready := false
	for _, row := range result {
		ready = ready || row.Registered
	}
	if !ready {
		return result
	}
	if e := browserhost.InstallDevelopmentBundle(ctx, r.config); e != nil {
		for i := range result {
			if result[i].Failure == "" {
				result[i].Failure = "write"
			}
		}
	}
	return result
}
