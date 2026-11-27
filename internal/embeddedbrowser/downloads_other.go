//go:build !windows && (!darwin || !cgo)

package embeddedbrowser

func newBrokerDownloads(*Broker, Config) (brokerDownloads, error) { return nil, nil }
