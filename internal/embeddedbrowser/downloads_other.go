//go:build !windows

package embeddedbrowser

func newBrokerDownloads(*Broker, Config) (brokerDownloads, error) { return nil, nil }
