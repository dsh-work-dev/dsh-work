//go:build windows

package selfupdate

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"
)

// This file builds the smallest Windows executable that still carries a version
// resource, so the installer-identity checks can be exercised without a real
// signed installer. The signing checks themselves cannot be faked: they are
// covered by the WinVerifyTrust tests against genuinely signed binaries.

const (
	peDOSHeaderSize   = 0x40
	peHeaderOffset    = 0x80
	peSectionAlign    = 0x1000
	peFileAlign       = 0x200
	peSectionRawStart = 0x200
	peResourceRVA     = peSectionAlign
	machineAMD64Value = 0x8664
)

// fakeInstallerPayload builds a minimal PE64 image whose version resource
// reports the supplied numeric and named versions.
func fakeInstallerPayload(t *testing.T, fileVersion, productVersion string) []byte {
	t.Helper()
	version := versionResource(t, fileVersion, productVersion)
	resource := resourceDirectoryFor(version)

	raw := make([]byte, peSectionRawStart)
	binary.LittleEndian.PutUint16(raw[0:2], 0x5A4D) // "MZ"
	binary.LittleEndian.PutUint32(raw[0x3C:0x40], peHeaderOffset)
	copy(raw[peHeaderOffset:], "PE\x00\x00")

	coff := peHeaderOffset + 4
	binary.LittleEndian.PutUint16(raw[coff:coff+2], machineAMD64Value)
	binary.LittleEndian.PutUint16(raw[coff+2:coff+4], 1)     // sections
	binary.LittleEndian.PutUint16(raw[coff+16:coff+18], 240) // optional header size

	optional := coff + 20
	binary.LittleEndian.PutUint16(raw[optional:optional+2], 0x20B) // PE32+
	binary.LittleEndian.PutUint32(raw[optional+32:optional+36], peSectionAlign)
	binary.LittleEndian.PutUint32(raw[optional+36:optional+40], peFileAlign)
	binary.LittleEndian.PutUint32(raw[optional+56:optional+60], peSectionRawStart)
	binary.LittleEndian.PutUint32(raw[optional+60:optional+64], peResourceRVA)
	const resourceDirEntry = 2
	entry := optional + 112 + resourceDirEntry*8
	binary.LittleEndian.PutUint32(raw[entry:entry+4], peResourceRVA)
	binary.LittleEndian.PutUint32(raw[entry+4:entry+8], uint32(len(resource)))

	section := optional + 240
	copy(raw[section:section+8], ".rsrc\x00\x00\x00")
	binary.LittleEndian.PutUint32(raw[section+8:section+12], peSectionAlign)
	binary.LittleEndian.PutUint32(raw[section+12:section+16], peSectionAlign)
	binary.LittleEndian.PutUint32(raw[section+16:section+20], uint32(len(resource)))
	binary.LittleEndian.PutUint32(raw[section+20:section+24], peSectionRawStart)
	binary.LittleEndian.PutUint32(raw[section+36:section+40], 0x40000040) // initialized, readable

	return append(raw, resource...)
}

