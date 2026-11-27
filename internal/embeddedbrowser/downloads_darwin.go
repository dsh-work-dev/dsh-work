//go:build darwin && cgo

package embeddedbrowser

import (
	"context"
	"errors"
	"syscall"

	"github.com/local/dsh-work/internal/embeddedbrowser/downloads"
	"github.com/local/dsh-work/internal/localipc"
	"github.com/local/dsh-work/internal/logging"
)

type downloadProcess = localipc.Process
type downloadWriterState struct{ offer *downloads.WriterOffer }

func newBrokerDownloads(b *Broker, c Config) (brokerDownloads, error) {
	// Enable CEF interception only for a native presenter or trusted fixture.
	if c.DownloadConsent == nil && !macDownloadSheetAvailable {
		return nil, nil
	}
	proof := b.worker.Diagnostics()
	if proof.PID <= 0 || proof.ProcessStarted == 0 {
		return nil, errors.New("DOWNLOAD_BROKER_IDENTITY_REJECTED")
	}
	controller, err := openOwnedDownloads(b, c, downloadProcess{PID: uint32(proof.PID), Started: proof.ProcessStarted})
	if err != nil {
		return nil, err
	}
	return controller, nil
}

func closeDownloadProcess(downloadProcess) error { return nil }

// The framed stream and the exact broker have exited. Join all publication
// tasks, then unlink stages independently of the still-held process tree
// and profile lease.
func (c *ownedDownloads) discardAbandonedStages() error {
	if err := c.stopHostTasks(); err != nil {
		return err
	}
	return c.manager.DiscardAbandonedMacStages(c.broker.generation)
}

func (c *ownedDownloads) startNative(ctx context.Context, p downloads.Pending, grant string, live *liveDownload, _ *downloads.Destination, args map[string]any) error {
	// Publish the offer under the same lock used by cancel. Once the manager
	// issues the writer, cancel must join this local sender before asking CEF
	// to drain its end.
	c.mu.Lock()
	offer, err := c.manager.OfferWriter(ctx, grant, c.process)
	if err == nil {
		live.offer = offer
	}
	c.mu.Unlock()
	if err != nil {
		return macDownloadStartError("writer-offer", err)
	}
	defer func() { _ = offer.Close() }()
	args["writerEndpoint"] = offer.Endpoint
	if _, err = c.broker.controlCall(ctx, "target.download.start", p.Owner, args); err != nil {
		return macDownloadStartError("broker-dispatch", err)
	}
	if err = offer.Wait(ctx); err != nil {
		return macDownloadStartError("writer-acknowledgement", err)
	}
	return nil
}

func macDownloadStartError(stage string, err error) error {
	reason := "failed"
	var errno syscall.Errno
	switch {
	case errors.Is(err, context.Canceled):
		reason = "context-canceled"
	case errors.Is(err, context.DeadlineExceeded):
		reason = "deadline"
	case errors.Is(err, downloads.ErrApprovalExpired) || err.Error() == downloads.ErrApprovalExpired.Error():
		reason = "approval-expired"
	case errors.Is(err, ErrClosed):
		reason = "broker-closed"
	case errors.As(err, &errno):
		reason = errno.Error()
	}
	logging.Module(logging.ModRuntime).Warn("CEF download start failed", "stage", stage, "reason", reason)
	return err
}

func nativeDownloadCancelArgs(id string, live *liveDownload) (map[string]any, func()) {
	args := map[string]any{"id": id}
	if live == nil || live.offer == nil {
		return args, nil
	}
	offer := live.offer
	return args, func() { _ = offer.Close() }
}
