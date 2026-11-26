#import <Cocoa/Cocoa.h>
#import <Carbon/Carbon.h>
#import <WebKit/WebKit.h>
#import <ApplicationServices/ApplicationServices.h>
#import <UserNotifications/UserNotifications.h>
#import "darwin_bridge_darwin.h"

static void runOnMainSync(dispatch_block_t block) {
    if ([NSThread isMainThread]) {
        block();
    } else {
        dispatch_sync(dispatch_get_main_queue(), block);
    }
}

static void runOnMainAsync(dispatch_block_t block) {
    if ([NSThread isMainThread]) {
        block();
    } else {
        dispatch_async(dispatch_get_main_queue(), block);
    }
}

static uint32_t carbonKeyCodeForASCII(uint8_t key) {
    switch (key) {
        case 'A': return kVK_ANSI_A;
        case 'B': return kVK_ANSI_B;
        case 'C': return kVK_ANSI_C;
        case 'D': return kVK_ANSI_D;
        case 'E': return kVK_ANSI_E;
        case 'F': return kVK_ANSI_F;
        case 'G': return kVK_ANSI_G;
        case 'H': return kVK_ANSI_H;
        case 'I': return kVK_ANSI_I;
        case 'J': return kVK_ANSI_J;
        case 'K': return kVK_ANSI_K;
        case 'L': return kVK_ANSI_L;
        case 'M': return kVK_ANSI_M;
        case 'N': return kVK_ANSI_N;
        case 'O': return kVK_ANSI_O;
        case 'P': return kVK_ANSI_P;
        case 'Q': return kVK_ANSI_Q;
        case 'R': return kVK_ANSI_R;
        case 'S': return kVK_ANSI_S;
        case 'T': return kVK_ANSI_T;
        case 'U': return kVK_ANSI_U;
        case 'V': return kVK_ANSI_V;
        case 'W': return kVK_ANSI_W;
        case 'X': return kVK_ANSI_X;
        case 'Y': return kVK_ANSI_Y;
        case 'Z': return kVK_ANSI_Z;
        case '0': return kVK_ANSI_0;
        case '1': return kVK_ANSI_1;
        case '2': return kVK_ANSI_2;
        case '3': return kVK_ANSI_3;
        case '4': return kVK_ANSI_4;
        case '5': return kVK_ANSI_5;
        case '6': return kVK_ANSI_6;
        case '7': return kVK_ANSI_7;
        case '8': return kVK_ANSI_8;
        case '9': return kVK_ANSI_9;
        default: return kVK_ANSI_C;
    }
}

static OSStatus hotKeyHandler(EventHandlerCallRef nextHandler, EventRef theEvent, void *userData) {
    (void)nextHandler;
    (void)theEvent;
    (void)userData;
    goOnHotkey();
    return noErr;
}

@interface UCAppDelegate : NSObject <NSApplicationDelegate, NSWindowDelegate, WKUIDelegate>
@property (nonatomic, strong) NSStatusItem *statusItem;
@property (nonatomic, strong) NSTimer *pbTimer;
@property (nonatomic, assign) NSInteger lastChangeCount;
@property (nonatomic, strong) NSRunningApplication *previousApp;
@property (nonatomic, strong) NSWindow *viewerWindow;
@property (nonatomic, strong) WKWebView *webView;
@property (nonatomic, strong) NSMenu *popupMenu;
@property (nonatomic, strong) NSMutableArray<NSMenu *> *menuStack;

@property (nonatomic, assign) int onlinePeers;
@property (nonatomic, assign) int totalPeers;
@property (nonatomic, assign) BOOL universal;
@property (nonatomic, assign) BOOL paused;
@property (nonatomic, copy) NSString *hotkeyLabel;

@property (nonatomic, assign) EventHotKeyRef hotKeyRef;
@property (nonatomic, assign) EventHandlerRef hotKeyHandlerRef;
@end

static UCAppDelegate *gDelegate = nil;

@implementation UCAppDelegate

