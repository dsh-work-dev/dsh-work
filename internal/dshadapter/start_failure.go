package dshadapter

import (
	"regexp"
	"strings"
)

// DSH's boot output is the only evidence of which plugin broke a start. The
// readings below are advisory: the Host keeps only packages that are installed
// in the failed profile, and the user decides whether to disable or remove one.

var (
	startLoaderEntry   = regexp.MustCompile(`failed to apply loader entry \S+ \(([^)\s]+)\)`)
	startLoadFailed    = regexp.MustCompile(`plugin\(s\) failed to load:\s*([^;\n]+)`)
	startMissingModule = regexp.MustCompile(`(?:Cannot find (?:module|package)|ERR_MODULE_NOT_FOUND[^'\n]*)\s*'([^'\n]+)'`)
	startImportedFrom  = regexp.MustCompile(`imported from (\S+)`)
	// The last node_modules segment of a path names the package that owns the
	// file; pnpm's `.pnpm` store directory is skipped by the leading-dot rule.
	startModulePath = regexp.MustCompile(`node_modules[/\\]((?:@[^/\\\s.][^/\\\s]*[/\\])?[^/\\\s.][^/\\\s]*)`)
	npmPackageName  = regexp.MustCompile(`^(?:@[a-z0-9][a-z0-9._~-]*/)?[a-z0-9][a-z0-9._~-]*$`)
)

// storedDataRejections are errors DSH raises about its own stored session
// data. The plugin whose loader entry surfaced one is not its cause.
var storedDataRejections = []string{
	"corrupt Zstandard session log",
	"first frame is not exactly one header line",
	"header frame failed validation",
	"invalid frame magic at byte",
	"torn physical tail",
}

// StoredDataRejected reports whether a failed start's output is DSH rejecting
// its own stored data rather than a plugin failing.
func StoredDataRejected(output string) bool {
	for _, signature := range storedDataRejections {
		if strings.Contains(output, signature) {
			return true
		}
	}
	return false
}

// PluginFailureCandidates returns the packages a failed start's output points
// at, most specific evidence first: the loader entry DSH could not apply, the
// plugins it reported as failed, the module that imported a missing one, then
// every package in the stack.
func PluginFailureCandidates(output string) []string {
	var candidates []string
	seen := map[string]bool{}
	add := func(specifier string) {
		name, ok := packageRoot(specifier)
		if ok && !seen[name] {
			seen[name] = true
			candidates = append(candidates, name)
		}
	}
	for _, match := range startLoaderEntry.FindAllStringSubmatch(output, -1) {
		add(match[1])
	}
	for _, match := range startLoadFailed.FindAllStringSubmatch(output, -1) {
		for _, name := range strings.Split(match[1], ",") {
			add(name)
		}
	}
	for _, match := range startImportedFrom.FindAllStringSubmatch(output, -1) {
		for _, owner := range startModulePath.FindAllStringSubmatch(match[1], -1) {
			add(owner[1])
		}
	}
	for _, match := range startMissingModule.FindAllStringSubmatch(output, -1) {
		add(match[1])
	}
	for _, match := range startModulePath.FindAllStringSubmatch(output, -1) {
		add(match[1])
	}
	return candidates
}

// packageRoot reduces a module specifier or package subpath to its package
// name, rejecting anything that is not a well-formed npm package name.
func packageRoot(specifier string) (string, bool) {
	value := strings.Trim(strings.TrimSpace(specifier), `'"`)
	value = strings.ReplaceAll(value, `\`, "/")
	parts := strings.Split(value, "/")
	name := parts[0]
	if strings.HasPrefix(name, "@") {
		if len(parts) < 2 {
			return "", false
		}
		name += "/" + parts[1]
	}
	if !npmPackageName.MatchString(strings.ToLower(name)) {
		return "", false
	}
	return name, true
}
