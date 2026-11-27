//go:build windows

package embeddedbrowser

import (
	"context"
	"errors"
	"strconv"

	"github.com/local/dsh-work/internal/embeddedbrowser/downloads"
	win "golang.org/x/sys/windows"
)

type windowsDownloads = ownedDownloads
type downloadProcess = win.Handle
type downloadWriterState struct{ writer uintptr }

func newBrokerDownloads(b *Broker, c Config) (brokerDownloads, error) {
	process := b.worker.Diagnostics()
	h, e := win.OpenProcess(win.PROCESS_DUP_HANDLE|win.PROCESS_QUERY_LIMITED_INFORMATION|win.SYNCHRONIZE, false, uint32(process.PID))
	if e != nil {
		return nil, e
	}
	var created, exit, kernel, user win.Filetime
	if e = win.GetProcessTimes(h, &created, &exit, &kernel, &user); e != nil || uint64(created.HighDateTime)<<32|uint64(created.LowDateTime) != process.ProcessStarted {
		win.CloseHandle(h)
		return nil, errors.New("DOWNLOAD_BROKER_IDENTITY_REJECTED")
	}
	controller, e := openOwnedDownloads(b, c, h)
	if e != nil {
		win.CloseHandle(h)
		return nil, e
	}
	return controller, nil
}

func closeDownloadProcess(process downloadProcess) error {
	return win.CloseHandle(process)
}

func (c *ownedDownloads) chooseDestination(ctx context.Context, owner downloads.DialogOwner, pending downloads.Pending) (downloads.Selection, error) {
	return downloads.ChooseDestination(ctx, owner, pending, c.valid)
}

func (c *ownedDownloads) confirmContentRisk(ctx context.Context, owner downloads.DialogOwner, snapshot downloads.Snapshot) bool {
	return downloads.ConfirmContentRisk(ctx, owner, snapshot)
}

func (c *ownedDownloads) startNative(ctx context.Context, p downloads.Pending, grant string, live *liveDownload, destination *downloads.Destination, args map[string]any) error {
	writer, err := c.manager.DuplicateWriter(grant, c.process)
	if err != nil {
		return err
	}
	c.mu.Lock()
	live.writer = writer
	c.mu.Unlock()
	args["writer"] = strconv.FormatUint(uint64(writer), 10)
	args["volume"] = destination.Record().Parent
	_, err = c.broker.controlCall(ctx, "target.download.start", p.Owner, args)
	return err
}

func nativeDownloadCancelArgs(id string, live *liveDownload) (map[string]any, func()) {
	var writer uintptr
	if live != nil {
		writer = live.writer
	}
	return map[string]any{"id": id, "writer": strconv.FormatUint(uint64(writer), 10)}, nil
}
