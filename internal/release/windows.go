package release

import "fmt"

// Windows file metadata is limited to four 16-bit fields. A release version is
// therefore mapped onto X.Y.Z.0, while every user-visible surface keeps the
// full semantic version. The date and run id of a nightly build never enter
// the numeric fields: they are not upgrade-eligibility information, and
// stuffing them in would overflow the fields.
const (
	// WindowsFieldMax is the largest value a single file-version field holds.
	WindowsFieldMax = 65535
	// WindowsFieldCount is the number of 16-bit fields in the mapping.
	WindowsFieldCount = 4
)

// WindowsFields maps the major, minor and patch components onto the four
// Windows file-version fields. It fails when a component cannot be
// represented, so a version that would silently truncate never reaches a
// build.
func (v Version) WindowsFields() ([WindowsFieldCount]uint16, error) {
	var fields [WindowsFieldCount]uint16
	for index, component := range [3]uint64{v.major, v.minor, v.patch} {
		if component > WindowsFieldMax {
			return fields, fmt.Errorf("version %s component %d exceeds the Windows file version limit %d",
				v.raw, component, WindowsFieldMax)
		}
		fields[index] = uint16(component)
	}
	return fields, nil
}

// WindowsFileVersion returns the dotted four-field mapping used by the Windows
// file metadata, for example "0.3.0.0".
func (v Version) WindowsFileVersion() (string, error) {
	fields, err := v.WindowsFields()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d.%d.%d.%d", fields[0], fields[1], fields[2], fields[3]), nil
}

// BaseWindowsFileVersion maps an X.Y.Z base version string onto the four-field
// Windows form.
func BaseWindowsFileVersion(base string) (string, error) {
	version, err := Parse(base)
	if err != nil {
		return "", err
	}
	if version.IsPrerelease() {
		return "", fmt.Errorf("base version %q must not carry a pre-release part", base)
	}
	return version.WindowsFileVersion()
}
