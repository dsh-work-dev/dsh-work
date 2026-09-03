package dshadapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/local/work/internal/lifecycle"
	"github.com/local/work/internal/supervisor"
	"github.com/local/work/internal/workspacecontext"
)

const SupportedVersion = "0.1.2-alpha.3"

type Runtime struct {
	Path    string
	Version string
}

type CommandResult struct {
	Stdout string
	Stderr string
}

// CommandExecutor is deliberately smaller than os/exec. The Windows
// implementation owns .cmd/.bat invocation details; tests can use a fake.
type CommandExecutor interface {
	Run(context.Context, string, []string, map[string]string, string) (CommandResult, error)
}

type ReadyAnnouncement struct {
	URL string
}

// LaunchContext is the immutable input for one Worker generation. Runtime,
// data-directory and profile come from the global launch target; Workspace is
// resolved separately for this generation and is never persisted by the
// manager.
type LaunchContext struct {
	GenerationID       string
	Runtime            Runtime
	BootstrapDirectory string
	DataDirectory      string
	Profile            string
	Workspace          workspacecontext.Context
	Port               int
}

type Adapter struct {
	executor           CommandExecutor
	expectedVersion    string
	executableOverride string
	discoveryRoot      string
	client             *http.Client
}

func New(executor CommandExecutor, expectedVersion string) *Adapter {
	if expectedVersion == "" {
		expectedVersion = SupportedVersion
	}
	return &Adapter{
		executor:        executor,
		expectedVersion: expectedVersion,
		client: &http.Client{
			Timeout: 2 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (a *Adapter) SetExecutableOverride(path string) {
	a.executableOverride = path
}

// SetDiscoveryRoot configures the repository/install root used only to find a
// DSH executable. It is never used as a DSH Workspace or launch context.
func (a *Adapter) SetDiscoveryRoot(path string) {
	a.discoveryRoot = path
}

// RuntimeHint exposes the configured executable/version pair to a catalog
// owner without claiming that the executable exists. The manager can show the
// candidate immediately, while DiscoverPath remains the authoritative check
// before a Worker starts.
func (a *Adapter) RuntimeHint() Runtime {
	return Runtime{Path: a.executableHint(), Version: a.expectedVersion}
}

func (a *Adapter) Discover(ctx context.Context) (Runtime, error) {
	if a.executor == nil {
		return Runtime{}, lifecycle.Failure{
			Code:      lifecycle.ErrorPlatformUnsupported,
			Summary:   "This platform has no native DSH command adapter yet.",
			Retryable: false,
		}
	}
	path, err := a.locateExecutable()
	if err != nil {
		return Runtime{}, lifecycle.Failure{
			Code:      lifecycle.ErrorDSHRuntimeNotFound,
			Summary:   "A compatible local DSH runtime was not found.",
			Retryable: false,
			Detail:    "Set WORK_DSH_EXECUTABLE or run task setup:dsh.",
		}
	}
	return a.DiscoverPath(ctx, path)
}

// DiscoverPath verifies one catalog-selected executable. Runtime management
// may expose several paths, but every selected path still crosses this DSH
// adapter so version compatibility is checked by the pinned DSH contract.
func (a *Adapter) DiscoverPath(ctx context.Context, path string) (Runtime, error) {
	path, err := existingExecutable(path)
	if err != nil {
		return Runtime{}, lifecycle.Failure{
			Code:      lifecycle.ErrorDSHRuntimeNotFound,
			Summary:   "The selected DSH runtime was not found.",
			Retryable: false,
		}
	}
	result, err := a.executor.Run(ctx, path, []string{"--version"}, nil, "")
	if err != nil {
		return Runtime{}, lifecycle.Failure{
			Code:      lifecycle.ErrorDSHVersionCheckFailed,
			Summary:   "The configured DSH runtime could not report its version.",
			Retryable: false,
		}
	}
	version := ParseVersion(result.Stdout + "\n" + result.Stderr)
	if version == "" || version != a.expectedVersion {
		return Runtime{}, lifecycle.Failure{
			Code:      lifecycle.ErrorDSHUnsupportedVersion,
			Summary:   "The configured DSH version is not supported by this Work build.",
			Retryable: false,
			Detail:    "Expected " + a.expectedVersion,
		}
	}
	return Runtime{Path: path, Version: version}, nil
}

// Verify checks a catalog-selected executable against the DSH adapter
// contract. It intentionally returns the adapter's stable lifecycle failure
// so the manager can project it without knowing DSH CLI details.
func (a *Adapter) Verify(ctx context.Context, path, expectedVersion string) error {
	runtime, err := a.DiscoverPath(ctx, path)
	if err != nil {
		return err
	}
	if expectedVersion != "" && runtime.Version != expectedVersion {
		return lifecycle.Failure{
			Code:    lifecycle.ErrorDSHUnsupportedVersion,
			Summary: "The selected DSH runtime does not match the catalog version.",
			Detail:  "Expected " + expectedVersion,
		}
	}
	return nil
}

// BuildLaunchPlan constructs one generation's DSH process plan. The bootstrap
// directory is an explicit Work application-data directory used only while
// DSH's Workspace surface is selecting a Workspace. A selected Workspace is
// passed as the explicit process context; the DSH data directory remains the
// value exported through DSH_HOME.
func (a *Adapter) BuildLaunchPlan(launch LaunchContext) (supervisor.LaunchPlan, error) {
	if launch.Runtime.Path == "" || launch.Runtime.Version != a.expectedVersion {
		return supervisor.LaunchPlan{}, fmt.Errorf("runtime is not the pinned DSH version")
	}
	if !validProfileName(launch.Profile) {
		return supervisor.LaunchPlan{}, fmt.Errorf("invalid DSH profile name")
	}
	if strings.TrimSpace(launch.BootstrapDirectory) == "" {
		return supervisor.LaunchPlan{}, fmt.Errorf("bootstrap directory is required")
	}
	if strings.TrimSpace(launch.DataDirectory) == "" {
		return supervisor.LaunchPlan{}, fmt.Errorf("DSH data directory is required")
	}
	if err := launch.Workspace.ValidateForGeneration(launch.GenerationID); err != nil {
		return supervisor.LaunchPlan{}, fmt.Errorf("workspace context is invalid: %w", err)
	}
	bootstrapDirectory, err := filepath.Abs(launch.BootstrapDirectory)
	if err != nil {
		return supervisor.LaunchPlan{}, fmt.Errorf("resolve bootstrap directory: %w", err)
	}
	dataDirectory, err := filepath.Abs(launch.DataDirectory)
	if err != nil {
		return supervisor.LaunchPlan{}, fmt.Errorf("resolve DSH data directory: %w", err)
	}
	workingDirectory := filepath.Clean(bootstrapDirectory)
	if launch.Workspace.State == workspacecontext.StateSelected {
		workingDirectory = launch.Workspace.Path
	}
	origin := "http://127.0.0.1:" + strconv.Itoa(launch.Port)
	plan := supervisor.LaunchPlan{
		GenerationID:     launch.GenerationID,
		Executable:       launch.Runtime.Path,
		Args:             []string{"--profile", launch.Profile, "--host", "127.0.0.1", "--port", strconv.Itoa(launch.Port), "--no-open"},
		Env:              map[string]string{"DSH_HOME": dataDirectory},
		WorkingDirectory: workingDirectory,
		ExpectedOrigin:   origin,
		ExpectedHost:     "127.0.0.1",
		ExpectedPort:     launch.Port,
	}
	if err := plan.Validate(); err != nil {
		return supervisor.LaunchPlan{}, err
	}
	return plan, nil
}

func validProfileName(profile string) bool {
	return profile != "" && profile != "." && profile != ".." &&
		filepath.Base(profile) == profile && !strings.ContainsAny(profile, `/\\`) && !strings.ContainsRune(profile, '\x00')
}

func (a *Adapter) ParseReadyAnnouncement(text string) (ReadyAnnouncement, bool) {
	text = stripANSI(text)
	match := readyLinePattern.FindStringSubmatch(text)
	if len(match) != 2 {
		return ReadyAnnouncement{}, false
	}
	return ReadyAnnouncement{URL: strings.TrimRight(match[1], ".,")}, true
}

func (a *Adapter) ValidateReady(announcement ReadyAnnouncement, plan supervisor.LaunchPlan) error {
	u, err := url.Parse(announcement.URL)
	if err != nil || u.Scheme != "http" || u.Hostname() != plan.ExpectedHost || u.Port() != strconv.Itoa(plan.ExpectedPort) || u.User != nil || u.Fragment != "" || (u.Path != "" && u.Path != "/") || !validSessionQuery(u.RawQuery) {
		return lifecycle.Failure{
			Code:      lifecycle.ErrorDSHInvalidReadiness,
			Summary:   "DSH announced an untrusted workspace origin.",
			Retryable: false,
		}
	}
	return nil
}

func validSessionQuery(rawQuery string) bool {
	if rawQuery == "" {
		return true
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil || len(values) != 1 {
		return false
	}
	tokens, ok := values["token"]
	return ok && len(tokens) == 1 && tokens[0] != "" && len(tokens[0]) <= 512
}

func (a *Adapter) Probe(ctx context.Context, announcement ReadyAnnouncement, plan supervisor.LaunchPlan) error {
	if err := a.ValidateReady(announcement, plan); err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, announcement.URL, nil)
	if err != nil {
		return &ProbeError{reason: "invalid readiness URL", cause: err}
	}
	request.Header.Set("Accept", "text/html")
	response, err := a.client.Do(request)
	if err != nil {
		return &ProbeError{reason: "loopback probe failed", cause: err}
	}
	if response.StatusCode == http.StatusSeeOther {
		if err := completeLaunchTokenExchange(ctx, a.client, request, response, plan); err != nil {
			return err
		}
		return nil
	}
	defer response.Body.Close()
	return validateHTMLResponse(response)
}

func completeLaunchTokenExchange(ctx context.Context, client *http.Client, initial *http.Request, response *http.Response, plan supervisor.LaunchPlan) error {
	defer response.Body.Close()
	location := response.Header.Get("Location")
	if location != "/" {
		return &ProbeError{reason: "loopback authentication redirect was not the expected clean root", cause: fmt.Errorf("location %q", location)}
	}
	cleanURL, err := url.Parse(plan.ExpectedOrigin + "/")
	if err != nil {
		return &ProbeError{reason: "loopback clean URL could not be constructed", cause: err}
	}
	cleanRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, cleanURL.String(), nil)
	if err != nil {
		return &ProbeError{reason: "loopback clean URL could not be requested", cause: err}
	}
	cleanRequest.Header.Set("Accept", "text/html")
	for _, cookie := range response.Cookies() {
		cleanRequest.AddCookie(cookie)
	}
	if len(response.Cookies()) == 0 {
		return &ProbeError{reason: "loopback authentication redirect did not issue a session cookie", cause: errors.New("missing session cookie")}
	}
	if initial.URL.Scheme != cleanURL.Scheme || initial.URL.Host != cleanURL.Host {
		return &ProbeError{reason: "loopback authentication redirect changed origin", cause: errors.New("origin mismatch")}
	}
	cleanResponse, err := client.Do(cleanRequest)
	if err != nil {
		return &ProbeError{reason: "loopback authenticated probe failed", cause: err}
	}
	defer cleanResponse.Body.Close()
	return validateHTMLResponse(cleanResponse)
}

func validateHTMLResponse(response *http.Response) error {
	if response.StatusCode != http.StatusOK {
		return &ProbeError{reason: "loopback probe returned unexpected status", cause: fmt.Errorf("status %d", response.StatusCode)}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if err != nil {
		return &ProbeError{reason: "loopback response could not be read", cause: err}
	}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if !strings.Contains(contentType, "text/html") && !strings.Contains(strings.ToLower(string(body)), "<html") {
		return &ProbeError{reason: "loopback response is not a web application", cause: errors.New("unexpected content type")}
	}
	return nil
}

func (a *Adapter) RequestShutdown(ctx context.Context, worker supervisor.Worker) error {
	if worker == nil {
		return nil
	}
	return worker.RequestStop(ctx)
}

func (a *Adapter) locateExecutable() (string, error) {
	if override := strings.TrimSpace(a.executableOverride); override != "" {
		return existingExecutable(override)
	}
	if override := strings.TrimSpace(os.Getenv("WORK_DSH_EXECUTABLE")); override != "" {
		return existingExecutable(override)
	}
	root := a.discoveryRoot
	if root == "" {
		root, _ = os.Getwd()
	}
	candidates := []string{
		filepath.Join(root, "tools", "dsh", "run-dsh.cmd"),
		filepath.Join(root, "tools", "dsh", "node_modules", ".bin", "dsh.cmd"),
		filepath.Join(root, "tools", "dsh", "node_modules", ".bin", "dsh.exe"),
		filepath.Join(root, "tools", "dsh", "node_modules", ".bin", "dsh"),
	}
	for _, candidate := range candidates {
		if path, err := existingExecutable(candidate); err == nil {
			return path, nil
		}
	}
	if os.Getenv("WORK_DSH_ALLOW_PATH") == "1" {
		if path, err := exec.LookPath("dsh"); err == nil {
			return path, nil
		}
	}
	return "", os.ErrNotExist
}

func (a *Adapter) executableHint() string {
	if override := strings.TrimSpace(a.executableOverride); override != "" {
		return override
	}
	if override := strings.TrimSpace(os.Getenv("WORK_DSH_EXECUTABLE")); override != "" {
		return override
	}
	root := a.discoveryRoot
	if root == "" {
		root, _ = os.Getwd()
	}
	return filepath.Join(root, "tools", "dsh", "run-dsh.cmd")
}

func existingExecutable(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absolute)
	if err != nil || info.IsDir() {
		return "", os.ErrNotExist
	}
	return absolute, nil
}

var versionPattern = regexp.MustCompile(`(?:^|[^0-9])v?([0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?)`)
var readyLinePattern = regexp.MustCompile(`(?m)(?:^|\s)dsh web:\s+(https?://[^\s]+)`)
var ansiPattern = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

func ParseVersion(text string) string {
	match := versionPattern.FindStringSubmatch(text)
	if len(match) != 2 {
		return ""
	}
	return match[1]
}

func stripANSI(text string) string {
	return ansiPattern.ReplaceAllString(text, "")
}

func AllocateLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

type ProbeError struct {
	reason string
	cause  error
}

func (e *ProbeError) Error() string {
	if e.cause == nil {
		return e.reason
	}
	return e.reason + ": " + e.cause.Error()
}

func (e *ProbeError) Unwrap() error { return e.cause }
