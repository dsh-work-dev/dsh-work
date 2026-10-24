package settings

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestOnChangeReceivesCommittedValuesAfterSettingsLockIsReleased(t *testing.T) {
	manager, err := New(Config{Path: filepath.Join(t.TempDir(), "settings.json")})
	if err != nil {
		t.Fatal(err)
	}
	updates := make(chan Values, 2)
	manager.OnChange(func(values Values) {
		// A callback may safely ask for a fresh snapshot; running this while the
		// manager lock is held would deadlock the writer.
		snapshotDone := make(chan Values, 1)
		go func() {
			snapshot, _ := manager.Snapshot(context.Background())
			snapshotDone <- snapshot
		}()
		select {
		case snapshot := <-snapshotDone:
			if snapshot.Locale != values.Locale {
				return
			}
		case <-time.After(time.Second):
			return
		}
		updates <- values
	})

	values, err := manager.SetLocale(context.Background(), LocaleEnglish)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case published := <-updates:
		if published.Locale != LocaleEnglish || values.Locale != published.Locale {
			t.Fatalf("published values = %+v, setter values = %+v", published, values)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnChange did not publish the committed preference")
	}
}
