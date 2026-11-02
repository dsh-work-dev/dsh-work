package automation

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/local/dsh-work/internal/logging"
)

func loadBrowserActivity(path string) []BrowserActivity {
	var out []BrowserActivity
	if path == "" {
		return out
	}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	scan := bufio.NewScanner(io.LimitReader(f, 2<<20))
	for scan.Scan() {
		var item BrowserActivity
		if json.Unmarshal(scan.Bytes(), &item) == nil && time.Since(item.At) <= 7*24*time.Hour {
			out = append(out, item)
		}
	}
	if len(out) > 1000 {
		out = out[len(out)-1000:]
	}
	return out
}
func (s *Service) browserActivityLocked(r Request, site, category, event string) {
	if s.gate == nil {
		return
	}
	g := s.gate
	g.activity = append(g.activity, BrowserActivity{At: time.Now().UTC(), Session: r.Session, Title: r.Title, Site: site, Category: category, Event: event, Tool: r.Tool})
	cut := 0
	for cut < len(g.activity) && time.Since(g.activity[cut].At) > 7*24*time.Hour {
		cut++
	}
	g.activity = append([]BrowserActivity{}, g.activity[cut:]...)
	if len(g.activity) > 1000 {
		g.activity = g.activity[len(g.activity)-1000:]
	}
	if g.config.ActivityPath == "" {
		return
	}
	g.auditWG.Add(1)
	go func() {
		defer g.auditWG.Done()
		g.auditMu.Lock()
		defer g.auditMu.Unlock()
		s.mu.Lock()
		items := append([]BrowserActivity{}, g.activity...)
		s.mu.Unlock()
		path := g.config.ActivityPath
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			logging.Module(logging.ModSettings).Warn("browser activity unavailable", "err", err)
			return
		}
		f, err := os.CreateTemp(filepath.Dir(path), ".browser-activity-")
		if err != nil {
			return
		}
		name := f.Name()
		defer os.Remove(name)
		defer f.Close()
		_ = f.Chmod(0600)
		for _, item := range items {
			if err = json.NewEncoder(f).Encode(item); err != nil {
				break
			}
		}
		if err == nil {
			err = f.Sync()
		}
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err == nil {
			if g.config.Replacer != nil {
				err = g.config.Replacer.Replace(name, path)
			} else {
				err = os.Rename(name, path)
			}
		}
		if err != nil {
			logging.Module(logging.ModSettings).Warn("browser activity could not be saved", "err", err)
		}
	}()
}
