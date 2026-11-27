//go:build darwin && cgo

package mac

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/embeddedbrowser/macowner"
	"github.com/local/dsh-work/internal/supervisor"
	"github.com/local/dsh-work/internal/workeripc"
)

// Runtime is the selected Mac preview execution world, not a POSIX executor.
type Runtime struct {
	owner     *macowner.RestrictedDSHAdapter
	node, cli string
}

func NewRuntime() (*Runtime, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pub, err := macowner.OpenPublication(ctx, os.Getenv("DSH_WORK_CEF_MAC_PUBLICATION"))
	if err != nil {
		return nil, err
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return nil, err
	}
	node, err = filepath.EvalSymlinks(node)
	if err != nil {
		return nil, err
	}
	cli := os.Getenv("DSH_WORK_EXECUTABLE")
	if cli == "" {
		root := os.Getenv("DSH_WORK_EMBEDDED_ROOT")
		if !filepath.IsAbs(root) {
			return nil, errors.New("MAC_PREVIEW_DSH_RUNTIME_REQUIRED")
		}
		cli = filepath.Join(root, "tools", "dsh", "node_modules", ".bin", "dsh")
	}
	cli, err = filepath.EvalSymlinks(cli)
	if err != nil {
		return nil, err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	owner, err := macowner.NewRestrictedDSHAdapter(pub, node, cli, filepath.Join(cache, "dsh-work", "mac-preview-owner"))
	if err != nil {
		return nil, err
	}
	return &Runtime{owner: owner, node: node, cli: cli}, nil
}
func (r *Runtime) Start(ctx context.Context, p supervisor.LaunchPlan, raw supervisor.RawOutputHandler) (supervisor.Worker, error) {
	return r.owner.Start(ctx, p, raw)
}
func (r *Runtime) Run(ctx context.Context, executable string, args []string, env map[string]string, dir string) (dshadapter.CommandResult, error) {
	actual, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return dshadapter.CommandResult{}, err
	}
	allowed := (actual == r.cli && len(args) == 1 && args[0] == "--version") || (actual == r.node && ((len(args) == 1 && args[0] == "--version") || (len(args) == 2 && args[0] == "-p" && args[1] == "process.execPath")))
	if !allowed {
		return dshadapter.CommandResult{}, errors.New("Local commands and package installation are unavailable in this Mac preview.")
	}
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
	w, err := r.owner.Start(ctx, supervisor.LaunchPlan{GenerationID: "preview-probe", Executable: actual, Args: args, Env: environment, WorkingDirectory: dir, ExpectedOrigin: workeripc.Origin}, func(_ supervisor.OutputStream, line string) { dshadapter.ReportCommandOutput(ctx, line) })
	if err != nil {
		if w != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err = errors.Join(err, w.ForceStop(cleanup), w.Close())
		}
		return dshadapter.CommandResult{}, err
	}
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case <-w.Exited():
		result := w.ExitResult()
		if result.Code != 0 || result.Err != "" {
			err = errors.New("Mac preview runtime version check failed")
		}
	}
	cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		err = errors.Join(err, w.ForceStop(cleanup))
	}
	err = errors.Join(err, w.WaitEmpty(cleanup), w.Close())
	d := w.Diagnostics()
	return dshadapter.CommandResult{Stdout: d.StdoutTail, Stderr: d.StderrTail}, err
}
func (r *Runtime) Resolve(ctx context.Context, selection dshmanager.NodeSelection, _ []dshmanager.NodeInstallationInfo) (dshmanager.ResolvedNode, error) {
	if selection.Kind != dshmanager.NodeSelectionSystem {
		return dshmanager.ResolvedNode{}, errors.New("Only the installed system Node is available in this Mac preview.")
	}
	version, err := r.Run(ctx, r.node, []string{"--version"}, nil, "")
	if err != nil {
		return dshmanager.ResolvedNode{}, err
	}
	return dshmanager.ResolvedNode{Selection: selection, NodePath: r.node, Version: dshadapter.ParseVersion(version.Stdout), ChildEnvironment: map[string]string{"PATH": filepath.Dir(r.node) + string(os.PathListSeparator) + os.Getenv("PATH")}}, nil
}

type FileReplacer struct{}

func (FileReplacer) Replace(source, destination string) error { return os.Rename(source, destination) }
