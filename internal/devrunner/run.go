//go:build windows

package devrunner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/daemon"
	"gopkg.in/natefinch/lumberjack.v2"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Metadata struct {
	Generation string `json:"generation"`
	PID        int    `json:"pid"`
	Started    uint64 `json:"started"`
	Workspace  string `json:"workspace"`
}

func identity(root string) string     { return filepath.Join(root, "bin", "e67-dev-runner") }
func metadataPath(root string) string { return filepath.Join(identity(root), "runner.json") }
func Run(args []string) error {
	if len(args) == 0 {
		return errors.New("commands: serve, build, start 1|2, stop 1|2|all, restart 1|2, status, shutdown")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	root, err := filepath.EvalSymlinks(filepath.Dir(filepath.Dir(exe)))
	if err != nil {
		return err
	}
	action := args[0]
	if action == "serve" {
		if len(args) != 1 {
			return errors.New("serve takes no arguments; initial environment comes from the approved launcher")
		}
		return serve(root)
	}
	req := Request{Action: action}
	if len(args) > 1 {
		if action != "start" && action != "stop" && action != "restart" {
			return errors.New("this command takes no instance selector")
		}
		if args[1] != "all" {
			value, e := strconv.Atoi(args[1])
			if e != nil {
				return e
			}
			req.Instance = value
		}
		if len(args) != 2 {
			return errors.New("one fixed instance selector allowed")
		}
	}
	duration := 90 * time.Second
	if action == "build" {
		duration = 170 * time.Second
	}
	if action == "status" {
		duration = 10 * time.Second
	}
	if action == "stop" || action == "shutdown" {
		duration = 60 * time.Second
	}
	req.Deadline = time.Now().Add(duration).UnixMilli()
	if err = validate(req, time.Now()); err != nil {
		return err
	}
	data, err := os.ReadFile(metadataPath(root))
	if err != nil {
		return fmt.Errorf("ordinary-terminal runner unavailable: %w", err)
	}
	var owner Metadata
	if json.Unmarshal(data, &owner) != nil || owner.Generation == "" || owner.Workspace != root {
		return errors.New("runner metadata does not match this workspace")
	}
	req.Generation = owner.Generation
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	client := daemon.NewProcessClient(identity(root), daemon.ProcessPeer{PID: uint32(owner.PID), Started: owner.Started})
	defer client.Close()
	var response Response
	if err = client.JSON(ctx, "/command", req, &response); err != nil {
		return fmt.Errorf("runner request incomplete; inspect status before retrying: %w", err)
	}
	if response.Status.Generation != owner.Generation {
		return errors.New("runner generation changed during request")
	}
	encoded, _ := json.MarshalIndent(response, "", "  ")
	fmt.Println(string(encoded))
	if response.Error != "" {
		return errors.New(response.Error)
	}
	return nil
}
func serve(root string) error {
	if err := ordinaryContext(); err != nil {
		return err
	}
	lock, err := app.AcquireManagerProcessLock(metadataPath(root))
	if err != nil {
		return fmt.Errorf("one runner already owns this workspace: %w", err)
	}
	defer lock.Close()
	loggerSink := &lumberjack.Logger{Filename: filepath.Join(identity(root), "runner.log"), MaxSize: 4, MaxBackups: 2}
	defer loggerSink.Close()
	logger := log.New(loggerSink, "", log.LstdFlags|log.Lmicroseconds)
	native, err := NewNative(Config{Workspace: root, Runtime: os.Getenv("DSH_WORK_EXECUTABLE"), Logger: logger})
	if err != nil {
		return err
	}
	preflight, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err = verifyNode(preflight, root, native.tools["node"])
	cancel()
	if err != nil {
		return err
	}
	listener, err := daemon.Listen(identity(root))
	if err != nil {
		return err
	}
	defer listener.Close()
	service := NewService(native)
	started, err := processStarted()
	if err != nil {
		return err
	}
	metadata := Metadata{Generation: service.Generation, PID: os.Getpid(), Started: started, Workspace: root}
	encoded, _ := json.MarshalIndent(metadata, "", "  ")
	if err = os.WriteFile(metadataPath(root), encoded, 0600); err != nil {
		return err
	}
	defer os.Remove(metadataPath(root))
	server := daemon.HTTPServer(service)
	defer server.Close()
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	fmt.Printf("E-67 ordinary-terminal runner ready (PID %d). Keep this terminal open. Controller: %s status\n", os.Getpid(), filepath.Join(root, "bin", "e67-dev-runner.exe"))
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	input := make(chan struct{}, 1)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			if strings.TrimSpace(scanner.Text()) == "exit" {
				input <- struct{}{}
				return
			}
		}
		input <- struct{}{}
	}()
	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := service.Shutdown(ctx); err != nil {
			if native.CleanupComplete() {
				logger.Printf("shutdown completed-abnormally: %v", err)
			} else {
				logger.Printf("shutdown incomplete: %v", err)
			}
			fmt.Fprintln(os.Stderr, err)
		}
	}
	for {
		select {
		case <-service.Stop:
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			err := server.Shutdown(ctx)
			cancel()
			return errors.Join(err, service.exitErr)
		case err := <-serveErrors:
			if !errors.Is(err, http.ErrServerClosed) {
				shutdown()
				select {
				case <-service.Stop:
					return err
				default:
					fmt.Fprintln(os.Stderr, "listener failed; cleanup owner retained. Use Ctrl+C to retry shutdown.")
				}
			}
		case <-interrupts:
			shutdown()
		case <-input:
			input = nil
			shutdown()
		}
	}
}
