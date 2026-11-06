package automation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// Component is a directly managed distribution, not an npm dependency tree.
type Component struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	Capability         Capability `json:"capability"`
	InstalledVersion   string     `json:"installedVersion"`
	SupportedVersion   string     `json:"supportedVersion"`
	LatestVersion      string     `json:"latestVersion,omitempty"`
	LoadedSource       string     `json:"loadedSource,omitempty"`
	LoadedVersion      string     `json:"loadedVersion,omitempty"`
	Source             string     `json:"source,omitempty"`
	ExpectedSource     string     `json:"expectedSource,omitempty"`
	Location           string     `json:"location"`
	Adapter            string     `json:"adapter,omitempty"`
	State              string     `json:"state"`
	Step               string     `json:"step,omitempty"`
	ReceivedBytes      int64      `json:"receivedBytes"`
	TotalBytes         int64      `json:"totalBytes"`
	Failure            string     `json:"failure,omitempty"`
	CheckFailure       bool       `json:"checkFailure,omitempty"`
	CheckedAt          string     `json:"checkedAt,omitempty"`
	UnsupportedRelease bool       `json:"unsupportedRelease"`
	ManagedWithApp     bool       `json:"managedWithApp"`
	BackendAvailable   bool       `json:"backendAvailable"`
}

type componentCheck struct {
	Version string
	At      time.Time
	Failed  bool
}

func (s *Service) ConfigureComponents(inspect func() []Component) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.componentInspector = inspect
}

func (s *Service) componentsLocked() []Component {
	if s.componentInspector == nil {
		return nil
	}
	rows := s.componentInspector()
	for n := range rows {
		r := &rows[n]
		if slot := s.slots[r.Capability]; slot != nil && r.ID != "browser-extension" {
			if slot.state.Dependency == "preparing" {
				r.State = "preparing"
				r.Step = slot.state.Step
				r.ReceivedBytes = slot.state.ReceivedBytes
				r.TotalBytes = slot.state.TotalBytes
			}
			r.Failure = slot.state.Failure
			if slot.state.RepairFailure != "" {
				r.Failure = slot.state.RepairFailure
			}
		}
		if r.ID == "browser-mcp" {
			for key := range s.entries {
				if strings.HasPrefix(key, "browser/") {
					r.LoadedVersion = r.InstalledVersion
					break
				}
			}
		}
		if r.ID == "browser-extension" {
			for _, registration := range s.profiles.registration {
				if registration.Failure != "" {
					r.Failure = registration.Failure
					break
				}
			}
			for _, b := range s.profileSnapshotLocked().Bindings {
				if b.ID == s.profiles.prefs.Selected && b.LoadedSource != "" {
					r.LoadedVersion = b.LoadedVersion
					r.LoadedSource = b.LoadedSource
					if b.LoadedSource == r.Source && r.State == "prepared" {
						r.State = "loaded"
					}
					if r.Source == CurrentVersionInputs().ExtensionSource && b.LoadedSource != r.Source {
						r.State = "reload"
					}
				}
			}
		}
		if check, ok := s.componentChecks[r.ID]; ok {
			r.LatestVersion, r.CheckedAt, r.CheckFailure = check.Version, check.At.Format(time.RFC3339), check.Failed
			r.UnsupportedRelease = semver.IsValid("v"+r.SupportedVersion) && semver.IsValid("v"+check.Version) && semver.Compare("v"+check.Version, "v"+r.SupportedVersion) > 0
		}
	}
	return rows
}

// probeLocked never acquires artifacts. It is safe on startup and intent changes.
func (s *Service) probeLocked(c Capability) {
	v := s.slots[c]
	if v.state.Dependency == "preparing" {
		return
	}
	v.state.Availability = "unavailable"
	if !v.state.Enabled {
		v.state.Availability = "disabled"
	}
	if c == Browser && !s.state.DefaultBrowserEnabled {
		v.state.Availability = "disabled"
	}
	if checker, ok := s.prepare.(interface{ Ready(Capability) bool }); ok {
		if checker.Ready(c) {
			v.state.Dependency = "prepared"
		} else {
			v.state.Dependency = "unprepared"
		}
	} else if v.state.Dependency != "prepared" {
		v.state.Dependency = "unprepared"
	}
}

