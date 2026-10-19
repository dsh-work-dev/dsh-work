// Package diagnostics derives user-facing problems and support artifacts from
// the process log files.
package diagnostics

import (
	"context"
	"os"
	"time"
)

// Environment is what a report states about the running installation.
type Environment struct {
	AppVersion  string `json:"appVersion"`
	AppChannel  string `json:"appChannel,omitempty"`
	OS          string `json:"os"`
	DSHVersion  string `json:"dshVersion,omitempty"`
	NodeVersion string `json:"nodeVersion,omitempty"`
	Profile     string `json:"profile,omitempty"`
	PluginCount int    `json:"pluginCount"`
	State       string `json:"state,omitempty"`
}

// Problem is a run of WARN/ERROR records with one error code.
type Problem struct {
	ID        string `json:"id"`
	Code      string `json:"code"`
	Level     string `json:"level"`
	Message   string `json:"message"`
	Proc      string `json:"proc"`
	Gen       string `json:"gen,omitempty"`
	Op        string `json:"op,omitempty"`
	FirstTime string `json:"firstTime"`
	LastTime  string `json:"lastTime"`
	Count     int    `json:"count"`
}

// BundleResult describes a written diagnostics bundle.
type BundleResult struct {
	Path    string   `json:"path"`
	Size    int64    `json:"size"`
	Omitted []string `json:"omitted,omitempty"`
}

// Config supplies log locations and installation details to Diagnostics.
type Config struct {
	Dir            string
	Environment    func(context.Context) Environment
	Now            func() time.Time
	HomeDir        string
	HostName       string
	MaxBundleBytes int64
}

// Diagnostics derives problems and support artifacts from the log directory.
type Diagnostics struct {
	config Config
}

// New normalizes config and returns a diagnostics reader.
func New(config Config) *Diagnostics {
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.HomeDir == "" {
		config.HomeDir, _ = os.UserHomeDir()
	}
	if config.HostName == "" {
		config.HostName, _ = os.Hostname()
	}
	if config.MaxBundleBytes == 0 {
		config.MaxBundleBytes = 24 << 20
	}
	if config.Environment == nil {
		config.Environment = func(context.Context) Environment { return Environment{} }
	}
	return &Diagnostics{config: config}
}

// Dir returns the directory containing the process logs.
func (d *Diagnostics) Dir() string {
	return d.config.Dir
}

func (d *Diagnostics) environment(ctx context.Context) Environment {
	environment := d.config.Environment(ctx)
	environment.OS = osDescription()
	return environment
}
