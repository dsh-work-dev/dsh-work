//go:build darwin && cgo && e67nativefixture

package macowner

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode"
)

type safeCrashFrame struct {
	Function string `json:"function"`
	Module   string `json:"module"`
}
type safeCrash struct {
	Status                      string           `json:"status"`
	PID                         uint32           `json:"pid,omitempty"`
	ParentPID                   uint32           `json:"parentPid,omitempty"`
	KernelStarted               uint64           `json:"kernelStarted,omitempty"`
	ReportLaunchAt              *time.Time       `json:"reportLaunchAt,omitempty"`
	ReportLaunchPrecisionMicros int64            `json:"reportLaunchPrecisionMicros,omitempty"`
	CapturedAt                  *time.Time       `json:"capturedAt,omitempty"`
	ExceptionType               string           `json:"exceptionType,omitempty"`
	ExceptionSignal             string           `json:"exceptionSignal,omitempty"`
	TerminationCode             int              `json:"terminationCode,omitempty"`
	TerminationNamespace        string           `json:"terminationNamespace,omitempty"`
	Frames                      []safeCrashFrame `json:"crashedThread,omitempty"`
}

// Deliberately omit registers, addresses, exception rawCodes, application-
// specific information, command arguments, environment and original report.
type systemCrash struct {
	PID         uint32 `json:"pid"`
	ParentPID   uint32 `json:"parentPid"`
	ProcPath    string `json:"procPath"`
	ProcLaunch  string `json:"procLaunch"`
	CaptureTime string `json:"captureTime"`
	Exception   struct {
		Type   string `json:"type"`
		Signal string `json:"signal"`
	} `json:"exception"`
	Termination struct {
		Code      int    `json:"code"`
		Namespace string `json:"namespace"`
	} `json:"termination"`
	FaultingThread *int `json:"faultingThread"`
	Threads        []struct {
		Triggered bool `json:"triggered"`
		Frames    []struct {
			Symbol     string `json:"symbol"`
			ImageIndex int    `json:"imageIndex"`
		} `json:"frames"`
	} `json:"threads"`
	UsedImages []struct {
		Name string `json:"name"`
		Path string `json:"path"`
	} `json:"usedImages"`
}

