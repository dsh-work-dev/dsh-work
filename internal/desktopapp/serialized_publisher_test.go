package desktopapp

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/local/dsh-work/internal/hostevents"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/settings"
)

func TestConcurrentLifecyclePublishersFinishAtCurrentValue(t *testing.T) {
	hub := hostevents.New()
	var sourceMu sync.Mutex
	current := lifecycleTopicValue{
		status:    lifecycle.Status{State: lifecycle.StateStarting},
		workerURL: "http://boot.local/old",
	}
	readStarted := make(chan struct{})
	continueRead := make(chan struct{})
	var reads int
	publisher := serializedCurrentPublisher[lifecycleTopicValue]{
		readCurrent: func() (lifecycleTopicValue, error) {
			sourceMu.Lock()
			value := current
			reads++
			firstRead := reads == 1
			sourceMu.Unlock()
			if firstRead {
				close(readStarted)
				<-continueRead
			}
			return value, nil
		},
		publish: func(value lifecycleTopicValue) {
			hub.SetTopic("lifecycle", value.status)
			hub.SetTopic("worker-url", value.workerURL)
		},
	}

	firstDone := make(chan error, 1)
	go func() { firstDone <- publisher.publishCurrent() }()
	<-readStarted
	sourceMu.Lock()
	current = lifecycleTopicValue{
		status:    lifecycle.Status{State: lifecycle.StateReady, WorkspaceURL: "http://workspace.local"},
		workerURL: "http://workspace.local",
	}
	sourceMu.Unlock()
	secondDone := make(chan error, 1)
	go func() { secondDone <- publisher.publishCurrent() }()
	close(continueRead)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}

	_, initial := hub.Subscribe()
	var gotStatus lifecycle.Status
	var gotURL string
	for _, message := range initial {
		switch message.Topic {
		case "lifecycle":
			if err := json.Unmarshal(message.Value, &gotStatus); err != nil {
				t.Fatal(err)
			}
		case "worker-url":
			if err := json.Unmarshal(message.Value, &gotURL); err != nil {
				t.Fatal(err)
			}
		}
	}
	if gotStatus.State != lifecycle.StateReady || gotURL != "http://workspace.local" {
		t.Fatalf("published lifecycle topics are stale: status=%+v worker-url=%q", gotStatus, gotURL)
	}
}

func TestConcurrentPreferencesPublishersFinishAtCurrentValue(t *testing.T) {
	hub := hostevents.New()
	var sourceMu sync.Mutex
	current := settings.Values{Locale: settings.LocaleChinese}
	readStarted := make(chan struct{})
	continueRead := make(chan struct{})
	var reads int
	publisher := serializedCurrentPublisher[settings.Values]{
		readCurrent: func() (settings.Values, error) {
			sourceMu.Lock()
			value := current
			reads++
			firstRead := reads == 1
			sourceMu.Unlock()
			if firstRead {
				close(readStarted)
				<-continueRead
			}
			return value, nil
		},
		publish: func(value settings.Values) { hub.SetTopic("preferences", value) },
	}

	firstDone := make(chan error, 1)
	go func() { firstDone <- publisher.publishCurrent() }()
	<-readStarted
	sourceMu.Lock()
	current = settings.Values{Locale: settings.LocaleEnglish}
	sourceMu.Unlock()
	secondDone := make(chan error, 1)
	go func() { secondDone <- publisher.publishCurrent() }()
	close(continueRead)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}

	_, initial := hub.Subscribe()
	for _, message := range initial {
		if message.Topic != "preferences" {
			continue
		}
		var got settings.Values
		if err := json.Unmarshal(message.Value, &got); err != nil {
			t.Fatal(err)
		}
		if got.Locale != settings.LocaleEnglish {
			t.Fatalf("published preferences locale = %q, want %q", got.Locale, settings.LocaleEnglish)
		}
		return
	}
	t.Fatal("preferences topic was not published")
}
