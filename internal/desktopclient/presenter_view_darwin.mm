//go:build darwin && cgo && !server

#import <Cocoa/Cocoa.h>
#import <QuartzCore/QuartzCore.h>
#include "presenter_view_darwin.h"
#include <sys/file.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <fcntl.h>
#include <unistd.h>
#include <cmath>
#include <algorithm>
extern "C" void goMacPresenterInput(uintptr_t, char*, int);

namespace {
// Matches the private component frame header, never a public page protocol.
struct FrameHeader { uint64_t magic, sequence, capacity; uint32_t width,height; };
constexpr uint64_t kFrameMagic=0x4453484d41434631ULL;
constexpr size_t kFrameBudget=256U*1024U*1024U;
NSMutableDictionary<NSNumber*,NSView*>* views;
void OnMain(dispatch_block_t call){if([NSThread isMainThread])call();else dispatch_sync(dispatch_get_main_queue(),call);}
int Modifiers(NSEvent* event){int v=0;auto f=event.modifierFlags;if(f&NSEventModifierFlagCapsLock)v|=1;if(f&NSEventModifierFlagShift)v|=2;if(f&NSEventModifierFlagControl)v|=4;if(f&NSEventModifierFlagOption)v|=8;if(f&NSEventModifierFlagCommand)v|=128;return v;}
void Input(uintptr_t handle,NSDictionary* event){NSData* data=[NSJSONSerialization dataWithJSONObject:event options:0 error:nil];if(data&&data.length<=16384)goMacPresenterInput(handle,(char*)data.bytes,(int)data.length);}
}

