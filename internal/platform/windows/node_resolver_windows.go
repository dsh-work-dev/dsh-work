//go:build windows

package windows

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/local/dsh-work/internal/acquisition"
	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
)

type RunNodeResolver struct {
	executor  dshadapter.CommandExecutor
	lookup    func(string) (string, error)
	storeRoot string
}

func NewRunNodeResolver(executor dshadapter.CommandExecutor, storeRoot string) RunNodeResolver {
	return RunNodeResolver{executor: executor, lookup: exec.LookPath, storeRoot: storeRoot}
}

func (r RunNodeResolver) Resolve(ctx context.Context, selection dshmanager.NodeSelection, nodes []dshmanager.NodeInstallationInfo) (dshmanager.ResolvedNode, error) {
	if selection.Kind == dshmanager.NodeSelectionSystem {
		nodePath, err := r.lookup("node")
		if err != nil {
			return dshmanager.ResolvedNode{}, lifecycle.Failure{
				Code: lifecycle.ErrorRuntimeInstallUnavailable, Summary: "System Node is unavailable.",
				Retryable: true, CorrelationID: lifecycle.NewCorrelationID(),
			}
		}
		result, err := r.executor.Run(ctx, nodePath, []string{"--version"}, nil, "")
		version := dshadapter.ParseVersion(result.Stdout + "\n" + result.Stderr)
		if err != nil || version == "" {
			return dshmanager.ResolvedNode{}, lifecycle.Failure{
				Code: lifecycle.ErrorRuntimeInstallUnavailable, Summary: "System Node is unavailable.",
				Retryable: true, CorrelationID: lifecycle.NewCorrelationID(),
			}
		}
		resolved := dshmanager.ResolvedNode{
			Selection: selection, Version: version, NodePath: nodePath,
			ChildEnvironment: childPathEnvironment(filepath.Dir(nodePath)),
		}
		if pnpmPath, lookupErr := r.lookup("pnpm"); lookupErr == nil {
			resolved.PNPMPath = pnpmPath
		} else if npmPath, lookupErr := r.lookup("npm"); lookupErr == nil {
			resolved.NPMPath = npmPath
		}
		return resolved, nil
	}
	for _, node := range nodes {
		if node.ID != selection.InstallationID {
			continue
		}
		if err := validateManagedNodeInstallation(r.storeRoot, node); err != nil {
			return dshmanager.ResolvedNode{}, lifecycle.Failure{
				Code: lifecycle.ErrorRuntimeInstallUnavailable, Summary: "The managed Node installation is unavailable.",
				Retryable: true, CorrelationID: lifecycle.NewCorrelationID(),
			}
		}
		result, err := r.executor.Run(ctx, node.NodePath, []string{"--version"}, childPathEnvironment(filepath.Dir(node.NodePath)), "")
		if err != nil || dshadapter.ParseVersion(result.Stdout+"\n"+result.Stderr) != strings.TrimPrefix(node.Version, "v") {
			return dshmanager.ResolvedNode{}, lifecycle.Failure{
				Code: lifecycle.ErrorRuntimeInstallUnavailable, Summary: "The managed Node installation is unavailable.",
				Retryable: true, CorrelationID: lifecycle.NewCorrelationID(),
			}
		}
		return dshmanager.ResolvedNode{
			Selection: selection, Version: node.Version, NodePath: node.NodePath,
			NPMPath: node.NPMPath, PNPMPath: node.PNPMPath,
			ChildEnvironment: childPathEnvironment(filepath.Dir(node.NodePath)),
		}, nil
	}
	return dshmanager.ResolvedNode{}, errors.New("managed Node installation not found")
}

func validateManagedNodeInstallation(storeRoot string, node dshmanager.NodeInstallationInfo) error {
	root, err := managedStoreRoot(storeRoot)
	if err != nil {
		return err
	}
	managedRoot := filepath.Join(root, "toolchains", "node")
	installationRoot := filepath.Clean(filepath.Dir(node.NodePath))
	if !isWithinDirectory(managedRoot, installationRoot) || installationRoot == managedRoot {
		return errors.New("managed Node installation is outside the managed store")
	}
	if filepath.Clean(node.NodePath) != filepath.Join(installationRoot, "node.exe") || filepath.Clean(node.NPMPath) != filepath.Join(installationRoot, "npm.cmd") {
		return errors.New("managed Node executable paths do not match the installation layout")
	}
	resolvedManagedRoot, err := filepath.EvalSymlinks(managedRoot)
	if err != nil {
		return err
	}
	resolvedInstallationRoot, err := filepath.EvalSymlinks(installationRoot)
	if err != nil || !isWithinDirectory(resolvedManagedRoot, resolvedInstallationRoot) || resolvedInstallationRoot == resolvedManagedRoot {
		return errors.New("managed Node installation resolves outside the managed store")
	}
	data, err := os.ReadFile(filepath.Join(installationRoot, nodeManifestName))
	if err != nil {
		return err
	}
	var manifest nodeInstallationManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return err
	}
	if manifest.Route != acquisition.RouteOfficial && manifest.Route != acquisition.RouteMirror {
		return errors.New("managed Node manifest has an invalid acquisition route")
	}
	expected := nodeInstallationFromManifest(installationRoot, manifest)
	if node.ID != expected.ID || node.Version != expected.Version || node.Platform != expected.Platform ||
		normalizedNodeArchitecture(node.Architecture) != expected.Architecture || !strings.EqualFold(node.SHA256, expected.SHA256) ||
		node.InstallSource != expected.InstallSource || node.Ownership != dshmanager.NodeOwnershipManaged || !node.Installed || !node.Verified {
		return errors.New("managed Node catalog identity does not match its manifest")
	}
	return nil
}
