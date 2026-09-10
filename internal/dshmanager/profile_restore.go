package dshmanager

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/local/dsh-work/internal/lifecycle"
)

const profileBackupFolder = ".dsh-work-backups"

const maxBackupBytes = 512 << 20
const maxBackupEntries = 100000

type ProfileRestoreRequest struct {
	DataDirectoryID string `json:"dataDirectoryId"`
	FileName        string `json:"fileName"`
	ProfileName     string `json:"profileName"`
}

type profileBackupManifest struct {
	Profile   ProfileRef `json:"profile"`
	CreatedAt string     `json:"createdAt"`
	Format    string     `json:"format"`
}

func readBackupManifest(archive *zip.Reader) (profileBackupManifest, error) {
	var manifest profileBackupManifest
	file, err := archive.Open("_dsh-work/manifest.json")
	if err != nil {
		return manifest, errors.New("not a dsh-work profile backup")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 64<<10))
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, err
	}
	if manifest.Format != "dsh-work-profile-v1" {
		return manifest, errors.New("unsupported profile backup format")
	}
	if _, err := time.Parse(time.RFC3339, manifest.CreatedAt); err != nil {
		return manifest, errors.New("invalid backup creation time")
	}
	return manifest, validateProfileRef(manifest.Profile)
}

func (m *Manager) backupDataDirectory(id string) (DataDirectoryInfo, error) {
	m.mu.RLock()
	directory, ok := findDataDirectory(m.config.DataDirectories, id)
	m.mu.RUnlock()
	if !ok || !directoryExists(directory.Path) {
		return directory, errors.New("DSH data directory is unavailable")
	}
	return directory, nil
}

// ProfileBackupDirectory upgrades legacy backups under the manager operation gate.
func (m *Manager) ProfileBackupDirectory(ctx context.Context, ref ProfileRef) (result string, resultErr error) {
	defer func() {
		resultErr = recoveryFailure(resultErr, lifecycle.ErrorProfileBackupFailed, "The backup directory is unavailable.")
	}()
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	if err := m.ensureMutationAllowed(); err != nil {
		return "", err
	}
	return m.prepareProfileBackups(ctx, ref)
}

// Called under the operation gate. Ownership follows the profile directory,
// while the portable ZIP manifest retains the identity at capture time.
func (m *Manager) prepareProfileBackups(ctx context.Context, ref ProfileRef) (string, error) {
	if err := contextError(ctx); err != nil {
		return "", err
	}
	if err := validateProfileRef(ref); err != nil {
		return "", err
	}
	directory, err := m.backupDataDirectory(ref.DataDirectoryID)
	if err != nil {
		return "", err
	}
	home, err := os.OpenRoot(directory.Path)
	if err != nil {
		return "", err
	}
	defer home.Close()
	relative := filepath.Join("profiles", ref.Name)
	info, err := home.Lstat(relative)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("profile must be a directory, not a link")
	}
	profile, err := home.OpenRoot(relative)
	if err != nil {
		return "", err
	}
	defer profile.Close()
	if err := profile.MkdirAll(profileBackupFolder, 0700); err != nil {
		return "", err
	}
	destination := filepath.Join(relative, profileBackupFolder)
	legacy, err := home.Open("profile-backups")
	if errors.Is(err, os.ErrNotExist) {
		return filepath.Join(directory.Path, destination), nil
	}
	if err != nil {
		return "", err
	}
	entries, err := legacy.ReadDir(-1)
	legacy.Close()
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if err := contextError(ctx); err != nil {
			return "", err
		}
		if !entry.Type().IsRegular() || !strings.EqualFold(filepath.Ext(entry.Name()), ".zip") {
			continue
		}
		source := filepath.Join("profile-backups", entry.Name())
		file, err := home.Open(source)
		if err != nil {
			return "", err
		}
		info, statErr := file.Stat()
		if statErr != nil {
			file.Close()
			return "", statErr
		}
		archive, readErr := zip.NewReader(file, info.Size())
		var manifest profileBackupManifest
		if readErr == nil {
			manifest, readErr = readBackupManifest(archive)
		}
		file.Close()
		if readErr != nil || manifest.Profile != ref {
			continue
		}
		name := entry.Name()
		for n := 2; ; n++ {
			_, err := home.Lstat(filepath.Join(destination, name))
			if errors.Is(err, os.ErrNotExist) {
				break
			}
			if err != nil {
				return "", err
			}
			name = fmt.Sprintf("%s-%d.zip", strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())), n)
		}
		if err := home.Rename(source, filepath.Join(destination, name)); err != nil {
			return "", err
		}
	}
	return filepath.Join(directory.Path, destination), nil
}

