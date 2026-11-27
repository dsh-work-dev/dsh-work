// Included only by the preview build. These helpers inspect the private view
// and sheet state already owned by the Mac presenter.
#include "presenter_fixture_darwin.h"
#include <initializer_list>
#include <stdlib.h>
#include <string.h>

int dsh_mac_fixture_cancel_download_save(void* native){
 __block int canceled=0;
 OnMain(^{
  NSWindow* window=(NSWindow*)native;
  NSWindow* sheet=window.attachedSheet;
  if(![sheet isKindOfClass:NSSavePanel.class]||![downloadSheets.allValues containsObject:sheet]||!sheet.visible)return;
  [window endSheet:sheet returnCode:NSModalResponseCancel];
  canceled=1;
 });
 return canceled;
}
int dsh_mac_fixture_download_save_visible(void* native){
 __block int visible=0;
 OnMain(^{
  NSWindow* sheet=((NSWindow*)native).attachedSheet;
  visible=[sheet isKindOfClass:NSSavePanel.class]&&[downloadSheets.allValues containsObject:sheet]&&sheet.visible;
 });
 return visible;
}
int dsh_mac_fixture_download_confirmation(void* native,int choice){
 __block int found=0;
 OnMain(^{
  NSWindow* window=(NSWindow*)native;
  NSWindow* sheet=window.attachedSheet;
  if(!sheet.visible)return;
  NSNumber* key=[downloadSheets allKeysForObject:sheet].firstObject;
  if(!key)return;
  NSArray<NSButton*>* buttons=fixtureDownloadButtons[key];
  if(buttons.count!=2)return;
  if(choice>=0){
   NSButton* button=buttons[choice==0?1:0];
   if(!button.enabled)return;
   [button performClick:nil];
  }
  found=1;
 });
 return found;
}
int dsh_mac_fixture_file_picker_visible(void* native){
 __block int result=0;
 OnMain(^{
  NSWindow* window=(NSWindow*)native;
  for(NSOpenPanel* panel in filePanels.allValues){
   if(window.attachedSheet==panel&&panel.visible){result=1;break;}
  }
 });
 return result;
}
int dsh_mac_fixture_cancel_workspace_picker(void* native){
 __block int result=0;
 OnMain(^{
  NSOpenPanel* panel=(NSOpenPanel*)((NSWindow*)native).attachedSheet;
  if(![panel isKindOfClass:NSOpenPanel.class]||!panel.visible||!panel.canChooseDirectories||panel.canChooseFiles||
     panel.allowsMultipleSelection||!panel.canCreateDirectories||![filePanels.allValues containsObject:panel])return;
  [panel cancel:nil];
  result=1;
 });
 return result;
}
int dsh_mac_fixture_file_picker_filter(void* native){
 __block int result=0;
 OnMain(^{
  NSWindow* window=(NSWindow*)native;
  for(NSOpenPanel* panel in filePanels.allValues){
   if(window.attachedSheet!=panel||!panel.visible)continue;
   NSPopUpButton* filter=(NSPopUpButton*)panel.accessoryView;
   if(![filter isKindOfClass:NSPopUpButton.class]||filter.numberOfItems!=2||filter.indexOfSelectedItem!=0||
      !panel.accessoryViewDisclosed){result=-1;return;}
   NSArray<UTType*>* initial=panel.allowedContentTypes;
   if(![initial containsObject:UTTypePlainText]||![initial containsObject:UTTypePNG]||
      ![initial containsObject:UTTypeJPEG]){result=-2;return;}
   [filter selectItemAtIndex:1];
   if(![NSApp sendAction:filter.action to:filter.target from:filter]||panel.allowedContentTypes.count!=0){result=-3;return;}
   [filter selectItemAtIndex:0];
   if(![NSApp sendAction:filter.action to:filter.target from:filter]||
      ![panel.allowedContentTypes isEqualToArray:initial]){result=-4;return;}
   result=1;
   return;
  }
 });
 return result;
}
char* dsh_mac_fixture_observe(uintptr_t handle){__block char* result=NULL;OnMain(^{DSHCEFView* v=(DSHCEFView*)views[@(handle)];if(!v||v->retired_||!v.window)return;NSWindow* w=v.window;NSRect frame=[v convertRect:v.bounds toView:w.contentView];NSPoint center=[v convertPoint:NSMakePoint(v.bounds.size.width/2,v.bounds.size.height/2) toView:w.contentView];BOOL hit=[w.contentView hitTest:center]==v;uint64_t sequence=0;if(v->memory_&&!flock(v->fd_,LOCK_SH|LOCK_NB)){sequence=((FrameHeader*)v->memory_)->sequence;flock(v->fd_,LOCK_UN);}
 // The production view publishes a CGImage as its layer contents.
 CGImageRef pixels=(CGImageRef)v.layer.contents;
 NSDictionary* observation=@{@"window":@(w.windowNumber),@"visible":@([v actualVisible]),@"miniaturized":@(w.miniaturized),@"occlusionVisible":@((w.occlusionState&NSWindowOcclusionStateVisible)!=0),@"scale":@(w.backingScaleFactor),@"firstResponderIsCEF":@(w.firstResponder==v),@"keyWindow":@(w.keyWindow),
 @"layerScale":@(v.layer.contentsScale),@"pixelWidth":@(pixels?CGImageGetWidth(pixels):0),@"pixelHeight":@(pixels?CGImageGetHeight(pixels):0),@"frameBytes":@(v->size_),
 @"applicationActive":@(NSApp.active),@"applicationActivationPolicy":@(NSApp.activationPolicy),
 @"canBecomeKeyWindow":@(w.canBecomeKeyWindow),@"mainWindow":@(w.mainWindow),
 @"ownsKeyWindow":@(NSApp.keyWindow!=nil),@"attachedSheet":@(w.attachedSheet!=nil),
 @"fixtureLeftDown":@(v->fixture_left_down_),@"fixtureLeftUp":@(v->fixture_left_up_),@"displayedFrames":@(v->fixture_displayed_frames_),@"nativeHitTestIsCEF":@(hit),@"paintSequence":@(sequence),@"frame":@{@"x":@(frame.origin.x),@"y":@(frame.origin.y),@"width":@(frame.size.width),@"height":@(frame.size.height)},@"renderer":@"actual-owned-NSWindow/NSView/CALayer"};NSData* data=[NSJSONSerialization dataWithJSONObject:observation options:0 error:nil];if(data){result=(char*)malloc(data.length+1);if(result){memcpy(result,data.bytes,data.length);result[data.length]=0;}}});return result;}
