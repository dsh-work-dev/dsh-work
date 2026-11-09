//go:build darwin && cgo

package macowner

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"

	"github.com/local/dsh-work/internal/supervisor"
)

const maxFrame = 1 << 20

type message struct {
	Output         *supervisor.OutputEvent `json:"output,omitempty"`
	Kind           string                  `json:"kind"`
	Generation     string                  `json:"generation,omitempty"`
	Secret         string                  `json:"secret,omitempty"`
	Type           string                  `json:"type,omitempty"`
	Broker         Process                 `json:"broker,omitempty"`
	Group          Census                  `json:"group,omitempty"`
	Children       Census                  `json:"children,omitempty"`
	State          *journal                `json:"state,omitempty"`
	Error          string                  `json:"error,omitempty"`
	Version        string                  `json:"version,omitempty"`
	CrashReporting *bool                   `json:"crashReporting,omitempty"`
}

type launchConfig struct {
	Restricted *restrictedConfig     `json:"restricted,omitempty"`
	MCP        *mcpConfig            `json:"mcp,omitempty"`
	Directory  string                `json:"directory"`
	Profile    string                `json:"profile"`
	Socket     string                `json:"socket"`
	Secret     string                `json:"secret"`
	Host       Process               `json:"host"`
	Helpers    []string              `json:"helpers"`
	Plan       supervisor.LaunchPlan `json:"plan"`
	Job        string                `json:"job"`
}

func readFrame(c net.Conn, value any) error {
	var header [4]byte
	if _, err := io.ReadFull(c, header[:]); err != nil {
		return err
	}
	n := binary.LittleEndian.Uint32(header[:])
	if n == 0 || n > maxFrame {
		return errors.New("MAC_CEF_OWNER_FRAME_REJECTED")
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(c, data); err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func writeFrame(c net.Conn, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) == 0 || len(data) > maxFrame {
		return errors.New("MAC_CEF_OWNER_FRAME_REJECTED")
	}
	var header [4]byte
	binary.LittleEndian.PutUint32(header[:], uint32(len(data)))
	if err = c.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return err
	}
	defer c.SetWriteDeadline(time.Time{})
	for _, part := range [][]byte{header[:], data} {
		for len(part) > 0 {
			n, e := c.Write(part)
			if e != nil {
				return e
			}
			if n == 0 {
				return io.ErrShortWrite
			}
			part = part[n:]
		}
	}
	return nil
}

func processKey(p Process) string { return fmtKey(p.PID, p.Started) }
