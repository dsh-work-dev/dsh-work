//go:build darwin && cgo

package mac

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/embeddedbrowser/macowner"
	"github.com/local/dsh-work/internal/redact"
	"github.com/local/dsh-work/internal/supervisor"
	"github.com/local/dsh-work/internal/workeripc"
)

// Runtime runs verified DSH components through the Mac process owner.
type Runtime struct {
	owner          *macowner.RestrictedDSHAdapter
	node, npm, cli string
	mu             sync.RWMutex
	managed        map[string]dshmanager.NodeInstallationInfo
	dshStore       string
}

func NewRuntime() (*Runtime, error) {
	fallback := []string{"/opt/homebrew/bin", "/usr/local/bin", "/opt/local/bin"}
	if userHome, err := os.UserHomeDir(); err == nil {
		fallback = append(fallback, filepath.Join(userHome, "n", "bin"), filepath.Join(userHome, ".local", "bin"))
	}
	return newRuntime(fallback)
}

func findMacNode(fallback []string) (string, string) {
	directories := append(filepath.SplitList(os.Getenv("PATH")), fallback...)
	var nodeOnly string
	for _, dir := range directories {
		if !filepath.IsAbs(dir) {
			continue
		}
		node, err := exec.LookPath(filepath.Join(dir, "node"))
		if err != nil {
			continue
		}
		node, err = filepath.EvalSymlinks(node)
		if err != nil {
			continue
		}
		if nodeOnly == "" {
			nodeOnly = node
		}
		for _, bin := range []string{dir, filepath.Dir(node)} {
			if npm, err := exec.LookPath(filepath.Join(bin, "npm")); err == nil {
				return node, npm
			}
		}
	}
	return nodeOnly, ""
}

