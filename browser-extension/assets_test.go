package browserextension

import (
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
