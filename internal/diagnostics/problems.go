package diagnostics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"time"

	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/logging"
)

func (d *Diagnostics) Problems(ctx context.Context) ([]Problem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	since := d.config.Now().Add(-7 * 24 * time.Hour)
	daemon, err := readRecords(d.Dir(), logging.DaemonLogFile, since)
	if err != nil {
		return nil, err
	}
	ui, err := readRecords(d.Dir(), logging.UILogFile, since)
	if err != nil {
		return nil, err
	}
	records := append(daemon, ui...)
	sort.SliceStable(records, func(i, j int) bool { return records[i].time.Before(records[j].time) })

	problems := make([]Problem, 0)
	currentByCode := make(map[string]int)
	for _, entry := range records {
		if entry.level != "WARN" && entry.level != "ERROR" {
			continue
		}
		if entry.code == "" || entry.code == string(lifecycle.ErrorCancelled) {
			continue
		}

		if index, ok := currentByCode[entry.code]; ok {
			lastTime, parseErr := time.Parse(time.RFC3339Nano, problems[index].LastTime)
			if parseErr == nil && entry.time.Sub(lastTime) <= 10*time.Minute {
				problems[index].Count++
				problems[index].LastTime = entry.time.Format(time.RFC3339Nano)
				if entry.level == "ERROR" {
					problems[index].Level = "ERROR"
				}
				continue
			}
		}

		sum := sha256.Sum256([]byte(entry.run + "|" + entry.timeText + "|" + entry.code))
		problem := Problem{
			ID:        hex.EncodeToString(sum[:])[:12],
			Code:      entry.code,
			Level:     entry.level,
			Message:   d.anonymize(entry.message),
			Proc:      entry.proc,
			Gen:       entry.gen,
			Op:        entry.op,
			FirstTime: entry.time.Format(time.RFC3339Nano),
			LastTime:  entry.time.Format(time.RFC3339Nano),
			Count:     1,
		}
		problems = append(problems, problem)
		currentByCode[entry.code] = len(problems) - 1
	}

	sort.SliceStable(problems, func(i, j int) bool {
		left, _ := time.Parse(time.RFC3339Nano, problems[i].LastTime)
		right, _ := time.Parse(time.RFC3339Nano, problems[j].LastTime)
		return left.After(right)
	})
	if len(problems) > 20 {
		problems = problems[:20]
	}
	return problems, nil
}
