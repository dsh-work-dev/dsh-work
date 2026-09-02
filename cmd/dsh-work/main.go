package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	workapp "github.com/local/work/internal/app"
	"github.com/local/work/internal/dshadapter"
	"github.com/local/work/internal/dshmanager"
	"github.com/local/work/internal/lifecycle"
	"github.com/local/work/internal/platform"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	manager, err := newManager()
	if err != nil {
		return err
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		printUsage(stdout)
		return nil
	}
	switch args[0] {
	case "runtime":
		return runRuntime(manager, args[1:], stdout)
	case "home":
		return runHome(manager, args[1:], stdout)
	case "profile":
		return runProfile(manager, args[1:], stdout)
	case "use":
		return runUse(manager, args[1:], stdout)
	case "plugin":
		return runPlugin(manager, args[1:], stdout)
	default:
		return fmt.Errorf("unknown dsh-work command %q; run dsh-work help", args[0])
	}
}

func newManager() (*dshmanager.Manager, error) {
	workspace, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolve workspace: %w", err)
	}
	config := workapp.DefaultConfig(workspace)
	dependencies := platform.New()
	dsh := dshadapter.New(dependencies.CommandExecutor, config.ExpectedDSHVersion)
	dsh.SetWorkspaceRoot(config.WorkspaceRoot)
	hint := dsh.RuntimeHint()
	var runner dshmanager.CommandRunner
	if dependencies.CommandExecutor != nil {
		runner = commandRunner{executor: dependencies.CommandExecutor}
	}
	runtimeStore := filepath.Join(filepath.Dir(config.DSHHome), "dsh-work", "runtimes")
	return dshmanager.New(dshmanager.Config{
		WorkspaceRoot:    workspace,
		CommandRunner:    runner,
		PluginCommands:   dshadapter.NewPluginCommands(),
		RuntimeInstaller: platform.NewRuntimeInstaller(runtimeStore),
		RuntimeVerifier:  dsh,
		ProfileCatalog:   dsh,
		Homes: []dshmanager.HomeInfo{{
			ID: "work", Name: "Work DSH home", Path: config.DSHHome, Ownership: dshmanager.HomeOwnershipWork,
		}},
		Runtimes: []dshmanager.RuntimeInfo{{
			ID: "dsh-" + hint.Version, Version: hint.Version, Path: hint.Path,
			Source: dshmanager.RuntimeSourceDevelopmentFixture, Installed: executableExists(hint.Path),
		}},
		DefaultSelection: dshmanager.LaunchSelection{
			RuntimeID: "dsh-" + hint.Version,
			Profile:   dshmanager.ProfileRef{HomeID: "work", Name: "web"},
			Workspace: workspace,
		},
	})
}

type commandRunner struct {
	executor dshadapter.CommandExecutor
}

func (r commandRunner) Run(ctx context.Context, executable string, args []string, env map[string]string, dir string) (dshmanager.CommandResult, error) {
	result, err := r.executor.Run(ctx, executable, args, env, dir)
	return dshmanager.CommandResult{Stdout: result.Stdout, Stderr: result.Stderr}, err
}

func runRuntime(manager *dshmanager.Manager, args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] == "list" {
		listArgs := args
		if len(args) > 0 && args[0] == "list" {
			listArgs = args[1:]
		}
		jsonOutput, remaining, err := jsonFlag(listArgs)
		if err != nil || len(remaining) != 0 {
			return flagError("runtime list", err, remaining)
		}
		snapshot, err := manager.Snapshot(context.Background())
		if err != nil {
			return err
		}
		return printValue(stdout, jsonOutput, snapshot.Runtimes, func() {
			for _, runtime := range snapshot.Runtimes {
				status := "unverified"
				if runtime.Installed {
					status = "installed"
				}
				fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", runtime.ID, runtime.Version, status, runtime.Path)
			}
		})
	}
	switch args[0] {
	case "install":
		set := flag.NewFlagSet("runtime install", flag.ContinueOnError)
		set.SetOutput(io.Discard)
		version := set.String("version", "", "DSH version to install")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if *version == "" {
			return errorsForUsage("runtime install requires --version")
		}
		snapshot, err := manager.InstallRuntime(context.Background(), *version)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "installed runtime dsh-%s\n", *version)
		return printSnapshotHint(stdout, snapshot)
	case "add":
		set := flag.NewFlagSet("runtime add", flag.ContinueOnError)
		set.SetOutput(io.Discard)
		id := set.String("id", "", "stable runtime id")
		version := set.String("version", "", "runtime version")
		path := set.String("path", "", "installed DSH executable or launcher path")
		source := set.String("source", string(dshmanager.RuntimeSourceManaged), "runtime source")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if *id == "" {
			*id = "dsh-" + *version
		}
		if *version == "" || *path == "" {
			return errorsForUsage("runtime add requires --version and --path")
		}
		snapshot, err := manager.RegisterRuntime(context.Background(), dshmanager.RuntimeInfo{
			ID: *id, Version: *version, Path: *path, Source: dshmanager.RuntimeSource(*source),
			Installed: executableExists(*path), Removable: true,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "registered runtime %s (%s)\n", *id, *version)
		return printSnapshotHint(stdout, snapshot)
	case "remove":
		set := flag.NewFlagSet("runtime remove", flag.ContinueOnError)
		set.SetOutput(io.Discard)
		id := set.String("id", "", "runtime id")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if *id == "" {
			return errorsForUsage("runtime remove requires --id")
		}
		_, err := manager.RemoveRuntime(context.Background(), *id)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "unregistered runtime %s from the catalog; managed files are retained\n", *id)
		return nil
	default:
		return fmt.Errorf("unknown runtime command %q", args[0])
	}
}

