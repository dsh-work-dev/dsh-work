//go:build windows || (darwin && cgo)

package embeddedbrowser

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/local/dsh-work/internal/embeddedbrowser/downloads"
)

func TestDownloadInvalidationCancelsOnlyTheNamedPrompt(t *testing.T) {
	const owner = "fixture-tab"
	b := &Broker{generation: "fixture-generation"}
	var process downloadProcess
	controller, err := openOwnedDownloads(b, Config{Profile: t.TempDir()}, process)
	if err != nil {
		t.Fatal(err)
	}
	defer controller.stop()

	// The earlier approval was already consumed by CEF, but its writer is
	// still being acknowledged and the Host still tracks its prompt context.
	earlier, cancelEarlier := context.WithCancel(context.Background())
	defer cancelEarlier()
	controller.prompts["earlier"] = downloadPrompt{owner, cancelEarlier}
	pending, cancelPending := context.WithCancel(context.Background())
	defer cancelPending()
	controller.prompts["pending"] = downloadPrompt{owner, cancelPending}
	request := downloads.Pending{
		ID: "pending", Generation: b.generation, Owner: owner, Profile: controller.profile,
		RequestID: "fetch", FrameID: "frame", Document: "document", Presentation: "presentation",
		SourceOrigin: "https://fixture.test", SourceURL: "https://fixture.test/page",
		URL: "https://fixture.test/download", Method: "GET",
	}
	if err = controller.manager.Offer(request); err != nil {
		t.Fatal(err)
	}
	invalidate := func(targetOwner string) error {
		body, err := json.Marshal(map[string]string{
			"kind": "download.invalidated", "generation": b.generation,
			"owner": targetOwner, "pending": request.ID, "reason": "presentation-hidden",
		})
		if err != nil {
			t.Fatal(err)
		}
		return controller.message(body)
	}
	if err = invalidate("different-tab"); err == nil || err.Error() != "DOWNLOAD_OWNER_REJECTED" {
		t.Fatalf("wrong owner was not rejected: %v", err)
	}
	if pending.Err() != nil {
		t.Fatal("a different tab canceled the pending prompt")
	}
	if err = invalidate(owner); err != nil {
		t.Fatal(err)
	}
	if pending.Err() != context.Canceled {
		t.Fatal("invalidated prompt is still live")
	}
	if _, ok := controller.manager.Pending(request.ID); ok {
		t.Fatal("invalidated approval is still available")
	}
	if err = earlier.Err(); err != nil {
		t.Fatalf("the earlier consumed request was also canceled: %v", err)
	}
}
