//go:build linux

package dshadapter

import (
	"os"
	"strings"
)

// Linux's supervised bootstrap receives an exact Env, rather than the service
// manager's environment. Construct the application's inherited environment at
// the DSH launch-plan boundary before applying its explicit child overrides.
func launchEnvironment() map[string]string {
	env := map[string]string{}
	for _, entry := range os.Environ() {
		if key, value, ok := strings.Cut(entry, "="); ok {
			env[key] = value
		}
	}
	return env
}
