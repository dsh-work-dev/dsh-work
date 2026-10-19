package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/local/dsh-work/internal/logging"
)

func (d *Diagnostics) Report(ctx context.Context, problemID string) (string, error) {
	var problem *Problem
	if problemID != "" {
		problems, err := d.Problems(ctx)
		if err != nil {
			return "", err
		}
		for i := range problems {
			if problems[i].ID == problemID {
				problem = &problems[i]
				break
			}
		}
		if problem == nil {
			return "", fmt.Errorf("problem %s was not found", problemID)
		}
	}

	environment := d.environment(ctx)
	if problem == nil {
		return d.anonymize(renderEnvironment(environment)), nil
	}

	location := d.config.Now().Location()
	firstTime, err := time.Parse(time.RFC3339Nano, problem.FirstTime)
	if err != nil {
		return "", err
	}
	lastTime, err := time.Parse(time.RFC3339Nano, problem.LastTime)
	if err != nil {
		return "", err
	}
	related, err := d.relatedRecords(firstTime, lastTime, location)
	if err != nil {
		return "", err
	}
	worker, err := d.workerTail(problem.Gen)
	if err != nil {
		return "", err
	}

	for {
		report := d.anonymize(renderReport(*problem, firstTime.In(location), environment, related, worker))
		if len(report) <= 12*1024 {
			return report, nil
		}
		if len(related) == 0 {
			return "", errors.New("report exceeds 12288 bytes after related records were removed")
		}
		related = related[1:]
	}
}

func (d *Diagnostics) relatedRecords(first, last time.Time, location *time.Location) ([]string, error) {
	since := first.Add(-5 * time.Minute)
	var records []record
	for _, name := range []string{logging.DaemonLogFile, logging.UILogFile} {
		rows, err := readRecords(d.Dir(), name, since)
		if err != nil {
			return nil, err
		}
		records = append(records, rows...)
	}
	start, end := first.Add(-5*time.Minute), last.Add(5*time.Minute)
	filtered := records[:0]
	for _, row := range records {
		if row.time.Before(start) || row.time.After(end) || !atLeastInfo(row.level) {
			continue
		}
		filtered = append(filtered, row)
	}
	sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].time.Before(filtered[j].time) })
	if len(filtered) > 60 {
		filtered = filtered[len(filtered)-60:]
	}
	lines := make([]string, 0, len(filtered))
	for _, row := range filtered {
		lines = append(lines, formatRecord(row, location))
	}
	return trimOldestLines(lines, 6000, runeCount), nil
}

func (d *Diagnostics) workerTail(generation string) ([]string, error) {
	if generation == "" {
		return nil, nil
	}
	rows, err := readRecordFile(filepath.Join(d.Dir(), logging.WorkerLogFile), time.Time{})
	if err != nil {
		return nil, err
	}
	filtered := make([]record, 0, len(rows))
	for _, row := range rows {
		if row.gen == generation && stringField(row.fields, "stream") == "stderr" {
			filtered = append(filtered, row)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].time.Before(filtered[j].time) })
	if len(filtered) > 40 {
		filtered = filtered[len(filtered)-40:]
	}
	lines := make([]string, 0, len(filtered))
	for _, row := range filtered {
		lines = append(lines, row.message)
	}
	return trimOldestLines(lines, 3000, runeCount), nil
}

func atLeastInfo(level string) bool {
	level = strings.ToUpper(level)
	for _, known := range []struct {
		name  string
		value int
	}{
		{name: "DEBUG", value: -4},
		{name: "INFO", value: 0},
		{name: "WARN", value: 4},
		{name: "ERROR", value: 8},
	} {
		if level == known.name {
			return known.value >= 0
		}
		if strings.HasPrefix(level, known.name) {
			offset, err := strconv.Atoi(strings.TrimPrefix(level, known.name))
			return err == nil && known.value+offset >= 0
		}
	}
	return false
}

func formatRecord(row record, location *time.Location) string {
	line := fmt.Sprintf("%s %s %s/%s %s", row.time.In(location).Format("15:04:05.000"), row.level, row.proc, row.mod, row.message)
	keys := make([]string, 0, len(row.fields))
	for key := range row.fields {
		switch key {
		case "time", "level", "msg", "proc", "mod", "run", "stack":
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		line += " " + key + "=" + fmt.Sprint(row.fields[key])
	}
	return line
}

func renderReport(problem Problem, localFirst time.Time, environment Environment, related, worker []string) string {
	sections := []string{
		fmt.Sprintf("### 问题\n%s · %s（%d 次）\n%s", problem.Code, localFirst.Format("2006-01-02 15:04"), problem.Count, problem.Message),
		renderEnvironment(environment),
		"### 相关记录\n```text\n" + strings.Join(related, "\n") + "\n```",
	}
	if len(worker) > 0 {
		sections = append(sections, "### DSH 输出尾部\n```text\n"+strings.Join(worker, "\n")+"\n```")
	}
	return strings.Join(sections, "\n\n") + "\n"
}

func renderEnvironment(environment Environment) string {
	app := environment.AppVersion
	if app == "" {
		app = "-"
	}
	if environment.AppChannel != "" {
		app += "（" + environment.AppChannel + "）"
	}
	dsh := "-"
	if environment.DSHVersion != "" || environment.NodeVersion != "" {
		version := environment.DSHVersion
		if version == "" {
			version = "-"
		}
		node := environment.NodeVersion
		if node == "" {
			node = "-"
		}
		dsh = version + " · Node " + node
	}
	profile := environment.Profile
	if profile == "" {
		profile = "-"
	}
	rows := [][2]string{
		{"dsh-work", app},
		{"系统", emptyAsDash(environment.OS)},
		{"DSH", dsh},
		{"配置", fmt.Sprintf("%s · 插件 %d 个", profile, environment.PluginCount)},
		{"状态", emptyAsDash(environment.State)},
	}
	var builder strings.Builder
	builder.WriteString("### 环境\n| 项 | 值 |\n|---|---|\n")
	for _, row := range rows {
		builder.WriteString("| ")
		builder.WriteString(tableCell(row[0]))
		builder.WriteString(" | ")
		builder.WriteString(tableCell(row[1]))
		builder.WriteString(" |\n")
	}
	return strings.TrimSuffix(builder.String(), "\n")
}

func emptyAsDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func tableCell(value string) string {
	return strings.NewReplacer("|", `\|`, "\r", " ", "\n", " ").Replace(value)
}

func trimOldestLines(lines []string, max int, count func(string) int) []string {
	total := 0
	for _, line := range lines {
		total += count(line)
	}
	if len(lines) > 1 {
		total += len(lines) - 1
	}
	for len(lines) > 0 && total > max {
		removed := count(lines[0])
		if len(lines) > 1 {
			removed++
		}
		total -= removed
		lines = lines[1:]
	}
	return lines
}

func runeCount(value string) int {
	return len([]rune(value))
}
