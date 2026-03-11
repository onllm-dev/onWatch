//go:build menubar && darwin && cgo

#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

@interface OnWatchBorderlessPanel : NSPanel
@end

@implementation OnWatchBorderlessPanel
- (BOOL)canBecomeKeyWindow {
  return YES;
}

- (BOOL)canBecomeMainWindow {
  return NO;
}

- (BOOL)acceptsFirstMouse:(NSEvent *)event {
  return YES;
}
@end

static void onwatch_run_on_main_sync(dispatch_block_t block) {
  if ([NSThread isMainThread]) {
    block();
    return;
  }
  dispatch_sync(dispatch_get_main_queue(), block);
}

@interface OnWatchPopoverController : NSObject <WKNavigationDelegate, WKUIDelegate, WKScriptMessageHandler>
@property(nonatomic, strong) OnWatchBorderlessPanel *panel;
@property(nonatomic, strong) NSView *containerView;
@property(nonatomic, strong) WKWebView *webView;
@property(nonatomic, strong) id globalMouseMonitor;
@property(nonatomic, strong) id localMouseMonitor;
@property(nonatomic, strong) id appDeactivationObserver;
@property(nonatomic, assign) CGFloat width;
@property(nonatomic, assign) CGFloat height;
- (instancetype)initWithWidth:(CGFloat)width height:(CGFloat)height;
- (void)applyHeight:(CGFloat)height;
- (void)loadURLString:(NSString *)urlString;
- (BOOL)show;
- (BOOL)toggle;
- (void)close;
- (BOOL)isShown;
@end

@implementation OnWatchPopoverController

- (instancetype)initWithWidth:(CGFloat)width height:(CGFloat)height {
  self = [super init];
  if (!self) {
    return nil;
  }

  self.width = width;
  self.height = height;

  WKWebViewConfiguration *configuration = [[WKWebViewConfiguration alloc] init];
  WKUserContentController *userContentController = [[WKUserContentController alloc] init];
  [userContentController addScriptMessageHandler:self name:@"onwatchResize"];
  [userContentController addScriptMessageHandler:self name:@"onwatchAction"];
  configuration.userContentController = userContentController;

  self.webView = [[WKWebView alloc] initWithFrame:NSMakeRect(0, 0, width, height)
                                    configuration:configuration];
  self.webView.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
  self.webView.navigationDelegate = self;
  self.webView.UIDelegate = self;

  self.containerView = [[NSView alloc] initWithFrame:NSMakeRect(0, 0, width, height)];
  self.containerView.autoresizesSubviews = YES;
  self.containerView.wantsLayer = YES;
  self.containerView.layer.masksToBounds = YES;
  self.containerView.layer.cornerRadius = 14.0;
  self.containerView.layer.cornerCurve = @"continuous";
  self.containerView.layer.backgroundColor = [[NSColor colorWithRed:0.04 green:0.04 blue:0.04 alpha:1.0] CGColor];

  self.webView.frame = self.containerView.bounds;
  [self.webView setValue:@NO forKey:@"drawsBackground"];
  self.webView.wantsLayer = YES;
  self.webView.layer.backgroundColor = [[NSColor colorWithRed:0.04 green:0.04 blue:0.04 alpha:1.0] CGColor];
  [self.containerView addSubview:self.webView];

  NSWindowStyleMask styleMask = NSWindowStyleMaskBorderless | NSWindowStyleMaskNonactivatingPanel;
  self.panel = [[OnWatchBorderlessPanel alloc] initWithContentRect:NSMakeRect(0, 0, width, height)
                                                          styleMask:styleMask
                                                            backing:NSBackingStoreBuffered
                                                              defer:YES];
  self.panel.floatingPanel = YES;
  self.panel.becomesKeyOnlyIfNeeded = YES;
  self.panel.hidesOnDeactivate = NO;
  self.panel.releasedWhenClosed = NO;
  self.panel.opaque = NO;
  self.panel.backgroundColor = [NSColor clearColor];
  self.panel.hasShadow = YES;
  self.panel.level = NSStatusWindowLevel;
  self.panel.collectionBehavior = NSWindowCollectionBehaviorMoveToActiveSpace | NSWindowCollectionBehaviorFullScreenAuxiliary;
  self.panel.contentView = self.containerView;

  return self;
}