static void FixtureClick(DSHCEFView* view,NSPoint point){
 NSWindow* window=view.window;
 NSPoint location=[view convertPoint:point toView:nil];
 for(NSEventType type:{NSEventTypeLeftMouseDown,NSEventTypeLeftMouseUp}){
  NSEvent* event=[NSEvent mouseEventWithType:type location:location modifierFlags:0 timestamp:NSProcessInfo.processInfo.systemUptime windowNumber:window.windowNumber context:nil eventNumber:0 clickCount:1 pressure:type==NSEventTypeLeftMouseDown?1:0];
  [window sendEvent:event];
 }
}
int dsh_mac_fixture_click(uintptr_t handle,double x,double y){
 __block int result=0;
 OnMain(^{
  DSHCEFView* view=(DSHCEFView*)views[@(handle)];
  NSPoint point=NSMakePoint(x,y);
  if(![view actualVisible]||!NSPointInRect(point,view.bounds))return;
  NSWindow* window=view.window;
  if(window.attachedSheet){result=-1;return;}
  NSView* root=window.contentView;
  if([root hitTest:[view convertPoint:point toView:root.superview]]!=view){result=-2;return;}
  FixtureClick(view,point);result=1;
 });
 return result;
}
static bool FixtureScroll(DSHCEFView* view){
 CGEventRef wheel=CGEventCreateScrollWheelEvent(nullptr,kCGScrollEventUnitPixel,1,-6);
 if(!wheel)return false;
 NSPoint location=[view convertPoint:NSMakePoint(100,140) toView:nil];
 // The synthetic event has no window. Supply the numeric window coordinates
 // through its screen-coordinate representation and deliver it only to the fixture view.
 CGEventSetLocation(wheel,CGPointMake(location.x,NSMaxY(NSScreen.screens.firstObject.frame)-location.y));
 NSEvent* event=[NSEvent eventWithCGEvent:wheel];
 CFRelease(wheel);
 if(!event||!NSEqualPoints(event.locationInWindow,location))return false;
 [view scrollWheel:event];
 return true;
}
int dsh_mac_fixture_interact(uintptr_t handle){
 __block int result=-1;
 OnMain(^{
  DSHCEFView* view=(DSHCEFView*)views[@(handle)];
  if(!view||![view actualVisible]||view.bounds.size.width<240||view.bounds.size.height<180)return;
  NSWindow* window=view.window;
  FixtureClick(view,NSMakePoint(100,70));
  if(window.firstResponder!=view){result=-2;return;}
  NSString* text=@"mac-ui-dirty";
  for(NSUInteger i=0;i<text.length;i++){
   NSString* character=[text substringWithRange:NSMakeRange(i,1)];
   for(NSEventType type:{NSEventTypeKeyDown,NSEventTypeKeyUp}){
    NSEvent* event=[NSEvent keyEventWithType:type location:NSZeroPoint modifierFlags:0 timestamp:NSProcessInfo.processInfo.systemUptime windowNumber:window.windowNumber context:nil characters:character charactersIgnoringModifiers:character isARepeat:NO keyCode:0];
    [window sendEvent:event];
   }
  }
  [view setMarkedText:@"zhong" selectedRange:NSMakeRange(5,0) replacementRange:NSMakeRange(NSNotFound,0)];
  [view insertText:@"中" replacementRange:NSMakeRange(NSNotFound,0)];
  FixtureClick(view,NSMakePoint(100,140));
  result=FixtureScroll(view)?1:-3;
 });
 return result;
}
int dsh_mac_fixture_render(uintptr_t handle,const char* path){__block int result=0;OnMain(^{DSHCEFView* v=(DSHCEFView*)views[@(handle)];if(!v||v->retired_||!v.window)return;NSView* root=v.window.contentView;NSBitmapImageRep* rep=[root bitmapImageRepForCachingDisplayInRect:root.bounds];if(!rep)return;[root cacheDisplayInRect:root.bounds toBitmapImageRep:rep];if([v actualVisible]){NSGraphicsContext* context=[NSGraphicsContext graphicsContextWithBitmapImageRep:rep];if(!context)return;NSRect frame=[v convertRect:v.bounds toView:root];CGContextRef cg=context.CGContext;CGContextSaveGState(cg);CGFloat scale=v.window.backingScaleFactor;CGContextScaleCTM(cg,scale,scale);CGContextTranslateCTM(cg,frame.origin.x,[root isFlipped]?root.bounds.size.height-NSMaxY(frame):frame.origin.y);[v.layer renderInContext:cg];CGContextRestoreGState(cg);}NSData* png=[rep representationUsingType:NSBitmapImageFileTypePNG properties:@{}];if(png&&[png writeToFile:[NSString stringWithUTF8String:path] options:NSDataWritingAtomic error:nil])result=1;});return result;}