// InspectInstalled refreshes settings state without acquiring any artifacts.
func (s *Service) InspectInstalled() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for c, slot := range s.slots {
		if slot.state.Dependency == "prepared" || slot.state.Dependency == "unprepared" {
			before := slot.state
			s.probeLocked(c)
			changed = changed || before != slot.state
		}
	}
	if changed {
		s.publishLocked()
	}
	return s.snapshotLocked()
}

func (s *Service) SelectComputerEngine(ctx context.Context, engine string) (Snapshot, error) {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	if !s.state.CanChange || s.componentWorkLocked(Computer) {
		s.mu.Unlock()
		return s.Snapshot(), errors.New("AUTOMATION_UPDATE_BUSY")
	}
	s.mu.Unlock()
	if s.manager == nil {
		return s.Snapshot(), ErrUnavailable
	}
	if _, err := s.manager.SetComputerEngine(ctx, engine); err != nil {
		return s.Snapshot(), err
	}
	s.mu.Lock()
	s.state.ComputerEngine = engine
	s.slots[Computer].state.Epoch++
	s.slots[AUV].state.Epoch++
	s.publishLocked()
	s.mu.Unlock()
	return s.Snapshot(), nil
}

func (s *Service) componentWorkLocked(c Capability) bool {
	if c == Browser {
		return s.browserWorkLocked()
	}
	s.pruneComputerPermitsLocked()
	if s.computer != nil && s.computer.lease != nil {
		select {
		case <-s.computer.lease.closed:
			if s.computer.lease.closeErr != nil {
				return true
			}
		default:
			return true
		}
	}
	if s.computer != nil && (s.computer.use != nil || len(s.computer.pending) > 0 || len(s.computer.permits) > 0) {
		return true
	}
	for key := range s.entries {
		if strings.HasPrefix(key, "computer/") || strings.HasPrefix(key, "computer-auv/") {
			return true
		}
	}
	return s.slots[Computer].state.Dependency == "preparing" || s.slots[AUV].state.Dependency == "preparing"
}

// CheckComponents fetches release metadata only. Only this build's frozen
// combination can be installed; upstream releases require a host update.
func (s *Service) CheckComponents(ctx context.Context, force bool) (Snapshot, error) {
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	s.mu.Lock()
	last := s.lastComponentCheck
	s.mu.Unlock()
	if !force && time.Since(last) < time.Hour {
		return s.Snapshot(), nil
	}
	bounded, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	urls := map[string]string{
		"browser-mcp": "https://registry.npmjs.org/@playwright%2fmcp/latest",
		"auv":         "https://registry.npmjs.org/@auv-js%2fsdk/latest",
		"cua-driver":  "https://api.github.com/repos/trycua/cua/releases?per_page=100",
	}
	checks := map[string]componentCheck{}
	for id, url := range urls {
		check := componentCheck{At: time.Now().UTC(), Failed: true}
		req, err := http.NewRequestWithContext(bounded, http.MethodGet, url, nil)
		if err == nil {
			req.Header.Set("Accept", "application/json")
			req.Header.Set("User-Agent", "dsh-work")
			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				if resp.StatusCode == http.StatusOK {
					decoder := json.NewDecoder(io.LimitReader(resp.Body, 8<<20))
					if id == "cua-driver" {
						var releases []struct {
							Tag        string `json:"tag_name"`
							Draft      bool   `json:"draft"`
							Prerelease bool   `json:"prerelease"`
						}
						if decoder.Decode(&releases) == nil {
							for _, release := range releases {
								if !release.Draft && !release.Prerelease && strings.HasPrefix(release.Tag, "cua-driver-rs-v") {
									candidate := strings.TrimPrefix(release.Tag, "cua-driver-rs-v")
									if semver.IsValid("v"+candidate) && (check.Version == "" || semver.Compare("v"+candidate, "v"+check.Version) > 0) {
										check.Version = candidate
									}
								}
							}
						}
					} else {
						var metadata struct {
							Version string `json:"version"`
						}
						if decoder.Decode(&metadata) == nil {
							check.Version = metadata.Version
						}
					}
					check.Failed = !semver.IsValid("v" + check.Version)
				}
				resp.Body.Close()
			}
		}
		checks[id] = check
	}
	s.mu.Lock()
	s.componentChecks = checks
	s.lastComponentCheck = time.Now()
	s.publishLocked()
	s.mu.Unlock()
	return s.Snapshot(), nil
}
