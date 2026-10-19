package diagnostics

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testNow() time.Time {
	return time.Date(2026, 10, 19, 12, 0, 0, 0, time.FixedZone("CST", 8*60*60))
}

func testDiagnostics(dir string) *Diagnostics {
	return New(Config{Dir: dir, Now: testNow})
}

func testRecord(at time.Time, level, code, generation string) map[string]any {
	return map[string]any{
		"time":  at.Format(time.RFC3339Nano),
		"level": level,
		"msg":   "worker start failed",
		"proc":  "daemon",
		"mod":   "lifecycle",
		"run":   "run-1",
		"code":  code,
		"gen":   generation,
		"op":    "op-1",
	}
}

func recordLine(t *testing.T, fields map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func writeRecords(t *testing.T, dir, name string, records ...map[string]any) {
	t.Helper()
	var data []byte
	for _, fields := range records {
		data = append(data, recordLine(t, fields)...)
	}
	writeFile(t, filepath.Join(dir, name), data)
}

func writeFile(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeGzip(t *testing.T, name string, data []byte) {
	t.Helper()
	file, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	writer := gzip.NewWriter(file)
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestProblemsMergesSameCodeWithinTenMinutes(t *testing.T) {
	dir := t.TempDir()
	start := testNow().Add(-time.Hour)
	first := testRecord(start, "WARN", "DSH_START_FAILED", "gen-1")
	second := testRecord(start.Add(5*time.Minute), "ERROR", "DSH_START_FAILED", "gen-2")
	third := testRecord(start.Add(20*time.Minute), "WARN", "DSH_START_FAILED", "gen-3")
	writeRecords(t, dir, "daemon.log", first, second, third)

	problems, err := testDiagnostics(dir).Problems(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 2 {
		t.Fatalf("problems = %#v, want two runs", problems)
	}
	if problems[0].Count != 1 || problems[0].Level != "WARN" {
		t.Fatalf("newest problem = %#v, want one WARN record", problems[0])
	}
	if problems[1].Count != 2 || problems[1].Level != "ERROR" {
		t.Fatalf("merged problem = %#v, want two records with ERROR level", problems[1])
	}
}

func TestProblemsReadsBackupsAndSkipsNoise(t *testing.T) {
	dir := t.TempDir()
	backup := testRecord(testNow().Add(-time.Hour), "ERROR", "BACKUP_FAILURE", "gen-1")
	writeGzip(t, filepath.Join(dir, "daemon-20261019-110000.log.gz"), recordLine(t, backup))

	old := testRecord(testNow().Add(-8*24*time.Hour), "ERROR", "TOO_OLD", "gen-2")
	info := testRecord(testNow().Add(-30*time.Minute), "INFO", "INFO_CODE", "gen-3")
	cancelled := testRecord(testNow().Add(-20*time.Minute), "WARN", "CANCELLED", "gen-4")
	writeRecords(t, dir, "daemon.log", old, info, cancelled)
	file, err := os.OpenFile(filepath.Join(dir, "daemon.log"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("not json\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	problems, err := testDiagnostics(dir).Problems(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 || problems[0].Code != "BACKUP_FAILURE" {
		t.Fatalf("problems = %#v, want only backup record", problems)
	}
}

func TestProblemsNewestFirstAndCapped(t *testing.T) {
	dir := t.TempDir()
	start := testNow().Add(-time.Hour)
	records := make([]map[string]any, 25)
	for i := range records {
		records[i] = testRecord(start.Add(time.Duration(i)*time.Minute), "ERROR", "CODE_"+timeTwoDigits(i), "")
	}
	writeRecords(t, dir, "daemon.log", records...)

	problems, err := testDiagnostics(dir).Problems(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 20 {
		t.Fatalf("problem count = %d, want 20", len(problems))
	}
	if problems[0].Code != "CODE_24" || problems[len(problems)-1].Code != "CODE_05" {
		t.Fatalf("newest-first range = %s through %s", problems[0].Code, problems[len(problems)-1].Code)
	}
}

func timeTwoDigits(value int) string {
	if value < 10 {
		return "0" + string(rune('0'+value))
	}
	return string(rune('0'+value/10)) + string(rune('0'+value%10))
}

func TestReportSectionsAndLimits(t *testing.T) {
	dir := t.TempDir()
	start := testNow().Add(-2 * time.Minute)
	problem := testRecord(start, "ERROR", "DSH_START_FAILED", "gen-report")
	problem["msg"] = "worker failed"
	records := []map[string]any{problem}
	for i := 0; i < 75; i++ {
		row := testRecord(start.Add(time.Duration(i%9-4)*time.Minute), "INFO", "", "gen-report")
		row["msg"] = strings.Repeat("related-", 20) + timeTwoDigits(i)
		records = append(records, row)
	}
	writeRecords(t, dir, "daemon.log", records...)

	var workerData []byte
	for i := 0; i < 50; i++ {
		row := testRecord(start.Add(time.Duration(i)*time.Second), "WARN", "", "gen-report")
		row["msg"] = strings.Repeat("worker-", 12) + timeTwoDigits(i)
		row["stream"] = "stderr"
		workerData = append(workerData, recordLine(t, row)...)
	}
	wrongGeneration := testRecord(start, "WARN", "", "other-gen")
	wrongGeneration["stream"] = "stderr"
	wrongGeneration["msg"] = "wrong generation"
	workerData = append(workerData, recordLine(t, wrongGeneration)...)
	stdout := testRecord(start, "INFO", "", "gen-report")
	stdout["stream"] = "stdout"
	stdout["msg"] = "stdout line"
	workerData = append(workerData, recordLine(t, stdout)...)
	writeFile(t, filepath.Join(dir, "worker.log"), workerData)

	diagnostics := New(Config{
		Dir: dir,
		Now: testNow,
		Environment: func(context.Context) Environment {
			return Environment{AppVersion: "0.4.0", AppChannel: "beta", DSHVersion: "0.2.1", NodeVersion: "22.x", Profile: "default", PluginCount: 4, State: "failed"}
		},
	})
	problems, err := diagnostics.Problems(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	report, err := diagnostics.Report(context.Background(), problems[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(report, "### ") != 4 {
		t.Fatalf("report sections = %d, want four; report:\n%s", strings.Count(report, "### "), report)
	}
	if len(report) > 12*1024 {
		t.Fatalf("report size = %d bytes, exceeds 12288", len(report))
	}
	related := sectionLines(t, report, "### 相关记录\n```text\n", "\n```\n\n### DSH 输出尾部")
	if len(related) > 60 {
		t.Fatalf("related record lines = %d, want at most 60", len(related))
	}
	worker := sectionLines(t, report, "### DSH 输出尾部\n```text\n", "\n```")
	if len(worker) > 40 {
		t.Fatalf("worker tail lines = %d, want at most 40", len(worker))
	}
	if runeCount(strings.Join(worker, "\n")) > 3000 {
		t.Fatalf("worker tail characters = %d, want at most 3000", runeCount(strings.Join(worker, "\n")))
	}
	if strings.Contains(report, "wrong generation") || strings.Contains(report, "stdout line") {
		t.Fatalf("report contains unrelated Worker output:\n%s", report)
	}
}

func sectionLines(t *testing.T, report, start, end string) []string {
	t.Helper()
	parts := strings.SplitN(report, start, 2)
	if len(parts) != 2 {
		t.Fatalf("section start %q not found", start)
	}
	parts = strings.SplitN(parts[1], end, 2)
	if len(parts) != 2 {
		t.Fatalf("section end %q not found", end)
	}
	content := strings.TrimSpace(parts[0])
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}

func TestReportWithoutProblemHasOnlyEnvironment(t *testing.T) {
	report, err := testDiagnostics(t.TempDir()).Report(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(report, "### ") != 1 || !strings.HasPrefix(report, "### 环境\n") {
		t.Fatalf("environment-only report = %q", report)
	}
}

func TestReportUnknownProblem(t *testing.T) {
	_, err := testDiagnostics(t.TempDir()).Report(context.Background(), "missing")
	if err == nil || err.Error() != "problem missing was not found" {
		t.Fatalf("error = %v, want missing problem error", err)
	}
}

func TestAnonymizeHomeAndHost(t *testing.T) {
	diagnostics := New(Config{HomeDir: `C:\Users\Alice`, HostName: "my-host"})
	input := `C:\Users\Alice C:\\Users\\Alice c:/users/alice MY-HOST my-hostname`
	got := diagnostics.anonymize(input)
	want := `~ ~ ~ <host> my-hostname`
	if got != want {
		t.Fatalf("anonymize(%q) = %q, want %q", input, got, want)
	}
}

func TestWriteBundleContents(t *testing.T) {
	dir := t.TempDir()
	outputDir := t.TempDir()
	dshPath := filepath.Join(t.TempDir(), "startup-x.log")
	writeFile(t, dshPath, []byte(`C:\Users\Alice my-host`))
	row := testRecord(testNow().Add(-time.Minute), "WARN", "", "gen-1")
	row["msg"] = "dsh startup diagnostics written"
	row["dshlog"] = dshPath
	row["details"] = `C:\Users\Alice my-host`
	writeRecords(t, dir, "daemon.log", row)
	writeRecords(t, dir, "ui.log", testRecord(testNow(), "INFO", "", ""))
	writeGzip(t, filepath.Join(dir, "daemon-20261019-110000.log.gz"), []byte(`{"path":"C:\\Users\\Alice","host":"my-host"}`+"\n"))
	crashDir := filepath.Join(dir, "crash")
	if err := os.MkdirAll(crashDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(crashDir, "daemon-x.txt"), []byte(`C:\Users\Alice my-host`))
	writeFile(t, filepath.Join(crashDir, "daemon.current"), []byte("not included"))

	diagnostics := New(Config{
		Dir:      dir,
		Now:      testNow,
		HomeDir:  `C:\Users\Alice`,
		HostName: "my-host",
		Environment: func(context.Context) Environment {
			return Environment{AppVersion: "1.2.3", Profile: `C:\Users\Alice my-host`}
		},
	})
	outputPath := filepath.Join(outputDir, "bundle.zip")
	result, err := diagnostics.WriteBundle(context.Background(), outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != outputPath || result.Size == 0 {
		t.Fatalf("bundle result = %#v", result)
	}
	contents := readZip(t, outputPath)
	wantNames := []string{
		"manifest.json",
		"environment.json",
		"logs/daemon.log",
		"logs/daemon-20261019-110000.log",
		"logs/ui.log",
		"crash/daemon-x.txt",
		"dsh/startup-x.log",
	}
	for _, name := range wantNames {
		if _, ok := contents[name]; !ok {
			t.Errorf("bundle missing %q; entries = %v", name, mapKeys(contents))
		}
	}
	if _, ok := contents["crash/daemon.current"]; ok {
		t.Fatal("bundle includes a current crash file")
	}
	for _, name := range []string{"environment.json", "logs/daemon.log", "crash/daemon-x.txt", "dsh/startup-x.log"} {
		text := string(contents[name])
		if strings.Contains(strings.ToLower(text), `c:\users\alice`) || strings.Contains(text, "my-host") {
			t.Errorf("%s was not anonymized: %s", name, text)
		}
		if !strings.Contains(text, "~") || !strings.Contains(text, "<host>") {
			t.Errorf("%s is missing anonymized values: %s", name, text)
		}
	}
}

func TestWriteBundleTrimsBackupsOverLimit(t *testing.T) {
	dir := t.TempDir()
	outputDir := t.TempDir()
	writeFile(t, filepath.Join(dir, "daemon.log"), bytes.Repeat([]byte("d"), 64))
	writeFile(t, filepath.Join(dir, "worker.log"), bytes.Repeat([]byte("w"), 64))
	writeFile(t, filepath.Join(dir, "daemon-older.log"), bytes.Repeat([]byte("b"), 500))
	writeGzip(t, filepath.Join(dir, "worker-old.log.gz"), bytes.Repeat([]byte("x"), 2000))
	old := testNow().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "worker-old.log.gz"), old, old); err != nil {
		t.Fatal(err)
	}
	diagnostics := New(Config{Dir: dir, Now: testNow, MaxBundleBytes: 1300})
	outputPath := filepath.Join(outputDir, "bundle.zip")
	result, err := diagnostics.WriteBundle(context.Background(), outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Omitted) != 1 || result.Omitted[0] != "logs/worker-old.log" {
		t.Fatalf("omitted = %v, want worker backup first", result.Omitted)
	}
	contents := readZip(t, outputPath)
	if _, ok := contents["logs/worker.log"]; !ok {
		t.Fatal("current worker log was omitted")
	}
	if _, ok := contents["logs/worker-old.log"]; ok {
		t.Fatal("worker backup remains in bundle")
	}
	if _, ok := contents["logs/daemon-older.log"]; !ok {
		t.Fatal("other backup should remain after the worker backup was removed")
	}
	var manifest struct {
		Omitted []string `json:"omitted"`
	}
	if err := json.Unmarshal(contents["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Omitted) != 1 || manifest.Omitted[0] != "logs/worker-old.log" {
		t.Fatalf("manifest omitted = %v", manifest.Omitted)
	}
}

func TestWriteBundleRejectsRelativePath(t *testing.T) {
	dir := t.TempDir()
	_, err := testDiagnostics(dir).WriteBundle(context.Background(), "relative.zip")
	if err == nil {
		t.Fatal("relative bundle path was accepted")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("directory contains files after rejection: %v", mapDirEntries(entries))
	}
}

func TestProblemsSkipHalfWrittenGzipBackup(t *testing.T) {
	dir := t.TempDir()
	writeRecords(t, dir, "daemon.log", testRecord(testNow().Add(-time.Minute), "ERROR", "DSH_START_FAILED", "gen-1"))
	// lumberjack writes the .gz in the background; a reader can see it cut short.
	writeFile(t, filepath.Join(dir, "daemon-20261019-110000.log.gz"), []byte{0x1f, 0x8b, 0x08, 0x00})

	problems, err := testDiagnostics(dir).Problems(context.Background())
	if err != nil {
		t.Fatalf("Problems() error = %v, want the damaged backup skipped", err)
	}
	if len(problems) != 1 || problems[0].Code != "DSH_START_FAILED" {
		t.Fatalf("problems = %#v, want the record from daemon.log", problems)
	}
}

func TestWriteBundleListsUnreadableBackupAsOmitted(t *testing.T) {
	dir := t.TempDir()
	writeRecords(t, dir, "daemon.log", testRecord(testNow(), "INFO", "", ""))
	writeFile(t, filepath.Join(dir, "daemon-20261019-110000.log.gz"), []byte{0x1f, 0x8b, 0x08, 0x00})

	outputPath := filepath.Join(t.TempDir(), "bundle.zip")
	result, err := testDiagnostics(dir).WriteBundle(context.Background(), outputPath)
	if err != nil {
		t.Fatalf("WriteBundle() error = %v, want the damaged backup omitted", err)
	}
	if len(result.Omitted) != 1 || result.Omitted[0] != "logs/daemon-20261019-110000.log.gz" {
		t.Fatalf("Omitted = %#v, want the damaged backup", result.Omitted)
	}
	entries := readZip(t, outputPath)
	if _, ok := entries["logs/daemon.log"]; !ok {
		t.Fatalf("bundle entries = %v, want logs/daemon.log", mapKeys(entries))
	}
	if !strings.Contains(string(entries["manifest.json"]), "daemon-20261019-110000.log.gz") {
		t.Fatalf("manifest = %s, want the omitted backup listed", entries["manifest.json"])
	}
}

func readZip(t *testing.T, name string) map[string][]byte {
	t.Helper()
	archive, err := zip.OpenReader(name)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	contents := make(map[string][]byte)
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		closeErr := reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		contents[file.Name] = data
	}
	return contents
}

func mapKeys(values map[string][]byte) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func mapDirEntries(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}
