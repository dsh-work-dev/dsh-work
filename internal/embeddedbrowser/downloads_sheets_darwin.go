//go:build darwin && cgo && !server

package embeddedbrowser

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/local/dsh-work/internal/embeddedbrowser/downloads"
	"github.com/local/dsh-work/internal/settings"
)

const macDownloadSheetAvailable = true

func (c *ownedDownloads) macDownloadSheet(ctx context.Context, owner downloads.DialogOwner, request macWindowFrame) (macWindowFrame, error) {
	request.Locale = settings.LocaleEnglish
	if c.locale != nil {
		if locale := c.locale(); locale.Valid() {
			request.Locale = locale
		}
	}
	return requestMacWindowSheet(ctx, PresenterPeer{PID: owner.PID, Started: owner.Started}, owner.Window, request, "download-result")
}

func (c *ownedDownloads) chooseDestination(ctx context.Context, owner downloads.DialogOwner, p downloads.Pending) (downloads.Selection, error) {
	if !c.valid(ctx, p) {
		return downloads.Selection{}, downloads.ErrApprovalExpired
	}
	selection, err := c.macDownloadSheet(ctx, owner, macWindowFrame{
		Op: "download-save", Filename: downloads.SuggestedName(p.SuggestedName), Origin: p.SourceOrigin,
	})
	if err != nil || selection.Path == "" {
		return downloads.Selection{}, err
	}
	if !filepath.IsAbs(selection.Path) || strings.ContainsRune(selection.Path, 0) || !c.valid(ctx, p) {
		return downloads.Selection{}, downloads.ErrApprovalExpired
	}
	destination, err := downloads.PrepareDestination(selection.Path, true)
	if err != nil {
		return downloads.Selection{}, err
	}
	accepted := false
	defer func() {
		if !accepted {
			_ = destination.Abort()
			_ = destination.Close()
		}
	}()
	record := destination.Record()
	overwrite := record.Previous != nil
	risk := downloads.RiskyName(record.Name, p.MIME)
	if overwrite || risk {
		// Ask after pinning the destination and existing file identity. The
		// system panel's earlier overwrite prompt cannot authorize a new occupant.
		if !c.valid(ctx, p) {
			return downloads.Selection{}, downloads.ErrApprovalExpired
		}
		confirmation, err := c.macDownloadSheet(ctx, owner, macWindowFrame{
			Op: "download-confirm", Filename: record.Name, Origin: p.SourceOrigin, Overwrite: overwrite, Risk: risk,
		})
		if err != nil || !confirmation.Accepted {
			return downloads.Selection{}, err
		}
	}
	if !c.valid(ctx, p) {
		return downloads.Selection{}, downloads.ErrApprovalExpired
	}
	accepted = true
	return downloads.Selection{Path: selection.Path, Prepared: destination, OverwriteConfirmed: overwrite, RiskConfirmed: risk}, nil
}

func (c *ownedDownloads) confirmContentRisk(ctx context.Context, owner downloads.DialogOwner, snapshot downloads.Snapshot) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	confirmation, err := c.macDownloadSheet(ctx, owner, macWindowFrame{
		Op: "download-content-risk", Filename: snapshot.Filename, Origin: snapshot.SourceOrigin,
	})
	return err == nil && confirmation.Accepted
}
