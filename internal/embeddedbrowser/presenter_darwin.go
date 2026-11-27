//go:build darwin && cgo && !server

package embeddedbrowser

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework CoreGraphics
#include <CoreGraphics/CoreGraphics.h>
#include <libproc.h>
#include <sys/proc_info.h>
#include <sys/proc.h>
#include <sys/stat.h>
#include <errno.h>
#include <unistd.h>
static int dsh_mac_instance(unsigned int pid,unsigned long long started){
 struct proc_bsdinfo b;int n=0,e=0;
 for(int i=0;i<3;i++){errno=0;n=proc_pidinfo(pid,PROC_PIDTBSDINFO,0,&b,sizeof(b));e=errno;if(n==sizeof(b)||e!=EINTR)break;}
 if(n!=sizeof(b))return e==ESRCH?0:-1;
 return b.pbi_start_tvsec*1000000ULL+b.pbi_start_tvusec==started&&b.pbi_status!=SZOMB?1:0;
}
static int dsh_mac_parent(unsigned int pid,unsigned long long started,unsigned long parent,double* width,double* height){
 if(dsh_mac_instance(pid,started)!=1)return 0;
 char path[PROC_PIDPATHINFO_MAXSIZE],self[PROC_PIDPATHINFO_MAXSIZE];struct stat a,b;
 if(proc_pidpath(pid,path,sizeof(path))<=0||proc_pidpath(getpid(),self,sizeof(self))<=0||stat(path,&a)||stat(self,&b)||a.st_dev!=b.st_dev||a.st_ino!=b.st_ino)return 0;
 CFArrayRef list=CGWindowListCopyWindowInfo(kCGWindowListOptionIncludingWindow,(CGWindowID)parent);if(!list)return 0;
 int valid=0;
 for(CFIndex i=0;i<CFArrayGetCount(list);i++){
  CFDictionaryRef d=(CFDictionaryRef)CFArrayGetValueAtIndex(list,i);int owner=0;CGRect bounds;
  CFNumberRef p=(CFNumberRef)CFDictionaryGetValue(d,kCGWindowOwnerPID);
  CFDictionaryRef rect=(CFDictionaryRef)CFDictionaryGetValue(d,kCGWindowBounds);
  if(p&&rect&&CFNumberGetValue(p,kCFNumberIntType,&owner)&&owner==(int)pid&&CGRectMakeWithDictionaryRepresentation(rect,&bounds)){
   *width=bounds.size.width;*height=bounds.size.height;valid=*width>0&&*height>0;break;
  }
 }
 CFRelease(list);return valid&&dsh_mac_instance(pid,started)==1;
}
*/
import "C"

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/localipc"
	"github.com/local/dsh-work/internal/settings"
)

type macPresenterLink struct {
	endpoint, token string
	done            chan struct{}
	conn            net.Conn
}

var macPresenterLinks = struct {
	sync.Mutex
	values map[macPresenterKey]*macPresenterLink
}{values: map[macPresenterKey]*macPresenterLink{}}

type macPresenterKey struct {
	peer   PresenterPeer
	parent uintptr
}

