//go:build linux

package workeripc

import (
	"context"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/localipc"
)

func TestLinuxProcessInstanceAdmission(t *testing.T) {
	for _, wrong := range []bool{false, true} {
		t.Run(map[bool]string{false: "owned", true: "wrong-creation"}[wrong], func(t *testing.T) {
			transport, err := New()
			if err != nil {
				t.Fatal(err)
			}
			defer transport.Close()
			c, err := net.Dial("unix", transport.path)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			child := make(chan error, 1)
			go func() {
				nonce := make([]byte, 32)
				if _, err := io.ReadFull(c, nonce); err != nil {
					child <- err
					return
				}
				if _, err := c.Write(handshakeMAC(transport.token, "worker", nonce)); err != nil {
					child <- err
					return
				}
				answer := make([]byte, 32)
				_, err := io.ReadFull(c, answer)
				child <- err
			}()
			started, err := localipc.ProcessStarted(uint32(os.Getpid()))
			if err != nil {
				t.Fatal(err)
			}
			if wrong {
				started++
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			admitted, err := transport.ConnectProcessInstance(ctx, uint32(os.Getpid()), started)
			if wrong {
				if err == nil {
					admitted.Close()
					t.Fatal("wrong creation accepted")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				admitted.Close()
			}
			if err := <-child; err != nil {
				t.Fatal(err)
			}
		})
	}
}