- (void)dealloc {
  [self stopTransientCloseMonitoring];
  [self.webView.configuration.userContentController removeScriptMessageHandlerForName:@"onwatchResize"];
  [self.webView.configuration.userContentController removeScriptMessageHandlerForName:@"onwatchAction"];
}

- (void)stopTransientCloseMonitoring {
  if (self.globalMouseMonitor != nil) {
    [NSEvent removeMonitor:self.globalMouseMonitor];
    self.globalMouseMonitor = nil;
  }
  if (self.localMouseMonitor != nil) {
    [NSEvent removeMonitor:self.localMouseMonitor];
    self.localMouseMonitor = nil;
  }
  if (self.appDeactivationObserver != nil) {
    [[NSNotificationCenter defaultCenter] removeObserver:self.appDeactivationObserver];
    self.appDeactivationObserver = nil;
  }
}

- (NSStatusItem *)statusItem {
  id delegate = NSApp.delegate;
  if (!delegate) {
    return nil;
  }

  @try {
    id item = [delegate valueForKey:@"statusItem"];
    if ([item isKindOfClass:[NSStatusItem class]]) {
      return (NSStatusItem *)item;
    }
  } @catch (NSException *exception) {
    return nil;
  }

  return nil;
}

- (NSRect)statusButtonScreenRect {
  NSStatusItem *statusItem = [self statusItem];
  NSStatusBarButton *button = statusItem.button;
  if (!button || !button.window) {
    return NSZeroRect;
  }
  NSRect buttonFrameInWindow = [button convertRect:button.bounds toView:nil];
  return [button.window convertRectToScreen:buttonFrameInWindow];
}

- (NSScreen *)screenForAnchorRect:(NSRect)anchorRect {
  NSPoint anchorPoint = NSMakePoint(NSMidX(anchorRect), NSMidY(anchorRect));
  for (NSScreen *screen in NSScreen.screens) {
    if (NSPointInRect(anchorPoint, screen.frame)) {
      return screen;
    }
  }
  return [NSScreen mainScreen];
}

- (BOOL)positionPanelAnchoredToStatusItem {
  NSRect buttonRect = [self statusButtonScreenRect];
  if (NSIsEmptyRect(buttonRect)) {
    return NO;
  }

  NSScreen *screen = [self screenForAnchorRect:buttonRect];
  if (!screen) {
    return NO;
  }

  NSRect visibleFrame = screen.visibleFrame;
  CGFloat width = self.width;
  CGFloat height = self.height;

  CGFloat targetX = NSMidX(buttonRect) - (width * 0.5);
  CGFloat minX = NSMinX(visibleFrame);
  CGFloat maxX = NSMaxX(visibleFrame) - width;
  if (maxX < minX) {
    maxX = minX;
  }
  if (targetX < minX) {
    targetX = minX;
  } else if (targetX > maxX) {
    targetX = maxX;
  }

  CGFloat targetY = NSMinY(buttonRect) - height - 6.0;
  CGFloat minY = NSMinY(visibleFrame);
  CGFloat maxY = NSMaxY(visibleFrame) - height;
  if (maxY < minY) {
    maxY = minY;
  }
  if (targetY < minY) {
    targetY = minY;
  } else if (targetY > maxY) {
    targetY = maxY;
  }

  NSRect nextFrame = NSMakeRect(round(targetX), round(targetY), width, height);
  [self.panel setFrame:nextFrame display:YES];
  return YES;
}

- (NSPoint)screenPointForEvent:(NSEvent *)event {
  NSPoint point = event.locationInWindow;
  if (event.window) {
    point = [event.window convertPointToScreen:point];
  }
  return point;
}

- (BOOL)containsScreenPoint:(NSPoint)screenPoint {
  if ([self isShown] && NSPointInRect(screenPoint, self.panel.frame)) {
    return YES;
  }

  NSRect buttonRect = [self statusButtonScreenRect];
  if (!NSIsEmptyRect(buttonRect) && NSPointInRect(screenPoint, buttonRect)) {
    return YES;
  }

  return NO;
}

- (void)closeIfInteractionIsOutside:(NSPoint)screenPoint {
  if (![self isShown]) {
    return;
  }
  if ([self containsScreenPoint:screenPoint]) {
    return;
  }
  [self close];
}

