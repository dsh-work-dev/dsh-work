//go:build darwin && cgo

// Package macowner owns only the pinned, app-owned CEF component. It is not an
// adapter for arbitrary DSH LaunchPlans, detached commands, or PTY processes.
package macowner

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	"github.com/local/dsh-work/internal/localipc"
	"github.com/local/dsh-work/internal/supervisor"
	"golang.org/x/sys/unix"
)

var ErrCleanupPending = errors.New("MAC_CEF_CLEANUP_PENDING")

type owned struct {
	Process Process                 `json:"process"`
	Target  *localipc.ProcessTarget `json:"target,omitempty"`
	Role    string                  `json:"role"`
}

type nativeEvent struct {
	Name string    `json:"name"`
	At   time.Time `json:"at"`
}

type journal struct {
	ExitSignal            int                   `json:"exitSignal,omitempty"`
	ExitCoreDump          bool                  `json:"exitCoreDump"`
	NativeEvents          []nativeEvent         `json:"nativeEvents,omitempty"`
	NativeEventsTruncated bool                  `json:"nativeEventsTruncated"`
	Generation            string                `json:"generation"`
	Job                   string                `json:"job"`
	Owner                 Process               `json:"owner"`
	Broker                Process               `json:"broker"`
	Phase                 string                `json:"phase"`
	Owned                 map[string]owned      `json:"owned"`
	Children              []Process             `json:"quiescentChildren,omitempty"`
	RuntimeVersion        string                `json:"runtimeVersion,omitempty"`
	RuntimeReady          bool                  `json:"runtimeReady"`
	Exit                  supervisor.ExitResult `json:"exit"`
}

type signedJournal struct {
	Body json.RawMessage `json:"body"`
	MAC  string          `json:"mac"`
}

func privateDir(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("absolute private owner directory required")
	}
	if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	var info unix.Stat_t
	if err = unix.Fstat(fd, &info); err != nil {
		return err
	}
	if info.Uid != uint32(os.Geteuid()) || info.Mode&0777 != 0700 {
		return errors.New("owner directory is not private")
	}
	return nil
}

func readPrivate(path string) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok || s.Uid != uint32(os.Geteuid()) || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 1<<20 {
		return nil, errors.New("private owner file rejected")
	}
	return io.ReadAll(io.LimitReader(f, (1<<20)+1))
}

func writePrivate(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".cef-owner-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func ownerKey(dir string) ([]byte, error) {
	path := filepath.Join(dir, "journal.key")
	key, err := readPrivate(path)
	if err == nil {
		if len(key) != 32 {
			return nil, errors.New("owner key invalid")
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key = make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return nil, err
	}
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if errors.Is(err, unix.EEXIST) {
		return readPrivate(path)
	}
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	_, err = f.Write(key)
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	return key, err
}

func signedFile(dir, name, kind string, key, body []byte) error {
	if len(body) > maxFrame-1024 {
		return ErrCleanupPending
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(kind + "\x00"))
	mac.Write(body)
	data, err := json.Marshal(signedJournal{Body: body, MAC: hex.EncodeToString(mac.Sum(nil))})
	if err != nil {
		return err
	}
	return writePrivate(filepath.Join(dir, name), data)
}
func readSigned(dir, name, kind string, key []byte) ([]byte, error) {
	data, err := readPrivate(filepath.Join(dir, name))
	if err != nil {
		return nil, err
	}
	var value signedJournal
	if err = json.Unmarshal(data, &value); err != nil {
		return nil, errors.Join(ErrCleanupPending, err)
	}
	actual, err := hex.DecodeString(value.MAC)
	if err != nil {
		return nil, errors.Join(ErrCleanupPending, err)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(kind + "\x00"))
	mac.Write(value.Body)
	if !hmac.Equal(actual, mac.Sum(nil)) {
		return nil, fmt.Errorf("%w: journal authentication failed", ErrCleanupPending)
	}
	return value.Body, nil
}
func generationName(id string) (string, error) {
	if !regexp.MustCompile(`^cef-[A-Za-z0-9]+$`).MatchString(id) {
		return "", ErrCleanupPending
	}
	return "generation-" + id + ".json", nil
}

// Each immutable generation has its own record. Completing an old Worker must
// never redirect the current profile scope onto a newer live generation.
func save(dir string, key []byte, state journal) error {
	name, err := generationName(state.Generation)
	if err != nil {
		return err
	}
	body, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return signedFile(dir, name, "cef-generation-v1", key, body)
}
func setCurrent(dir string, key []byte, id string) error {
	if _, err := generationName(id); err != nil {
		return err
	}
	body, err := json.Marshal(struct {
		Generation string `json:"generation"`
	}{id})
	if err != nil {
		return err
	}
	return signedFile(dir, "current.json", "cef-current-v1", key, body)
}
func loadGeneration(dir string, key []byte, id string) (journal, error) {
	var state journal
	name, err := generationName(id)
	if err != nil {
		return state, err
	}
	body, err := readSigned(dir, name, "cef-generation-v1", key)
	if err != nil {
		return state, err
	}
	if err = json.Unmarshal(body, &state); err != nil {
		return state, errors.Join(ErrCleanupPending, err)
	}
	if state.Generation != id || state.Owned == nil {
		return state, ErrCleanupPending
	}
	return state, nil
}
func load(dir string, key []byte) (journal, error) {
	var current struct {
		Generation string `json:"generation"`
	}
	body, err := readSigned(dir, "current.json", "cef-current-v1", key)
	if err != nil {
		return journal{}, err
	}
	if err = json.Unmarshal(body, &current); err != nil {
		return journal{}, errors.Join(ErrCleanupPending, err)
	}
	return loadGeneration(dir, key, current.Generation)
}