func runHome(manager *dshmanager.Manager, args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] == "list" {
		listArgs := args
		if len(args) > 0 && args[0] == "list" {
			listArgs = args[1:]
		}
		jsonOutput, remaining, err := jsonFlag(listArgs)
		if err != nil || len(remaining) != 0 {
			return flagError("home list", err, remaining)
		}
		snapshot, err := manager.Snapshot(context.Background())
		if err != nil {
			return err
		}
		return printValue(stdout, jsonOutput, snapshot.Homes, func() {
			for _, home := range snapshot.Homes {
				fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", home.ID, home.Name, home.Ownership, home.Path)
			}
		})
	}
	switch args[0] {
	case "add":
		set := flag.NewFlagSet("home add", flag.ContinueOnError)
		set.SetOutput(io.Discard)
		id := set.String("id", "", "home id")
		name := set.String("name", "", "display name")
		path := set.String("path", "", "DSH_HOME path")
		ownership := set.String("ownership", string(dshmanager.HomeOwnershipUser), "home ownership: work or user")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if *id == "" || *name == "" || *path == "" {
			return errorsForUsage("home add requires --id, --name and --path")
		}
		_, err := manager.RegisterHome(context.Background(), dshmanager.HomeInfo{ID: *id, Name: *name, Path: *path, Ownership: dshmanager.HomeOwnership(*ownership)})
		return err
	case "remove":
		set := flag.NewFlagSet("home remove", flag.ContinueOnError)
		set.SetOutput(io.Discard)
		id := set.String("id", "", "home id")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if *id == "" {
			return errorsForUsage("home remove requires --id")
		}
		_, err := manager.RemoveHome(context.Background(), *id)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "unregistered DSH home %s from the catalog; home files are retained\n", *id)
		return nil
	default:
		return fmt.Errorf("unknown home command %q", args[0])
	}
}

func runProfile(manager *dshmanager.Manager, args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] == "list" {
		listArgs := args
		if len(args) > 0 && args[0] == "list" {
			listArgs = args[1:]
		}
		jsonOutput, remaining, err := jsonFlag(listArgs)
		if err != nil || len(remaining) != 0 {
			return flagError("profile list", err, remaining)
		}
		snapshot, err := manager.Snapshot(context.Background())
		if err != nil {
			return err
		}
		return printValue(stdout, jsonOutput, snapshot.Profiles, func() {
			for _, profile := range snapshot.Profiles {
				fmt.Fprintf(stdout, "%s\t%s\t%s\t%d plugins\n", profile.Ref.HomeID, profile.Ref.Name, profile.Kind, profile.PluginCount)
			}
		})
	}
	return fmt.Errorf("unknown profile command %q", args[0])
}

