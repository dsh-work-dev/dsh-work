//go:build darwin && cgo && server

package embeddedbrowser

import (
	"context"
	"errors"

	"github.com/local/dsh-work/internal/embeddedbrowser/downloads"
)

const macDownloadSheetAvailable = false

func (c *ownedDownloads) chooseDestination(context.Context, downloads.DialogOwner, downloads.Pending) (downloads.Selection, error) {
	return downloads.Selection{}, errors.New("DOWNLOAD_DESTINATION_UNAVAILABLE")
}

func (c *ownedDownloads) confirmContentRisk(context.Context, downloads.DialogOwner, downloads.Snapshot) bool {
	return false
}
