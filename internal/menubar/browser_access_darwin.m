//go:build menubar && darwin && cgo

#import <Cocoa/Cocoa.h>

// Result codes shared with the Go side.
#define ONWATCH_ACCESS_CANCELLED 0
#define ONWATCH_ACCESS_GRANTED 1
#define ONWATCH_ACCESS_NO_APP 2
#define ONWATCH_ACCESS_TIMEOUT 3

// Presents the system folder panel so the user can grant this binary access to
// one browser data directory. Choosing a folder is what makes macOS record the
// per-application grant; nothing here can grant it silently.
//
// The panel is shown with beginWithCompletionHandler rather than runModal: a
// menubar-only app runs as an accessory, and a modal session started from a
// dispatch_sync block returns immediately without ever drawing the panel.
int onwatch_request_folder_access(const char *path, const char *message) {
  if (NSApp == nil) {
    return ONWATCH_ACCESS_NO_APP;
  }
  __block int outcome = ONWATCH_ACCESS_CANCELLED;
  __block NSOpenPanel *panel = nil;
  __block BOOL finished = NO;
  __block NSApplicationActivationPolicy previous;
  dispatch_semaphore_t done = dispatch_semaphore_create(0);
  NSString *dir = path ? [NSString stringWithUTF8String:path] : nil;
  NSString *msg = message ? [NSString stringWithUTF8String:message] : @"";

  dispatch_async(dispatch_get_main_queue(), ^{
    // An accessory app cannot reliably own a key window. Become a regular app
    // for the lifetime of the panel, then drop back so no dock icon lingers.
    previous = [NSApp activationPolicy];
    if (previous != NSApplicationActivationPolicyRegular) {
      [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
    }
    [NSApp activateIgnoringOtherApps:YES];

    panel = [NSOpenPanel openPanel];
    // Keep the asynchronous panel alive until its completion handler runs,
    // and keep it visible if focus returns to the browser or the tray.
    panel.releasedWhenClosed = NO;
    panel.hidesOnDeactivate = NO;
    panel.collectionBehavior = NSWindowCollectionBehaviorMoveToActiveSpace |
                               NSWindowCollectionBehaviorFullScreenAuxiliary;
    panel.canChooseDirectories = YES;
    panel.canChooseFiles = NO;
    panel.allowsMultipleSelection = NO;
    panel.canCreateDirectories = NO;
    panel.showsHiddenFiles = YES;
    panel.message = msg;
    panel.prompt = @"Grant Access";
    panel.title = @"Grant onWatch access";
    if (dir.length > 0) {
      panel.directoryURL = [NSURL fileURLWithPath:dir isDirectory:YES];
    }
    panel.level = NSModalPanelWindowLevel;
    [panel beginWithCompletionHandler:^(NSModalResponse result) {
      if (finished) {
        return;
      }
      finished = YES;
      outcome = (result == NSModalResponseOK) ? ONWATCH_ACCESS_GRANTED
                                              : ONWATCH_ACCESS_CANCELLED;
      [panel orderOut:nil];
      panel = nil;
      if (previous != NSApplicationActivationPolicyRegular) {
        [NSApp setActivationPolicy:previous];
      }
      dispatch_semaphore_signal(done);
    }];
    // Present first, then bring the actual system panel forward. Ordering an
    // unpresented panel does not reliably bring the remote picker onscreen.
    [panel makeKeyAndOrderFront:nil];
    [panel orderFrontRegardless];
  });

  // Bounded so a panel the user never answers cannot wedge the menu handler.
  if (dispatch_semaphore_wait(
          done, dispatch_time(DISPATCH_TIME_NOW, (int64_t)(300 * NSEC_PER_SEC))) != 0) {
    dispatch_async(dispatch_get_main_queue(), ^{
      if (finished) {
        return;
      }
      finished = YES;
      [panel cancel:nil];
      [panel orderOut:nil];
      panel = nil;
      if (previous != NSApplicationActivationPolicyRegular) {
        [NSApp setActivationPolicy:previous];
      }
    });
    return ONWATCH_ACCESS_TIMEOUT;
  }
  return outcome;
}