func runUse(manager *dshmanager.Manager, args []string, stdout io.Writer) error {
	set := flag.NewFlagSet("use", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	runtimeID := set.String("runtime", "", "runtime id")
	homeID := set.String("home", "", "DSH home id")
	profileName := set.String("profile", "", "profile name")
	workspace := set.String("workspace", "", "workspace path")
	if err := set.Parse(args); err != nil {
		return err
	}
	if *runtimeID == "" || *homeID == "" || *profileName == "" {
		return errorsForUsage("use requires --runtime, --home and --profile")
	}
	snapshot, err := manager.SetDesired(context.Background(), dshmanager.LaunchSelection{
		RuntimeID: *runtimeID,
		Profile:   dshmanager.ProfileRef{HomeID: *homeID, Name: *profileName},
		Workspace: *workspace,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "selected %s / %s:%s\n", *runtimeID, *homeID, *profileName)
	return printSnapshotHint(stdout, snapshot)
}

func runPlugin(manager *dshmanager.Manager, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errorsForUsage("plugin requires list, add or remove")
	}
	set := flag.NewFlagSet("plugin "+args[0], flag.ContinueOnError)
	set.SetOutput(io.Discard)
	runtimeID := set.String("runtime", "", "runtime id; defaults to the selected runtime")
	homeID := set.String("home", "", "DSH home id")
	profileName := set.String("profile", "", "profile name")
	jsonOutput := set.Bool("json", false, "print JSON")
	if err := set.Parse(args[1:]); err != nil {
		return err
	}
	if *homeID == "" || *profileName == "" {
		return errorsForUsage("plugin commands require --home and --profile")
	}
	target := dshmanager.PluginTarget{RuntimeID: *runtimeID, Profile: dshmanager.ProfileRef{HomeID: *homeID, Name: *profileName}}
	switch args[0] {
	case "list":
		plugins, err := manager.ListPlugins(context.Background(), dshmanager.PluginListRequest{Target: target})
		if err != nil {
			return err
		}
		return printValue(stdout, *jsonOutput, plugins, func() {
			for _, plugin := range plugins {
				fmt.Fprintf(stdout, "%s\t%s\n", plugin.Name, plugin.Spec)
			}
		})
	case "add", "install":
		if set.NArg() != 1 {
			return errorsForUsage("plugin add requires one package spec")
		}
		result, err := manager.InstallPlugin(context.Background(), dshmanager.PluginInstallRequest{Target: target, Package: set.Arg(0)})
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "installed %s in %s\n", set.Arg(0), *profileName)
		if result.RestartRequired {
			fmt.Fprintln(stdout, "restart required to apply the active profile")
		}
		return nil
	case "remove":
		if set.NArg() != 1 {
			return errorsForUsage("plugin remove requires one package name")
		}
		result, err := manager.RemovePlugin(context.Background(), dshmanager.PluginRemoveRequest{Target: target, Package: set.Arg(0)})
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "removed %s from %s\n", set.Arg(0), *profileName)
		if result.RestartRequired {
			fmt.Fprintln(stdout, "restart required to apply the active profile")
		}
		return nil
	default:
		return fmt.Errorf("unknown plugin command %q", args[0])
	}
}

func jsonFlag(args []string) (bool, []string, error) {
	set := flag.NewFlagSet("list", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := set.Bool("json", false, "print JSON")
	if err := set.Parse(args); err != nil {
		return false, nil, err
	}
	return *jsonOutput, set.Args(), nil
}

func printValue[T any](stdout io.Writer, jsonOutput bool, value T, table func()) error {
	if jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(value)
	}
	table()
	return nil
}

func printSnapshotHint(stdout io.Writer, snapshot dshmanager.Snapshot) error {
	if snapshot.Desired == nil {
		return nil
	}
	fmt.Fprintf(stdout, "desired: %s / %s:%s\n", snapshot.Desired.RuntimeID, snapshot.Desired.Profile.HomeID, snapshot.Desired.Profile.Name)
	return nil
}

func flagError(command string, err error, remaining []string) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("unexpected arguments for %s: %s", command, strings.Join(remaining, " "))
}

func errorsForUsage(message string) error {
	return lifecycle.Failure{
		Code:          lifecycle.ErrorManagerStateInvalid,
		Summary:       message,
		CorrelationID: lifecycle.NewCorrelationID(),
	}
}

func executableExists(path string) bool {
	info, err := os.Stat(path)
	return path != "" && err == nil && !info.IsDir()
}

func printUsage(stdout io.Writer) {
	fmt.Fprintln(stdout, "dsh-work manages explicit DSH runtimes, homes, profiles and profile-scoped plugins.")
	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "Usage:")
	fmt.Fprintln(stdout, "  dsh-work runtime list [--json]")
	fmt.Fprintln(stdout, "  dsh-work runtime install --version VERSION")
	fmt.Fprintln(stdout, "  dsh-work runtime add --id ID --version VERSION --path PATH")
	fmt.Fprintln(stdout, "  dsh-work runtime remove --id ID    # unregisters catalog entry; retains files")
	fmt.Fprintln(stdout, "  dsh-work home list|add|remove ...  # home removal retains files")
	fmt.Fprintln(stdout, "  dsh-work profile list [--json]")
	fmt.Fprintln(stdout, "  dsh-work use --runtime ID --home ID --profile NAME [--workspace PATH]")
	fmt.Fprintln(stdout, "  dsh-work plugin list|add|remove --home ID --profile NAME [--runtime ID] ...")
}
