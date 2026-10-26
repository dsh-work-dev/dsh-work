package dshactivity

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type QuitInspection struct {
	ActiveTasks    bool `json:"activeTasks"`
	ScheduledTasks bool `json:"scheduledTasks"`
}

// InspectQuit uses the current Worker's authenticated activity connection.
func (b *Bridge) InspectQuit(ctx context.Context) (QuitInspection, error) {
	b.mu.Lock()
	client, origin, token, done := b.client, b.origin, b.token, b.done
	b.mu.Unlock()
	if client == nil || done == nil {
		return QuitInspection{}, nil
	}
	select {
	case <-done:
		return QuitInspection{}, nil
	default:
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/__dshwork/quit-inspection", nil)
	if err != nil {
		return QuitInspection{}, err
	}
	req.Header.Set("X-DSH-Work-Token", token)
	req.Header.Set("Origin", origin)
	resp, err := client.Do(req)
	if err != nil {
		return QuitInspection{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return QuitInspection{}, fmt.Errorf("quit inspection status %d", resp.StatusCode)
	}
	var inspection QuitInspection
	err = json.NewDecoder(resp.Body).Decode(&inspection)
	return inspection, err
}