- (void)startTransientCloseMonitoring {
  [self stopTransientCloseMonitoring];

  __weak typeof(self) weakSelf = self;
  NSEventMask mask = NSEventMaskLeftMouseDown | NSEventMaskRightMouseDown | NSEventMaskOtherMouseDown;

  self.globalMouseMonitor = [NSEvent addGlobalMonitorForEventsMatchingMask:mask
                                                                    handler:^(NSEvent *event) {
                                                                      __strong typeof(weakSelf) strongSelf = weakSelf;
                                                                      if (!strongSelf) {
                                                                        return;
                                                                      }
                                                                      NSPoint screenPoint = event.locationInWindow;
                                                                      dispatch_async(dispatch_get_main_queue(), ^{
                                                                        [strongSelf closeIfInteractionIsOutside:screenPoint];
                                                                      });
                                                                    }];

  self.localMouseMonitor = [NSEvent addLocalMonitorForEventsMatchingMask:mask
                                                                  handler:^NSEvent *_Nullable(NSEvent *event) {
                                                                    __strong typeof(weakSelf) strongSelf = weakSelf;
                                                                    if (!strongSelf) {
                                                                      return event;
                                                                    }
                                                                    NSPoint screenPoint = [strongSelf screenPointForEvent:event];
                                                                    [strongSelf closeIfInteractionIsOutside:screenPoint];
                                                                    return event;
                                                                  }];

  self.appDeactivationObserver = [[NSNotificationCenter defaultCenter]
      addObserverForName:NSApplicationDidResignActiveNotification
                  object:NSApp
                   queue:[NSOperationQueue mainQueue]
              usingBlock:^(__unused NSNotification *note) {
                __strong typeof(weakSelf) strongSelf = weakSelf;
                if (!strongSelf) {
                  return;
                }
                [strongSelf close];
              }];
}

- (void)applyHeight:(CGFloat)height {
  CGFloat clampedHeight = MAX(140.0, MIN(600.0, height));
  CGFloat delta = clampedHeight - self.height;
  if (delta < 0) {
    delta = -delta;
  }

  self.height = clampedHeight;
  NSSize size = NSMakeSize(self.width, clampedHeight);
  self.containerView.frame = NSMakeRect(0, 0, self.width, clampedHeight);
  self.webView.frame = self.containerView.bounds;
  [self.panel setContentSize:size];

  if ([self isShown] && delta >= 0.5) {
    [self positionPanelAnchoredToStatusItem];
  }
}

- (BOOL)isLocalURL:(NSURL *)url {
  if (!url) {
    return NO;
  }
  if ([url.scheme isEqualToString:@"about"]) {
    return YES;
  }
  NSString *host = url.host.lowercaseString;
  return [host isEqualToString:@"localhost"] || [host isEqualToString:@"127.0.0.1"];
}

- (void)loadURLString:(NSString *)urlString {
  if (!urlString.length) {
    return;
  }

  NSURL *url = [NSURL URLWithString:urlString];
  if (!url) {
    return;
  }

  NSURLRequest *request = [NSURLRequest requestWithURL:url];
  [self.webView loadRequest:request];
}

- (BOOL)show {
  if (!self.panel) {
    return NO;
  }

  [self applyHeight:self.height];
  if (![self positionPanelAnchoredToStatusItem]) {
    return NO;
  }

  if (![self isShown]) {
    [self.panel makeKeyAndOrderFront:nil];
  }
  [self startTransientCloseMonitoring];
  return YES;
}

- (BOOL)toggle {
  if ([self isShown]) {
    [self close];
    return YES;
  }
  return [self show];
}

- (void)close {
  [self stopTransientCloseMonitoring];
  if (![self isShown]) {
    return;
  }
  [self.panel orderOut:nil];
}

- (BOOL)isShown {
  return self.panel != nil && self.panel.visible;
}

- (void)webView:(WKWebView *)webView
    decidePolicyForNavigationAction:(WKNavigationAction *)navigationAction
                    decisionHandler:(void (^)(WKNavigationActionPolicy))decisionHandler {
  NSURL *url = navigationAction.request.URL;
  if ([self isLocalURL:url]) {
    decisionHandler(WKNavigationActionPolicyAllow);
    return;
  }

  if (url) {
    [[NSWorkspace sharedWorkspace] openURL:url];
  }
  decisionHandler(WKNavigationActionPolicyCancel);
}

