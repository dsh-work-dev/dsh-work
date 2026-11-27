//go:build darwin && cgo && e67previewfixture && !server

#import <Cocoa/Cocoa.h>
#import <UserNotifications/UserNotifications.h>
#include "presenter_fixture_darwin.h"
#include <future>
#include <memory>

namespace {
void OnMain(dispatch_block_t call) {
 if([NSThread isMainThread])call();
 else dispatch_sync(dispatch_get_main_queue(),call);
}
}

@interface DSHFixtureNotificationResponse : NSObject {
 UNNotificationRequest* request_;
}
- (instancetype)initWithRequest:(UNNotificationRequest*)request;
@end

@implementation DSHFixtureNotificationResponse
- (instancetype)initWithRequest:(UNNotificationRequest*)request {
 self=[super init];
 if(self)request_=[request retain];
 return self;
}
- (id)notification { return self; }
- (UNNotificationRequest*)request { return request_; }
- (NSString*)actionIdentifier { return UNNotificationDefaultActionIdentifier; }
- (void)dealloc { [request_ release]; [super dealloc]; }
@end

int dsh_mac_fixture_notification_default(const char* category,const char* allowTitle,const char* rejectTitle,int invokeDefault){
 @autoreleasepool {
  NSString* identifier=[NSString stringWithUTF8String:category];
  NSString* allow=[NSString stringWithUTF8String:allowTitle];
  NSString* reject=[NSString stringWithUTF8String:rejectTitle];
  UNUserNotificationCenter* center=UNUserNotificationCenter.currentNotificationCenter;
  auto observed=std::make_shared<std::promise<bool>>();
  auto registration=observed->get_future();
  [center getNotificationCategoriesWithCompletionHandler:^(NSSet<UNNotificationCategory*>* categories){
   for(UNNotificationCategory* entry in categories){
    if(![entry.identifier isEqualToString:identifier])continue;
    NSArray<UNNotificationAction*>* actions=entry.actions;
    observed->set_value(actions.count==2&&[actions[0].identifier isEqualToString:@"allow-once"]&&
                       [actions[0].title isEqualToString:allow]&&[actions[1].identifier isEqualToString:@"reject"]&&
                       [actions[1].title isEqualToString:reject]);
    return;
   }
   if(!invokeDefault){observed->set_value(false);return;}
   [center getNotificationSettingsWithCompletionHandler:^(UNNotificationSettings* settings){
    observed->set_value(settings.authorizationStatus==UNAuthorizationStatusNotDetermined||
                       settings.authorizationStatus==UNAuthorizationStatusDenied);
   }];
  }];
  if(registration.wait_for(std::chrono::seconds(3))!=std::future_status::ready||!registration.get())return -1;
  if(!invokeDefault)return 1;
  id<UNUserNotificationCenterDelegate> delegate=center.delegate;
  if(![delegate respondsToSelector:@selector(userNotificationCenter:didReceiveNotificationResponse:withCompletionHandler:)])return -2;
  // Supply only a default response to the installed delegate. This fixture
  // does not register, request permission, deliver or click a notification.
  UNMutableNotificationContent* content=[[[UNMutableNotificationContent alloc] init] autorelease];
  UNNotificationRequest* request=[UNNotificationRequest requestWithIdentifier:@"dsh-preview-notification" content:content trigger:nil];
  DSHFixtureNotificationResponse* response=[[[DSHFixtureNotificationResponse alloc] initWithRequest:request] autorelease];
  __block bool completed=false;
  [delegate userNotificationCenter:center didReceiveNotificationResponse:(UNNotificationResponse*)response
             withCompletionHandler:^{completed=true;}];
  return completed?1:-3;
 }
}


int dsh_mac_fixture_application_reopen(void){
 __block int invoked=0;
 OnMain(^{
  id<NSApplicationDelegate> delegate=NSApp.delegate;
  if(![delegate respondsToSelector:@selector(applicationShouldHandleReopen:hasVisibleWindows:)])return;
  BOOL visible=NO;
  for(NSWindow* window in NSApp.windows){
   if(window.visible){visible=YES;break;}
  }
  [delegate applicationShouldHandleReopen:NSApp hasVisibleWindows:visible];
  invoked=1;
 });
 return invoked;
}
int dsh_mac_fixture_pet_frame(void* native,DSHPetFrame* out){
 __block int observed=0;
 OnMain(^{
  NSWindow* window=(NSWindow*)native;
  NSScreen* primary=NSScreen.screens.firstObject;
  if(!window.visible||!window.screen||!primary)return;
  NSRect frame=window.frame, area=window.screen.visibleFrame;
  CGFloat top=NSMaxY(primary.frame);
  *out={(int)frame.origin.x,(int)(top-NSMaxY(frame)),(int)frame.size.width,(int)frame.size.height,
        (int)area.origin.x,(int)(top-NSMaxY(area)),(int)area.size.width,(int)area.size.height,window.backingScaleFactor};
  observed=1;
 });
 return observed;
}
int dsh_mac_fixture_pet_display_change(void* native,int change){
 __block int invoked=0;
 OnMain(^{
  NSWindow* window=(NSWindow*)native;
  if(!window.visible)return;
  id<NSWindowDelegate> delegate=window.delegate;
  if(change==0&&[delegate respondsToSelector:@selector(windowDidChangeScreen:)]){
   [delegate windowDidChangeScreen:[NSNotification notificationWithName:NSWindowDidChangeScreenNotification object:window]];
  }else if(change==1&&[delegate respondsToSelector:@selector(windowDidChangeBackingProperties:)]){
   [delegate windowDidChangeBackingProperties:[NSNotification notificationWithName:NSWindowDidChangeBackingPropertiesNotification object:window]];
  }else if(change==2&&[NSApp.delegate respondsToSelector:@selector(applicationDidChangeScreenParameters:)]){
   [NSApp.delegate applicationDidChangeScreenParameters:[NSNotification notificationWithName:NSApplicationDidChangeScreenParametersNotification object:NSApp]];
  }else{return;}
  invoked=1;
 });
 return invoked;
}
static NSStatusBarButton* FixtureStatusButton(NSView* view, Class controllerClass){
 if([view isKindOfClass:NSStatusBarButton.class]){
  NSStatusBarButton* button=(NSStatusBarButton*)view;
  if([button.target isKindOfClass:controllerClass])return button;
 }
 for(NSView* child in view.subviews){
  NSStatusBarButton* button=FixtureStatusButton(child,controllerClass);
  if(button)return button;
 }
 return nil;
}
static struct {
 bool pending,began,ended;
 NSTimeInterval deadline;
 id beginObserver,endObserver;
 NSTimer* cancelTimer;
} fixtureTrayTracking{};

