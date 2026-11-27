// Package downloads owns approved embedded-browser download transactions.
// Its file capability is Windows-only; no browser/network stack is implemented here.
package downloads

import (
	"errors"
	"path/filepath"
	"strings"
	"unicode"
)

var (
	ErrDestinationChanged = errors.New("DOWNLOAD_DESTINATION_CHANGED")
	ErrDestinationBusy    = errors.New("DOWNLOAD_DESTINATION_BUSY")
	ErrOwnedFileChanged   = errors.New("DOWNLOAD_OWNED_FILE_CHANGED")
	ErrClosed             = errors.New("DOWNLOAD_CLOSED")
	ErrApprovalExpired    = errors.New("DOWNLOAD_APPROVAL_EXPIRED")
	ErrBudget             = errors.New("DOWNLOAD_BUDGET_EXCEEDED")
	ErrRisk               = errors.New("DOWNLOAD_RISK_CHECK_FAILED")
)

type FileIdentity struct {
	Volume uint32 `json:"volume"`
	Index  uint64 `json:"index"`
}
type FileVersion struct {
	Identity FileIdentity `json:"identity"`
	Size     uint64       `json:"size"`
	Modified uint64       `json:"modified"`
}
type FileRecord struct {
	Parent         string       `json:"parent"`
	ParentIdentity FileIdentity `json:"parentIdentity"`
	Name           string       `json:"name"`
	Stage          string       `json:"stage"`
	StageIdentity  FileIdentity `json:"stageIdentity"`
	Previous       *FileVersion `json:"previous,omitempty"`
	Recovery       string       `json:"recovery,omitempty"`
	Published      bool         `json:"published,omitempty"`
	Phase          string       `json:"phase,omitempty"`
}

// SuggestedName never accepts a website-supplied directory or ADS.
// The native Save dialog remains the only source of a destination.
func SuggestedName(s string) string {
	s = strings.ReplaceAll(s, "\\", "/")
	s = filepath.Base(s)
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune("<>:\"/\\|?*", r) || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 {
			return '_'
		}
		return r
	}, s)
	s = strings.Trim(s, " .")
	if len([]rune(s)) > 140 {
		s = string([]rune(s)[:140])
	}
	if s == "" || s == "." || !filepath.IsLocal(s) {
		return "download"
	}
	return s
}

func RiskyName(name, mime string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	// Unknown extensions and MIME mismatches require a separate confirmation.
	safe := map[string]string{".txt": "text/", ".csv": "text/", ".json": "application/json", ".pdf": "application/pdf", ".png": "image/", ".jpg": "image/", ".jpeg": "image/", ".gif": "image/", ".webp": "image/", ".mp3": "audio/", ".wav": "audio/", ".mp4": "video/", ".webm": "video/", ".zip": "application/zip", ".gz": "application/gzip", ".tar": "application/"}
	prefix, ok := safe[ext]
	return !ok || !strings.HasPrefix(strings.ToLower(mime), prefix)
}
