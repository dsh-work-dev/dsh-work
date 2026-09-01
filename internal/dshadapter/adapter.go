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

type Adapter struct {
	executor           CommandExecutor
	expectedVersion    string
	executableOverride string
	workspaceRoot      string
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

func (a *Adapter) SetWorkspaceRoot(path string) {
	a.workspaceRoot = path
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

func (a *Adapter) BuildLaunchPlan(runtime Runtime, generationID, workspace, dshHome string, port int) (supervisor.LaunchPlan, error) {
	if runtime.Path == "" || runtime.Version != a.expectedVersion {
		return supervisor.LaunchPlan{}, fmt.Errorf("runtime is not the pinned DSH version")
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return supervisor.LaunchPlan{}, fmt.Errorf("resolve workspace: %w", err)
	}
	dshHome, err = filepath.Abs(dshHome)
	if err != nil {
		return supervisor.LaunchPlan{}, fmt.Errorf("resolve DSH home: %w", err)
	}
	origin := "http://127.0.0.1:" + strconv.Itoa(port)
	plan := supervisor.LaunchPlan{
		GenerationID:     generationID,
		Executable:       runtime.Path,
		Args:             []string{"--profile", "web", "--host", "127.0.0.1", "--port", strconv.Itoa(port), "--no-open"},
		Env:              map[string]string{"DSH_HOME": dshHome},
		WorkingDirectory: workspace,
		ExpectedOrigin:   origin,
		ExpectedHost:     "127.0.0.1",
		ExpectedPort:     port,
	}
	if err := plan.Validate(); err != nil {
		return supervisor.LaunchPlan{}, err
	}
	return plan, nil
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
	root := a.workspaceRoot
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
