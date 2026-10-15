package selfupdate

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
)

// parseEd25519PublicKey accepts the key forms the release pipeline can pin: a
// raw 32-byte key, a PEM block, a base64 encoding of either, or a PKIX public
// key blob. Anything else fails closed.
func parseEd25519PublicKey(raw []byte) (ed25519.PublicKey, error) {
	if len(raw) == ed25519.PublicKeySize {
		return ed25519.PublicKey(raw), nil
	}
	trimmed := trimSpace(raw)
	if trimmed != nil && len(trimmed) != len(raw) {
		raw = trimmed
	}
	if len(raw) == ed25519.PublicKeySize {
		return ed25519.PublicKey(raw), nil
	}
	if block, _ := pem.Decode(raw); block != nil {
		raw = block.Bytes
	}
	if len(raw) == ed25519.PublicKeySize {
		return ed25519.PublicKey(raw), nil
	}
	if decoded, err := base64.StdEncoding.DecodeString(string(raw)); err == nil {
		raw = decoded
	} else if decoded, err := base64.RawStdEncoding.DecodeString(string(raw)); err == nil {
		raw = decoded
	}
	if len(raw) == ed25519.PublicKeySize {
		return ed25519.PublicKey(raw), nil
	}
	parsed, err := x509.ParsePKIXPublicKey(raw)
	if err != nil {
		return nil, fmt.Errorf("update public key is not a usable Ed25519 key: %w", err)
	}
	public, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("update public key has unexpected type %T, want Ed25519", parsed)
	}
	return public, nil
}

// PublicKeyFingerprint returns a short stable identifier for a public key so
// logs and release reports can name the trust root without printing key
// material. It is the first 8 bytes of the SHA-256 of the raw key, hex encoded
// and labelled with the algorithm, so a reported fingerprint can be reproduced
// and compared independently.
func PublicKeyFingerprint(publicKey []byte) string {
	public, err := parseEd25519PublicKey(publicKey)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(public)
	return "sha256:" + hex.EncodeToString(sum[:8])
}

func trimSpace(raw []byte) []byte {
	start := 0
	for start < len(raw) && isSpace(raw[start]) {
		start++
	}
	end := len(raw)
	for end > start && isSpace(raw[end-1]) {
		end--
	}
	return raw[start:end]
}

func isSpace(character byte) bool {
	switch character {
	case ' ', '\t', '\r', '\n', '\v', '\f':
		return true
	}
	return false
}
