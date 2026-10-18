package dshmanager

import (
	"context"
	"regexp"
	"strings"
)

type acceptIncompatibleKey struct{}

// WithAcceptIncompatible marks a plugin operation the user confirmed despite
// DSH reporting an incompatible plugin version.
func WithAcceptIncompatible(ctx context.Context) context.Context {
	return context.WithValue(ctx, acceptIncompatibleKey{}, true)
}

// AcceptIncompatible reports whether ctx carries that confirmation.
func AcceptIncompatible(ctx context.Context) bool {
	accepted, _ := ctx.Value(acceptIncompatibleKey{}).(bool)
	return accepted
}

// IncompatiblePluginVersion is one package DSH refused for the running DSH version.
type IncompatiblePluginVersion struct {
	Name, Version, DSHVersion string
}

type incompatiblePluginError struct{ items []IncompatiblePluginVersion }

func (e incompatiblePluginError) Error() string {
	parts := make([]string, 0, len(e.items))
	for _, item := range e.items {
		parts = append(parts, item.Name+"@"+item.Version+" (DSH "+item.DSHVersion+")")
	}
	return strings.Join(parts, ", ")
}

var incompatibleLinePattern = regexp.MustCompile(`(?m)^dsh: to accept the risk, run: dsh plugin --profile \S+ allow-version (\S+) --dsh-version (\S+) --accept-risk\s*$`)

// parseIncompatiblePlugins reads the packages DSH names in its refusal output.
func parseIncompatiblePlugins(output string) []IncompatiblePluginVersion {
	matches := incompatibleLinePattern.FindAllStringSubmatch(output, -1)
	var items []IncompatiblePluginVersion
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		packageVersion := match[1]
		separator := strings.LastIndex(packageVersion, "@")
		if separator <= 0 || separator == len(packageVersion)-1 {
			continue
		}
		item := IncompatiblePluginVersion{
			Name:       packageVersion[:separator],
			Version:    packageVersion[separator+1:],
			DSHVersion: match[2],
		}
		key := item.Name + "@" + item.Version
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		items = append(items, item)
	}
	return items
}
