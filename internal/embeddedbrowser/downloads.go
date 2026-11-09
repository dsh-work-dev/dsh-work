package embeddedbrowser

import (
	"context"
	"encoding/json"
)

// DownloadConsent is a trusted Host dependency, never an RPC/site argument.
// Prepared must be a live Windows downloads.Destination object, not a path DTO.
// The default is the owner-bound native Save/confirmation UI.
type DownloadConsentRequest struct{ ID, SourceOrigin, URL, MIME, SuggestedName string }
type DownloadConsentDecision struct {
	Prepared                          any
	OverwriteConfirmed, RiskConfirmed bool
}
type DownloadConsent func(context.Context, DownloadConsentRequest) (DownloadConsentDecision, error)

type DownloadView struct {
	owner           string
	ID              string `json:"id"`
	State           string `json:"state"`
	SourceOrigin    string `json:"sourceOrigin"`
	TargetOrigin    string `json:"targetOrigin"`
	Filename        string `json:"filename"`
	Received        int64  `json:"received"`
	Total           int64  `json:"total"`
	Error           string `json:"error,omitempty"`
	RecoveryPath    string `json:"recoveryPath,omitempty"`
	CleanupRequired bool   `json:"cleanupRequired,omitempty"`
}
type brokerDownloads interface {
	message(json.RawMessage) error
	state() []DownloadView
	cancel(context.Context, string, bool) error
	afterJoin() error
	pending() bool
}

func (b *Broker) DownloadState() []DownloadView {
	if b.downloads == nil {
		return nil
	}
	return b.downloads.state()
}
func (b *Broker) CancelDownload(ctx context.Context, id string, revoke bool) error {
	if b.downloads == nil {
		return ErrClosed
	}
	return b.downloads.cancel(ctx, id, revoke)
}
