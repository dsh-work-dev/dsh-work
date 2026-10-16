package selfupdate

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// A feed signature proves who produced the bytes. It does not prove which
// release a payload installs, and a provider that advertises a higher version
// than the payload actually carries must not be able to make an older signed
// payload look like an upgrade. The version resource inside the signed
// installer is the evidence for that: publishing a version that does not match
// the payload would require re-signing the payload, because the resource lives
// inside the bytes the signature covers.

const (
	minDosHeaderSize = 0x40
	resourceDirEntry = 2 // IMAGE_DIRECTORY_ENTRY_RESOURCE
	// versionResourceType is RT_VERSION, the resource type that carries the
	// version information this package reads.
	versionResourceType = 16
	// versionTextType marks a block whose value is a UTF-16 string rather than
	// binary data, which is what distinguishes a displayed version string from
	// the translation table.
	versionTextType  = 1
	versionFixedSize = 13 * 4
	fixedSignature   = 0xFEEF04BD

	// pe32Magic and pe32PlusMagic are the two optional-header formats, and
	// pe32DirectoryOffset/pe32PlusDirectoryOffset the offsets of the data
	// directory array inside each of them.
	pe32Magic               = 0x10B
	pe32PlusMagic           = 0x20B
	pe32DirectoryOffset     = 96
	pe32PlusDirectoryOffset = 112
)

// machineAMD64, machineARM64 and machineI386 are the architectures this product
// meets: the release unit it builds, and the installer stub that carries it.
const (
	machineAMD64 = 0x8664
	machineARM64 = 0xAA64
	machineI386  = 0x014C
)

// readMachine returns IMAGE_FILE_HEADER.Machine from a PE file.
func readMachine(path string) (uint16, error) {
	var header [minDosHeaderSize]byte
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	if _, err := file.ReadAt(header[:], 0); err != nil {
		return 0, fmt.Errorf("%s is not a PE file", filepath.Base(path))
	}
	if header[0] != 'M' || header[1] != 'Z' {
		return 0, errors.New("missing DOS header")
	}
	peOffset := int64(binary.LittleEndian.Uint32(header[0x3C:0x40]))
	if peOffset <= 0 || peOffset > 1<<24 {
		return 0, errors.New("invalid PE header offset")
	}
	var signature [4]byte
	if _, err := file.ReadAt(signature[:], peOffset); err != nil {
		return 0, errors.New("truncated PE header")
	}
	if signature != [4]byte{'P', 'E', 0, 0} {
		return 0, errors.New("missing PE signature")
	}
	machine := make([]byte, 2)
	if _, err := file.ReadAt(machine, peOffset+4); err != nil {
		return 0, errors.New("truncated PE header")
	}
	return binary.LittleEndian.Uint16(machine), nil
}

// payloadIdentity is the release identity a Windows executable declares: the
// four-field numeric version in its fixed file info, and the version strings in
// its string table.
type payloadIdentity struct {
	fixed          string
	fileVersion    string
	productVersion string
}

// versionStrings returns every release version string the payload declares. A
// payload that declares none cannot be told apart from another build of the
// same base version.
func (i payloadIdentity) versionStrings() []string {
	var declared []string
	for _, value := range []string{i.fileVersion, i.productVersion} {
		if strings.TrimSpace(value) != "" {
			declared = append(declared, strings.TrimSpace(value))
		}
	}
	return declared
}

// readPayloadIdentity returns the identity a signed executable declares. A
// missing version resource is an error rather than an unknown, because the
// caller needs the evidence.
func readPayloadIdentity(path string) (payloadIdentity, error) {
	empty := payloadIdentity{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return empty, err
	}
	layout, resourceOffset, resourceSize, err := resourceDirectory(raw)
	if err != nil {
		return empty, err
	}
	if resourceOffset == 0 || resourceSize == 0 {
		return empty, errors.New("no resource directory")
	}
	info, err := versionInfoResource(raw, layout, resourceOffset, resourceSize)
	if err != nil {
		return empty, err
	}
	identity := payloadIdentity{
		fileVersion:    info.named["FILEVERSION"],
		productVersion: info.named["PRODUCTVERSION"],
	}
	if info.fixed {
		identity.fixed = fmt.Sprintf("%d.%d.%d.%d", info.major, info.minor, info.build, info.revision)
	}
	return identity, nil
}

type executableVersion struct {
	fixed bool

	major    uint16
	minor    uint16
	build    uint16
	revision uint16

	named map[string]string
}