func (m *Manager) ListProfileBackups(ctx context.Context, ref ProfileRef) (backups []ProfileBackupResult, resultErr error) {
	defer func() {
		resultErr = recoveryFailure(resultErr, lifecycle.ErrorProfileBackupFailed, "Profile backups could not be listed.")
	}()
	result := []ProfileBackupResult{}
	directory, err := m.ProfileBackupDirectory(ctx, ref)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if err := contextError(ctx); err != nil {
			return nil, err
		}
		if !entry.Type().IsRegular() || !strings.EqualFold(filepath.Ext(entry.Name()), ".zip") {
			continue
		}
		archive, err := zip.OpenReader(filepath.Join(directory, entry.Name()))
		if err != nil {
			continue
		}
		manifest, readErr := readBackupManifest(&archive.Reader)
		createdAt := manifest.CreatedAt
		archive.Close()
		info, statErr := entry.Info()
		if readErr != nil || statErr != nil {
			continue
		}
		result = append(result, ProfileBackupResult{Profile: ref, FileName: entry.Name(), Size: info.Size(), CreatedAt: createdAt})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].FileName > result[j].FileName })
	return result, nil
}

// DeleteProfileBackup removes one archive from the selected profile's backup directory.
func (m *Manager) DeleteProfileBackup(ctx context.Context, ref ProfileRef, fileName string) (resultErr error) {
	defer func() {
		resultErr = recoveryFailure(resultErr, lifecycle.ErrorProfileBackupFailed, "The profile backup could not be deleted.")
	}()
	if filepath.Base(fileName) != fileName || !filepath.IsLocal(fileName) || strings.ContainsAny(fileName, `\/:`) || !strings.EqualFold(filepath.Ext(fileName), ".zip") {
		return errors.New("invalid backup filename")
	}
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err := m.ensureMutationAllowed(); err != nil {
		return err
	}
	if _, err := m.prepareProfileBackups(ctx, ref); err != nil {
		return err
	}
	directory, err := m.backupDataDirectory(ref.DataDirectoryID)
	if err != nil {
		return err
	}
	home, err := os.OpenRoot(directory.Path)
	if err != nil {
		return err
	}
	defer home.Close()
	relative := filepath.Join("profiles", ref.Name, profileBackupFolder)
	info, err := home.Lstat(relative)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("backup directory must not be a link")
	}
	backups, err := home.OpenRoot(relative)
	if err != nil {
		return err
	}
	defer backups.Close()
	info, err = backups.Lstat(fileName)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("backup must be a regular file")
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	return backups.Remove(fileName)
}

func (m *Manager) RestoreProfileBackup(ctx context.Context, request ProfileRestoreRequest) (result ProfileCloneResult, resultErr error) {
	defer func() {
		resultErr = recoveryFailure(resultErr, lifecycle.ErrorProfileBackupFailed, "The profile backup could not be restored.")
	}()
	if filepath.Base(request.FileName) != request.FileName || !filepath.IsLocal(request.FileName) || strings.ContainsAny(request.FileName, `\/:`) {
		return ProfileCloneResult{}, errors.New("invalid backup filename")
	}
	directory, err := m.ProfileBackupDirectory(ctx, ProfileRef{DataDirectoryID: request.DataDirectoryID, Name: request.ProfileName})
	if err != nil {
		return ProfileCloneResult{}, err
	}
	file := filepath.Join(directory, request.FileName)
	info, err := os.Lstat(file)
	if err != nil {
		return ProfileCloneResult{}, err
	}
	if !info.Mode().IsRegular() {
		return ProfileCloneResult{}, errors.New("backup must be a regular file")
	}
	return m.ImportProfileBackup(ctx, request.DataDirectoryID, file)
}