// ConfigureMacPresenter is called only from trusted native UI registration over
// the existing authenticated daemon connection; no frame chooses these fields.
func ConfigureMacPresenter(peer PresenterPeer, parent uintptr, endpoint, token string) error {
	if _, _, err := macParentBounds(peer, parent); err != nil {
		return err
	}
	if len(token) < 32 || len(token) > 128 || !filepath.IsAbs(endpoint) || len(endpoint) > 103 {
		return errors.New("CEF_MAC_PRESENTER_LINK_REJECTED")
	}
	for _, r := range token {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return errors.New("CEF_MAC_PRESENTER_LINK_REJECTED")
		}
	}
	dir := filepath.Dir(endpoint)
	if filepath.Base(endpoint) != "ui.sock" || !strings.HasPrefix(filepath.Base(dir), "dsh-mac-view-") {
		return errors.New("CEF_MAC_PRESENTER_LINK_REJECTED")
	}
	d, err := os.Lstat(dir)
	if err != nil || !d.IsDir() || d.Mode().Perm() != 0700 {
		return errors.New("CEF_MAC_PRESENTER_LINK_REJECTED")
	}
	s, err := os.Lstat(endpoint)
	if err != nil || s.Mode()&os.ModeSocket == 0 || s.Mode().Perm() != 0600 {
		return errors.New("CEF_MAC_PRESENTER_LINK_REJECTED")
	}
	key := macPresenterKey{peer, parent}
	macPresenterLinks.Lock()
	if old, ok := macPresenterLinks.values[key]; ok {
		macPresenterLinks.Unlock()
		if old.endpoint != endpoint || old.token != token {
			return errors.New("CEF_MAC_PRESENTER_LINK_ALREADY_BOUND")
		}
		return nil
	}
	count := 0
	for k := range macPresenterLinks.values {
		if k.peer == peer {
			count++
		}
	}
	if count >= 64 || len(macPresenterLinks.values) >= 1024 {
		macPresenterLinks.Unlock()
		return errors.New("CEF_MAC_PRESENTER_WINDOW_LIMIT")
	}
	macPresenterLinks.Unlock()
	link, err := openMacWindowLease(key, endpoint, token)
	if err != nil {
		return err
	}
	macPresenterLinks.Lock()
	if old := macPresenterLinks.values[key]; old != nil {
		macPresenterLinks.Unlock()
		link.conn.Close()
		if old.endpoint != endpoint || old.token != token {
			return errors.New("CEF_MAC_PRESENTER_LINK_ALREADY_BOUND")
		}
		return nil
	}
	count = 0
	for k := range macPresenterLinks.values {
		if k.peer == peer {
			count++
		}
	}
	if count >= 64 || len(macPresenterLinks.values) >= 1024 {
		macPresenterLinks.Unlock()
		link.conn.Close()
		return errors.New("CEF_MAC_PRESENTER_WINDOW_LIMIT")
	}
	macPresenterLinks.values[key] = link
	macPresenterLinks.Unlock()
	go func() {
		var value [1]byte
		_, _ = link.conn.Read(value[:])
		link.conn.Close()
		macPresenterLinks.Lock()
		if macPresenterLinks.values[key] == link {
			delete(macPresenterLinks.values, key)
		}
		macPresenterLinks.Unlock()
		close(link.done)
	}()
	return nil
}
func presenterParameters(peer PresenterPeer, parent uintptr) (map[string]any, error) {
	macPresenterLinks.Lock()
	link, ok := macPresenterLinks.values[macPresenterKey{peer, parent}]
	macPresenterLinks.Unlock()
	if !ok {
		return nil, errors.New("CEF_MAC_PRESENTER_LINK_REQUIRED")
	}
	return map[string]any{"presenterEndpoint": link.endpoint, "presenterToken": link.token}, nil
}
func macParentBounds(peer PresenterPeer, parent uintptr) (int, int, error) {
	var width, height C.double
	if parent == 0 || peer.PID == 0 || peer.Started == 0 || C.dsh_mac_parent(C.uint(peer.PID), C.ulonglong(peer.Started), C.ulong(parent), &width, &height) != 1 {
		return 0, 0, errors.New("CEF_MAC_WINDOW_OWNER_REJECTED")
	}
	return int(width), int(height), nil
}
func presenterAlive(peer PresenterPeer) bool {
	return peer.PID != 0 && peer.Started != 0 && C.dsh_mac_instance(C.uint(peer.PID), C.ulonglong(peer.Started)) != 0
}

