package daemon

import "context"

// ProcessPeer is kernel transport evidence; it is never decoded from Call.Args.
type ProcessPeer struct {
	PID     uint32
	Started uint64
}
type processPeerKey struct{}

func ProcessPeerFrom(ctx context.Context) (ProcessPeer, bool) {
	peer, ok := ctx.Value(processPeerKey{}).(ProcessPeer)
	return peer, ok
}