// peLayout records where the parts of an image header live, so an RVA can be
// translated once and the resource directory structure can be walked without
// re-reading the headers at every level.
type peLayout struct {
	optional     int
	optionalSize int
	sections     int
}

// parsePELayout validates the DOS and PE headers and returns the header layout.
func parsePELayout(raw []byte) (peLayout, error) {
	if len(raw) < minDosHeaderSize || raw[0] != 'M' || raw[1] != 'Z' {
		return peLayout{}, errors.New("not a PE file")
	}
	peOffset := int(binary.LittleEndian.Uint32(raw[0x3C:0x40]))
	if peOffset <= 0 || peOffset+24 > len(raw) {
		return peLayout{}, errors.New("invalid PE header offset")
	}
	if raw[peOffset] != 'P' || raw[peOffset+1] != 'E' {
		return peLayout{}, errors.New("missing PE signature")
	}
	coff := peOffset + 4
	layout := peLayout{
		optional:     coff + 20,
		optionalSize: int(binary.LittleEndian.Uint16(raw[coff+16 : coff+18])),
		sections:     int(binary.LittleEndian.Uint16(raw[coff+2 : coff+4])),
	}
	if layout.optional+layout.optionalSize > len(raw) {
		return peLayout{}, errors.New("truncated optional header")
	}
	return layout, nil
}

// dataDirectoryOffset reports where the data directory array starts. The fixed
// part of the optional header differs in size between the two PE formats: the
// installer this product ships is a PE32 image, because the NSIS stub is 32-bit
// even when it installs a 64-bit release unit, while a natively targeted
// payload can be PE32+.
func (l peLayout) dataDirectoryOffset(raw []byte) (int, error) {
	switch magic := binary.LittleEndian.Uint16(raw[l.optional : l.optional+2]); magic {
	case pe32Magic:
		return l.optional + pe32DirectoryOffset, nil
	case pe32PlusMagic:
		return l.optional + pe32PlusDirectoryOffset, nil
	default:
		return 0, fmt.Errorf("unsupported PE optional header 0x%04X", magic)
	}
}

// fileOffset translates an RVA range into a file offset through the section
// table, refusing a range no section maps or that runs past the file.
func (l peLayout) fileOffset(raw []byte, rva, size uint32) (uint32, error) {
	if rva == 0 {
		return 0, errors.New("empty RVA")
	}
	table := l.optional + l.optionalSize
	for i := 0; i < l.sections; i++ {
		entry := table + i*40
		if entry+40 > len(raw) {
			return 0, errors.New("truncated section table")
		}
		virtualSize := binary.LittleEndian.Uint32(raw[entry+8:])
		virtualAddress := binary.LittleEndian.Uint32(raw[entry+12:])
		rawSize := binary.LittleEndian.Uint32(raw[entry+16:])
		rawPointer := binary.LittleEndian.Uint32(raw[entry+20:])
		span := virtualSize
		if span == 0 {
			span = rawSize
		}
		if rva < virtualAddress || rva+size > virtualAddress+span {
			continue
		}
		offset := rawPointer + (rva - virtualAddress)
		if int(offset)+int(size) > len(raw) {
			return 0, errors.New("resource range is outside the file")
		}
		return offset, nil
	}
	return 0, fmt.Errorf("RVA 0x%X is not mapped by any section", rva)
}

// resourceDirectory returns the file offset and size of the resource directory,
// translating its RVA through the section table.
func resourceDirectory(raw []byte) (peLayout, uint32, uint32, error) {
	layout, err := parsePELayout(raw)
	if err != nil {
		return peLayout{}, 0, 0, err
	}
	directoryOffset, err := layout.dataDirectoryOffset(raw)
	if err != nil {
		return layout, 0, 0, err
	}
	if directoryOffset+resourceDirEntry*8+8 > layout.optional+layout.optionalSize {
		return layout, 0, 0, errors.New("missing data directory")
	}
	rva := binary.LittleEndian.Uint32(raw[directoryOffset+resourceDirEntry*8:])
	size := binary.LittleEndian.Uint32(raw[directoryOffset+resourceDirEntry*8+4:])
	if rva == 0 || size == 0 {
		return layout, 0, 0, nil
	}
	offset, err := layout.fileOffset(raw, rva, size)
	if err != nil {
		return layout, 0, 0, err
	}
	return layout, offset, size, nil
}

