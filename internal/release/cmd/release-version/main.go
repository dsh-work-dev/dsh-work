// Command release-version is the preflight that computes one release identity.
//
// It is invoked through scripts/release-version.mjs so a workflow, a package
// script and a developer all run the same rules. The full semantic version is
// printed on stdout; the individual components are available with --field, and
// the whole identity with --json. Invalid input is refused with a non-zero exit
// code and a message on stderr, so a build never starts on a version that was
// not computed here.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/local/dsh-work/internal/release"
)

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

type options struct {
	channel      string
	version      string
	base         string
	baseFile     string
	date         string
	runID        string
	sourceSHA    string
	lastStable   string
	field        string
	asJSON       bool
	showHelp     bool
	unknownFlags []string
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	opts, err := parseArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	if opts.showHelp {
		usage(stderr)
		return exitOK
	}

	baseFile := strings.TrimSpace(opts.baseFile)
	if baseFile == "" {
		baseFile = defaultConfigPath()
	}
	base := strings.TrimSpace(opts.base)
	if base == "" {
		base, err = readConfigBase(baseFile)
		if err != nil {
			fmt.Fprintf(stderr, "release-version: %v\n", err)
			return exitError
		}
	}

	date := strings.TrimSpace(opts.date)
	if date == "" {
		date = time.Now().UTC().Format(time.DateOnly)
	}

	computed, err := release.Compute(release.ReleaseOptions{
		Channel:           release.Channel(strings.TrimSpace(opts.channel)),
		Version:           opts.version,
		Base:              base,
		Date:              date,
		RunID:             opts.runID,
		SourceSHA:         opts.sourceSHA,
		LastStableVersion: opts.lastStable,
	})
	if err != nil {
		fmt.Fprintf(stderr, "release-version: %s\n", message(err))
		if release.InputError(err) {
			return exitUsage
		}
		return exitError
	}

	if opts.asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(struct {
			Version        string `json:"version"`
			Channel        string `json:"channel"`
			Base           string `json:"base"`
			SourceSHA      string `json:"sourceSha,omitempty"`
			Date           string `json:"date,omitempty"`
			RunID          string `json:"runId,omitempty"`
			FileVersion    string `json:"fileVersion"`
			LastStableBase string `json:"lastStableBase,omitempty"`
		}{
			Version:        computed.Version.String(),
			Channel:        string(computed.Channel),
			Base:           computed.Base,
			SourceSHA:      computed.SourceSHA,
			Date:           computed.Date,
			RunID:          computed.RunID,
			FileVersion:    computed.FileVersion,
			LastStableBase: computed.LastStableBase,
		}); err != nil {
			fmt.Fprintf(stderr, "release-version: %v\n", err)
			return exitError
		}
		return exitOK
	}

	if opts.field != "" {
		value, err := fieldValue(computed, opts.field)
		if err != nil {
			fmt.Fprintf(stderr, "release-version: %v\n", err)
			return exitUsage
		}
		fmt.Fprintln(stdout, value)
		return exitOK
	}

	fmt.Fprintln(stdout, computed.Version.String())
	return exitOK
}

func fieldValue(computed release.Release, field string) (string, error) {
	switch field {
	case "version":
		return computed.Version.String(), nil
	case "channel":
		return string(computed.Channel), nil
	case "base":
		return computed.Base, nil
	case "date":
		return computed.Date, nil
	case "runId":
		return computed.RunID, nil
	case "sourceSha":
		return computed.SourceSHA, nil
	case "fileVersion":
		return computed.FileVersion, nil
	case "lastStableBase":
		return computed.LastStableBase, nil
	default:
		return "", fmt.Errorf("unknown field %q", field)
	}
}

// message strips the sentinel prefix so the caller sees only the reason.
func message(err error) string {
	return strings.TrimPrefix(err.Error(), "invalid release input: ")
}

func parseArgs(args []string) (options, error) {
	var opts options
	flags := flag.NewFlagSet("release-version", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.channel, "channel", "stable", "release channel: stable, beta or nightly")
	flags.StringVar(&opts.version, "version", "", "full release version, for example v0.3.0 or v0.3.0-beta.2")
	flags.StringVar(&opts.base, "base", "", "base version from build/config.yml; read from the file when empty")
	flags.StringVar(&opts.baseFile, "config", "", "path to build/config.yml, relative to the working directory")
	flags.StringVar(&opts.date, "date", "", "UTC date of the workflow run as YYYY-MM-DD; defaults to today in UTC")
	flags.StringVar(&opts.runID, "run-id", "", "GitHub workflow run id, required for the nightly channel")
	flags.StringVar(&opts.sourceSHA, "source-sha", "", "build commit recorded next to the version")
	flags.StringVar(&opts.lastStable, "last-stable", "", "highest published stable version, used to gate a nightly base")
	flags.StringVar(&opts.field, "field", "", "print one component instead of the version: version, channel, base, date, runId, sourceSha, fileVersion, lastStableBase")
	flags.BoolVar(&opts.asJSON, "json", false, "print the whole release identity as JSON")
	flags.BoolVar(&opts.showHelp, "help", false, "print usage")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			opts.showHelp = true
			return opts, nil
		}
		return opts, err
	}
	if rest := flags.Args(); len(rest) > 0 {
		opts.unknownFlags = rest
		return opts, fmt.Errorf("unexpected argument %q", rest[0])
	}
	return opts, nil
}

var versionLine = regexp.MustCompile(`(?m)^\s+version:\s*["']([^"']+)["']\s*$`)

// defaultConfigPath locates build/config.yml without assuming the working
// directory is the repository root. The nearest ancestor that has one wins, so
// the command behaves the same from the root, from a package directory under
// `go test`, and from a nested checkout directory. The last fallback is the
// documented repository-relative path, whose absence is reported by
// readConfigBase.
func defaultConfigPath() string {
	relative := filepath.Join("build", "config.yml")
	directory, err := os.Getwd()
	if err != nil {
		return relative
	}
	for {
		candidate := filepath.Join(directory, relative)
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return relative
		}
		directory = parent
	}
}

// readConfigBase reads info.version from build/config.yml, the only base
// version source in the repository.
func readConfigBase(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", path, err)
	}
	match := versionLine.FindSubmatch(content)
	if match == nil {
		return "", fmt.Errorf("%s must contain info.version", path)
	}
	return string(match[1]), nil
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage: release-version [flags]

Computes the single release version for one build.

  release-version --channel stable
  release-version --channel beta --version v0.3.0-beta.2
  release-version --channel nightly --run-id 123456789 --date 2026-10-13 --last-stable 0.2.0

Flags:
  --channel stable|beta|nightly   release channel (default stable)
  --version VERSION               full release version for stable and beta
  --base X.Y.Z                    base version; read from build/config.yml when empty
  --config PATH                   build/config.yml path; the nearest ancestor with one is used by default
  --date YYYY-MM-DD               UTC date of the run; defaults to today in UTC
  --run-id ID                     workflow run id, required for nightly
  --source-sha SHA                build commit, recorded next to the version
  --last-stable X.Y.Z             published stable version that a nightly base must exceed
  --field NAME                    print one component
  --json                          print the whole identity as JSON
`)
}