static int FixtureTrackTrayMenu(NSStatusBarButton* button,NSMenu* menu){
 NSNotificationCenter* center=NSNotificationCenter.defaultCenter;
 NSTimeInterval now=NSProcessInfo.processInfo.systemUptime;
 if(fixtureTrayTracking.pending){
  if(!fixtureTrayTracking.ended&&now<fixtureTrayTracking.deadline)return 2;
  [fixtureTrayTracking.cancelTimer invalidate];
  [fixtureTrayTracking.cancelTimer release];
  [center removeObserver:fixtureTrayTracking.beginObserver];
  [center removeObserver:fixtureTrayTracking.endObserver];
  bool began=fixtureTrayTracking.began;
  bool ended=fixtureTrayTracking.ended;
  fixtureTrayTracking={};
  if(!began)return -6;
  if(!ended)return -8;
  if(![button.target respondsToSelector:@selector(statusItem)])return -7;
  NSStatusItem* item=[button.target performSelector:@selector(statusItem)];
  return item.menu==nil&&menu.delegate==nil?1:-7;
 }
 NSWindow* window=button.window;
 if(!window||window.windowNumber<=0)return -5;
 NSRect bounds=[button convertRect:button.bounds toView:nil];
 if(NSIsEmptyRect(bounds))return -5;
 NSPoint point=NSMakePoint(NSMidX(bounds),NSMidY(bounds));
 NSEvent* event=[NSEvent mouseEventWithType:NSEventTypeRightMouseDown
                                  location:point
                             modifierFlags:0
                                 timestamp:now
                              windowNumber:window.windowNumber
                                   context:nil eventNumber:1 clickCount:1 pressure:1.0];
 NSEvent* up=[NSEvent mouseEventWithType:NSEventTypeRightMouseUp
                               location:point
                          modifierFlags:0 timestamp:now
                           windowNumber:window.windowNumber
                                context:nil eventNumber:1 clickCount:1 pressure:0];
 if(event.window!=window||up.window!=window)return -5;
 fixtureTrayTracking.pending=true;
 fixtureTrayTracking.deadline=now+2;
 fixtureTrayTracking.beginObserver=[center addObserverForName:NSMenuDidBeginTrackingNotification object:menu queue:nil
                         usingBlock:^(NSNotification*){
  fixtureTrayTracking.began=true;
  // Defer cancellation until AppKit enters the tracking loop.
  fixtureTrayTracking.cancelTimer.fireDate=[NSDate dateWithTimeIntervalSinceNow:0.03];
 }];
 fixtureTrayTracking.endObserver=[center addObserverForName:NSMenuDidEndTrackingNotification object:menu queue:nil
                       usingBlock:^(NSNotification*){fixtureTrayTracking.ended=true;}];
 NSTimer* cancel=[NSTimer timerWithTimeInterval:1.5 repeats:NO block:^(NSTimer*){[menu cancelTracking];}];
 fixtureTrayTracking.cancelTimer=[cancel retain];
 [NSRunLoop.currentRunLoop addTimer:cancel forMode:NSEventTrackingRunLoopMode];
 [NSRunLoop.currentRunLoop addTimer:cancel forMode:NSRunLoopCommonModes];
 // A local queue event reaches Wails' monitor and becomes NSApp.currentEvent.
 [NSApp postEvent:up atStart:YES];
 [NSApp postEvent:event atStart:YES];
 return 2;
}
int dsh_mac_fixture_tray_action(const char* menuTitle,int trackMenu){
 __block int result=0;
 OnMain(^{
  // The pinned Wails adapter owns the button and its cached native menu.
  Class controllerClass=NSClassFromString(@"StatusItemController");
  if(!controllerClass)return;
  NSStatusBarButton* button=nil;
  for(NSWindow* window in NSApp.windows){
   NSStatusBarButton* candidate=FixtureStatusButton(window.contentView,controllerClass);
   if(!candidate)continue;
   if(button){result=-1;return;}
   button=candidate;
  }
  if(!button||![button.image isTemplate])return;
  if(!menuTitle){
   result=[NSApp sendAction:button.action to:button.target from:button]?1:-2;
   return;
  }
  if(![button.target respondsToSelector:@selector(cachedMenu)]){result=-3;return;}
  NSMenu* menu=[button.target performSelector:@selector(cachedMenu)];
  NSMenuItem* item=[menu itemWithTitle:[NSString stringWithUTF8String:menuTitle]];
  if(!item||!item.enabled||!item.action){result=-4;return;}
  if(trackMenu){result=FixtureTrackTrayMenu(button,menu);return;}
  [menu performActionForItemAtIndex:[menu indexOfItem:item]];
  result=1;
 });
 return result;
}