- (void)applicationDidFinishLaunching:(NSNotification *)notification {
    (void)notification;
    [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
    [self setupMainMenu];

    self.menuStack = [NSMutableArray array];
    self.hotkeyLabel = @"Option+C";
    self.universal = YES;

    self.statusItem = [[NSStatusBar systemStatusBar] statusItemWithLength:NSVariableStatusItemLength];
    self.statusItem.button.image = [self makeTrayImage];
    self.statusItem.button.toolTip = @"UnionClipboard";
    [self rebuildTrayMenu];

    self.lastChangeCount = [[NSPasteboard generalPasteboard] changeCount];
    self.pbTimer = [NSTimer scheduledTimerWithTimeInterval:0.20
                                                    target:self
                                                  selector:@selector(pollPasteboard:)
                                                  userInfo:nil
                                                   repeats:YES];
    [[NSRunLoop mainRunLoop] addTimer:self.pbTimer forMode:NSRunLoopCommonModes];
    goOnReady();
}

- (NSImage *)makeTrayImage {
    NSSize sz = NSMakeSize(18, 18);
    NSImage *img = [NSImage imageWithSize:sz flipped:NO drawingHandler:^BOOL(NSRect dstRect) {
        (void)dstRect;
        [[NSColor blackColor] setStroke];
        NSBezierPath *outer = [NSBezierPath bezierPathWithRoundedRect:NSMakeRect(3.5, 2.5, 11, 13) xRadius:2.2 yRadius:2.2];
        [outer setLineWidth:1.5];
        [outer stroke];

        NSBezierPath *clip = [NSBezierPath bezierPathWithRoundedRect:NSMakeRect(6.5, 13.0, 5, 2.5) xRadius:1.0 yRadius:1.0];
        [clip setLineWidth:1.3];
        [clip stroke];

        NSBezierPath *line1 = [NSBezierPath bezierPath];
        [line1 moveToPoint:NSMakePoint(6.2, 9.8)];
        [line1 lineToPoint:NSMakePoint(11.8, 9.8)];
        [line1 setLineWidth:1.3];
        [line1 stroke];

        NSBezierPath *line2 = [NSBezierPath bezierPath];
        [line2 moveToPoint:NSMakePoint(6.2, 6.8)];
        [line2 lineToPoint:NSMakePoint(10.2, 6.8)];
        [line2 setLineWidth:1.3];
        [line2 stroke];
        return YES;
    }];
    [img setTemplate:YES];
    return img;
}

- (void)rebuildTrayMenu {
    NSMenu *menu = [[NSMenu alloc] initWithTitle:@"UnionClipboard"];

    NSString *statusTitle = [NSString stringWithFormat:@"연결된 기기: %d / %d", self.onlinePeers, self.totalPeers];
    NSMenuItem *statusItem = [[NSMenuItem alloc] initWithTitle:statusTitle action:nil keyEquivalent:@""];
    statusItem.enabled = NO;
    [menu addItem:statusItem];
    [menu addItem:[NSMenuItem separatorItem]];

    NSString *popupTitle = [NSString stringWithFormat:@"클립보드 히스토리 열기 (%@)", self.hotkeyLabel ?: @"Option+C"];
    NSMenuItem *popItem = [[NSMenuItem alloc] initWithTitle:popupTitle action:@selector(onTrayPopup:) keyEquivalent:@""];
    popItem.target = self;
    [menu addItem:popItem];

    NSMenuItem *viewerItem = [[NSMenuItem alloc] initWithTitle:@"히스토리 · 템플릿 · 기기 관리..." action:@selector(onTrayViewer:) keyEquivalent:@""];
    viewerItem.target = self;
    [menu addItem:viewerItem];

    [menu addItem:[NSMenuItem separatorItem]];

    NSMenuItem *univItem = [[NSMenuItem alloc] initWithTitle:@"유니버설 클립보드 (자동 반영)" action:@selector(onTrayToggleUniversal:) keyEquivalent:@""];
    univItem.target = self;
    univItem.state = self.universal ? NSControlStateValueOn : NSControlStateValueOff;
    [menu addItem:univItem];

    NSMenuItem *pauseItem = [[NSMenuItem alloc] initWithTitle:@"클립보드 캡처 일시중지" action:@selector(onTrayTogglePause:) keyEquivalent:@""];
    pauseItem.target = self;
    pauseItem.state = self.paused ? NSControlStateValueOn : NSControlStateValueOff;
    [menu addItem:pauseItem];

    [menu addItem:[NSMenuItem separatorItem]];

    NSMenuItem *quitItem = [[NSMenuItem alloc] initWithTitle:@"종료" action:@selector(onTrayQuit:) keyEquivalent:@"q"];
    quitItem.target = self;
    [menu addItem:quitItem];

    self.statusItem.menu = menu;
}

- (void)onTrayPopup:(id)sender { (void)sender; goOnHotkey(); }
- (void)onTrayViewer:(id)sender { (void)sender; goOnOpenViewer(); }
- (void)onTrayToggleUniversal:(id)sender { (void)sender; goOnToggleUniversal(); }
- (void)onTrayTogglePause:(id)sender { (void)sender; goOnTogglePause(); }
- (void)onTrayQuit:(id)sender { (void)sender; goOnQuit(); }

- (void)pollPasteboard:(NSTimer *)timer {
    (void)timer;
    NSPasteboard *pb = [NSPasteboard generalPasteboard];
    NSInteger cc = [pb changeCount];
    if (cc == self.lastChangeCount) {
        return;
    }
    self.lastChangeCount = cc;
    [self captureCurrentPasteboard];
}

- (void)captureCurrentPasteboard {
    NSPasteboard *pb = [NSPasteboard generalPasteboard];
    NSArray<NSPasteboardType> *types = [pb types];

    int sensitive = 0;
    for (NSPasteboardType t in types) {
        if ([t isEqualToString:@"org.nspasteboard.ConcealedType"] ||
            [t isEqualToString:@"org.nspasteboard.TransientType"] ||
            [t isEqualToString:@"org.nspasteboard.AutoGeneratedType"] ||
            [t containsString:@"onepassword"]) {
            sensitive = 1;
            break;
        }
    }

    NSRunningApplication *front = [[NSWorkspace sharedWorkspace] frontmostApplication];
    const char *fgCStr = front.bundleIdentifier ? [front.bundleIdentifier UTF8String] : "";

    // Priority 1: File URLs
    NSDictionary *fileOpts = @{NSPasteboardURLReadingFileURLsOnlyKey: @YES};
    NSArray<NSURL *> *urls = [pb readObjectsForClasses:@[[NSURL class]] options:fileOpts];
    if (urls.count > 0) {
        NSMutableArray<NSString *> *paths = [NSMutableArray arrayWithCapacity:urls.count];
        for (NSURL *u in urls) {
            if (u.isFileURL && u.path.length > 0) {
                [paths addObject:u.path];
            }
        }
        if (paths.count > 0) {
            NSString *joined = [paths componentsJoinedByString:@"\n"];
            goOnClipboardChange(3, NULL, NULL, 0, (char *)[joined UTF8String], (char *)fgCStr, sensitive);
            return;
        }
    }

    // Priority 2: PNG / TIFF Image
    NSData *pngData = [pb dataForType:NSPasteboardTypePNG];
    if (!pngData) {
        NSData *tiffData = [pb dataForType:NSPasteboardTypeTIFF];
        if (tiffData) {
            NSBitmapImageRep *rep = [NSBitmapImageRep imageRepWithData:tiffData];
            if (rep) {
                pngData = [rep representationUsingType:NSBitmapImageFileTypePNG properties:@{}];
            }
        }
    }
    if (pngData.length > 0) {
        goOnClipboardChange(2, NULL, (void *)pngData.bytes, (int)pngData.length, NULL, (char *)fgCStr, sensitive);
        return;
    }

    // Priority 3: String
    NSString *str = [pb stringForType:NSPasteboardTypeString];
    if (str.length > 0) {
        goOnClipboardChange(1, (char *)[str UTF8String], NULL, 0, NULL, (char *)fgCStr, sensitive);
    }
}

- (void)onPopupItemSelected:(NSMenuItem *)sender {
    int tag = (int)sender.tag;
    goOnPopupTag(tag);
}

- (void)setupMainMenu {
    NSMenu *mainMenu = [[NSMenu alloc] init];

    NSMenuItem *appItem = [[NSMenuItem alloc] init];
    [mainMenu addItem:appItem];
    NSMenu *appMenu = [[NSMenu alloc] initWithTitle:@"UnionClipboard"];
    [appMenu addItemWithTitle:@"종료 UnionClipboard" action:@selector(onTrayQuit:) keyEquivalent:@"q"].target = self;
    [appItem setSubmenu:appMenu];

    NSMenuItem *editItem = [[NSMenuItem alloc] init];
    [mainMenu addItem:editItem];
    NSMenu *editMenu = [[NSMenu alloc] initWithTitle:@"편집"];
    [editMenu addItemWithTitle:@"실행 취소" action:@selector(undo:) keyEquivalent:@"z"];
    [editMenu addItemWithTitle:@"다시 실행" action:@selector(redo:) keyEquivalent:@"Z"];
    [editMenu addItem:[NSMenuItem separatorItem]];
    [editMenu addItemWithTitle:@"오려두기" action:@selector(cut:) keyEquivalent:@"x"];
    [editMenu addItemWithTitle:@"복사" action:@selector(copy:) keyEquivalent:@"c"];
    [editMenu addItemWithTitle:@"붙여넣기" action:@selector(paste:) keyEquivalent:@"v"];
    [editMenu addItemWithTitle:@"모두 선택" action:@selector(selectAll:) keyEquivalent:@"a"];
    [editItem setSubmenu:editMenu];

    NSMenuItem *winItem = [[NSMenuItem alloc] init];
    [mainMenu addItem:winItem];
    NSMenu *winMenu = [[NSMenu alloc] initWithTitle:@"창"];
    [winMenu addItemWithTitle:@"창 닫기" action:@selector(performClose:) keyEquivalent:@"w"];
    [winItem setSubmenu:winMenu];

    [NSApp setMainMenu:mainMenu];
}

- (BOOL)applicationShouldHandleReopen:(NSApplication *)sender hasVisibleWindows:(BOOL)flag {
    (void)sender;
    (void)flag;
    goOnOpenViewer();
    return YES;
}

- (BOOL)windowShouldClose:(NSWindow *)sender {
    [sender orderOut:nil];
    [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
    return NO;
}

- (void)webView:(WKWebView *)webView runJavaScriptAlertPanelWithMessage:(NSString *)message initiatedByFrame:(WKFrameInfo *)frame completionHandler:(void (^)(void))completionHandler {
    (void)webView; (void)frame;
    NSAlert *alert = [[NSAlert alloc] init];
    alert.messageText = @"UnionClipboard";
    alert.informativeText = message ?: @"";
    [alert addButtonWithTitle:@"확인"];
    if (self.viewerWindow) {
        [alert beginSheetModalForWindow:self.viewerWindow completionHandler:^(NSModalResponse returnCode) {
            (void)returnCode;
            completionHandler();
        }];
    } else {
        [alert runModal];
        completionHandler();
    }
}

- (void)webView:(WKWebView *)webView runJavaScriptConfirmPanelWithMessage:(NSString *)message initiatedByFrame:(WKFrameInfo *)frame completionHandler:(void (^)(BOOL))completionHandler {
    (void)webView; (void)frame;
    NSAlert *alert = [[NSAlert alloc] init];
    alert.messageText = @"UnionClipboard";
    alert.informativeText = message ?: @"";
    [alert addButtonWithTitle:@"확인"];
    [alert addButtonWithTitle:@"취소"];
    if (self.viewerWindow) {
        [alert beginSheetModalForWindow:self.viewerWindow completionHandler:^(NSModalResponse returnCode) {
            completionHandler(returnCode == NSAlertFirstButtonReturn);
        }];
    } else {
        NSModalResponse r = [alert runModal];
        completionHandler(r == NSAlertFirstButtonReturn);
    }
}

- (void)webView:(WKWebView *)webView runJavaScriptTextInputPanelWithPrompt:(NSString *)prompt defaultText:(NSString *)defaultText initiatedByFrame:(WKFrameInfo *)frame completionHandler:(void (^)(NSString * _Nullable))completionHandler {
    (void)webView; (void)frame;
    NSAlert *alert = [[NSAlert alloc] init];
    alert.messageText = prompt ?: @"입력";
    [alert addButtonWithTitle:@"확인"];
    [alert addButtonWithTitle:@"취소"];
    NSTextField *input = [[NSTextField alloc] initWithFrame:NSMakeRect(0, 0, 280, 24)];
    input.stringValue = defaultText ?: @"";
    alert.accessoryView = input;
    alert.window.initialFirstResponder = input;
    if (self.viewerWindow) {
        [alert beginSheetModalForWindow:self.viewerWindow completionHandler:^(NSModalResponse returnCode) {
            if (returnCode == NSAlertFirstButtonReturn) {
                completionHandler(input.stringValue);
            } else {
                completionHandler(nil);
            }
        }];
    } else {
        NSModalResponse r = [alert runModal];
        completionHandler(r == NSAlertFirstButtonReturn ? input.stringValue : nil);
    }
}

@end

void uc_run_app(void) {
    @autoreleasepool {
        [NSApplication sharedApplication];
        gDelegate = [[UCAppDelegate alloc] init];
        [NSApp setDelegate:gDelegate];
        [NSApp run];
    }
}

void uc_quit_app(void) {
    runOnMainAsync(^{
        if (gDelegate.hotKeyRef) {
            UnregisterEventHotKey(gDelegate.hotKeyRef);
            gDelegate.hotKeyRef = NULL;
        }
        [NSApp terminate:nil];
    });
}

bool uc_register_hotkey(bool ctrl, bool alt, bool shift, bool cmd, uint8_t asciiKey) {
    __block bool ok = false;
    runOnMainSync(^{
        if (!gDelegate) return;
        if (gDelegate.hotKeyRef) {
            UnregisterEventHotKey(gDelegate.hotKeyRef);
            gDelegate.hotKeyRef = NULL;
        }
        if (!gDelegate.hotKeyHandlerRef) {
            EventTypeSpec eventType = {kEventClassKeyboard, kEventHotKeyPressed};
            EventHandlerRef hRef = NULL;
            InstallApplicationEventHandler(&hotKeyHandler, 1, &eventType, NULL, &hRef);
            gDelegate.hotKeyHandlerRef = hRef;
        }
        UInt32 mods = 0;
        if (ctrl) mods |= controlKey;
        if (alt) mods |= optionKey;
        if (shift) mods |= shiftKey;
        if (cmd) mods |= cmdKey;

        EventHotKeyID hkID = {'UCLP', 1};
        EventHotKeyRef hkRef = NULL;
        OSStatus st = RegisterEventHotKey(carbonKeyCodeForASCII(asciiKey), mods, hkID, GetApplicationEventTarget(), 0, &hkRef);
        gDelegate.hotKeyRef = hkRef;
        ok = (st == noErr);
    });
    return ok;
}

void uc_read_clipboard(void) {
    runOnMainSync(^{
        [gDelegate captureCurrentPasteboard];
    });
}

void uc_write_clipboard_text(const char *utf8Text) {
    NSString *str = utf8Text ? [NSString stringWithUTF8String:utf8Text] : @"";
    runOnMainSync(^{
        NSPasteboard *pb = [NSPasteboard generalPasteboard];
        [pb clearContents];
        [pb setString:str forType:NSPasteboardTypeString];
        gDelegate.lastChangeCount = [pb changeCount];
    });
}

void uc_write_clipboard_png(const void *pngBytes, int len) {
    NSData *data = (pngBytes && len > 0) ? [NSData dataWithBytes:pngBytes length:(NSUInteger)len] : nil;
    if (!data) return;
    runOnMainSync(^{
        NSPasteboard *pb = [NSPasteboard generalPasteboard];
        [pb clearContents];
        [pb setData:data forType:NSPasteboardTypePNG];
        NSImage *img = [[NSImage alloc] initWithData:data];
        if (img) {
            NSData *tiff = [img TIFFRepresentation];
            if (tiff) {
                [pb setData:tiff forType:NSPasteboardTypeTIFF];
            }
        }
        gDelegate.lastChangeCount = [pb changeCount];
    });
}

void uc_write_clipboard_files(const char **paths, int count) {
    if (!paths || count <= 0) return;
    NSMutableArray<NSURL *> *urls = [NSMutableArray arrayWithCapacity:(NSUInteger)count];
    for (int i = 0; i < count; i++) {
        if (paths[i]) {
            NSString *p = [NSString stringWithUTF8String:paths[i]];
            if (p.length > 0) {
                [urls addObject:[NSURL fileURLWithPath:p]];
            }
        }
    }
    if (urls.count == 0) return;
    runOnMainSync(^{
        NSPasteboard *pb = [NSPasteboard generalPasteboard];
        [pb clearContents];
        [pb writeObjects:urls];
        gDelegate.lastChangeCount = [pb changeCount];
    });
}

void uc_popup_reset(void) {
    runOnMainSync(^{
        gDelegate.popupMenu = [[NSMenu alloc] initWithTitle:@"UnionClipboard"];
        [gDelegate.popupMenu setAutoenablesItems:NO];
        [gDelegate.menuStack removeAllObjects];
        [gDelegate.menuStack addObject:gDelegate.popupMenu];
    });
}

void uc_popup_add_item(int tag, const char *title, const void *thumbPNG, int thumbLen, const char *keyEquiv, bool enabled) {
    NSString *nsTitle = title ? [NSString stringWithUTF8String:title] : @"";
    NSString *nsKey = keyEquiv ? [NSString stringWithUTF8String:keyEquiv] : @"";
    NSData *imgData = (thumbPNG && thumbLen > 0) ? [NSData dataWithBytes:thumbPNG length:(NSUInteger)thumbLen] : nil;
    runOnMainSync(^{
        NSMenu *cur = gDelegate.menuStack.lastObject ?: gDelegate.popupMenu;
        NSMenuItem *item = [[NSMenuItem alloc] initWithTitle:nsTitle
                                                      action:(enabled ? @selector(onPopupItemSelected:) : nil)
                                               keyEquivalent:nsKey];
        item.keyEquivalentModifierMask = 0;
        item.target = gDelegate;
        item.tag = tag;
        item.enabled = enabled;
        if (imgData) {
            NSImage *img = [[NSImage alloc] initWithData:imgData];
            if (img) {
                [img setSize:NSMakeSize(22, 22)];
                item.image = img;
            }
        }
        [cur addItem:item];
    });
}

void uc_popup_add_separator(void) {
    runOnMainSync(^{
        NSMenu *cur = gDelegate.menuStack.lastObject ?: gDelegate.popupMenu;
        [cur addItem:[NSMenuItem separatorItem]];
    });
}

void uc_popup_begin_submenu(const char *title) {
    NSString *nsTitle = title ? [NSString stringWithUTF8String:title] : @"";
    runOnMainSync(^{
        NSMenu *parent = gDelegate.menuStack.lastObject ?: gDelegate.popupMenu;
        NSMenuItem *holder = [[NSMenuItem alloc] initWithTitle:nsTitle action:nil keyEquivalent:@""];
        NSMenu *sub = [[NSMenu alloc] initWithTitle:nsTitle];
        [sub setAutoenablesItems:NO];
        [parent addItem:holder];
        [parent setSubmenu:sub forItem:holder];
        [gDelegate.menuStack addObject:sub];
    });
}

void uc_popup_end_submenu(void) {
    runOnMainSync(^{
        if (gDelegate.menuStack.count > 1) {
            [gDelegate.menuStack removeLastObject];
        }
    });
}

void uc_popup_show(void) {
    runOnMainAsync(^{
        gDelegate.previousApp = [[NSWorkspace sharedWorkspace] frontmostApplication];
        NSPoint loc = [NSEvent mouseLocation];
        [gDelegate.popupMenu popUpMenuPositioningItem:nil atLocation:loc inView:nil];
    });
}

void uc_send_paste(void) {
    runOnMainAsync(^{
        if (gDelegate.previousApp && !gDelegate.previousApp.terminated) {
            [gDelegate.previousApp activateWithOptions:0];
        }
        dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(70 * NSEC_PER_MSEC)), dispatch_get_main_queue(), ^{
            if (!AXIsProcessTrusted()) {
                return;
            }
            CGEventSourceRef src = CGEventSourceCreate(kCGEventSourceStateHIDSystemState);
            if (!src) return;
            CGEventRef down = CGEventCreateKeyboardEvent(src, (CGKeyCode)kVK_ANSI_V, true);
            CGEventRef up = CGEventCreateKeyboardEvent(src, (CGKeyCode)kVK_ANSI_V, false);
            CGEventSetFlags(down, kCGEventFlagMaskCommand);
            CGEventSetFlags(up, kCGEventFlagMaskCommand);
            CGEventPost(kCGHIDEventTap, down);
            CGEventPost(kCGHIDEventTap, up);
            CFRelease(down);
            CFRelease(up);
            CFRelease(src);
        });
    });
}

