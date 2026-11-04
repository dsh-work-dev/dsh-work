package browserhost

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	browserextension "github.com/local/dsh-work/browser-extension"
)

func DevelopmentDirectory(c Config) string {
	return filepath.Join(c.Directory, "extension", "development")
}

// InstallDevelopmentBundle publishes only the module's known owned files. It
// neither installs an extension into a browser nor changes browser configuration.
func InstallDevelopmentBundle(ctx context.Context, c Config) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	dir := DevelopmentDirectory(c)
	marker := filepath.Join(dir, ".dsh-work-owned")
	if info, e := os.Lstat(dir); e == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("development extension path is not a directory")
		}
		data, e := os.ReadFile(marker)
		if e != nil || string(data) != Name+"\n"+c.Identity+"\n" {
			return errors.New("development extension directory belongs to another owner")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	// Build a complete new directory first; never publish a mixed file set.
	files := map[string][]byte{".dsh-work-owned": []byte(Name + "\n" + c.Identity + "\n"), "bundle-source.txt": []byte(browserextension.SourceIdentity + "\n")}
	for _, name := range []string{"manifest.json", "background.js", "protocol.js", "popup.html", "popup.js", "routes.js", "LICENSE", "NOTICE", "PROVENANCE.txt", "icon-16.png", "icon-32.png", "icon-48.png", "icon-128.png"} {
		b, e := browserextension.Files.ReadFile(name)
		if e != nil {
			return e
		}
		files[name] = b
	}
	equal := true
	for name, want := range files {
		got, e := os.ReadFile(filepath.Join(dir, name))
		if e != nil || string(got) != string(want) {
			equal = false
			break
		}
	}
	if equal {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		return err
	}
	stage, e := os.MkdirTemp(filepath.Dir(dir), ".development-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(stage)
	for name, b := range files {
		if e = ctx.Err(); e != nil {
			return e
		}
		f, err := os.OpenFile(filepath.Join(stage, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, err = f.Write(b)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if _, e = os.Stat(dir); errors.Is(e, os.ErrNotExist) {
		return os.Rename(stage, dir)
	} else if e != nil {
		return e
	}
	backup, e := os.MkdirTemp(filepath.Dir(dir), "development-previous-")
	if e != nil {
		return e
	}
	if e = os.Remove(backup); e != nil {
		return e
	}
	if e = os.Rename(dir, backup); e != nil {
		return e
	}
	if e = os.Rename(stage, dir); e != nil {
		if rollback := os.Rename(backup, dir); rollback != nil {
			return errors.Join(e, rollback)
		}
		return e
	}
	// Retain the owned previous complete bundle for recovery; storage is not reset.
	return nil
}