// The held, authenticated NSWindow registrar connection is the window
// instance lease. CGWindowID alone could be recycled inside a live UI process.
func openMacWindowLease(key macPresenterKey, endpoint, token string) (*macPresenterLink, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := localipc.Dial(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			c.Close()
		}
	}()
	peer, err := localipc.Peer(c)
	if err != nil || peer.PID != key.peer.PID || peer.Started != key.peer.Started {
		return nil, errors.New("CEF_MAC_WINDOW_INSTANCE_REJECTED")
	}
	c.SetDeadline(time.Now().Add(3 * time.Second))
	defer c.SetDeadline(time.Time{})
	id := fmt.Sprint(key.parent)
	if err = writeMacWindowFrame(c, macWindowFrame{Op: "watch", ID: id}); err != nil {
		return nil, err
	}
	reply, err := readMacWindowFrame(c)
	if err != nil || reply.Op != "ack" || reply.ID != id {
		return nil, errors.New("CEF_MAC_WINDOW_LEASE_REJECTED")
	}
	if _, err = localipc.Peer(c); err != nil {
		return nil, err
	}
	ok = true
	return &macPresenterLink{endpoint: endpoint, token: token, done: make(chan struct{}), conn: c}, nil
}

type macWindowFrame struct {
	Op        string          `json:"op"`
	ID        string          `json:"id"`
	Path      string          `json:"path,omitempty"`
	Error     string          `json:"error,omitempty"`
	Locale    settings.Locale `json:"locale,omitempty"`
	Filename  string          `json:"filename,omitempty"`
	Origin    string          `json:"origin,omitempty"`
	Overwrite bool            `json:"overwrite,omitempty"`
	Risk      bool            `json:"risk,omitempty"`
	Accepted  bool            `json:"accepted,omitempty"`
}

func writeMacWindowFrame(c net.Conn, value macWindowFrame) error {
	body, err := json.Marshal(value)
	if err != nil || len(body) > 16384 {
		return errors.New("CEF_MAC_WINDOW_FRAME_REJECTED")
	}
	var header [4]byte
	binary.LittleEndian.PutUint32(header[:], uint32(len(body)))
	for _, part := range [][]byte{header[:], body} {
		for len(part) > 0 {
			n, err := c.Write(part)
			if err != nil {
				return err
			}
			if n == 0 {
				return io.ErrShortWrite
			}
			part = part[n:]
		}
	}
	return nil
}

func readMacWindowFrame(c net.Conn) (macWindowFrame, error) {
	var reply macWindowFrame
	var header [4]byte
	if _, err := io.ReadFull(c, header[:]); err != nil {
		return reply, err
	}
	n := binary.LittleEndian.Uint32(header[:])
	if n == 0 || n > 16384 {
		return reply, errors.New("CEF_MAC_WINDOW_FRAME_REJECTED")
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(c, body); err != nil {
		return reply, err
	}
	err := json.Unmarshal(body, &reply)
	return reply, err
}

type macPresenterOwner struct {
	done, stop, joined chan struct{}
	once               sync.Once
}

func (p *macPresenterOwner) Done() <-chan struct{} { return p.done }
func (p *macPresenterOwner) Close() error {
	p.once.Do(func() { close(p.stop) })
	<-p.joined
	return nil
}
func admitPresenter(peer PresenterPeer, parent uintptr) (presenterOwner, error) {
	if _, _, err := macParentBounds(peer, parent); err != nil {
		return nil, err
	}
	macPresenterLinks.Lock()
	link := macPresenterLinks.values[macPresenterKey{peer, parent}]
	macPresenterLinks.Unlock()
	if link == nil {
		return nil, errors.New("CEF_MAC_PRESENTER_LINK_REQUIRED")
	}
	select {
	case <-link.done:
		return nil, errors.New("CEF_MAC_WINDOW_RETIRED")
	default:
	}
	p := &macPresenterOwner{done: make(chan struct{}), stop: make(chan struct{}), joined: make(chan struct{})}
	go func() {
		defer close(p.done)
		defer close(p.joined)
		select {
		case <-p.stop:
		case <-link.done:
		}
	}()
	return p, nil
}
func presenterBounds(peer PresenterPeer, parent uintptr, b SlotBounds, zoom float64) (PixelBounds, error) {
	if err := validatePresenterZoom(zoom, 5); err != nil {
		return PixelBounds{}, err
	}
	if err := b.validate(); err != nil {
		return PixelBounds{}, err
	}
	width, height, err := macParentBounds(peer, parent)
	if err != nil {
		return PixelBounds{}, err
	}
	return clipPresenterBounds(b, width, height, zoom, presenterChromeHeight), nil
}
