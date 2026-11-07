// Wails-side live OSR presentation. No CEF library or engine in this process.
// CEF's public CPU OSR API lives exclusively in the broker application.
#include "osr_surface.h"
#import <Cocoa/Cocoa.h>
#include <algorithm>
#include <array>
#include <atomic>
#include <csignal>
#include <cstdio>
#include <cstring>
#include <mach-o/dyld.h>
#include <mutex>
#include <spawn.h>
#include <sstream>
#include <string>
#include <sys/wait.h>
#include <thread>
extern char **environ;
#define LAB_EXPORT extern "C" __attribute__((visibility("default")))
static NSWindow *main_window, *float_window;
static std::array<lab_osr::Surface, 2> surfaces;
static std::array<std::string, 2> surface_paths, last_geometry;
static std::array<std::atomic<int>, 2> browser_ids{};
static std::array<std::atomic<bool>, 2> closed{};
static std::atomic<bool> alive{false}, shutdown_returned{false},
    exited_cleanly{false};
static pid_t broker_pid = 0;
static int command_fd = -1;
static std::thread reader;
static std::string cache_path, helper_path, broker_path, log_path, last_error;
static bool floating = false, covered = false;
static std::array<bool, 2> faults{};
static int selected = 0;
static NSTimer *frame_timer;
static bool Send(NSDictionary *command) {
  if (!alive || command_fd < 0)
    return false;
  NSData *data =
      [NSJSONSerialization dataWithJSONObject:command options:0 error:nullptr];
  if (!data || data.length > 65535)
    return false;
  std::string line((const char *)data.bytes, data.length);
  line += '\n';
  // All sends occur on the host UI thread, below PIPE_BUF for bounded inputs.
  const char *p = line.data();
  size_t left = line.size();
  while (left) {
    ssize_t n = write(command_fd, p, left);
    if (n <= 0)
      return false;
    p += n;
    left -= n;
  }
  return true;
}
static uint32_t Modifiers(NSEvent *event) {
  uint32_t m = 0;
  auto f = event.modifierFlags;
  if (f & NSEventModifierFlagCapsLock)
    m |= 1;
  if (f & NSEventModifierFlagShift)
    m |= 2;
  if (f & NSEventModifierFlagControl)
    m |= 4;
  if (f & NSEventModifierFlagOption)
    m |= 8;
  if (f & NSEventModifierFlagCommand)
    m |= 128;
  if (NSEvent.pressedMouseButtons & 1)
    m |= 16;
  if (NSEvent.pressedMouseButtons & 2)
    m |= 64;
  return m;
}
@interface BrowserLabFrameView : NSView <NSTextInputClient> {
@public
  int index_;
@public
  uint64_t presented_;
  CGImageRef image_;
  NSString *marked_;
  NSRange selection_;
  NSRect caret_;
}
- (instancetype)initWithIndex:(int)index;
- (void)refreshFrame;
- (void)resetFrame;
@end
static std::array<BrowserLabFrameView *, 2> views{};
@implementation BrowserLabFrameView
- (void)dealloc {
  if (image_)
    CGImageRelease(image_);
  [marked_ release];
  [super dealloc];
}
- (instancetype)initWithIndex:(int)index {
  if ((self = [super initWithFrame:NSMakeRect(240, 20, 890, 680)])) {
    index_ = index;
    marked_ = [@"" copy];
    selection_ = NSMakeRange(0, 0);
    caret_ = NSMakeRect(0, 0, 1, 18);
  }
  return self;
}
- (BOOL)isFlipped {
  return YES;
}
- (BOOL)acceptsFirstResponder {
  return YES;
}
- (BOOL)becomeFirstResponder {
  Send(@{@"op" : @"focus", @"index" : @(index_), @"focused" : @YES});
  return YES;
}
- (BOOL)resignFirstResponder {
  Send(@{@"op" : @"focus", @"index" : @(index_), @"focused" : @NO});
  return YES;
}
- (void)resetFrame {
  if (image_)
    CGImageRelease(image_);
  image_ = nullptr;
  presented_ = 0;
  [self setNeedsDisplay:YES];
}
- (void)refreshFrame {
  if (!alive || !browser_ids[index_] || self.hiddenOrHasHiddenAncestor)
    return;
  auto &surface = surfaces[index_];
  if (surface.fd < 0 || flock(surface.fd, LOCK_SH | LOCK_NB))
    return;
  auto h = *surface.header();
  NSData *bytes = nil;
  if (h.sequence != presented_ && h.width > 0 && h.height > 0 &&
      h.width <= lab_osr::kMaxWidth && h.height <= lab_osr::kMaxHeight)
    bytes = [NSData dataWithBytes:surface.pixels()
                           length:size_t(h.width) * h.height * 4];
  flock(surface.fd, LOCK_UN);
  if (!bytes)
    return;
  CGDataProviderRef provider = CGDataProviderCreateWithCFData((CFDataRef)bytes);
  CGColorSpaceRef color = CGColorSpaceCreateDeviceRGB();
  CGImageRef image = CGImageCreate(
      h.width, h.height, 8, 32, size_t(h.width) * 4, color,
      static_cast<CGBitmapInfo>(kCGBitmapByteOrder32Little) |
          static_cast<CGBitmapInfo>(kCGImageAlphaPremultipliedFirst), provider,
      nullptr, false, kCGRenderingIntentDefault);
  CGColorSpaceRelease(color);
  CGDataProviderRelease(provider);
  if (!image)
    return;
  if (image_)
    CGImageRelease(image_);
  image_ = image;
  presented_ = h.sequence;
  [self setNeedsDisplay:YES];
}
- (void)drawRect:(NSRect)rect {
  [[NSColor blackColor] setFill];
  NSRectFill(rect);
  if (image_) {
    NSImage *image =
        [[NSImage alloc] initWithCGImage:image_ size:self.bounds.size];
    [image drawInRect:self.bounds
              fromRect:NSZeroRect
             operation:NSCompositingOperationCopy
              fraction:1
        respectFlipped:YES
                 hints:nil];
    [image release];
  }
}
- (void)mouse:(NSEvent *)event kind:(NSString *)kind button:(int)button {
  NSPoint p = [self convertPoint:event.locationInWindow fromView:nil];
  Send(@{
    @"op" : @"mouse",
    @"index" : @(index_),
    @"kind" : kind,
    @"x" : @(int(p.x)),
    @"y" : @(int(p.y)),
    @"modifiers" : @(Modifiers(event)),
    @"button" : @(button),
    @"count" : @(event.clickCount),
    @"dx" : @(int(event.scrollingDeltaX * 20)),
    @"dy" : @(int(event.scrollingDeltaY * 20))
  });
}
- (void)mouseDown:(NSEvent *)e {
  [self.window makeFirstResponder:self];
  [self mouse:e kind:@"down" button:0];
}
- (void)mouseUp:(NSEvent *)e {
  [self mouse:e kind:@"up" button:0];
}
- (void)rightMouseDown:(NSEvent *)e {
  [self.window makeFirstResponder:self];
  [self mouse:e kind:@"down" button:1];
}
- (void)rightMouseUp:(NSEvent *)e {
  [self mouse:e kind:@"up" button:1];
}
- (void)mouseMoved:(NSEvent *)e {
  [self mouse:e kind:@"move" button:0];
}
- (void)mouseDragged:(NSEvent *)e {
  [self mouse:e kind:@"move" button:0];
}
- (void)scrollWheel:(NSEvent *)e {
  [self mouse:e kind:@"wheel" button:0];
}
- (void)key:(NSEvent *)e type:(int)type {
  NSString *chars = e.charactersIgnoringModifiers ?: @"";
  unichar c = chars.length ? [chars characterAtIndex:0] : 0;
  int vk = c >= 'a' && c <= 'z' ? c - 32 : c;
  Send(@{
    @"op" : @"key",
    @"index" : @(index_),
    @"type" : @(type),
    @"vk" : @(vk),
    @"native" : @(e.keyCode),
    @"character" : @(c),
    @"unmodified" : @(c),
    @"modifiers" : @(Modifiers(e))
  });
}
- (void)keyDown:(NSEvent *)e {
  [self key:e type:0];
  [self interpretKeyEvents:@[ e ]];
}
- (void)keyUp:(NSEvent *)e {
  [self key:e type:2];
}
- (void)insertText:(id)value replacementRange:(NSRange)range {
  NSString *text =
      [value isKindOfClass:[NSAttributedString class]] ? [value string] : value;
  Send(@{@"op" : @"text", @"index" : @(index_), @"text" : text ?: @""});
  [marked_ release];
  marked_ = [@"" copy];
}
- (void)setMarkedText:(id)value
        selectedRange:(NSRange)range
     replacementRange:(NSRange)replacement {
  NSString *text =
      [value isKindOfClass:[NSAttributedString class]] ? [value string] : value;
  [marked_ release];
  marked_ = [text copy];
  selection_ = range;
  Send(@{
    @"op" : @"composition",
    @"index" : @(index_),
    @"text" : text ?: @"",
    @"from" : @(range.location),
    @"to" : @(NSMaxRange(range))
  });
}
- (void)unmarkText {
  [marked_ release];
  marked_ = [@"" copy];
  Send(@{@"op" : @"cancel-composition", @"index" : @(index_)});
}
- (BOOL)hasMarkedText {
  return marked_.length > 0;
}
- (NSRange)markedRange {
  return marked_.length ? NSMakeRange(0, marked_.length)
                        : NSMakeRange(NSNotFound, 0);
}
- (NSRange)selectedRange {
  return selection_;
}
- (NSArray<NSAttributedStringKey> *)validAttributesForMarkedText {
  return @[];
}
- (NSAttributedString *)attributedSubstringForProposedRange:(NSRange)r
                                                actualRange:
                                                    (NSRangePointer)actual {
  if (actual)
    *actual = NSMakeRange(NSNotFound, 0);
  return nil;
}
- (NSUInteger)characterIndexForPoint:(NSPoint)p {
  return NSNotFound;
}
- (NSRect)firstRectForCharacterRange:(NSRange)r
                         actualRange:(NSRangePointer)actual {
  if (actual)
    *actual = selection_;
  return [self.window convertRectToScreen:[self convertRect:caret_ toView:nil]];
}
- (void)doCommandBySelector:(SEL)selector {
  int vk = 0;
  if (selector == @selector(deleteBackward:))
    vk = 8;
  else if (selector == @selector(insertNewline:))
    vk = 13;
  else if (selector == @selector(insertTab:))
    vk = 9;
  if (vk)
    Send(@{
      @"op" : @"key",
      @"index" : @(index_),
      @"type" : @3,
      @"vk" : @(vk),
      @"character" : @(vk),
      @"native" : @0,
      @"modifiers" : @0
    });
}
- (void)setIme:(NSDictionary *)d {
  selection_ =
      NSMakeRange([d[@"from"] unsignedIntegerValue],
                  std::max(0, [d[@"to"] intValue] - [d[@"from"] intValue]));
  if (d[@"x"])
    caret_ = NSMakeRect([d[@"x"] doubleValue], [d[@"y"] doubleValue],
                        std::max(1, [d[@"width"] intValue]),
                        std::max(18, [d[@"height"] intValue]));
}
@end
static void Layout() {
  for (int i = 0; i < 2; i++) {
    auto view = views[i];
    NSWindow *owner = floating && i == selected ? float_window : main_window;
    if (!view || !owner)
      continue;
    if (view.superview != owner.contentView) {
      [view removeFromSuperview];
      [owner.contentView addSubview:view
                         positioned:NSWindowAbove
                         relativeTo:nil];
    }
    CGFloat left = owner == main_window ? 240 : 20;
    view.frame = NSMakeRect(
        left, 20,
        std::max<CGFloat>(100, owner.contentView.bounds.size.width - left - 20),
        std::max<CGFloat>(100, owner.contentView.bounds.size.height - 120));
    view.hidden = !alive || !browser_ids[i] || closed[i] || i != selected ||
                  covered || faults[i];
    NSRect screen =
        [owner convertRectToScreen:[view convertRect:view.bounds toView:nil]];
    NSDictionary *g = @{
      @"op" : @"geometry",
      @"index" : @(i),
      @"width" : @(int(view.bounds.size.width)),
      @"height" : @(int(view.bounds.size.height)),
      @"scale" : @(owner.backingScaleFactor),
      @"x" : @(int(screen.origin.x)),
      @"y" : @(int(NSMaxY(owner.screen.frame) - NSMaxY(screen))),
      @"hidden" : @(bool(view.hidden))
    };
    NSData *json =
        [NSJSONSerialization dataWithJSONObject:g
                                        options:NSJSONWritingSortedKeys
                                          error:nullptr];
    std::string key((const char *)json.bytes, json.length);
    if (alive && key != last_geometry[i] && Send(g))
      last_geometry[i] = key;
    [view refreshFrame];
  }
}
static bool Spawn() {
  if (reader.joinable())
    reader.join();
  if (command_fd >= 0) {
    close(command_fd);
    command_fd = -1;
  }
  for (int i = 0; i < 2; i++) {
    browser_ids[i] = 0;
    closed[i] = false;
    faults[i] = false;
    last_geometry[i].clear();
    *surfaces[i].header() = lab_osr::Header{};
    [views[i] resetFrame];
  }
  shutdown_returned = false;
  exited_cleanly = false;
  covered = false;
  floating = false;
  int input[2], output[2];
  if (pipe(input) || pipe(output)) {
    last_error = "broker pipe failed";
    return false;
  }
  posix_spawn_file_actions_t a;
  posix_spawn_file_actions_init(&a);
  posix_spawn_file_actions_adddup2(&a, input[0], STDIN_FILENO);
  posix_spawn_file_actions_adddup2(&a, output[1], STDOUT_FILENO);
  for (int fd : {input[0], input[1], output[0], output[1]})
    posix_spawn_file_actions_addclose(&a, fd);
  std::string cache = "--cache-path=" + cache_path,
              helper = "--helper-path=" + helper_path,
              log = "--log-file=" + log_path,
              sa = "--surface-a=" + surface_paths[0],
              sb = "--surface-b=" + surface_paths[1];
  char *argv[] = {broker_path.data(), cache.data(), helper.data(), log.data(),
                  sa.data(),          sb.data(),    nullptr};
  int status =
      posix_spawn(&broker_pid, broker_path.c_str(), &a, nullptr, argv, environ);
  posix_spawn_file_actions_destroy(&a);
  close(input[0]);
  close(output[1]);
  if (status) {
    close(input[1]);
    close(output[0]);
    last_error = "broker spawn failed";
    return false;
  }
  command_fd = input[1];
  alive = true;
  reader = std::thread([pid = broker_pid, fd = output[0]] {
    FILE *f = fdopen(fd, "r");
    char *line = nullptr;
    size_t size = 0;
    while (getline(&line, &size, f) > 0) {
      @autoreleasepool {
        NSData *data = [NSData dataWithBytes:line length:strlen(line)];
        NSDictionary *d = [NSJSONSerialization JSONObjectWithData:data
                                                          options:0
                                                            error:nullptr];
        if (![d isKindOfClass:[NSDictionary class]])
          continue;
        NSString *event = d[@"event"];
        int i = [d[@"index"] intValue];
        if (i < 0 || i > 1)
          continue;
        if ([event isEqual:@"created"])
          browser_ids[i] = [d[@"id"] intValue];
        else if ([event isEqual:@"closed"])
          closed[i] = true;
        else if ([event isEqual:@"shutdown"])
          shutdown_returned = true;
        else if ([event isEqual:@"ime"])
          dispatch_async(dispatch_get_main_queue(), ^{
            [views[i] setIme:d];
          });
        fprintf(stderr, "Browser Lab broker: %s", line);
      }
    }
    free(line);
    fclose(f);
    int status = 0;
    if (waitpid(pid, &status, 0) == pid)
      exited_cleanly = WIFEXITED(status) && WEXITSTATUS(status) == 0;
    alive = false;
  });
  Layout();
  return true;
}
LAB_EXPORT int lab_init(const char *cache, const char *helper,
                        const char *log) {
  cache_path = cache;
  helper_path = helper;
  log_path = log;
  NSString *helperString = [NSString stringWithUTF8String:helper];
  NSString *framework = [[[[helperString stringByDeletingLastPathComponent]
      stringByDeletingLastPathComponent] stringByDeletingLastPathComponent]
      stringByDeletingLastPathComponent];
  // .../broker.app/Contents/Frameworks/helper.app/Contents/MacOS/helper
  broker_path = [[[framework stringByDeletingLastPathComponent]
      stringByAppendingPathComponent:@"MacOS/cefsimple"] UTF8String];
  for (int i = 0; i < 2; i++) {
    surface_paths[i] = cache_path + "-surface-" + std::to_string(i);
    if (!surfaces[i].Open(surface_paths[i].c_str(), true)) {
      last_error = "private OSR surface allocation failed";
      return 0;
    }
  }
  return 1;
}
LAB_EXPORT int lab_create(void *native, const char *) {
  main_window = (NSWindow *)native;
  for (int i = 0; i < 2; i++) {
    views[i] = [[BrowserLabFrameView alloc] initWithIndex:i];
    [main_window.contentView addSubview:views[i]
                             positioned:NSWindowAbove
                             relativeTo:nil];
  }
  frame_timer = [NSTimer timerWithTimeInterval:1.0 / 30
                                       repeats:YES
                                         block:^(NSTimer *) {
                                           Layout();
                                         }];
  [[NSRunLoop mainRunLoop] addTimer:frame_timer forMode:NSRunLoopCommonModes];
  return Spawn();
}
LAB_EXPORT int lab_float_parent(void *native) {
  float_window = (NSWindow *)native;
  return float_window != nil;
}
LAB_EXPORT int lab_command(const char *command, int index) {
  std::string op(command);
  if (op == "tab") {
    selected = index;
    floating = false;
  } else if (op == "float") {
    if (!float_window)
      return 0;
    floating = true;
  } else if (op == "dock")
    floating = false;
  else if (op == "cover")
    covered = true;
  else if (op == "uncover")
    covered = false;
  else if (op == "renderer-fault")
    faults[index] = true;
  else if (op == "renderer-recovered")
    faults[index] = false;
  else if (op == "crash-broker") {
    return alive && kill(broker_pid, SIGKILL) == 0;
  } else if (op == "restart-broker") {
    if (alive)
      return 0;
    return Spawn();
  } else if (op == "close-broker") {
    return Send(@{@"op" : @"close", @"index" : @0});
  } else if (op == "focus-guest") {
    if (covered || faults[selected] || !alive)
      return 0;
    NSWindow *w = floating ? float_window : main_window;
    [w makeKeyAndOrderFront:nil];
    [w makeFirstResponder:views[selected]];
  } else if (op == "focus-host") {
    [main_window makeKeyAndOrderFront:nil];
  } else if (op == "native-key") {
    NSWindow *w = floating ? float_window : main_window;
    NSEvent *e =
        [NSEvent keyEventWithType:NSEventTypeKeyDown
                               location:NSZeroPoint
                          modifierFlags:0
                              timestamp:NSProcessInfo.processInfo.systemUptime
                           windowNumber:w.windowNumber
                                context:nil
                             characters:@"r"
            charactersIgnoringModifiers:@"r"
                              isARepeat:NO
                                keyCode:15];
    [w sendEvent:e];
  } else if (op == "stop-host-loop") {
    if (alive || !shutdown_returned || !exited_cleanly)
      return 0;
    [frame_timer invalidate];
    [NSApp stop:nil];
    [NSApp postEvent:[NSEvent otherEventWithType:NSEventTypeApplicationDefined
                                        location:NSZeroPoint
                                   modifierFlags:0
                                       timestamp:0
                                    windowNumber:0
                                         context:nil
                                         subtype:0
                                           data1:0
                                           data2:0]
             atStart:NO];
    return 1;
  } else if (op != "layout")
    return 0;
  Layout();
  return 1;
}
LAB_EXPORT const char *lab_state() {
  static thread_local std::string result;
  Layout();
  auto main = main_window ? main_window.windowNumber : 0,
       fl = float_window ? float_window.windowNumber : 0;
  auto dpi = [](NSWindow *w) { return w ? int(w.backingScaleFactor * 96) : 0; };
  NSWindow *active = floating ? float_window : main_window;
  NSResponder *responder = active.firstResponder;
  bool inGuest = responder == views[selected];
  NSView *fv =
      [responder isKindOfClass:[NSView class]] ? (NSView *)responder : nil;
  std::ostringstream s;
  int cefImages = 0;
  for (uint32_t i = 0; i < _dyld_image_count(); ++i) {
    const char *name = _dyld_get_image_name(i);
    if (name && strstr(name, "Chromium Embedded Framework"))
      ++cefImages;
  }
  s << "{\"initialized\":true,\"created\":"
    << ((browser_ids[0] ? 1 : 0) + (browser_ids[1] ? 1 : 0))
    << ",\"closed\":" << ((closed[0] ? 1 : 0) + (closed[1] ? 1 : 0))
    << ",\"rendering\":\"cef-osr-shared-frame\",\"cefImagesInHost\":"
    << cefImages << ",\"cefInHost\":" << (cefImages ? "true" : "false")
    << ",\"parentAttached\":"
    << (views[0] && views[0].window == main_window ? "true" : "false")
    << ",\"windowId\":\"" << main << "\",\"main\":{\"hwnd\":" << main
    << ",\"windowId\":\"" << main << "\",\"dpi\":" << dpi(main_window)
    << "},\"float\":{\"hwnd\":" << fl << ",\"windowId\":\"" << fl
    << "\",\"dpi\":" << dpi(float_window)
    << ",\"visible\":" << (float_window.visible ? "true" : "false")
    << "},\"guestRunning\":" << (alive ? "true" : "false")
    << ",\"guestPID\":" << broker_pid << ",\"hostPID\":" << getpid()
    << ",\"selected\":\"" << (selected ? "B" : "A")
    << "\",\"floating\":" << (floating ? "true" : "false")
    << ",\"covered\":" << (covered ? "true" : "false") << ",\"tabs\":[";
  for (int i = 0; i < 2; i++) {
    if (i)
      s << ",";
    auto v = views[i];
    auto w = v.window;
    s << "{\"name\":\"" << (i ? "B" : "A")
      << "\",\"hwnd\":" << (browser_ids[i] ? (uintptr_t)v : 0)
      << ",\"cefNativeHandle\":0,\"browserID\":" << browser_ids[i]
      << ",\"presentedSequence\":" << (v ? v->presented_ : 0)
      << ",\"parentHWND\":" << (w ? w.windowNumber : 0) << ",\"visible\":"
      << (v && w.visible && !v.hiddenOrHasHiddenAncestor ? "true" : "false")
      << ",\"dpi\":" << dpi(w)
      << ",\"fault\":" << (faults[i] ? "true" : "false") << "}";
  }
  s << "],\"guestTopLevelWindows\":[],\"nativeFocus\":{\"inGuest\":"
    << (inGuest ? "true" : "false") << ",\"inHost\":"
    << (fv && [fv isDescendantOf:main_window.contentView] && !inGuest ? "true"
                                                                      : "false")
    << "}}";
  result = s.str();
  return result.c_str();
}
LAB_EXPORT void lab_close() {
  Send(@{@"op" : @"close", @"index" : @0});
}
LAB_EXPORT int lab_closed() {
  return closed[0] && closed[1] && shutdown_returned && exited_cleanly &&
         !alive;
}
LAB_EXPORT void lab_shutdown() {
  [frame_timer invalidate];
  if (reader.joinable())
    reader.join();
  if (command_fd >= 0)
    close(command_fd);
  for (int i = 0; i < 2; i++) {
    [views[i] removeFromSuperview];
    [views[i] release];
    views[i] = nil;
    surfaces[i].Close();
    unlink(surface_paths[i].c_str());
  }
}
LAB_EXPORT const char *lab_error() { return last_error.c_str(); }
