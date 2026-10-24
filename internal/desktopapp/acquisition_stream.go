package desktopapp

import (
	"strings"
	"time"

	"github.com/local/dsh-work/internal/acquisition"
)

const acquisitionPublishInterval = 250 * time.Millisecond
const maxAcquisitionBatchLogBytes = 64 * 1024

func mergeAcquisitionStatus(previous, next acquisition.OperationStatus) acquisition.OperationStatus {
	if previous.OperationID != next.OperationID {
		return next
	}

	merged := next
	if next.Log != "" && next.Step == "output" && previous.Step != "output" {
		// Output lines are diagnostic events rather than a newer progress
		// projection. Keep the latest progress state while batching the lines.
		merged = previous
	}
	merged.Log = mergeAcquisitionLogs(previous.Log, next.Log)
	return merged
}

func mergeAcquisitionLogs(previous, next string) string {
	if previous == "" {
		return next
	}
	if next == "" {
		return previous
	}
	joined := previous + "\n" + next
	if len(joined) <= maxAcquisitionBatchLogBytes {
		return joined
	}
	start := len(joined) - maxAcquisitionBatchLogBytes
	if newline := strings.IndexByte(joined[start:], '\n'); newline >= 0 {
		start += newline + 1
	}
	return joined[start:]
}
