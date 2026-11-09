//go:build linux

package linux

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// Only process/cgroup permission facts. Never serialize LaunchPlan, its Env,
// arguments, manager Environment, output, or page values into this diagnostic.
func launchDiagnostic(destination, executable string, anchor uint32, cause error) string {
	data, _ := os.ReadFile("/proc/self/cgroup")
	caller := ""
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "0::") {
			caller = strings.TrimPrefix(line, "0::")
			break
		}
	}
	callerPath := filepath.Join("/sys/fs/cgroup", caller)
	ancestor := destination
	for ancestor != "/" && callerPath != ancestor && !strings.HasPrefix(callerPath, ancestor+"/") {
		ancestor = filepath.Dir(ancestor)
	}
	facts := map[string]any{"callerCgroup": caller, "destination": destination, "commonAncestor": ancestor, "anchorPID": anchor, "uid": os.Getuid(), "euid": os.Geteuid()}
	var errno syscall.Errno
	if errors.As(cause, &errno) {
		facts["errno"] = int(errno)
	}
	paths := []string{callerPath, destination, ancestor, filepath.Join(callerPath, "cgroup.procs"), filepath.Join(destination, "cgroup.procs"), filepath.Join(ancestor, "cgroup.procs")}
	var permissions []map[string]any
	for _, path := range paths {
		row := map[string]any{"path": path}
		info, err := os.Stat(path)
		if err != nil {
			row["statError"] = err.Error()
		} else {
			row["mode"] = strconv.FormatUint(uint64(info.Mode().Perm()), 8)
			if st, ok := info.Sys().(*syscall.Stat_t); ok {
				row["uid"] = st.Uid
				row["gid"] = st.Gid
			}
			if e := unix.Access(path, unix.W_OK); e != nil {
				row["writeAccess"] = e.Error()
			} else {
				row["writeAccess"] = "ok"
			}
		}
		permissions = append(permissions, row)
	}
	facts["permissions"] = permissions
	if e := unix.Access(executable, unix.X_OK); e != nil {
		facts["executableAccess"] = e.Error()
	} else {
		facts["executableAccess"] = "ok"
	}
	var fs unix.Statfs_t
	if unix.Statfs(filepath.Dir(executable), &fs) == nil {
		facts["executableMountNoExec"] = fs.Flags&unix.MS_NOEXEC != 0
	}
	status, _ := os.ReadFile("/proc/self/status")
	for _, line := range strings.Split(string(status), "\n") {
		for _, key := range []string{"NoNewPrivs:", "Seccomp:", "Seccomp_filters:"} {
			if strings.HasPrefix(line, key) {
				facts[strings.TrimSuffix(key, ":")] = strings.TrimSpace(strings.TrimPrefix(line, key))
			}
		}
	}
	if value, e := os.Readlink("/proc/self/ns/cgroup"); e == nil {
		facts["cgroupNamespace"] = value
	}
	encoded, _ := json.Marshal(facts)
	return string(encoded)
}