- (WKWebView *)webView:(WKWebView *)webView
    createWebViewWithConfiguration:(WKWebViewConfiguration *)configuration
               forNavigationAction:(WKNavigationAction *)navigationAction
                    windowFeatures:(WKWindowFeatures *)windowFeatures {
  NSURL *url = navigationAction.request.URL;
  if (url) {
    [[NSWorkspace sharedWorkspace] openURL:url];
  }
  return nil;
}

- (void)userContentController:(WKUserContentController *)userContentController
      didReceiveScriptMessage:(WKScriptMessage *)message {
  if ([message.name isEqualToString:@"onwatchResize"]) {
    CGFloat nextHeight = self.height;
    id body = message.body;
    if ([body isKindOfClass:[NSNumber class]]) {
      nextHeight = [body doubleValue];
    } else if ([body isKindOfClass:[NSDictionary class]]) {
      id value = [(NSDictionary *)body objectForKey:@"height"];
      if ([value respondsToSelector:@selector(doubleValue)]) {
        nextHeight = [value doubleValue];
      }
    }
    [self applyHeight:nextHeight];
    return;
  }

  if (![message.name isEqualToString:@"onwatchAction"]) {
    return;
  }

  NSString *action = nil;
  id body = message.body;
  if ([body isKindOfClass:[NSString class]]) {
    action = (NSString *)body;
  } else if ([body isKindOfClass:[NSDictionary class]]) {
    id value = [(NSDictionary *)body objectForKey:@"action"];
    if ([value isKindOfClass:[NSString class]]) {
      action = (NSString *)value;
    }
  }

  if (![action isKindOfClass:[NSString class]]) {
    return;
  }

  if ([action isEqualToString:@"close"]) {
    [self close];
    return;
  }

  if ([action isEqualToString:@"open_dashboard"]) {
    NSURL *url = [NSURL URLWithString:@"http://localhost:9211"];
    if (url) {
      [[NSWorkspace sharedWorkspace] openURL:url];
      [self close];
    }
  }
}

@end

static OnWatchPopoverController *onwatch_popover_controller(void *handle) {
  if (!handle) {
    return nil;
  }
  return (__bridge OnWatchPopoverController *)handle;
}

void *onwatch_popover_create(int width, int height) {
  __block void *handle = nil;
  onwatch_run_on_main_sync(^{
    [NSApplication sharedApplication];
    OnWatchPopoverController *controller =
        [[OnWatchPopoverController alloc] initWithWidth:width height:height];
    handle = (__bridge_retained void *)controller;
  });
  return handle;
}

void onwatch_popover_destroy(void *handle) {
  if (!handle) {
    return;
  }

  onwatch_run_on_main_sync(^{
    OnWatchPopoverController *controller = (__bridge_transfer OnWatchPopoverController *)handle;
    [controller close];
  });
}

bool onwatch_popover_show(void *handle) {
  __block BOOL shown = NO;
  onwatch_run_on_main_sync(^{
    shown = [onwatch_popover_controller(handle) show];
  });
  return shown;
}

bool onwatch_popover_toggle(void *handle) {
  __block BOOL toggled = NO;
  onwatch_run_on_main_sync(^{
    toggled = [onwatch_popover_controller(handle) toggle];
  });
  return toggled;
}

void onwatch_popover_load_url(void *handle, const char *url) {
  if (!handle || !url) {
    return;
  }

  onwatch_run_on_main_sync(^{
    NSString *urlString = [[NSString alloc] initWithUTF8String:url];
    [onwatch_popover_controller(handle) loadURLString:urlString];
  });
}

void onwatch_popover_close(void *handle) {
  if (!handle) {
    return;
  }

  onwatch_run_on_main_sync(^{
    [onwatch_popover_controller(handle) close];
  });
}

bool onwatch_popover_is_shown(void *handle) {
  __block BOOL shown = NO;
  onwatch_run_on_main_sync(^{
    shown = [onwatch_popover_controller(handle) isShown];
  });
  return shown;
}