func newRuntime(fallback []string) (*Runtime, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var previewManifest string
	if root := os.Getenv("DSH_WORK_EMBEDDED_ROOT"); os.Getenv("DSH_WORK_EMBEDDED_CHECKPOINT") == "1" && filepath.IsAbs(root) {
		previewManifest = os.Getenv("DSH_WORK_CEF_MAC_PUBLICATION")
	}
	pub, err := macowner.OpenHostPublication(ctx, previewManifest)
	if err != nil {
		return nil, err
	}
	node, npm := findMacNode(fallback)
	cli := os.Getenv("DSH_WORK_EXECUTABLE")
	if cli == "" && previewManifest != "" {
		fixture := filepath.Join(os.Getenv("DSH_WORK_EMBEDDED_ROOT"), "tools", "dsh", "node_modules", ".bin", "dsh")
		if _, e := os.Lstat(fixture); e == nil {
			cli = fixture
		} else if !errors.Is(e, os.ErrNotExist) {
			return nil, e
		}
	}
	var ownerRoot string
	if root := os.Getenv("DSH_WORK_EMBEDDED_ROOT"); os.Getenv("DSH_WORK_EMBEDDED_CHECKPOINT") == "1" && filepath.IsAbs(root) {
		ownerRoot = filepath.Join(root, "cache", "mac-preview-owner")
	} else {
		cache, err := os.UserCacheDir()
		if err != nil {
			return nil, err
		}
		ownerRoot = filepath.Join(cache, "dsh-work", "mac-preview-owner")
	}
	var owner *macowner.RestrictedDSHAdapter
	if cli == "" {
		owner, err = macowner.NewManagedDSHAdapter(pub, ownerRoot)
	} else {
		cli, err = filepath.EvalSymlinks(cli)
		if err == nil {
			owner, err = macowner.NewRestrictedDSHAdapter(pub, node, cli, ownerRoot)
		}
	}
	if err != nil {
		return nil, err
	}
	return &Runtime{owner: owner, node: node, npm: npm, cli: cli, managed: make(map[string]dshmanager.NodeInstallationInfo)}, nil
}
func (r *Runtime) Start(ctx context.Context, p supervisor.LaunchPlan, raw supervisor.RawOutputHandler) (supervisor.Worker, error) {
	node, err := r.nodeForEnvironment(p.Env)
	if err != nil {
		return nil, err
	}
	cli, err := r.dshForExecutable(p.Executable)
	if err != nil {
		return nil, err
	}
	// A complete-looking node_modules tree can still contain an interrupted
	// install whose bundle selection was never committed by DSH.
	if home := p.Env["DSH_HOME"]; filepath.IsAbs(home) && len(p.Args) >= 4 && p.Args[2] == "--profile" {
		args, err := dshadapter.NewPluginCommands().List(p.Args[3])
		if err != nil {
			return nil, err
		}
		_, err = os.Lstat(filepath.Join(home, "profiles", p.Args[3], ".dsh-work", "mac-package-operation.json"))
		if err == nil {
			if _, err = r.runRestricted(ctx, node, cli, cli, args, p.Env, home); err != nil {
				if failure := dshmanager.ProfilePackageRecoveryFailure(err); failure != nil {
					return nil, failure
				}
				return nil, err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return r.owner.StartWithComponent(ctx, node, cli, p, raw)
}
func (r *Runtime) Run(ctx context.Context, executable string, args []string, env map[string]string, dir string) (dshadapter.CommandResult, error) {
	actual, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return dshadapter.CommandResult{}, err
	}
	node, err := r.nodeForEnvironment(env)
	if err != nil {
		return dshadapter.CommandResult{}, err
	}
	cli := r.cli
	if actual != node {
		cli, err = r.dshForExecutable(actual)
		if err != nil {
			return dshadapter.CommandResult{}, err
		}
	}
	commands := dshadapter.NewPluginCommands()
	allowed := (actual == cli && (len(args) == 1 && args[0] == "--version" || commands.IsVersionApproval(args) || commands.IsPackageMaintenance(args))) ||
		(actual == node && (len(args) == 1 && args[0] == "--version" || len(args) == 2 && args[0] == "-p" && args[1] == "process.execPath"))
	if !allowed {
		return dshadapter.CommandResult{}, errors.New("Mac DSH command is unavailable")
	}
	return r.runRestricted(ctx, node, cli, actual, args, env, dir)
}

func (r *Runtime) runRestricted(ctx context.Context, node, cli, executable string, args []string, env map[string]string, dir string) (dshadapter.CommandResult, error) {
	var err error
	if dir == "" {
		dir, err = os.Getwd()
		if err != nil {
			return dshadapter.CommandResult{}, err
		}
	}
	environment := map[string]string{}
	for _, item := range os.Environ() {
		if k, v, ok := strings.Cut(item, "="); ok {
			environment[k] = v
		}
	}
	for k, v := range env {
		environment[k] = v
	}
	start := r.owner.StartWithComponent
	commands := dshadapter.NewPluginCommands()
	if executable == cli && commands.IsVersionApproval(args) {
		start = r.owner.StartProfileVersionApproval
		// The target must be supplied by the manager. A parent process's
		// DSH_HOME is unrelated to this explicit profile operation.
		environment["DSH_HOME"] = env["DSH_HOME"]
	}
	if executable == cli && commands.IsPackageMaintenance(args) {
		home := env["DSH_HOME"]
		if !filepath.IsAbs(home) || filepath.Clean(home) != filepath.Clean(dir) {
			return dshadapter.CommandResult{}, errors.New("Mac DSH profile maintenance requires the selected data directory")
		}
		environment["DSH_HOME"] = home
		environment["CI"] = "1"
		registry := env["npm_config_registry"]
		if len(args) >= 6 && args[len(args)-2] == "--registry" {
			registry = args[len(args)-1]
			args = args[:len(args)-2]
		}
		if registry != "" {
			// pnpm 11 reads pnpm_config_registry. Keep the npm spelling
			// for older recorded package managers.
			for key := range environment {
				if strings.EqualFold(key, "npm_config_registry") || strings.EqualFold(key, "pnpm_config_registry") {
					delete(environment, key)
				}
			}
			environment["npm_config_registry"] = registry
			environment["pnpm_config_registry"] = registry
		}
		recorded := dshmanager.RestorePackageManager(ctx)
		if recorded == "" {
			recorded, err = dshadapter.ReadProfilePackageManager(filepath.Join(home, "profiles", args[2]))
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return dshadapter.CommandResult{}, err
			}
		}
		pnpm, err := r.profilePNPM(ctx, node, recorded)
		if err != nil {
			return dshadapter.CommandResult{}, err
		}
		start = func(ctx context.Context, node, cli string, plan supervisor.LaunchPlan, raw supervisor.RawOutputHandler) (supervisor.Worker, error) {
			return r.owner.StartProfileMaintenance(ctx, node, cli, pnpm, plan, raw)
		}
	}
	var outputMu sync.Mutex
	var stdout, stderr limitedNPMOutput
	w, err := start(ctx, node, cli, supervisor.LaunchPlan{GenerationID: "preview-command", Executable: executable, Args: args, Env: environment, WorkingDirectory: dir, ExpectedOrigin: workeripc.Origin}, func(stream supervisor.OutputStream, line string) {
		dshadapter.ReportCommandOutput(ctx, line)
		outputMu.Lock()
		defer outputMu.Unlock()
		buffer := &stdout
		if stream == supervisor.StreamStderr {
			buffer = &stderr
		}
		_, _ = buffer.Write([]byte(redact.Secrets(line) + "\n"))
	})
	commandFailed := false
	if w != nil {
		if err == nil {
			select {
			case <-ctx.Done():
				err = ctx.Err()
			case <-w.Exited():
			}
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err != nil {
			err = errors.Join(err, w.ForceStop(cleanup))
		}
		err = errors.Join(err, w.WaitEmpty(cleanup), w.Close())
		if result := w.ExitResult(); err == nil && (!result.Started || result.Code != 0 || result.Err != "") {
			commandFailed = true
			err = fmt.Errorf("Mac DSH command failed (exit code %d, %s)", result.Code, result.Err)
		}
	}
	outputMu.Lock()
	defer outputMu.Unlock()
	if commandFailed && commands.IsPackageMaintenance(args) {
		switch {
		case strings.Contains(string(stderr), "Error: MAC_PROFILE_RECOVERY_CONFLICT"):
			err = dshmanager.ErrProfilePackageRecoveryConflict
		case strings.Contains(string(stderr), "Error: MAC_PROFILE_RECOVERY_PENDING"):
			err = dshmanager.ErrProfilePackageRecoveryPending
		}
	}
	return dshadapter.CommandResult{Stdout: string(stdout), Stderr: string(stderr)}, err
}
func (r *Runtime) NodeResolver(storeRoot string) dshmanager.NodeResolver {
	return macNodeResolver{runtime: r, installer: NewManagedNodeInstaller(storeRoot)}
}

type macNodeResolver struct {
	runtime   *Runtime
	installer ManagedNodeInstaller
}

func (resolver macNodeResolver) Resolve(ctx context.Context, selection dshmanager.NodeSelection, nodes []dshmanager.NodeInstallationInfo) (dshmanager.ResolvedNode, error) {
	r := resolver.runtime
	if selection.Kind == dshmanager.NodeSelectionManaged {
		for _, node := range nodes {
			if node.ID != selection.InstallationID {
				continue
			}
			_, directory, err := resolver.installer.nodeDirectory(node.Version, false)
			if err != nil {
				return dshmanager.ResolvedNode{}, err
			}
			actual, err := readMacNode(directory)
			if err != nil || actual != node {
				return dshmanager.ResolvedNode{}, errors.New("managed Mac Node no longer matches its catalog")
			}
			if err = ctx.Err(); err != nil {
				return dshmanager.ResolvedNode{}, err
			}
			bin := filepath.Dir(node.NodePath)
			r.mu.Lock()
			r.managed[bin] = node
			r.mu.Unlock()
			return dshmanager.ResolvedNode{Selection: selection, NodePath: node.NodePath, NPMPath: node.NPMPath, Version: node.Version, ChildEnvironment: macNodeEnvironment(bin)}, nil
		}
		return dshmanager.ResolvedNode{}, errors.New("managed Mac Node installation not found")
	}
	if selection.Kind != dshmanager.NodeSelectionSystem {
		return dshmanager.ResolvedNode{}, errors.New("invalid Mac Node selection")
	}
	if r.node == "" {
		return dshmanager.ResolvedNode{}, exec.ErrNotFound
	}
	version, err := r.Run(ctx, r.node, []string{"--version"}, nil, "")
	if err != nil {
		return dshmanager.ResolvedNode{}, err
	}
	return dshmanager.ResolvedNode{Selection: selection, NodePath: r.node, NPMPath: r.npm, Version: dshadapter.ParseVersion(version.Stdout), ChildEnvironment: macNodeEnvironment(filepath.Dir(r.node))}, nil
}

func (r *Runtime) nodeForEnvironment(env map[string]string) (string, error) {
	bin, _, _ := strings.Cut(env["PATH"], string(os.PathListSeparator))
	r.mu.RLock()
	selected, ok := r.managed[bin]
	r.mu.RUnlock()
	if !ok {
		if r.node == "" {
			return "", exec.ErrNotFound
		}
		return r.node, nil
	}
	actual, err := readMacNode(filepath.Dir(bin))
	if err != nil || actual != selected {
		return "", errors.New("selected Mac Node installation changed")
	}
	return selected.NodePath, nil
}

func macNodeEnvironment(bin string) map[string]string {
	return map[string]string{"PATH": bin + string(os.PathListSeparator) + os.Getenv("PATH")}
}

type FileReplacer struct{}

func (FileReplacer) Replace(source, destination string) error { return os.Rename(source, destination) }