@interface DSHCEFView : NSView <NSTextInputClient> {
@public
 uintptr_t handle_;
 int fd_;
 void* memory_;
 size_t size_;
 uint64_t sequence_;
 NSTimer* timer_;
 NSTrackingArea* tracking_;
 NSString* marked_;
 NSRange selection_;
 NSRect caret_;
 BOOL retired_,has_visibility_,last_visibility_;
 NSMutableArray* visibility_observers_;
}
-(BOOL)openFrame:(const char*)path size:(size_t)size handle:(uintptr_t)handle;
-(void)retire;
-(void)refresh;
-(BOOL)actualVisible;
-(void)publishVisibility;
@end
@implementation DSHCEFView
-(BOOL)actualVisible{return !retired_&&!self.hiddenOrHasHiddenAncestor&&self.window&&self.window.visible&&!self.window.miniaturized&&!NSApp.hidden&&(self.window.occlusionState&NSWindowOcclusionStateVisible)!=0;}
-(void)publishVisibility {if(!handle_||retired_)return;BOOL value=[self actualVisible];if(!has_visibility_||value!=last_visibility_){has_visibility_=YES;last_visibility_=value;Input(handle_,@{@"op":@"visibility",@"visible":@(value)});}}
-(void)viewDidHide{[super viewDidHide];[self publishVisibility];}
-(void)viewDidUnhide{[super viewDidUnhide];[self publishVisibility];}
-(BOOL)isFlipped{return YES;}
-(BOOL)acceptsFirstResponder{return !retired_&&!self.hidden;}
-(BOOL)becomeFirstResponder{Input(handle_,@{@"op":@"focus",@"focused":@YES});return YES;}
-(BOOL)resignFirstResponder{if(!retired_)Input(handle_,@{@"op":@"focus",@"focused":@NO});return YES;}
-(BOOL)openFrame:(const char*)path size:(size_t)size handle:(uintptr_t)handle {
 fd_=-1;memory_=nullptr;handle_=handle;
 if(size<sizeof(FrameHeader)||size>sizeof(FrameHeader)+kFrameBudget)return NO;
 fd_=open(path,O_RDONLY|O_NOFOLLOW|O_CLOEXEC);struct stat st{};
 if(fd_<0||fstat(fd_,&st)||!S_ISREG(st.st_mode)||st.st_uid!=getuid()||(st.st_mode&0777)!=0600||st.st_size!=(off_t)size){if(fd_>=0)close(fd_);fd_=-1;return NO;}
 memory_=mmap(nullptr,size,PROT_READ,MAP_SHARED,fd_,0);if(memory_==MAP_FAILED){memory_=nullptr;close(fd_);fd_=-1;return NO;}size_=size;
 self.wantsLayer=YES;self.layer.contentsGravity=kCAGravityResize;self.layer.backgroundColor=NSColor.whiteColor.CGColor;
 marked_=[@"" copy];selection_=NSMakeRange(0,0);caret_=NSMakeRect(0,0,1,18);
 visibility_observers_=[[NSMutableArray alloc]init];
 for(NSString* name in @[NSWindowDidMiniaturizeNotification,NSWindowDidDeminiaturizeNotification,NSWindowDidChangeOcclusionStateNotification,NSApplicationDidHideNotification,NSApplicationDidUnhideNotification]){
  id observer=[[NSNotificationCenter defaultCenter]addObserverForName:name object:nil queue:nil usingBlock:^(NSNotification*){[self publishVisibility];}];[visibility_observers_ addObject:observer];
 }
 timer_=[[NSTimer timerWithTimeInterval:1.0/30.0 target:self selector:@selector(refresh) userInfo:nil repeats:YES] retain];[[NSRunLoop mainRunLoop]addTimer:timer_ forMode:NSRunLoopCommonModes];
 return YES;
}
-(void)updateTrackingAreas {
 if(tracking_){[self removeTrackingArea:tracking_];[tracking_ release];}
 tracking_=[[NSTrackingArea alloc]initWithRect:NSZeroRect options:NSTrackingMouseMoved|NSTrackingMouseEnteredAndExited|NSTrackingActiveInKeyWindow|NSTrackingInVisibleRect owner:self userInfo:nil];[self addTrackingArea:tracking_];[super updateTrackingAreas];
}
-(void)refresh {
 [self publishVisibility];
 if(retired_||self.hiddenOrHasHiddenAncestor||!self.window.visible||!memory_||flock(fd_,LOCK_SH|LOCK_NB))return;
 FrameHeader h=*(FrameHeader*)memory_;NSData* bytes=nil;
 size_t count=(size_t)h.width*h.height*4;
 if(h.magic==kFrameMagic&&h.sequence!=sequence_&&h.width&&h.height&&h.width<=8192&&h.height<=8192&&h.capacity==size_-sizeof(FrameHeader)&&count<=h.capacity)bytes=[NSData dataWithBytes:(char*)memory_+sizeof(FrameHeader) length:count];
 flock(fd_,LOCK_UN);if(!bytes)return;
 CGDataProviderRef provider=CGDataProviderCreateWithCFData((CFDataRef)bytes);CGColorSpaceRef color=CGColorSpaceCreateDeviceRGB();
 CGImageRef image=CGImageCreate(h.width,h.height,8,32,(size_t)h.width*4,color,(CGBitmapInfo)(kCGBitmapByteOrder32Little|kCGImageAlphaPremultipliedFirst),provider,nullptr,false,kCGRenderingIntentDefault);
 CGColorSpaceRelease(color);CGDataProviderRelease(provider);if(!image)return;
 [CATransaction begin];[CATransaction setDisableActions:YES];self.layer.contentsScale=self.window.backingScaleFactor;self.layer.contents=(id)image;[CATransaction commit];CGImageRelease(image);sequence_=h.sequence;
}
-(void)retire {
 if(retired_)return;retired_=YES;
 for(id observer in visibility_observers_)[[NSNotificationCenter defaultCenter]removeObserver:observer];[visibility_observers_ release];visibility_observers_=nil;
 [timer_ invalidate];[timer_ release];timer_=nil;
 if(self.window.firstResponder==self)[self.window makeFirstResponder:nil];
 [self removeFromSuperview];self.layer.contents=nil;
 if(memory_)munmap(memory_,size_);memory_=nullptr;if(fd_>=0)close(fd_);fd_=-1;
}
-(void)dealloc {[self retire];[tracking_ release];[marked_ release];[super dealloc];}
-(void)mouse:(NSEvent*)e kind:(NSString*)kind button:(int)button {
 if(retired_||self.hiddenOrHasHiddenAncestor)return;NSPoint p=[self convertPoint:e.locationInWindow fromView:nil];
 Input(handle_,@{@"op":@"mouse",@"kind":kind,@"x":@(p.x),@"y":@(p.y),@"button":@(button),@"count":@(e.clickCount),@"modifiers":@(Modifiers(e)),@"dx":@(int(e.scrollingDeltaX*20)),@"dy":@(int(e.scrollingDeltaY*20))});
}
-(void)mouseDown:(NSEvent*)e{[self.window makeFirstResponder:self];[self mouse:e kind:@"down" button:0];}
-(void)mouseUp:(NSEvent*)e{[self mouse:e kind:@"up" button:0];}
-(void)rightMouseDown:(NSEvent*)e{[self.window makeFirstResponder:self];[self mouse:e kind:@"down" button:1];}
-(void)rightMouseUp:(NSEvent*)e{[self mouse:e kind:@"up" button:1];}
-(void)otherMouseDown:(NSEvent*)e{[self.window makeFirstResponder:self];[self mouse:e kind:@"down" button:2];}
-(void)otherMouseUp:(NSEvent*)e{[self mouse:e kind:@"up" button:2];}
-(void)mouseMoved:(NSEvent*)e{[self mouse:e kind:@"move" button:0];}
-(void)mouseDragged:(NSEvent*)e{[self mouse:e kind:@"move" button:0];}
-(void)rightMouseDragged:(NSEvent*)e{[self mouse:e kind:@"move" button:1];}
-(void)scrollWheel:(NSEvent*)e{[self mouse:e kind:@"wheel" button:0];}
-(void)key:(NSEvent*)e type:(int)type {
 NSString* chars=e.charactersIgnoringModifiers?:@"";unichar c=chars.length?[chars characterAtIndex:0]:0;int vk=c>='a'&&c<='z'?c-32:c;
 switch(e.keyCode){case 123:vk=37;break;case 124:vk=39;break;case 125:vk=40;break;case 126:vk=38;break;case 51:vk=8;break;case 36:vk=13;break;case 48:vk=9;break;case 53:vk=27;break;}
 Input(handle_,@{@"op":@"key",@"type":@(type),@"vk":@(vk),@"native":@(e.keyCode),@"character":@(c),@"unmodified":@(c),@"modifiers":@(Modifiers(e))});
}
-(void)keyDown:(NSEvent*)e{if(retired_||self.hidden)return;[self key:e type:0];[self interpretKeyEvents:@[e]];}
-(void)keyUp:(NSEvent*)e{if(!retired_&&!self.hidden)[self key:e type:2];}
-(void)insertText:(id)value replacementRange:(NSRange)range {NSString* text=[value isKindOfClass:NSAttributedString.class]?[value string]:value;Input(handle_,@{@"op":@"text",@"text":text?:@""});[marked_ release];marked_=[@"" copy];selection_=NSMakeRange(0,0);}
-(void)setMarkedText:(id)value selectedRange:(NSRange)range replacementRange:(NSRange)replacement {NSString* text=[value isKindOfClass:NSAttributedString.class]?[value string]:value;[marked_ release];marked_=[text copy];selection_=range;Input(handle_,@{@"op":@"composition",@"text":text?:@"",@"from":@(range.location),@"to":@(NSMaxRange(range))});}
-(void)unmarkText {[marked_ release];marked_=[@"" copy];Input(handle_,@{@"op":@"cancel-composition"});}
-(BOOL)hasMarkedText{return marked_.length>0;}
-(NSRange)markedRange{return marked_.length?NSMakeRange(0,marked_.length):NSMakeRange(NSNotFound,0);}
-(NSRange)selectedRange{return selection_;}
-(NSArray<NSAttributedStringKey>*)validAttributesForMarkedText{return @[];}
-(NSAttributedString*)attributedSubstringForProposedRange:(NSRange)range actualRange:(NSRangePointer)actual {if(actual)*actual=NSMakeRange(NSNotFound,0);return nil;}
-(NSUInteger)characterIndexForPoint:(NSPoint)p{return NSNotFound;}
-(NSRect)firstRectForCharacterRange:(NSRange)range actualRange:(NSRangePointer)actual {if(actual)*actual=selection_;return [self.window convertRectToScreen:[self convertRect:caret_ toView:nil]];}
-(void)doCommandBySelector:(SEL)selector {int vk=0;if(selector==@selector(deleteBackward:))vk=8;else if(selector==@selector(insertNewline:))vk=13;else if(selector==@selector(insertTab:))vk=9;if(vk)Input(handle_,@{@"op":@"key",@"type":@3,@"vk":@(vk),@"character":@(vk),@"native":@0,@"modifiers":@0});}
@end

