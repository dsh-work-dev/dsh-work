package main

import (
	"encoding/json"
	"fmt"
	"github.com/wailsapp/wails/v3/pkg/application"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// The UI thread owns broker lifecycle state; only explicit recovery starts a
// replacement. The injected kill targets this lab's retained process handle.
func (l *lab) startBroker(root, data, exe, token string) error {
	var blocked bool
	var parent uintptr
	application.InvokeSync(func() {
		blocked = l.starting || l.guestRunning || l.closing || l.closed
		if blocked {
			return
		}
		l.starting = true
		l.ready = false
		l.covered = false
		l.guestExit = ""
		l.viewGeneration = strconv.FormatInt(time.Now().UnixNano(), 36)
		l.selectionEpoch++
		l.presentationEpoch++
		l.selected = 0
		l.floating = false
		l.float.Hide()
		l.tabs = [2]labTab{{Name: "A"}, {Name: "B"}}
		parent = l.mainHWND
		l.record("broker-start-requested", l.viewGeneration)
	})
	if blocked {
		return fmt.Errorf("broker is running or a lifecycle transition is in progress")
	}
	success := false
	defer func() {
		if !success {
			application.InvokeSync(func() { l.starting = false })
		}
	}()
	cmd := exec.Command(exe, "--use-native", "--use-alloy-style", "--demo-two-tabs", "--remote-debugging-port=19479", "--cache-path="+filepath.Join(data, "guest-profile"), "--url=http://127.0.0.1:19478/interactions", "--parent-hwnd="+strconv.FormatUint(uint64(parent), 10), "--enable-logging", "--log-file="+filepath.Join(root, "evidence", "cef.log"), "--trace-file="+filepath.Join(root, "evidence", "startup-trace.txt"))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	application.InvokeSync(func() { l.guest = cmd; l.guestPID = uint32(cmd.Process.Pid); l.guestDone = done; l.guestRunning = true })
	go func() {
		err := cmd.Wait()
		done <- err
		application.InvokeSync(func() {
			if l.guest != cmd {
				return
			}
			l.guestRunning = false
			l.ready = false
			l.starting = false
			if err != nil {
				l.guestExit = err.Error()
			} else {
				l.guestExit = "normal"
			}
			l.presentationEpoch++
			l.viewGeneration = strconv.FormatInt(time.Now().UnixNano(), 36)
			for i := range l.tabs {
				l.tabs[i].HWND = 0
				l.tabs[i].Fault = true
			}
			l.record("broker-exited", map[string]any{"pid": cmd.Process.Pid, "exit": l.guestExit, "closing": l.closing})
		})
	}()
	found := false
	for i := 0; i < 150; i++ {
		running := false
		application.InvokeSync(func() {
			running = l.guestRunning
			if running {
				l.discover()
				found = l.tabs[0].HWND != 0 && l.tabs[1].HWND != 0
			}
		})
		if !running {
			return fmt.Errorf("broker exited before child discovery")
		}
		if found {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !found {
		return fmt.Errorf("two CEF children not ready; retained for inspection")
	}
	application.InvokeSync(func() {
		l.starting = false
		l.ready = true
		l.layout()
		showQuiet(l.main, l.mainHWND)
		l.record("ready", l.viewGeneration)
	})
	bytes, _ := json.Marshal(map[string]any{"token": token, "control": "http://127.0.0.1:19481", "hostPID": os.Getpid(), "cefExe": exe, "dataRoot": data})
	if err := os.WriteFile(filepath.Join(root, "session.json"), bytes, 0600); err != nil {
		return err
	}
	success = true
	log.Println("BROWSER_LAB batch demo ready")
	return nil
}
