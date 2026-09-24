package dshadapter

import (
	"slices"
	"testing"
)

// incidentStderr is the 2026-09-18 early exit, trimmed. The loader entry names a
// third-party plugin, but the error is DSH rejecting its own session log.
const incidentStderr = `Error: dsh: plugin tree failed to load: failed to apply loader entry workspace-archive-manager (@michengai/dsh-archive-manager/workspace): corrupt Zstandard session log: first frame is not exactly one header line
    at assertZstdHeaderFrame (file:///C:/Users/livei/AppData/Roaming/dsh-work/runtimes/dsh-0.1.5-rc.2/node_modules/@deepseek-ai/dsh-session-persistence-jsonl/lib/index.js:2185:86)
    at async ArchiveWorkspaceRegistry.listStoredHeaders (file:///C:/Users/livei/AppData/Roaming/dsh-work/environment/profiles/web/node_modules/@michengai/dsh-archive-manager/lib/workspace.js:731:13)
Node.js v24.2.0`

func TestStoredDataRejectionIsNotAPluginFault(t *testing.T) {
	if !StoredDataRejected(incidentStderr) {
		t.Fatal("the session-log rejection was not recognized")
	}
	if StoredDataRejected("dsh: plugin tree failed to load: failed to apply loader entry ui (@acme/widget): boom") {
		t.Fatal("an ordinary plugin failure was read as a stored-data rejection")
	}
}

func TestPluginFailureCandidatesOrderLoaderEntryFirst(t *testing.T) {
	got := PluginFailureCandidates(incidentStderr)
	want := []string{"@michengai/dsh-archive-manager", "@deepseek-ai/dsh-session-persistence-jsonl"}
	if !slices.Equal(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
}

func TestPluginFailureCandidatesReadLoadAndModuleFailures(t *testing.T) {
	for name, testCase := range map[string]struct {
		output string
		want   []string
	}{
		"web boot failed import list": {
			output: "HARNESS\nFailed to load plugins\ndsh-univer-office",
			want:   []string{"dsh-univer-office"},
		},
		"web boot pending service": {
			output: "web boot: 1 entry did not activate\ndsh-just-chat: pending (waiting for service: settingsScope)",
			want:   []string{"dsh-just-chat"},
		},
		"failed to load list": {
			output: `dsh: 2 plugin(s) failed to load: @acme/widget, plain-plugin`,
			want:   []string{"@acme/widget", "plain-plugin"},
		},
		"missing module blames its importer first": {
			output: `Error [ERR_MODULE_NOT_FOUND]: Cannot find package 'left-pad' imported from C:\dsh\profiles\web\node_modules\.pnpm\@acme+widget@1.0.0\node_modules\@acme\widget\lib\index.js`,
			want:   []string{"@acme/widget", "left-pad"},
		},
		"stack frame through the pnpm store": {
			output: `    at init (file:///C:/dsh/profiles/web/node_modules/.pnpm/widget@1.0.0/node_modules/widget/lib/index.js:1:1)`,
			want:   []string{"widget"},
		},
		"no package evidence": {
			output: `Error: listen EADDRINUSE 127.0.0.1:1`,
			want:   nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := PluginFailureCandidates(testCase.output); !slices.Equal(got, testCase.want) {
				t.Fatalf("candidates = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestPackageRootRejectsPathsAndOptions(t *testing.T) {
	for _, specifier := range []string{"../escape", "--registry", "C:/dsh/x", "@scope", ""} {
		if name, ok := packageRoot(specifier); ok {
			t.Errorf("packageRoot(%q) = %q, want rejection", specifier, name)
		}
	}
	if name, ok := packageRoot("@acme/widget/lib/index.js"); !ok || name != "@acme/widget" {
		t.Errorf("packageRoot subpath = %q %v", name, ok)
	}
}
