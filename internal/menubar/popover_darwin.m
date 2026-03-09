//go:build menubar && darwin && cgo

#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

static void onwatch_run_on_main_sync(dispatch_block_t block) {
  if ([NSThread isMainThread]) {
    block();
    return;
  }
  dispatch_sync(dispatch_get_main_queue(), block);
}

@interface OnWatchPopoverController : NSObject <WKNavigationDelegate, WKUIDelegate>
@property(nonatomic, strong) NSPopover *popover;
@property(nonatomic, strong) NSViewController *contentController;
@property(nonatomic, strong) WKWebView *webView;
@property(nonatomic, assign) CGFloat width;
@property(nonatomic, assign) CGFloat height;
- (instancetype)initWithWidth:(CGFloat)width height:(CGFloat)height;
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
  self.webView = [[WKWebView alloc] initWithFrame:NSMakeRect(0, 0, width, height)
                                    configuration:configuration];
  self.webView.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
  self.webView.navigationDelegate = self;
  self.webView.UIDelegate = self;

  NSView *containerView = [[NSView alloc] initWithFrame:NSMakeRect(0, 0, width, height)];
  containerView.autoresizesSubviews = YES;
  self.webView.frame = containerView.bounds;
  [containerView addSubview:self.webView];

  self.contentController = [[NSViewController alloc] init];
  self.contentController.view = containerView;
  self.contentController.preferredContentSize = NSMakeSize(width, height);

  self.popover = [[NSPopover alloc] init];
  self.popover.animates = YES;
  self.popover.behavior = NSPopoverBehaviorTransient;
  self.popover.contentSize = NSMakeSize(width, height);
  self.popover.contentViewController = self.contentController;

  return self;
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
  NSStatusItem *statusItem = [self statusItem];
  NSStatusBarButton *button = statusItem.button;
  if (!button) {
    return NO;
  }

  if (self.popover.shown) {
    return YES;
  }

  self.popover.contentSize = NSMakeSize(self.width, self.height);
  [self.popover showRelativeToRect:button.bounds
                            ofView:button
                     preferredEdge:NSRectEdgeMinY];
  return YES;
}

- (BOOL)toggle {
  if (self.popover.shown) {
    [self close];
    return YES;
  }
  return [self show];
}

- (void)close {
  if (!self.popover.shown) {
    return;
  }
  [self.popover performClose:nil];
}

- (BOOL)isShown {
  return self.popover.shown;
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
