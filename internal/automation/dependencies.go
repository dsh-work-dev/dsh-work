package automation

import (
	"embed"
	"errors"
	browserextension "github.com/local/dsh-work/browser-extension"
)

const DSHVersion = "0.2.1-rc.5"
const MCPVersion = "0.0.83"
const SDKVersion = "1.32.2"
const DriverVersion = "0.28.1"
const DriverArchive = "cua-driver-rs-0.28.1-windows-x86_64.zip"
const DriverSHA256 = "0f864381d3bcf29e7caf20d4b93bdf8476faf4e1ba986ce03890162beba5c3ee"
const DriverURL = "https://github.com/trycua/cua/releases/download/cua-driver-rs-v0.28.1/" + DriverArchive

//go:embed assets/*/package*.json
var dependencyAssets embed.FS

func DependencyFiles(c Capability) (map[string][]byte, error) {
	if !c.Valid() {
		return nil, errors.New("unknown automation capability")
	}
	out := map[string][]byte{}
	for _, name := range []string{"package.json", "package-lock.json"} {
		data, err := dependencyAssets.ReadFile("assets/" + string(c) + "/" + name)
		if err != nil {
			return nil, err
		}
		out[name] = data
	}
	return out, nil
}

// VersionInputs contains reproducible identities only: no runtime connections,
// authorization, browser data, installed trees or user profiles.
type VersionInputs struct {
	SchemaVersion   int    `json:"schemaVersion"`
	BrowserLock     string `json:"browserLock"`
	ComputerLock    string `json:"computerLock"`
	DriverVersion   string `json:"driverVersion"`
	DriverArchive   string `json:"driverArchive"`
	DriverSHA256    string `json:"driverSha256"`
	ExtensionSource string `json:"extensionSource"`
}

func CurrentVersionInputs() VersionInputs {
	browser, _ := DependencyFiles(Browser)
	computer, _ := DependencyFiles(Computer)
	return VersionInputs{1, string(browser["package-lock.json"]), string(computer["package-lock.json"]), DriverVersion, DriverArchive, DriverSHA256, browserextension.SourceIdentity}
}
func (v VersionInputs) Validate() error {
	if v != CurrentVersionInputs() {
		return errors.New("automation dependencies require the recorded host version")
	}
	return nil
}
