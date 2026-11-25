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

// InspectQuit checks only the requested Worker's authenticated connection.
// The caller owns deciding whether there is no Worker to inspect.
func (b *Bridge) InspectQuit(ctx context.Context, generation string) (QuitInspection, error) {
	b.mu.Lock()
	client, origin, token, done := b.client, b.origin, b.token, b.done
	currentGeneration := b.generation
	b.mu.Unlock()
	if generation == "" || currentGeneration != generation || client == nil || done == nil {
		return QuitInspection{}, fmt.Errorf("quit inspection unavailable for generation %q", generation)
	}
	select {
	case <-done:
		return QuitInspection{}, fmt.Errorf("quit inspection connection closed")
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
	var payload struct {
		ActiveTasks    *bool `json:"activeTasks"`
		ScheduledTasks *bool `json:"scheduledTasks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return QuitInspection{}, err
	}
	if payload.ActiveTasks == nil || payload.ScheduledTasks == nil {
		return QuitInspection{}, fmt.Errorf("quit inspection missing task state")
	}
	b.mu.Lock()
	current := b.generation == generation && b.done == done && b.client == client
	b.mu.Unlock()
	select {
	case <-done:
		current = false
	default:
	}
	if !current {
		return QuitInspection{}, fmt.Errorf("quit inspection connection changed")
	}
	return QuitInspection{ActiveTasks: *payload.ActiveTasks, ScheduledTasks: *payload.ScheduledTasks}, nil
}