void uc_open_viewer(const char *url) {
    NSString *nsURL = url ? [NSString stringWithUTF8String:url] : @"";
    runOnMainAsync(^{
        [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
        if (!gDelegate.viewerWindow) {
            NSRect frame = NSMakeRect(0, 0, 1080, 740);
            NSWindowStyleMask style = NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable;
            gDelegate.viewerWindow = [[NSWindow alloc] initWithContentRect:frame styleMask:style backing:NSBackingStoreBuffered defer:NO];
            gDelegate.viewerWindow.title = @"UnionClipboard";
            gDelegate.viewerWindow.delegate = gDelegate;
            [gDelegate.viewerWindow center];

            WKWebViewConfiguration *cfg = [[WKWebViewConfiguration alloc] init];
            gDelegate.webView = [[WKWebView alloc] initWithFrame:frame configuration:cfg];
            gDelegate.webView.UIDelegate = gDelegate;
            gDelegate.webView.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
            [gDelegate.viewerWindow setContentView:gDelegate.webView];

            NSURL *u = [NSURL URLWithString:nsURL];
            if (u) {
                [gDelegate.webView loadRequest:[NSURLRequest requestWithURL:u]];
            }
        }
        [gDelegate.viewerWindow makeKeyAndOrderFront:nil];
        [gDelegate.viewerWindow orderFrontRegardless];
        [NSApp activateIgnoringOtherApps:YES];
    });
}

void uc_update_tray(int onlinePeers, int totalPeers, bool universal, bool paused, const char *hotkey) {
    NSString *hk = hotkey ? [NSString stringWithUTF8String:hotkey] : @"Option+C";
    runOnMainAsync(^{
        gDelegate.onlinePeers = onlinePeers;
        gDelegate.totalPeers = totalPeers;
        gDelegate.universal = universal;
        gDelegate.paused = paused;
        gDelegate.hotkeyLabel = hk;
        [gDelegate rebuildTrayMenu];
    });
}

void uc_show_notification(const char *title, const char *body) {
    NSString *nsTitle = title ? [NSString stringWithUTF8String:title] : @"UnionClipboard";
    NSString *nsBody = body ? [NSString stringWithUTF8String:body] : @"";
    runOnMainAsync(^{
        if (![[[NSBundle mainBundle] bundlePath] hasSuffix:@".app"]) {
            return;
        }
        UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];
        [center requestAuthorizationWithOptions:(UNAuthorizationOptionAlert) completionHandler:^(BOOL granted, NSError *err) {
            (void)err;
            if (!granted) return;
            UNMutableNotificationContent *content = [[UNMutableNotificationContent alloc] init];
            content.title = nsTitle;
            content.body = nsBody;
            UNNotificationRequest *req = [UNNotificationRequest requestWithIdentifier:[[NSUUID UUID] UUIDString] content:content trigger:nil];
            [center addNotificationRequest:req withCompletionHandler:nil];
        }];
    });
}

bool uc_has_accessibility(void) {
    return AXIsProcessTrusted();
}

void uc_request_accessibility(void) {
    runOnMainAsync(^{
        NSDictionary *opts = @{(__bridge NSString *)kAXTrustedCheckOptionPrompt: @YES};
        AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)opts);
    });
}

void uc_reveal_paths(const char **paths, int count) {
    if (!paths || count <= 0) return;
    NSMutableArray<NSURL *> *urls = [NSMutableArray array];
    for (int i = 0; i < count; i++) {
        if (paths[i]) {
            NSString *p = [NSString stringWithUTF8String:paths[i]];
            if (p.length > 0) {
                [urls addObject:[NSURL fileURLWithPath:p]];
            }
        }
    }
    if (urls.count == 0) return;
    runOnMainAsync(^{
        [[NSWorkspace sharedWorkspace] activateFileViewerSelectingURLs:urls];
    });
}
