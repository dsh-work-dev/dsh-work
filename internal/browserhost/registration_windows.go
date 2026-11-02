//go:build windows

package browserhost

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

type registrar struct {
	config Config
	mu     sync.Mutex
}
type manifest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Path        string   `json:"path"`
	Type        string   `json:"type"`
	Origins     []string `json:"allowed_origins"`
}
type ownership struct {
	Application string `json:"application"`
	Identity    string `json:"identity"`
	Manifest    string `json:"manifest"`
}

func newRegistrar(c Config) Registrar { return &registrar{config: c} }
func localAbsolute(p string) bool {
	volume := filepath.VolumeName(filepath.Clean(p))
	return filepath.IsAbs(p) && len(volume) == 2 && volume[1] == ':'
}
func (r *registrar) paths() (string, string) {
	m := filepath.Join(r.config.Directory, Name+".json")
	return m, m + ".owner.json"
}
func readSmallJSON(path string, v any) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() || info.Size() > 16384 {
		return errors.New("invalid native manifest")
	}
	return json.NewDecoder(io.LimitReader(f, 16384)).Decode(v)
}
func (r *registrar) owns(path string) bool {
	if !localAbsolute(path) {
		return false
	}
	var owner ownership
	var m manifest
	if readSmallJSON(path+".owner.json", &owner) != nil || readSmallJSON(path, &m) != nil {
		return false
	}
	return owner.Application == Name && owner.Identity == r.config.Identity && strings.EqualFold(owner.Manifest, path) && m.Name == Name && m.Type == "stdio" && len(m.Origins) == 1 && m.Origins[0] == Origin
}
func (r *registrar) write(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	if len(b) > 16384 {
		return errors.New("path")
	}
	f, e := os.CreateTemp(filepath.Dir(path), "native-host-*.tmp")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if e = f.Chmod(0600); e != nil {
		return e
	}
	if _, e = f.Write(b); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if r.config.Replacer == nil {
		return errors.New("native atomic file replacement is unavailable")
	}
	return r.config.Replacer.Replace(f.Name(), path)
}
func regPath(browser string) string {
	vendor := `Google\Chrome`
	if browser == "edge" {
		vendor = `Microsoft\Edge`
	}
	return `Software\` + vendor + `\NativeMessagingHosts\` + Name
}
func unpackaged() error {
	// Packaged registration/discovery is not implemented. Never report a write
	// into a package-private HKCU view as a successful browser registration.
	var length uint32
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCurrentPackageFullName")
	code, _, _ := proc.Call(uintptr(unsafe.Pointer(&length)), 0)
	if code != 15700 {
		return errors.New("package-context")
	}
	return nil
}
func (r *registrar) prepare() error {
	if !localAbsolute(r.config.Directory) || !localAbsolute(r.config.Executable) || r.config.Identity == "" {
		return errors.New("path")
	}
	info, e := os.Stat(r.config.Executable)
	if e != nil || !info.Mode().IsRegular() {
		return errors.New("executable")
	}
	m, owner := r.paths()
	if _, e := os.Stat(m); e == nil && !r.owns(m) {
		return errors.New("ownership")
	} else if e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if e := os.MkdirAll(r.config.Directory, 0700); e != nil {
		return e
	}
	var previous ownership
	if e := readSmallJSON(owner, &previous); e == nil {
		if previous.Application != Name || previous.Identity != r.config.Identity || !strings.EqualFold(previous.Manifest, m) {
			return errors.New("ownership")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return errors.New("ownership")
	}
	if e := r.write(owner, ownership{Name, r.config.Identity, m}); e != nil {
		return e
	}
	return r.write(m, manifest{Name, "DSH Work browser connection", r.config.Executable, "stdio", []string{Origin}})
}

// Refresh updates only already-owned manifests. Install/update never enables
// native messaging for a user who has not configured the feature.
func (r *registrar) Refresh(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := unpackaged(); e != nil {
		return e
	}
	if !localAbsolute(r.config.Executable) {
		return errors.New("invalid native executable path")
	}
	paths := map[string]bool{}
	var failures []error
	for _, browser := range []string{"chrome", "edge"} {
		for _, view := range []uint32{registry.WOW64_32KEY, registry.WOW64_64KEY} {
			k, e := registry.OpenKey(registry.CURRENT_USER, regPath(browser), registry.QUERY_VALUE|view)
			if errors.Is(e, registry.ErrNotExist) {
				continue
			}
			if e != nil {
				failures = append(failures, e)
				continue
			}
			value, _, e := k.GetStringValue("")
			k.Close()
			if e == nil && r.owns(value) {
				paths[value] = true
			}
		}
	}
	for path := range paths {
		var m manifest
		if e := readSmallJSON(path, &m); e != nil {
			failures = append(failures, e)
			continue
		}
		if strings.EqualFold(m.Path, r.config.Executable) {
			continue
		}
		m.Path = r.config.Executable
		if e := r.write(path, m); e != nil {
			failures = append(failures, e)
		}
	}
	return errors.Join(failures...)
}
func (r *registrar) Ensure(ctx context.Context) []Registration {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := []Registration{{Browser: "chrome"}, {Browser: "edge"}}
	if e := ctx.Err(); e != nil {
		for i := range result {
			result[i].Failure = "cancelled"
		}
		return result
	}
	if e := unpackaged(); e != nil {
		for i := range result {
			result[i].Failure = "package-context"
		}
		return result
	}
	// Check every same-name registration before writing any file or key.
	for i := range result {
		for _, hive := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
			for _, view := range []uint32{registry.WOW64_32KEY, registry.WOW64_64KEY} {
				k, e := registry.OpenKey(hive, regPath(result[i].Browser), registry.QUERY_VALUE|view)
				if errors.Is(e, registry.ErrNotExist) {
					continue
				}
				if e != nil {
					result[i].Failure = "registry"
					continue
				}
				value, _, e := k.GetStringValue("")
				k.Close()
				if e != nil || hive == registry.LOCAL_MACHINE || !r.owns(value) {
					result[i].Failure = "ownership"
				}
			}
		}
	}
	if result[0].Failure != "" && result[1].Failure != "" {
		return result
	}
	if e := r.prepare(); e != nil {
		code := e.Error()
		if code != "package-context" && code != "ownership" && code != "path" && code != "executable" {
			code = "write"
		}
		for i := range result {
			if result[i].Failure == "" {
				result[i].Failure = code
			}
		}
		return result
	}
	m, _ := r.paths()
	for i := range result {
		if result[i].Failure != "" {
			continue
		}
		// Refresh any owned 32-bit registration too, since Chrome queries it first.
		for _, view := range []uint32{registry.WOW64_32KEY, registry.WOW64_64KEY} {
			if view == registry.WOW64_32KEY {
				k, e := registry.OpenKey(registry.CURRENT_USER, regPath(result[i].Browser), registry.QUERY_VALUE|view)
				if e != nil {
					continue
				}
				k.Close()
			}
			k, _, e := registry.CreateKey(registry.CURRENT_USER, regPath(result[i].Browser), registry.SET_VALUE|view)
			if e != nil {
				result[i].Failure = "registry"
				continue
			}
			e = k.SetStringValue("", m)
			k.Close()
			if e != nil {
				result[i].Failure = "registry"
			}
		}
		result[i].Registered = result[i].Failure == ""
	}
	return result
}
func (r *registrar) Remove(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := unpackaged(); e != nil {
		return e
	}
	var failures []error
	ownedPaths := map[string]bool{}
	for _, browser := range []string{"chrome", "edge"} {
		for _, view := range []uint32{registry.WOW64_32KEY, registry.WOW64_64KEY} {
			path := regPath(browser)
			k, e := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE|registry.SET_VALUE|view)
			if errors.Is(e, registry.ErrNotExist) {
				continue
			}
			if e != nil {
				failures = append(failures, e)
				continue
			}
			value, _, e := k.GetStringValue("")
			var m manifest
			if e != nil || !r.owns(value) || readSmallJSON(value, &m) != nil || !strings.EqualFold(m.Path, r.config.Executable) {
				k.Close()
				continue
			}
			e = k.DeleteValue("")
			info, statErr := k.Stat()
			k.Close()
			if e != nil {
				failures = append(failures, e)
				continue
			}
			ownedPaths[value] = true
			if statErr == nil && info.ValueCount == 0 && info.SubKeyCount == 0 {
				parent := filepath.Dir(path)
				base, openErr := registry.OpenKey(registry.CURRENT_USER, parent, registry.ALL_ACCESS|view)
				if openErr != nil {
					failures = append(failures, openErr)
				} else {
					if e := registry.DeleteKey(base, Name); e != nil {
						failures = append(failures, e)
					}
					base.Close()
				}
			}
		}
	}
	// Delete only the two exact owned descriptor files, never their parent or
	// extension assets. Retain descriptors still referenced by any registry view.
	for path := range ownedPaths {
		referenced := false
		for _, browser := range []string{"chrome", "edge"} {
			for _, hive := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
				for _, view := range []uint32{registry.WOW64_32KEY, registry.WOW64_64KEY} {
					k, e := registry.OpenKey(hive, regPath(browser), registry.QUERY_VALUE|view)
					if errors.Is(e, registry.ErrNotExist) {
						continue
					}
					if e != nil {
						referenced = true
						continue
					}
					value, _, e := k.GetStringValue("")
					k.Close()
					if e == nil && strings.EqualFold(value, path) {
						referenced = true
					}
				}
			}
		}
		if referenced {
			continue
		}
		if !r.owns(path) {
			continue
		}
		for _, file := range []string{path, path + ".owner.json"} {
			if e := os.Remove(file); e != nil && !errors.Is(e, os.ErrNotExist) {
				failures = append(failures, e)
			}
		}
	}
	return errors.Join(failures...)
}
