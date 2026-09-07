//go:build windows

package windows

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
)

type runtimeToolchainResolver interface {
	Resolve(context.Context, dshmanager.RuntimeInstallObserver) (runtimeToolchain, error)
}

type runtimeToolchain struct {
	kind               dshmanager.RuntimeToolchain
	version            string
	nodePath           string
	packageManagerPath string
	env                map[string]string
}

type systemToolchainResolver struct {
	executor dshadapter.CommandExecutor
	lookup   func(string) (string, error)
}

func (r systemToolchainResolver) Resolve(ctx context.Context, observer dshmanager.RuntimeInstallObserver) (runtimeToolchain, error) {
	if r.executor == nil || r.lookup == nil {
		return runtimeToolchain{}, errSystemToolchainUnavailable
	}
	nodePath, err := r.lookup("node")
	if err != nil {
		return runtimeToolchain{}, errSystemToolchainUnavailable
	}
	emitPreparation(observer, lifecycle.RuntimePreparation{
		State:     lifecycle.RuntimePreparationResolvingToolchain,
		Operation: lifecycle.RuntimePreparationOperationDetectNode,
		Toolchain: string(dshmanager.RuntimeToolchainNone),
		Source:    lifecycle.RuntimePreparationSourceLocal,
		CanCancel: true,
	})
	nodeResult, err := r.executor.Run(ctx, nodePath, []string{"--version"}, nil, "")
	nodeVersion := dshadapter.ParseVersion(nodeResult.Stdout + "\n" + nodeResult.Stderr)
	if err != nil || nodeVersion == "" {
		return runtimeToolchain{}, errSystemToolchainUnavailable
	}
	env := childPathEnvironment(filepath.Dir(nodePath))

	emitPreparation(observer, lifecycle.RuntimePreparation{
		State:     lifecycle.RuntimePreparationResolvingToolchain,
		Operation: lifecycle.RuntimePreparationOperationDetectPNPM,
		Toolchain: string(dshmanager.RuntimeToolchainNone),
		Source:    lifecycle.RuntimePreparationSourceLocal,
		CanCancel: true,
	})
	if pnpmPath, lookupErr := r.lookup("pnpm"); lookupErr == nil {
		if result, probeErr := r.executor.Run(ctx, pnpmPath, []string{"--version"}, env, ""); probeErr == nil && strings.TrimSpace(result.Stdout+"\n"+result.Stderr) != "" {
			return runtimeToolchain{
				kind:               dshmanager.RuntimeToolchainSystemPNPM,
				version:            nodeVersion,
				nodePath:           nodePath,
				packageManagerPath: pnpmPath,
				env:                env,
			}, nil
		}
	}

	emitPreparation(observer, lifecycle.RuntimePreparation{
		State:     lifecycle.RuntimePreparationResolvingToolchain,
		Operation: lifecycle.RuntimePreparationOperationDetectNPM,
		Toolchain: string(dshmanager.RuntimeToolchainNone),
		Source:    lifecycle.RuntimePreparationSourceLocal,
		CanCancel: true,
	})
	if npmPath, lookupErr := r.lookup("npm"); lookupErr == nil {
		if result, probeErr := r.executor.Run(ctx, npmPath, []string{"--version"}, env, ""); probeErr == nil && strings.TrimSpace(result.Stdout+"\n"+result.Stderr) != "" {
			return runtimeToolchain{
				kind:               dshmanager.RuntimeToolchainSystemNPM,
				version:            nodeVersion,
				nodePath:           nodePath,
				packageManagerPath: npmPath,
				env:                env,
			}, nil
		}
	}
	return runtimeToolchain{}, errSystemToolchainUnavailable
}

func artifactSource(source lifecycle.RuntimePreparationSource) dshmanager.RuntimeArtifactSource {
	switch source {
	case lifecycle.RuntimePreparationSourceLocal:
		return dshmanager.RuntimeArtifactSourceLocal
	case lifecycle.RuntimePreparationSourceOfficial:
		return dshmanager.RuntimeArtifactSourceOfficial
	case lifecycle.RuntimePreparationSourceMirror:
		return dshmanager.RuntimeArtifactSourceMirror
	default:
		return dshmanager.RuntimeArtifactSourceNone
	}
}

var _ runtimeToolchainResolver = systemToolchainResolver{}
