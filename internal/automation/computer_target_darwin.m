//go:build darwin && cgo

#import <Foundation/Foundation.h>
#import <CoreGraphics/CoreGraphics.h>
#import <Security/Security.h>
#include <libproc.h>
#include <sys/proc.h>
#include <sys/proc_info.h>
#include <unistd.h>
#include "computer_target_darwin.h"

static int process_info(uint32_t pid, struct proc_bsdinfo *result) {
    int size;
    do {
        size = proc_pidinfo((int)pid, PROC_PIDTBSDINFO, 0, result, sizeof(*result));
    } while (size != sizeof(*result) && errno == EINTR);
    return size == sizeof(*result) && result->pbi_status != SZOMB;
}

static uint64_t process_start(struct proc_bsdinfo p) {
    return p.pbi_start_tvsec * 1000000ULL + p.pbi_start_tvusec;
}

static int own_process(struct proc_bsdinfo process) {
    uint32_t self = (uint32_t)getpid();
    for (int depth = 0; depth < 64; ++depth) {
        if (process.pbi_pid == self) return 1;
        if (process.pbi_ppid <= 1) return 0;
        struct proc_bsdinfo parent = {0};
        if (!process_info(process.pbi_ppid, &parent) || process_start(parent) > process_start(process)) return 1;
        process = parent;
    }
    return 1;
}

static NSDictionary *window_info(uint32_t pid, uint32_t window, CGRect *bounds) {
    NSArray *windows = [(NSArray *)CGWindowListCopyWindowInfo(kCGWindowListOptionIncludingWindow, window) autorelease];
    for (NSDictionary *item in windows) {
        NSNumber *number = item[(NSString *)kCGWindowNumber];
        NSNumber *owner = item[(NSString *)kCGWindowOwnerPID];
        NSNumber *layer = item[(NSString *)kCGWindowLayer];
        NSNumber *onscreen = item[(NSString *)kCGWindowIsOnscreen];
        NSDictionary *rectangle = item[(NSString *)kCGWindowBounds];
        // The exact-ID query can still describe a just-closed backing window.
        // Match the gateway's on-screen discovery policy at inspection time.
        if ([number unsignedIntValue] != window || [owner unsignedIntValue] != pid ||
            ![onscreen boolValue] || !layer || [layer integerValue] != 0 || !rectangle) continue;
        if (!CGRectMakeWithDictionaryRepresentation((CFDictionaryRef)rectangle, bounds) ||
            CGRectIsEmpty(*bounds) || CGRectIsInfinite(*bounds)) return nil;
        return item;
    }
    return nil;
}

static void copy_text(NSString *text, char *field, size_t capacity) {
    if ([text isKindOfClass:NSString.class]) {
        if (![text getCString:field maxLength:capacity encoding:NSUTF8StringEncoding]) field[0] = 0;
    }
}

static void signing_identity(uint32_t pid, dsh_computer_target *target) {
    SecCodeRef code = NULL;
    NSDictionary *attributes = @{(NSString *)kSecGuestAttributePid: @(pid)};
    if (SecCodeCopyGuestWithAttributes(NULL, (CFDictionaryRef)attributes, kSecCSDefaultFlags, &code) != errSecSuccess) return;
    SecRequirementRef requirement = NULL;
    if (SecRequirementCreateWithString(CFSTR("anchor apple generic"), kSecCSDefaultFlags, &requirement) == errSecSuccess &&
        SecCodeCheckValidity(code, kSecCSDefaultFlags, requirement) == errSecSuccess) {
        CFDictionaryRef info = NULL;
        if (SecCodeCopySigningInformation(code, kSecCSSigningInformation, &info) == errSecSuccess && info) {
            NSDictionary *data = (NSDictionary *)info;
            copy_text(data[(NSString *)kSecCodeInfoIdentifier], target->identifier, sizeof(target->identifier));
            copy_text(data[(NSString *)kSecCodeInfoTeamIdentifier], target->team, sizeof(target->team));
            CFRelease(info);
        }
    }
    if (requirement) CFRelease(requirement);
    CFRelease(code);
}

int dsh_inspect_computer_target(uint32_t pid, uint32_t window, dsh_computer_target *target) {
    @autoreleasepool {
        struct proc_bsdinfo before = {0}, after = {0};
        if (!process_info(pid, &before)) return 0;
        if (before.pbi_uid == 0 || before.pbi_uid != geteuid() || before.pbi_ruid != geteuid()) return -1;
        CGRect bounds, current;
        NSDictionary *item = window_info(pid, window, &bounds);
        if (!item || proc_pidpath((int)pid, target->executable, sizeof(target->executable)) <= 0) return 0;
        NSString *title = item[(NSString *)kCGWindowName];
        copy_text(title, target->title, sizeof(target->title));
        copy_text(item[(NSString *)kCGWindowOwnerName], target->app, sizeof(target->app));
        target->owned = own_process(before);
        signing_identity(pid, target);
        NSDictionary *latest = window_info(pid, window, &current);
        NSString *currentTitle = latest[(NSString *)kCGWindowName];
        char executable[sizeof(target->executable)] = {0};
        if (!latest || !CGRectEqualToRect(bounds, current) ||
            (title != currentTitle && ![title isEqualToString:currentTitle]) ||
            !process_info(pid, &after) || process_start(before) != process_start(after) ||
            before.pbi_uid != after.pbi_uid || before.pbi_ruid != after.pbi_ruid ||
            proc_pidpath((int)pid, executable, sizeof(executable)) <= 0 ||
            strcmp(target->executable, executable) != 0) return 0;
        target->started = process_start(before);
        target->x = bounds.origin.x;
        target->y = bounds.origin.y;
        target->width = bounds.size.width;
        target->height = bounds.size.height;
        return target->started ? 1 : 0;
    }
}
