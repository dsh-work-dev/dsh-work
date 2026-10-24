package desktopapp

import (
	"sync"

	"github.com/local/dsh-work/internal/lifecycle"
)

type lifecycleTopicValue struct {
	status    lifecycle.Status
	workerURL string
}

// serializedCurrentPublisher keeps the source read and publication in one
// critical section so concurrent notifications cannot publish stale values
// after newer ones.
type serializedCurrentPublisher[T any] struct {
	mu          sync.Mutex
	readCurrent func() (T, error)
	publish     func(T)
}

func (p *serializedCurrentPublisher[T]) publishCurrent() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	value, err := p.readCurrent()
	if err != nil {
		return err
	}
	p.publish(value)
	return nil
}