// ImportProfileBackup accepts a native-picker selection. Restoration publishes a
// new profile, never merges archive entries into an existing user's directory.
func (m *Manager) ImportProfileBackup(ctx context.Context, id, file string) (result ProfileCloneResult, resultErr error) {
	defer func() {
		resultErr = recoveryFailure(resultErr, lifecycle.ErrorProfileBackupFailed, "The profile backup could not be restored.")
	}()
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return ProfileCloneResult{}, err
	}
	defer release()
	if err := m.ensureMutationAllowed(); err != nil {
		return ProfileCloneResult{}, err
	}
	directory, err := m.backupDataDirectory(id)
	if err != nil {
		return ProfileCloneResult{}, err
	}
	archive, err := zip.OpenReader(file)
	if err != nil {
		return ProfileCloneResult{}, fmt.Errorf("open profile backup: %w", err)
	}
	defer archive.Close()
	manifest, err := readBackupManifest(&archive.Reader)
	if err != nil {
		return ProfileCloneResult{}, err
	}
	if len(archive.File) > maxBackupEntries {
		return ProfileCloneResult{}, errors.New("profile backup has too many files")
	}
	home, err := os.OpenRoot(directory.Path)
	if err != nil {
		return ProfileCloneResult{}, err
	}
	defer home.Close()
	if err := home.MkdirAll("profiles", 0700); err != nil {
		return ProfileCloneResult{}, err
	}
	staging, err := os.MkdirTemp(directory.Path, ".profile-restore-")
	if err != nil {
		return ProfileCloneResult{}, err
	}
	defer os.RemoveAll(staging)
	root, err := os.OpenRoot(staging)
	if err != nil {
		return ProfileCloneResult{}, err
	}
	defer root.Close()
	seen := map[string]bool{}
	var total uint64
	for _, entry := range archive.File {
		if err := contextError(ctx); err != nil {
			return ProfileCloneResult{}, err
		}
		name := strings.TrimSuffix(entry.Name, "/")
		if name == "" || path.Clean(name) != name || !filepath.IsLocal(name) || strings.ContainsAny(name, `\:`) {
			return ProfileCloneResult{}, errors.New("profile backup contains an invalid path")
		}
		for _, component := range strings.Split(name, "/") {
			if strings.TrimRight(component, " .") != component || !filepath.IsLocal(component) {
				return ProfileCloneResult{}, errors.New("profile backup contains an invalid filename")
			}
		}
		key := strings.ToLower(name)
		if seen[key] {
			return ProfileCloneResult{}, errors.New("profile backup contains duplicate files")
		}
		seen[key] = true
		if entry.Mode()&os.ModeSymlink != 0 || (!entry.FileInfo().IsDir() && !entry.Mode().IsRegular()) {
			return ProfileCloneResult{}, errors.New("profile backup contains a linked or special file")
		}
		if entry.UncompressedSize64 > maxBackupBytes-total {
			return ProfileCloneResult{}, errors.New("profile backup is too large")
		}
		total += entry.UncompressedSize64
		if name == "_dsh-work/manifest.json" || name == "_dsh-work" {
			continue
		}
		if strings.HasPrefix(name, "_dsh-work/") {
			return ProfileCloneResult{}, errors.New("unsupported backup metadata")
		}
		if strings.EqualFold(strings.Split(name, "/")[0], profileBackupFolder) {
			continue
		}
		if entry.FileInfo().IsDir() {
			if err := root.MkdirAll(name, 0700); err != nil {
				return ProfileCloneResult{}, err
			}
			continue
		}
		if err := root.MkdirAll(path.Dir(name), 0700); err != nil {
			return ProfileCloneResult{}, err
		}
		input, err := entry.Open()
		if err != nil {
			return ProfileCloneResult{}, err
		}
		output, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			input.Close()
			return ProfileCloneResult{}, err
		}
		_, copyErr := io.Copy(output, &backupContextReader{ctx: ctx, reader: input})
		syncErr := output.Sync()
		err = errors.Join(copyErr, syncErr, output.Close(), input.Close())
		if err != nil {
			return ProfileCloneResult{}, fmt.Errorf("extract profile backup: %w", err)
		}
	}
	if err := contextError(ctx); err != nil {
		return ProfileCloneResult{}, err
	}
	if err := validateBackupProfile(staging); err != nil {
		return ProfileCloneResult{}, err
	}
	// Close the rooted handle before renaming on Windows.
	if err := root.Close(); err != nil {
		return ProfileCloneResult{}, err
	}
	m.mu.RLock()
	catalog := m.config.ProfileCatalog
	m.mu.RUnlock()
	name := nextProfileName(catalog, directory, manifest.Profile.Name+" restored")
	if name == "" {
		return ProfileCloneResult{}, errors.New("no available profile name")
	}
	if err := contextError(ctx); err != nil {
		return ProfileCloneResult{}, err
	}
	if err := home.Rename(filepath.Base(staging), filepath.Join("profiles", name)); err != nil {
		return ProfileCloneResult{}, err
	}
	snapshot, err := m.Snapshot(context.Background())
	return ProfileCloneResult{Profile: ProfileRef{DataDirectoryID: id, Name: name}, Snapshot: snapshot}, err
}

type backupContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func recoveryFailure(err error, code lifecycle.ErrorCode, summary string) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return preserveFailure(err, code, summary, err.Error(), true, false)
}

func validateBackupProfile(directory string) error {
	file, err := os.Open(filepath.Join(directory, "package.json"))
	if err != nil {
		return errors.New("backup has no profile package.json")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxProfileManifestBytes+1))
	if err != nil {
		return err
	}
	var manifest map[string]json.RawMessage
	if len(data) > maxProfileManifestBytes || json.Unmarshal(data, &manifest) != nil || manifest == nil {
		return errors.New("invalid profile package.json")
	}
	return nil
}

func (r *backupContextReader) Read(p []byte) (int, error) {
	if err := contextError(r.ctx); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