func crashTime(text string) (time.Time, int64, error) {
	at, err := time.Parse("2006-01-02 15:04:05.999999999 -0700", text)
	if err != nil {
		return time.Time{}, 0, err
	}
	fields := strings.Fields(text)
	digits := 0
	if len(fields) >= 2 {
		if _, fraction, ok := strings.Cut(fields[1], "."); ok {
			digits = len(fraction)
		}
	}
	// An entire-second or millisecond timestamp is too coarse to bind a report.
	// Apple's normal report has four fractional digits; retain its actual precision.
	precision := int64(1000000)
	for i := 0; i < digits && i < 6; i++ {
		precision /= 10
	}
	return at, precision, nil
}
func diagnosticText(text string, limit int) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, text)
	if len(text) > limit {
		return text[:limit]
	}
	return text
}
func projectSystemCrash(report systemCrash, state journal, now time.Time) *safeCrash {
	original := state.Broker
	if original.PID == 0 || original.Started == 0 || original.Path == "" || state.Owner.PID == 0 {
		return nil
	}
	if report.PID != original.PID || report.ParentPID != state.Owner.PID || report.ProcPath != original.Path {
		return nil
	}
	launch, precision, err := crashTime(report.ProcLaunch)
	if err != nil || precision > 100 || precision < 1 {
		return nil
	}
	// Compare at the precision supplied by the system rather than inventing
	// missing digits. Parent, path and PID must independently match this single
	// broker instance launched by the still-owned, non-restarting guard.
	if launch.UnixMicro()/precision != int64(original.Started)/precision {
		return nil
	}
	captured, _, err := crashTime(report.CaptureTime)
	if err != nil || captured.Before(launch) || captured.After(now.Add(time.Second)) {
		return nil
	}
	index := -1
	if report.FaultingThread != nil {
		index = *report.FaultingThread
	} else {
		for i, thread := range report.Threads {
			if thread.Triggered {
				if index != -1 {
					return nil
				}
				index = i
			}
		}
	}
	if index < 0 || index >= len(report.Threads) {
		return nil
	}
	result := &safeCrash{Status: "matched-system-report", PID: report.PID, ParentPID: report.ParentPID, KernelStarted: original.Started, ReportLaunchAt: &launch, ReportLaunchPrecisionMicros: precision, CapturedAt: &captured, ExceptionType: diagnosticText(report.Exception.Type, 64), ExceptionSignal: diagnosticText(report.Exception.Signal, 32), TerminationCode: report.Termination.Code, TerminationNamespace: diagnosticText(report.Termination.Namespace, 64), Frames: []safeCrashFrame{}}
	for _, frame := range report.Threads[index].Frames {
		if len(result.Frames) == 64 {
			break
		}
		module := "<unresolved>"
		if frame.ImageIndex >= 0 && frame.ImageIndex < len(report.UsedImages) {
			image := report.UsedImages[frame.ImageIndex]
			module = image.Name
			if module == "" {
				module = filepath.Base(image.Path)
			}
		}
		function := frame.Symbol
		if function == "" {
			function = "<unresolved>"
		}
		result.Frames = append(result.Frames, safeCrashFrame{Function: diagnosticText(function, 1024), Module: diagnosticText(module, 256)})
	}
	return result
}
func conditionalSystemCrash(state journal) *safeCrash {
	if state.ExitSignal == 0 {
		return &safeCrash{Status: "not-signalled"}
	}
	if os.Getenv("CI") != "true" {
		return &safeCrash{Status: "not-isolated-ci"}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return &safeCrash{Status: "missing-home"}
	}
	dir := filepath.Join(home, "Library", "Logs", "DiagnosticReports")
	deadline := time.Now().Add(3 * time.Second)
	for {
		entries, err := os.ReadDir(dir)
		if err == nil {
			// Only this broker's executable reports are candidates; no general log export.
			names := []string{}
			for _, entry := range entries {
				if !entry.IsDir() && strings.HasPrefix(entry.Name(), "cefsimple-") && strings.HasSuffix(entry.Name(), ".ips") {
					names = append(names, entry.Name())
				}
			}
			sort.Sort(sort.Reverse(sort.StringSlice(names)))
			if len(names) > 32 {
				names = names[:32]
			}
			for _, name := range names {
				path := filepath.Join(dir, name)
				info, err := os.Lstat(path)
				if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<20 || info.ModTime().Before(time.UnixMicro(int64(state.Broker.Started)).Add(-time.Second)) {
					continue
				}
				f, err := os.Open(path)
				if err != nil {
					continue
				}
				decoder := json.NewDecoder(io.LimitReader(f, 16<<20))
				// macOS .ips is either one JSON object or a metadata JSON line followed
				// by the report JSON. Decoding directly into the allowlist drops all else.
				for i := 0; i < 2; i++ {
					var report systemCrash
					if decoder.Decode(&report) != nil {
						break
					}
					if result := projectSystemCrash(report, state, time.Now()); result != nil {
						f.Close()
						return result
					}
				}
				f.Close()
			}
		}
		if time.Now().After(deadline) {
			return &safeCrash{Status: "missing-matching-system-report"}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestCrashProjectionRequiresGeneration(t *testing.T) {
	launch := time.Date(2026, 11, 9, 11, 32, 2, 123456000, time.UTC)
	state := journal{Broker: Process{PID: 101, Started: uint64(launch.UnixMicro()), Path: "/owned/cefsimple"}, Owner: Process{PID: 100}}
	var report systemCrash
	// Apple's report launch timestamp may retain only 100us precision.
	data := `{"pid":101,"parentPid":100,"procPath":"/owned/cefsimple","procLaunch":"2026-11-09 11:32:02.1234 +0000","captureTime":"2026-11-09 11:32:07.7149 +0000","exception":{"type":"EXC_BAD_ACCESS","signal":"SIGSEGV","codes":"PRIVATE-CANARY"},"termination":{"code":11,"namespace":"SIGNAL"},"faultingThread":0,"threads":[{"frames":[{"symbol":"CloseBrowser","imageIndex":0}],"threadState":{"registers":"PRIVATE-CANARY"}}],"usedImages":[{"name":"Chromium Embedded Framework","path":"/private/PRIVATE-CANARY"}],"environment":"PRIVATE-CANARY"}`
	if err := json.Unmarshal([]byte(data), &report); err != nil {
		t.Fatal(err)
	}
	now := launch.Add(10 * time.Second)
	proof := projectSystemCrash(report, state, now)
	if proof == nil || len(proof.Frames) != 1 || proof.Frames[0].Function != "CloseBrowser" || proof.ReportLaunchPrecisionMicros != 100 {
		t.Fatal("expected matched symbolic report", proof)
	}
	safe, err := json.Marshal(proof)
	if err != nil || strings.Contains(string(safe), "PRIVATE-CANARY") {
		t.Fatal("private crash fields escaped", err)
	}
	changed := report
	changed.PID++
	if projectSystemCrash(changed, state, now) != nil {
		t.Fatal("foreign PID accepted")
	}
	changed = report
	changed.ParentPID++
	if projectSystemCrash(changed, state, now) != nil {
		t.Fatal("foreign owner accepted")
	}
	changed = report
	changed.ProcLaunch = "2026-11-09 11:32:02.1233 +0000"
	if projectSystemCrash(changed, state, now) != nil {
		t.Fatal("old creation accepted")
	}
	changed = report
	changed.ProcLaunch = "2026-11-09 11:32:02.123 +0000"
	if projectSystemCrash(changed, state, now) != nil {
		t.Fatal("coarse/unknown creation accepted")
	}
	changed = report
	changed.ProcPath = "/foreign/cefsimple"
	if projectSystemCrash(changed, state, now) != nil {
		t.Fatal("foreign executable accepted")
	}
}
