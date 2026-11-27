//go:build darwin && cgo

package downloads

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation -framework CoreServices
#import <Foundation/Foundation.h>
#import <CoreServices/CoreServices.h>
#include <stdlib.h>

static int dsh_quarantine_download(const char *path, const char *source, const char *referrer) {
    @autoreleasepool {
        NSURL *file = [NSURL fileURLWithFileSystemRepresentation:path isDirectory:NO relativeToURL:nil];
        NSMutableDictionary *properties = [NSMutableDictionary dictionary];
        properties[(NSString *)kLSQuarantineAgentNameKey] = @"DSH Work";
        properties[(NSString *)kLSQuarantineTypeKey] = (NSString *)kLSQuarantineTypeWebDownload;
        if (source && *source) {
            properties[(NSString *)kLSQuarantineDataURLKey] = [NSURL URLWithString:[NSString stringWithUTF8String:source]];
        }
        if (referrer && *referrer) {
            properties[(NSString *)kLSQuarantineOriginURLKey] = [NSURL URLWithString:[NSString stringWithUTF8String:referrer]];
        }
        return [file setResourceValue:properties forKey:NSURLQuarantinePropertiesKey error:nil] ? 1 : 0;
    }
}
*/
import "C"

import (
	"errors"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/unix"
)

// CheckRisk applies the macOS web-download quarantine to the verified staged
// file. The caller has already collected consent for any content risk.
func (d *Destination) CheckRisk(source, referrer string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.stage == nil || d.record.Published {
		return ErrClosed
	}
	if err := d.checkMacNames(); err != nil {
		return err
	}
	// Persist origins only. A download URL can contain a private path or token.
	dataOrigin, err := origin(source)
	if err != nil {
		return ErrRisk
	}
	referrerOrigin, _ := origin(referrer)
	path := C.CString(filepath.Join(d.record.Parent, d.record.Stage))
	data := C.CString(dataOrigin)
	from := C.CString(referrerOrigin)
	defer C.free(unsafe.Pointer(path))
	defer C.free(unsafe.Pointer(data))
	defer C.free(unsafe.Pointer(from))
	if C.dsh_quarantine_download(path, data, from) != 1 {
		return ErrRisk
	}
	if err := d.checkMacNames(); err != nil {
		return err
	}
	// Check the held inode. A successful path call alone is insufficient.
	n, err := unix.Fgetxattr(int(d.stage.Fd()), "com.apple.quarantine", nil)
	if err != nil || n <= 0 {
		return errors.Join(ErrRisk, err)
	}
	return d.stage.Sync()
}