int dsh_mac_window(void* native,uint64_t* id,double* scale,double* width,double* height){__block int result=0;OnMain(^{NSWindow* w=(NSWindow*)native;if(![w isKindOfClass:NSWindow.class]||w.windowNumber<=0)return;*id=(uint64_t)w.windowNumber;*scale=w.backingScaleFactor;*width=w.contentView.bounds.size.width;*height=w.contentView.bounds.size.height;result=1;});return result;}
static void Layout(DSHCEFView* v,double x,double y,double width,double height,int visible){NSView* parent=v.superview;v.frame=NSMakeRect(x,[parent isFlipped]?y:parent.bounds.size.height-y-height,width,height);v.hidden=!visible||width<=0||height<=0;if(v.hidden&&v.window.firstResponder==v)[v.window makeFirstResponder:nil];}
int dsh_mac_view_attach(void* native,uintptr_t handle,const char* path,size_t size,double x,double y,double width,double height,int visible){__block int result=0;OnMain(^{NSWindow* window=(NSWindow*)native;if(![window isKindOfClass:NSWindow.class]||window.windowNumber<=0)return;if(!views)views=[[NSMutableDictionary alloc]init];if(views[@(handle)])return;DSHCEFView* view=[[DSHCEFView alloc]initWithFrame:NSZeroRect];if(![view openFrame:path size:size handle:handle]){[view release];return;}[window.contentView addSubview:view positioned:NSWindowAbove relativeTo:nil];views[@(handle)]=view;Layout(view,x,y,width,height,visible);[view release];result=1;});return result;}
int dsh_mac_view_update(uintptr_t handle,double x,double y,double width,double height,int visible){__block int result=0;OnMain(^{DSHCEFView* v=(DSHCEFView*)views[@(handle)];if(v&&!v->retired_&&v.window){Layout(v,x,y,width,height,visible);result=1;}});return result;}
void dsh_mac_view_ime(uintptr_t handle,int from,int to,double x,double y,double width,double height){OnMain(^{DSHCEFView* v=(DSHCEFView*)views[@(handle)];if(v){v->selection_=NSMakeRange(std::max(0,from),std::max(0,to-from));v->caret_=NSMakeRect(x,y,std::max(1.,width),std::max(1.,height));}});}
void dsh_mac_view_close(uintptr_t handle){OnMain(^{DSHCEFView* v=(DSHCEFView*)views[@(handle)];if(v){[v retire];[views removeObjectForKey:@(handle)];}});}
// The UI retains its own NSWindow; the daemon never receives this pointer.
extern "C" void goMacPresenterWindowClosed(uintptr_t);
namespace { NSMutableDictionary<NSNumber*,NSDictionary*>* windows; }
int dsh_mac_window_watch(void* native,uintptr_t handle){__block int result=0;OnMain(^{NSWindow* w=(NSWindow*)native;if(![w isKindOfClass:NSWindow.class]||w.windowNumber<=0)return;if(!windows)windows=[[NSMutableDictionary alloc]init];id observer=[[NSNotificationCenter defaultCenter]addObserverForName:NSWindowWillCloseNotification object:w queue:nil usingBlock:^(NSNotification*){goMacPresenterWindowClosed(handle);}];windows[@(handle)]=@{@"window":w,@"observer":observer};result=1;});return result;}
void dsh_mac_window_unwatch(uintptr_t handle){OnMain(^{NSDictionary* entry=windows[@(handle)];if(entry){[[NSNotificationCenter defaultCenter]removeObserver:entry[@"observer"]];[windows removeObjectForKey:@(handle)];}});}
#include <libproc.h>
int dsh_mac_process_matches(uint32_t pid,const char* expected){char path[PROC_PIDPATHINFO_MAXSIZE]{};if(proc_pidpath((int)pid,path,sizeof(path))<=0)return 0;struct stat a{},b{};return !stat(path,&a)&&!stat(expected,&b)&&S_ISREG(a.st_mode)&&S_ISREG(b.st_mode)&&a.st_dev==b.st_dev&&a.st_ino==b.st_ino;}
int dsh_mac_view_replace_frame(uintptr_t handle,const char* path,size_t size){__block int result=0;OnMain(^{DSHCEFView* v=(DSHCEFView*)views[@(handle)];if(!v||v->retired_||size<sizeof(FrameHeader)||size>sizeof(FrameHeader)+kFrameBudget)return;int fd=open(path,O_RDONLY|O_NOFOLLOW|O_CLOEXEC);struct stat st{};if(fd<0||fstat(fd,&st)||!S_ISREG(st.st_mode)||st.st_uid!=getuid()||(st.st_mode&0777)!=0600||st.st_size!=(off_t)size){if(fd>=0)close(fd);return;}void* memory=mmap(nullptr,size,PROT_READ,MAP_SHARED,fd,0);if(memory==MAP_FAILED){close(fd);return;}munmap(v->memory_,v->size_);close(v->fd_);v->memory_=memory;v->size_=size;v->fd_=fd;v->sequence_=0;result=1;});return result;}

int dsh_mac_view_visible(uintptr_t handle){__block int result=0;OnMain(^{DSHCEFView* v=(DSHCEFView*)views[@(handle)];if(v)result=[v actualVisible]?1:0;});return result;}
