// Package overviewpc samples read-only machine metrics independently of DSH.
package overviewpc

import (
	"context"
	"time"
)

type Ownership struct {
	Generation string
	PIDs       []int32
}

type Config struct {
	// DataPath selects the volume containing the daemon's persistent data.
	DataPath string
	// OwnedProcesses must return authoritative current Worker Job membership.
	// An absent Worker is an empty successful result, not an unavailable source.
	OwnedProcesses func(context.Context) (Ownership, error)
	Interval       time.Duration
}

type Usage struct {
	UsedBytes  uint64  `json:"usedBytes"`
	TotalBytes uint64  `json:"totalBytes"`
	Percent    float64 `json:"percent"`
}

type DiskUsage struct {
	Usage
	Path string `json:"path"`
}

// Process roles, localized by the UI.
const (
	RoleWorker  = "worker"  // the Worker root: DSH itself
	RoleShell   = "shell"   // a command shell an agent opened
	RoleConsole = "console" // a console host attached to a shell
	RoleMCP     = "mcp"     // a tool server the Worker started
	RoleBrowser = "browser" // a browser an agent controls
	RoleTool    = "tool"    // any other program an agent ran
)

type Process struct {
	PID    int32  `json:"pid"`
	Parent int32  `json:"parent,omitempty"`
	Name   string `json:"name"`
	Role   string `json:"role,omitempty"`
	// CPUPercent uses whole-machine capacity (0..100), not per-core capacity.
	CPUPercent  *float64 `json:"cpuPercent"`
	MemoryBytes *uint64  `json:"memoryBytes"`
	Error       string   `json:"error,omitempty"`
}

type AgentUsage struct {
	CPUPercent  *float64  `json:"cpuPercent"`
	MemoryBytes *uint64   `json:"memoryBytes"`
	Processes   []Process `json:"processes"`
	Error       string    `json:"error,omitempty"`
}

type Point struct {
	At               time.Time `json:"at"`
	CPUPercent       *float64  `json:"cpuPercent"`
	MemoryPercent    *float64  `json:"memoryPercent"`
	DiskPercent      *float64  `json:"diskPercent"`
	AgentCPUPercent  *float64  `json:"agentCPUPercent"`
	AgentMemoryBytes *uint64   `json:"agentMemoryBytes"`
}

type Snapshot struct {
	SampledAt  time.Time         `json:"sampledAt"`
	CPUPercent *float64          `json:"cpuPercent"`
	Memory     *Usage            `json:"memory"`
	Disk       *DiskUsage        `json:"disk"`
	Agents     AgentUsage        `json:"agents"`
	History    []Point           `json:"history"`
	Errors     map[string]string `json:"errors"`
}
