//go:build windows

package windows

import "testing"

func TestValidateBatchInvocationRejectsShellSyntax(t *testing.T) {
	for _, test := range []struct {
		name       string
		executable string
		args       []string
	}{
		{name: "argument ampersand", executable: `C:\tools\dsh.cmd`, args: []string{"plugin", "@example/plugin&whoami"}},
		{name: "argument percent expansion", executable: `C:\tools\dsh.cmd`, args: []string{"plugin", "%PATH%"}},
		{name: "executable pipe", executable: `C:\tools\safe|dsh.cmd`, args: nil},
		{name: "argument newline", executable: `C:\tools\dsh.cmd`, args: []string{"plugin\nwhoami"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateBatchInvocation(test.executable, test.args); err == nil {
				t.Fatal("batch invocation unexpectedly accepted shell syntax")
			}
		})
	}
}

func TestValidateBatchInvocationAllowsFixedDSHArguments(t *testing.T) {
	if err := validateBatchInvocation(`C:\tools\dsh.cmd`, []string{
		"--profile", "web", "--host", "127.0.0.1", "--port", "4321", "--no-open",
	}); err != nil {
		t.Fatalf("fixed DSH launch args rejected: %v", err)
	}
}