// versionInfoResource walks the three resource-directory levels (type, name,
// language) to the version resource's data entry, translates the RVA that entry
// holds, and parses the VS_VERSIONINFO block it points at.
//
// Every offset inside a resource directory is relative to the start of that
// directory, so each level is resolved against the same slice rather than
// against the level it was reached from.
func versionInfoResource(raw []byte, layout peLayout, resourceOffset, resourceSize uint32) (executableVersion, error) {
	empty := executableVersion{}
	if int(resourceOffset)+int(resourceSize) > len(raw) {
		return empty, errors.New("resource directory is outside the file")
	}
	directory := raw[resourceOffset : resourceOffset+resourceSize]
	if len(directory) < 16 {
		return empty, errors.New("truncated resource directory")
	}
	for i := 0; i < directoryEntries(directory); i++ {
		id, err := directoryEntryID(directory, i)
		if err != nil {
			return empty, err
		}
		if id != versionResourceType {
			continue
		}
		names, err := subdirectory(directory, directory, i)
		if err != nil {
			return empty, err
		}
		if names == nil {
			continue
		}
		for j := 0; j < directoryEntries(names); j++ {
			languages, err := subdirectory(directory, names, j)
			if err != nil {
				return empty, err
			}
			if languages == nil {
				continue
			}
			for k := 0; k < directoryEntries(languages); k++ {
				dataEntry, err := resourceDataEntry(directory, languages, k)
				if err != nil {
					return empty, err
				}
				if dataEntry == nil {
					continue
				}
				rva := binary.LittleEndian.Uint32(dataEntry[0:4])
				size := binary.LittleEndian.Uint32(dataEntry[4:8])
				offset, err := layout.fileOffset(raw, rva, size)
				if err != nil {
					continue
				}
				parsed, err := parseVersionInfo(raw[offset:])
				if err == nil {
					return parsed, nil
				}
			}
		}
	}
	return empty, errors.New("no version resource found")
}

// directoryEntries reports how many entries one resource directory holds.
func directoryEntries(directory []byte) int {
	if len(directory) < 16 {
		return 0
	}
	return int(binary.LittleEndian.Uint16(directory[12:14])) + int(binary.LittleEndian.Uint16(directory[14:16]))
}

// directoryEntryID returns the numeric identifier an entry is filed under. A
// name that is not numeric cannot be the version resource's type.
func directoryEntryID(directory []byte, index int) (uint32, error) {
	entry := 16 + index*8
	if entry+8 > len(directory) {
		return 0, errors.New("truncated resource directory")
	}
	name := binary.LittleEndian.Uint32(directory[entry:])
	if name&0x80000000 != 0 {
		return 0, nil
	}
	return name & 0x7FFFFFFF, nil
}

// directoryEntry reads one entry: the offset it holds, relative to the start of
// the resource directory, and whether that offset names a deeper level.
func directoryEntry(directory []byte, index int) (uint32, bool, error) {
	entry := 16 + index*8
	if entry+8 > len(directory) {
		return 0, false, errors.New("truncated resource directory")
	}
	offset := binary.LittleEndian.Uint32(directory[entry+4:])
	return offset & 0x7FFFFFFF, offset&0x80000000 != 0, nil
}

// subdirectory resolves one entry that points at a deeper resource directory. A
// nil slice means the entry points at data rather than at another level.
//
// The offset is read from the level the entry lives in but resolved against the
// root directory: every offset inside a resource directory is relative to that
// directory's own start, not to the level it was reached from.
func subdirectory(root, level []byte, index int) ([]byte, error) {
	offset, isDirectory, err := directoryEntry(level, index)
	if err != nil || !isDirectory {
		return nil, err
	}
	if int(offset)+16 > len(root) {
		return nil, errors.New("resource directory level is outside the directory")
	}
	return root[offset:], nil
}

// resourceDataEntry resolves one entry of the language level, whose offset
// points at an IMAGE_RESOURCE_DATA_ENTRY. A nil slice means the entry names
// another directory, which the language level never does.
func resourceDataEntry(directory, languages []byte, index int) ([]byte, error) {
	offset, isDirectory, err := directoryEntry(languages, index)
	if err != nil || isDirectory {
		return nil, err
	}
	if int(offset)+16 > len(directory) {
		return nil, errors.New("resource data entry is outside the directory")
	}
	return directory[offset : offset+16], nil
}

