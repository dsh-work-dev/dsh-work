package app

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/local/dsh-work/internal/storagepaths"
)

// DefaultUserDataPath keeps OS path semantics out of presentation code.
func (s *StorageService) DefaultUserDataPath(ctx context.Context, root string) (string, error) {
	if !isTrustedWindow(ctx, "settings") {
		return "", trustedSurfaceRequired("Storage locations are available in Settings.")
	}
	if !filepath.IsAbs(root) {
		return "", errors.New("invalid storage root")
	}
	return (storagepaths.Locations{Root: root}).UserDataPath(), nil
}

// StorageService exposes only the two desktop-owned location choices.
type StorageService struct {
	manager *storagepaths.Manager
	open    func(string) error
	choose  func(string) (string, error)
}

func NewStorageService(manager *storagepaths.Manager, open func(string) error, choose func(string) (string, error)) *StorageService {
	return &StorageService{manager: manager, open: open, choose: choose}
}
func (s *StorageService) OpenLocation(ctx context.Context, userData bool) error {
	if !isTrustedWindow(ctx, "settings") {
		return trustedSurfaceRequired("Storage locations are available in Settings.")
	}
	current := s.manager.Snapshot().Current
	path := current.Root
	if userData {
		path = current.UserDataPath()
	}
	return s.open(path)
}
func (s *StorageService) ChooseDirectory(ctx context.Context, currentPath string) (string, error) {
	if !isTrustedWindow(ctx, "settings") {
		return "", trustedSurfaceRequired("Storage locations are available in Settings.")
	}
	return s.choose(currentPath)
}
func (s *StorageService) GetLocations(ctx context.Context) (storagepaths.State, error) {
	if !isTrustedWindow(ctx, "settings") {
		return storagepaths.State{}, trustedSurfaceRequired("Storage locations are available in Settings.")
	}
	return s.manager.Snapshot(), nil
}
func (s *StorageService) SaveLocations(ctx context.Context, locations storagepaths.Locations) (storagepaths.State, error) {
	if !isTrustedWindow(ctx, "settings") {
		return storagepaths.State{}, trustedSurfaceRequired("Storage locations are available in Settings.")
	}
	return s.manager.Save(locations)
}
func (s *StorageService) CancelMigration(ctx context.Context) (storagepaths.State, error) {
	if !isTrustedWindow(ctx, "settings") {
		return storagepaths.State{}, trustedSurfaceRequired("Storage locations are available in Settings.")
	}
	return s.manager.Cancel()
}