// writeFakeInstaller writes the synthetic executable to a temporary file.
func writeFakeInstaller(t *testing.T, fileVersion, productVersion string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dsh-work-installer.exe")
	if err := os.WriteFile(path, fakeInstallerPayload(t, fileVersion, productVersion), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// resourceDirectoryFor builds the three-level resource directory (type, name,
// language) that points at a version resource. Offsets inside a resource
// directory are relative to the start of the directory: the root header holds
// the type entry, each deeper level is its own header plus one entry, and the
// data entry is reached through the section table.
func resourceDirectoryFor(version []byte) []byte {
	const (
		rootHeader = 16
		entrySize  = 8
		nodeSize   = rootHeader + entrySize // a directory header plus its single entry
		dataSize   = 16                     // IMAGE_RESOURCE_DATA_ENTRY
	)
	nameNode := uint32(rootHeader + entrySize) // the root entry sits before it
	languageNode := nameNode + nodeSize
	dataEntryOffset := languageNode + nodeSize
	blobOffset := dataEntryOffset + dataSize

	directory := make([]byte, 0, int(blobOffset)+len(version))
	header := make([]byte, rootHeader)
	binary.LittleEndian.PutUint16(header[14:16], 1) // one id entry
	directory = append(directory, header...)
	// Root entry: resource type 16 (RT_VERSION) points at the name level.
	directory = append(directory, resourceDirectoryEntry(16, nameNode, true)...)

	// Name level: resource name 1 points at the language level.
	directory = append(directory, resourceDirectoryLevel(1, languageNode, true)...)
	// Language level: this language names the version resource's data entry.
	directory = append(directory, resourceDirectoryLevel(0x0409, dataEntryOffset, false)...)

	// Data entry: the offset is relative to the start of the resource
	// directory, which in this image is the start of the resource section.
	dataEntry := make([]byte, dataSize)
	binary.LittleEndian.PutUint32(dataEntry[0:4], peResourceRVA+uint32(blobOffset))
	binary.LittleEndian.PutUint32(dataEntry[4:8], uint32(len(version)))
	directory = append(directory, dataEntry...)
	return append(directory, version...)
}

// resourceDirectoryEntry is one numeric entry. An entry whose high bit is set
// names a deeper directory; otherwise its offset names a data entry.
func resourceDirectoryEntry(id, offset uint32, pointsToLevel bool) []byte {
	entry := make([]byte, 8)
	binary.LittleEndian.PutUint32(entry[0:4], id)
	if pointsToLevel {
		offset |= 0x80000000
	}
	binary.LittleEndian.PutUint32(entry[4:8], offset)
	return entry
}

// resourceDirectoryLevel is a directory header holding a single entry.
func resourceDirectoryLevel(id, offset uint32, pointsToLevel bool) []byte {
	level := make([]byte, 16)
	binary.LittleEndian.PutUint16(level[14:16], 1) // one id entry
	return append(level, resourceDirectoryEntry(id, offset, pointsToLevel)...)
}

// versionResource builds a VS_VERSIONINFO block with the fixed file info and a
// StringFileInfo table carrying FileVersion and ProductVersion.
func versionResource(t *testing.T, fileVersion, productVersion string) []byte {
	t.Helper()
	major, minor, build, revision := parseNumericVersion(t, fileVersion)

	fixed := make([]byte, versionFixedSize)
	binary.LittleEndian.PutUint32(fixed[0:4], fixedSignature)
	binary.LittleEndian.PutUint32(fixed[8:12], fileVersionDword(major, minor))
	binary.LittleEndian.PutUint32(fixed[12:16], fileVersionDword(build, revision))

	table := stringTable(t, "FileVersion", fileVersion, "ProductVersion", productVersion)
	stringFileInfo := versionBlock("StringFileInfo", 0, 0, table)
	return versionBlock("VS_VERSION_INFO", 0, uint16(len(fixed)), fixed, stringFileInfo)
}

// stringTable wraps KEY=VALUE pairs in a StringTable block whose key is the
// eight-character hexadecimal language/codepage code.
func stringTable(t *testing.T, pairs ...string) []byte {
	t.Helper()
	if len(pairs)%2 != 0 {
		t.Fatalf("stringTable needs key/value pairs, got %d values", len(pairs))
	}
	var children []byte
	for index := 0; index < len(pairs); index += 2 {
		for len(children)%4 != 0 {
			children = append(children, 0)
		}
		value := utf16Bytes(pairs[index+1])
		children = append(children, versionBlock(pairs[index], versionTextType, uint16(len(value)), value)...)
	}
	return versionBlock("040904B0", 0, 0, children)
}

// versionBlock assembles one version structure: length, value length, type, a
// UTF-16 key, padding to a 32-bit boundary, then the value and any child blocks.
// A container declares type 0 and a string value declares type 1, which is how
// the reader tells the two apart.
func versionBlock(key string, valueType, declaredValueLength uint16, values ...[]byte) []byte {
	var raw []byte
	for _, value := range values {
		raw = append(raw, value...)
	}

	block := make([]byte, 6)
	binary.LittleEndian.PutUint16(block[4:6], valueType)
	block = append(block, utf16Bytes(key)...)
	for len(block)%4 != 0 {
		block = append(block, 0)
	}
	// The value length is declared by the block; the value itself starts here.
	binary.LittleEndian.PutUint16(block[2:4], declaredValueLength)
	block = append(block, raw...)
	for len(block)%4 != 0 {
		block = append(block, 0)
	}
	binary.LittleEndian.PutUint16(block[0:2], uint16(len(block)))
	return block
}

func utf16Bytes(value string) []byte {
	encoded := utf16.Encode([]rune(value))
	raw := make([]byte, 0, (len(encoded)+1)*2)
	for _, character := range encoded {
		raw = binary.LittleEndian.AppendUint16(raw, character)
	}
	return binary.LittleEndian.AppendUint16(raw, 0)
}

func parseNumericVersion(t *testing.T, value string) (uint16, uint16, uint16, uint16) {
	t.Helper()
	var parts [4]uint16
	index := 0
	current := 0
	seen := false
	for _, character := range value {
		if character == '.' {
			if !seen || index >= 3 {
				t.Fatalf("unexpected version %q", value)
			}
			parts[index] = uint16(current)
			index++
			current = 0
			seen = false
			continue
		}
		if character < '0' || character > '9' {
			t.Fatalf("unexpected version %q", value)
		}
		current = current*10 + int(character-'0')
		seen = true
	}
	if !seen || index != 3 {
		t.Fatalf("version %q is not a four-component version", value)
	}
	parts[3] = uint16(current)
	return parts[0], parts[1], parts[2], parts[3]
}

func fileVersionDword(major, minor uint16) uint32 {
	return uint32(major)<<16 | uint32(minor)
}
