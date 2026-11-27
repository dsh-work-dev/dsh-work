//go:build darwin && cgo && !server

package embeddedbrowser

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/local/dsh-work/internal/localipc"
	"github.com/local/dsh-work/internal/settings"
	"github.com/local/dsh-work/internal/workeripc"
)

func chooseArtifactFile(ctx context.Context, peer PresenterPeer, parent uintptr, locale settings.Locale) (string, error) {
	reply, err := requestMacWindowSheet(ctx, peer, parent, macWindowFrame{Op: "artifact-picker", Locale: locale}, "artifact-result")
	return reply.Path, err
}

func requestMacWindowSheet(ctx context.Context, peer PresenterPeer, parent uintptr, request macWindowFrame, replyOp string) (macWindowFrame, error) {
	if err := ctx.Err(); err != nil {
		return macWindowFrame{}, err
	}
	key := macPresenterKey{peer, parent}
	macPresenterLinks.Lock()
	link := macPresenterLinks.values[key]
	macPresenterLinks.Unlock()
	if link == nil {
		return macWindowFrame{}, errors.New("CEF_MAC_PRESENTER_LINK_REQUIRED")
	}
	c, err := localipc.Dial(ctx, link.endpoint)
	if err != nil {
		return macWindowFrame{}, err
	}
	defer c.Close()
	identity, err := localipc.Peer(c)
	if err != nil || identity.PID != peer.PID || identity.Started != peer.Started {
		return macWindowFrame{}, errors.New("CEF_MAC_WINDOW_INSTANCE_REJECTED")
	}
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	id := fmt.Sprint(parent)
	if err = writeMacWindowFrame(c, macWindowFrame{Op: request.Op, ID: id}); err != nil {
		return macWindowFrame{}, err
	}
	if err = workeripc.AuthenticateClient(c, link.token); err != nil {
		return macWindowFrame{}, err
	}
	if err = ctx.Err(); err != nil {
		return macWindowFrame{}, err
	}
	_ = c.SetWriteDeadline(time.Now().Add(3 * time.Second))
	request.ID = id
	if err = writeMacWindowFrame(c, request); err != nil {
		return macWindowFrame{}, err
	}
	_ = c.SetDeadline(time.Time{})
	stop := context.AfterFunc(ctx, func() {
		// Wait briefly for the native sheet to drain before releasing the lease.
		_ = c.SetDeadline(time.Now().Add(3 * time.Second))
		_, _ = c.Write([]byte{1})
	})
	defer stop()
	reply, err := readMacWindowFrame(c)
	if ctx.Err() != nil {
		return macWindowFrame{}, ctx.Err()
	}
	if err != nil {
		return macWindowFrame{}, err
	}
	if reply.Op != replyOp || reply.ID != id || reply.Error != "" {
		return macWindowFrame{}, errors.New("CEF_MAC_WINDOW_SHEET_UNAVAILABLE")
	}
	select {
	case <-link.done:
		return macWindowFrame{}, errors.New("CEF_MAC_WINDOW_RETIRED")
	default:
		return reply, nil
	}
}
