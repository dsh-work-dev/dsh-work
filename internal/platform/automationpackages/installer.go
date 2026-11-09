// Package automationpackages publishes the application's locked npm collections.
// Platform callers supply their existing command owner, selected toolchain and
// atomic file replacement; this module does not select a platform capability.
package automationpackages

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/local/dsh-work/internal/acquisition"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/settings"
)

type Failure struct {
	Code  string
	Cause error
}

func (e Failure) Error() string             { return "automation preparation failed: " + e.Code }
func (e Failure) Unwrap() error             { return e.Cause }
func (e Failure) AutomationFailure() string { return e.Code }

type Installer struct {
	Root       string
	Executor   dshadapter.CommandExecutor
	Replacer   settings.FileReplacer
	Resolve    func(context.Context) (dshmanager.ResolvedNode, error)
	NPMCommand string
}

func containsAny(text string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}

func (i *Installer) Ready(c automation.Capability) bool {
	dir, e := automation.PublishedPackage(i.Root, c)
	if e != nil {
		return false
	}
	files, e := automation.DependencyFiles(c)
	if e != nil {
		return false
	}
	for name, want := range files {
		got, e := os.ReadFile(filepath.Join(dir, name))
		if e != nil || string(got) != string(want) {
			return false
		}
	}
	if Validate(dir, c) != nil {
		return false
	}
	return true
}
func (i *Installer) Prepare(ctx context.Context, c automation.Capability, report func(automation.Progress), force bool) error {
	files, err := automation.DependencyFiles(c)
	if err != nil {
		return err
	}
	valid := func(dir string) bool {
		for name, want := range files {
			got, e := os.ReadFile(filepath.Join(dir, name))
			if e != nil || string(got) != string(want) {
				return false
			}
		}
		return Validate(dir, c) == nil
	}
	publish := func(dir string) error {
		root := automation.PackageRoot(i.Root, c)
		if err := os.MkdirAll(root, 0700); err != nil {
			return err
		}
		rel, e := filepath.Rel(root, dir)
		if e != nil {
			return e
		}
		b, e := json.Marshal(automation.PackagePublication{Revision: automation.PackageRevision(c), Directory: rel})
		if e != nil {
			return e
		}
		f, e := os.CreateTemp(root, "current-*.tmp")
		if e != nil {
			return e
		}
		defer os.Remove(f.Name())
		defer f.Close()
		if _, e = f.Write(b); e != nil {
			return e
		}
		if e = f.Sync(); e != nil {
			return e
		}
		if e = f.Close(); e != nil {
			return e
		}
		return i.Replacer.Replace(f.Name(), automation.PackagePointer(i.Root, c))
	}
	if !force {
		if dir, e := automation.PublishedPackage(i.Root, c); e == nil && valid(dir) {
			report(automation.Progress{Step: "verify"})
			return nil
		}
		// Adopt the old valid directory without mutating it; running jobs may own it.
		legacy := filepath.Join(automation.PackageRoot(i.Root, c), automation.DSHVersion)
		if valid(legacy) {
			report(automation.Progress{Step: "verify"})
			return publish(legacy)
		}
	}
	var command string
	var args []string
	var env map[string]string
	if i.Resolve != nil {
		node, e := i.Resolve(ctx)
		if e != nil {
			return Failure{"toolchain", e}
		}
		cli := filepath.Join(filepath.Dir(node.NodePath), "node_modules", "npm", "bin", "npm-cli.js")
		if info, e := os.Stat(cli); e == nil && !info.IsDir() {
			command = node.NodePath
			args = []string{cli}
		} else if node.NPMPath != "" {
			command = node.NPMPath
		} else {
			return Failure{"toolchain", errors.New("selected Node installation has no npm; install or repair Node in Runtimes")}
		}
		env = node.ChildEnvironment
	} else {
		command, err = exec.LookPath(i.NPMCommand)
		if err != nil {
			return Failure{"toolchain", err}
		}
	}
	stageRoot := filepath.Join(i.Root, ".staging")
	if err = os.MkdirAll(stageRoot, 0700); err != nil {
		return err
	}
	adapter := automationPackageAdapter{executor: i.Executor, files: files, command: command, args: args, environment: env}
	report(automation.Progress{Step: "download"})
	result, err := acquisition.Acquire(ctx, acquisition.Request{OperationID: "automation-" + string(c), Identity: acquisition.ArtifactIdentity{Kind: acquisition.ArtifactPlugin, Name: "dsh-work-" + string(c), Version: automation.PackageRevision(c)}, Candidates: []acquisition.SourceCandidate{{Route: acquisition.RouteOfficial, Location: "https://registry.npmjs.org/"}}, StagingRoot: stageRoot, StagePrefix: "automation-"}, adapter, nil)
	if err != nil {
		return PreparationError(err)
	}
	defer os.RemoveAll(result.StagingPath)
	report(automation.Progress{Step: "verify"})
	if err = Validate(result.PayloadPath, c); err != nil {
		return Failure{"verify", err}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	releases := filepath.Join(automation.PackageRoot(i.Root, c), "releases", automation.PackageRevision(c))
	if err = os.MkdirAll(releases, 0700); err != nil {
		return err
	}
	// Every publication is immutable, including a repair of the same lock.
	destination, err := os.MkdirTemp(releases, "set-")
	if err != nil {
		return err
	}
	if err = os.Remove(destination); err != nil {
		return err
	}
	if err = os.Rename(result.PayloadPath, destination); err != nil {
		return err
	}
	// A failed pointer replacement leaves the previous complete set selected.
	return publish(destination)
}

func PreparationError(err error) error {
	var f acquisition.Failure
	if errors.As(err, &f) {
		if f.Kind == acquisition.FailureIntegrity {
			return Failure{"verify", err}
		}
		if f.Kind == acquisition.FailureReachability || f.Kind == acquisition.FailureNotFound {
			return Failure{"network", err}
		}
	}
	return Failure{"other", err}
}

type automationPackageAdapter struct {
	executor    dshadapter.CommandExecutor
	files       map[string][]byte
	command     string
	args        []string
	environment map[string]string
}

func (a automationPackageAdapter) Attempt(ctx context.Context, r acquisition.AttemptRequest) (acquisition.AttemptResult, error) {
	for name, data := range a.files {
		if err := os.WriteFile(filepath.Join(r.StagingPath, name), data, 0600); err != nil {
			return acquisition.AttemptResult{}, err
		}
	}
	command := a.command
	args := append(append([]string{}, a.args...), "ci", "--ignore-scripts", "--no-audit", "--no-fund")
	result, err := a.executor.Run(ctx, command, args, a.environment, r.StagingPath)
	if err != nil {
		return acquisition.AttemptResult{}, ClassifyPackageManagerFailure(result, err)
	}
	payload := filepath.Join(r.StagingPath, "payload")
	if err := os.Mkdir(payload, 0700); err != nil {
		return acquisition.AttemptResult{}, err
	}
	for _, name := range []string{"package.json", "package-lock.json", "node_modules"} {
		if err := os.Rename(filepath.Join(r.StagingPath, name), filepath.Join(payload, name)); err != nil {
			return acquisition.AttemptResult{}, err
		}
	}
	return acquisition.AttemptResult{ResolvedIdentity: r.Identity, PayloadPath: payload}, nil
}
func Validate(root string, c automation.Capability) error {
	if c == automation.AUV {
		info, err := os.Stat(filepath.Join(root, "node_modules", "@auv-js", "cli-win32-x64-msvc", "bin", "auv.exe"))
		if err != nil {
			return err
		}
		if info.IsDir() || info.Size() == 0 {
			return errors.New("AUV executable missing")
		}
	}
	expected := map[string]string{"@modelcontextprotocol/sdk": automation.SDKVersion, "@playwright/mcp": automation.MCPVersion, "ws": automation.BrowserWebSocketVersion}
	if c == automation.AUV {
		expected = map[string]string{"@auv-js/sdk": automation.AUVVersion, "@auv-js/cli-win32-x64-msvc": automation.AUVVersion}
	}
	if c == automation.Computer {
		expected = map[string]string{"@deepseek-ai/dsh-computer-use": automation.DSHVersion, "@deepseek-ai/dsh-experimental-computer-use-cua-driver-mcp": automation.DSHVersion}
	}
	for name, version := range expected {
		data, err := os.ReadFile(filepath.Join(root, "node_modules", filepath.FromSlash(name), "package.json"))
		if err != nil {
			return err
		}
		var m struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(data, &m) != nil || m.Version != version {
			return errors.New("automation package version mismatch")
		}
	}
	return nil
}
func ClassifyPackageManagerFailure(result dshadapter.CommandResult, err error) acquisition.Failure {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return acquisition.Failure{Kind: acquisition.FailureCanceled, Summary: "package acquisition was canceled", Cause: err}
	}
	message := ""
	if err != nil {
		message = err.Error()
	}
	text := strings.ToLower(result.Stdout + "\n" + result.Stderr + "\n" + message)
	if containsAny(text, "eintegrity", "integrity", "checksum") {
		return acquisition.Failure{Kind: acquisition.FailureIntegrity, Summary: "package integrity verification failed", Cause: err}
	}
	if containsAny(text, "e401", "401 unauthorized", "authentication required", "unable to authenticate", "self_signed", "certificate") {
		return acquisition.Failure{Kind: acquisition.FailureAuthentication, Summary: "package source authentication failed", Cause: err}
	}
	if containsAny(text, "e403", "403 forbidden", "authorization") {
		return acquisition.Failure{Kind: acquisition.FailureAuthorization, Summary: "package source authorization failed", Cause: err}
	}
	if containsAny(text, "e404", "404 not found", "no matching version", "notarget", " etarget") {
		return acquisition.Failure{Kind: acquisition.FailureNotFound, Summary: "the requested package was not found", Cause: err}
	}
	if containsAny(text,
		"enotfound", "eai_again", "econnrefused", "econnreset", "etimedout", "enetunreach", "ehostunreach",
		"err_socket_timeout", "fetch failed", "no such host", "name or service not known",
		"temporary failure in name resolution", "network is unreachable", "connection reset", "connection refused", "timeout",
	) {
		return acquisition.Failure{Kind: acquisition.FailureReachability, Summary: "package source could not be reached", Cause: err}
	}
	return acquisition.Failure{Kind: acquisition.FailureSemantic, Summary: "package manager rejected the acquisition", Cause: err}
}
