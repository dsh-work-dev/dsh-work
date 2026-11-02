package workeripc

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"io"
	"net"
	"time"
)

func handshakeMAC(token, role string, nonce []byte) []byte {
	h := hmac.New(sha256.New, []byte(token))
	h.Write([]byte(role))
	h.Write(nonce)
	return h.Sum(nil)
}

// AuthenticateClient is the Go counterpart of host.mjs. The secret is never a
// browser-profile identity, execution grant, URL parameter or on-disk pairing key.
func AuthenticateClient(c net.Conn, token string) error {
	c.SetDeadline(time.Now().Add(3 * time.Second))
	defer c.SetDeadline(time.Time{})
	nonce := make([]byte, 32)
	if _, e := io.ReadFull(c, nonce); e != nil {
		return e
	}
	if _, e := c.Write(handshakeMAC(token, "worker", nonce)); e != nil {
		return e
	}
	answer := make([]byte, 32)
	if _, e := io.ReadFull(c, answer); e != nil {
		return e
	}
	if !hmac.Equal(answer, handshakeMAC(token, "host", nonce)) {
		return errors.New("Host IPC authentication failed")
	}
	return nil
}
