package browserextension

import (
	"bytes"
	"encoding/json"
	"image/png"
	"strconv"
	"strings"
	"testing"
)

func TestShippedHandshakeMatchesRecoveryIdentity(t *testing.T) {
	b, e := Files.ReadFile("protocol.js")
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(b), "SOURCE_IDENTITY = '"+SourceIdentity+"'") {
		t.Fatal("extension handshake and host recovery identity differ")
	}
}

// Both Chrome's extension page and toolbar must refer to decodable shipped PNGs.
func TestShippedBrandIcons(t *testing.T) {
	raw, err := Files.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Icons  map[string]string `json:"icons"`
		Action struct {
			Icons map[string]string `json:"default_icon"`
		} `json:"action"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{16, 32, 48, 128} {
		key := strconv.Itoa(size)
		name := "icon-" + key + ".png"
		if manifest.Icons[key] != name || manifest.Action.Icons[key] != name {
			t.Fatalf("missing brand icon %d", size)
		}
		b, err := Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		config, err := png.DecodeConfig(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		if config.Width != size || config.Height != size {
			t.Fatalf("%s is %dx%d", name, config.Width, config.Height)
		}
	}
}