// parseVersionInfo parses one VS_VERSIONINFO structure: a 16-bit length, a
// 16-bit value length, a 16-bit type, a UTF-16 key, then padding to a 32-bit
// boundary and the value, which is either fixed file info or an aligned child
// block.
func parseVersionInfo(raw []byte) (executableVersion, error) {
	empty := executableVersion{}
	if len(raw) < 6 {
		return empty, errors.New("truncated version resource")
	}
	totalLength := int(binary.LittleEndian.Uint16(raw[0:2]))
	valueLength := int(binary.LittleEndian.Uint16(raw[2:4]))
	valueType := binary.LittleEndian.Uint16(raw[4:6])
	if totalLength < 6 || totalLength > len(raw) {
		totalLength = len(raw)
	}
	block := raw[:totalLength]
	keyEnd := 6
	for keyEnd+1 < len(block) && !(block[keyEnd] == 0 && block[keyEnd+1] == 0) {
		keyEnd += 2
	}
	keyBytes := keyEnd + 2 - 6
	key := strings.ToUpper(decodeUTF16(block[6 : keyEnd+2]))
	valueStart := alignTo32(6 + keyBytes)
	if valueStart > len(block) {
		return empty, errors.New("truncated version resource")
	}

	parsed := executableVersion{named: map[string]string{}}
	if key == "VS_VERSION_INFO" && valueLength >= versionFixedSize && valueStart+versionFixedSize <= len(block) {
		fixed := block[valueStart:]
		if binary.LittleEndian.Uint32(fixed[0:4]) != fixedSignature {
			return empty, errors.New("version resource has no valid fixed file info")
		}
		// dwFileVersionMS holds the major component in its high word and the
		// minor component in its low word, and dwFileVersionLS holds build and
		// revision the same way. Both words are little-endian, so the high word
		// of each pair is the second one on disk.
		parsed.fixed = true
		parsed.major = binary.LittleEndian.Uint16(fixed[10:12])
		parsed.minor = binary.LittleEndian.Uint16(fixed[8:10])
		parsed.build = binary.LittleEndian.Uint16(fixed[14:16])
		parsed.revision = binary.LittleEndian.Uint16(fixed[12:14])
	}
	// A value is a string only when the block declares one: a StringFileInfo or
	// StringTable block carries no value of its own, only children, and its key
	// is a structure name or a language/codepage identifier rather than a
	// version field.
	valueEnd := valueStart + valueLength
	if valueType == versionTextType && valueLength > 0 && valueStart < len(block) {
		length := textValueLength(block[valueStart:])
		if value := decodeUTF16(block[valueStart : valueStart+length]); value != "" {
			parsed.named[key] = value
		}
		// The declared value length is not always in bytes: NSIS declares the
		// character count, so the terminator, not the declaration, marks where
		// the value ends and where any children would begin.
		valueEnd = valueStart + length
	}
	for _, child := range childBlocks(block, alignTo32(valueEnd)) {
		nested, err := parseVersionInfo(child)
		if err != nil {
			continue
		}
		mergeVersionInfo(&parsed, nested)
	}
	return parsed, nil
}

// textValueLength returns the length of a UTF-16 value including its
// terminator, or the whole slice when the producer wrote no terminator.
func textValueLength(raw []byte) int {
	for index := 0; index+1 < len(raw); index += 2 {
		if raw[index] == 0 && raw[index+1] == 0 {
			return index + 2
		}
	}
	return len(raw)
}

func mergeVersionInfo(destination *executableVersion, source executableVersion) {
	if source.fixed {
		destination.fixed = true
		destination.major = source.major
		destination.minor = source.minor
		destination.build = source.build
		destination.revision = source.revision
	}
	if destination.named == nil {
		destination.named = map[string]string{}
	}
	for key, value := range source.named {
		destination.named[key] = value
	}
}

// childBlocks returns the sequence of child structures that follows a block,
// stopping at the first one that does not fit inside the parent.
func childBlocks(block []byte, start int) [][]byte {
	var children [][]byte
	for index := start; index+6 <= len(block); {
		length := int(binary.LittleEndian.Uint16(block[index : index+2]))
		if length < 6 || index+length > len(block) {
			break
		}
		children = append(children, block[index:index+length])
		index += alignTo32(length)
	}
	return children
}

func alignTo32(value int) int { return (value + 3) &^ 3 }

func decodeUTF16(raw []byte) string {
	characters := make([]uint16, 0, len(raw)/2)
	for index := 0; index+1 < len(raw); index += 2 {
		character := binary.LittleEndian.Uint16(raw[index : index+2])
		if character == 0 {
			break
		}
		characters = append(characters, character)
	}
	return strings.TrimSpace(string(utf16.Decode(characters)))
}
