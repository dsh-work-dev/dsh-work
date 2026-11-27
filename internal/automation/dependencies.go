package automation

import (
	"embed"
	"errors"
	browserextension "github.com/local/dsh-work/browser-extension"
	"runtime"
)

const AUVVersion = "0.0.43"

const DSHVersion = "0.2.2-rc.1"
const MCPVersion = "0.0.83"
const SDKVersion = "1.32.2"
const BrowserWebSocketVersion = "8.22.1"
const DriverVersion = "0.28.1"
const DriverArchive = "cua-driver-rs-0.28.1-windows-x86_64.zip"
const DriverSHA256 = "0f864381d3bcf29e7caf20d4b93bdf8476faf4e1ba986ce03890162beba5c3ee"
const DriverURL = "https://github.com/trycua/cua/releases/download/cua-driver-rs-v0.28.1/" + DriverArchive
const MacDriverArchive = "cua-driver-rs-" + DriverVersion + "-darwin-arm64.tar.gz"
const MacDriverSHA256 = "b3cb3781feb9855dfcdd616bc5c9c8e627453e3e16a073c1446326691a549b24"
const MacDriverURL = "https://github.com/trycua/cua/releases/download/cua-driver-rs-v" + DriverVersion + "/" + MacDriverArchive

//go:embed assets/*/package*.json
var dependencyAssets embed.FS

func DependencyFiles(c Capability) (map[string][]byte, error) {
	if !c.Valid() {
		return nil, errors.New("unknown automation capability")
	}
	directory := string(c)
	if c == AUV && nativeMacAutomation() {
		directory = "computer-auv-darwin-arm64"
	}
	return dependencyFiles(directory)
}

func dependencyFiles(directory string) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, name := range []string{"package.json", "package-lock.json"} {
		data, err := dependencyAssets.ReadFile("assets/" + directory + "/" + name)
		if err != nil {
			return nil, err
		}
		out[name] = data
	}
	return out, nil
}

func nativeMacAutomation() bool { return runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" }

// VersionInputs contains reproducible identities only: no runtime connections,
// authorization, browser data, installed trees or user profiles.
type VersionInputs struct {
	SchemaVersion   int    `json:"schemaVersion"`
	BrowserLock     string `json:"browserLock"`
	ComputerLock    string `json:"computerLock"`
	AUVLock         string `json:"auvLock"`
	DriverVersion   string `json:"driverVersion"`
	DriverArchive   string `json:"driverArchive"`
	DriverSHA256    string `json:"driverSha256"`
	ExtensionSource string `json:"extensionSource"`
}

func CurrentVersionInputs() VersionInputs {
	browser, _ := DependencyFiles(Browser)
	computer, _ := DependencyFiles(Computer)
	auv, _ := DependencyFiles(AUV)
	input := VersionInputs{1, string(browser["package-lock.json"]), string(computer["package-lock.json"]), string(auv["package-lock.json"]), DriverVersion, DriverArchive, DriverSHA256, browserextension.SourceIdentity}
	if nativeMacAutomation() {
		input.DriverArchive, input.DriverSHA256 = MacDriverArchive, MacDriverSHA256
	}
	return input
}

// MatchesHost accepts the previous Mac preview record for the computer engines
// that were unavailable there. Browser and all other host inputs remain pinned.
func (v VersionInputs) MatchesHost(available VersionInputs) bool {
	if v == available {
		return true
	}
	if !nativeMacAutomation() || available != CurrentVersionInputs() {
		return false
	}
	legacy := available
	auv, _ := dependencyFiles(string(AUV))
	legacy.AUVLock = string(auv["package-lock.json"])
	legacy.DriverArchive, legacy.DriverSHA256 = DriverArchive, DriverSHA256
	return v == legacy
}

func (v VersionInputs) Validate() error {
	if !v.MatchesHost(CurrentVersionInputs()) {
		return errors.New("automation dependencies require the recorded host version")
	}
	return nil
}
