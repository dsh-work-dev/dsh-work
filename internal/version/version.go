// Package version carries the application version injected by release builds.
package version

// Value is replaced by the Windows release task through -ldflags. Development
// builds keep the explicit fallback so the CLI remains useful without a
// packaging step.
var Value = "development"

// String returns the application version reported by this executable.
func String() string {
	if Value == "" {
		return "development"
	}
	return Value
}
