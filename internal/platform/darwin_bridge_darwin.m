#import <Cocoa/Cocoa.h>
#import <Carbon/Carbon.h>
#import <WebKit/WebKit.h>
#import <ApplicationServices/ApplicationServices.h>
#import <UserNotifications/UserNotifications.h>
#include <unistd.h>
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

static uint32_t carbonKeyCodeForName(const char *keyName) {
    if (!keyName || keyName[0] == '\0') return kVK_ANSI_V;
    if (strcmp(keyName, "Space") == 0 || strcmp(keyName, "SPACE") == 0) return kVK_Space;
    if (strcmp(keyName, "F1") == 0) return kVK_F1;
    if (strcmp(keyName, "F2") == 0) return kVK_F2;
    if (strcmp(keyName, "F3") == 0) return kVK_F3;
    if (strcmp(keyName, "F4") == 0) return kVK_F4;
    if (strcmp(keyName, "F5") == 0) return kVK_F5;
    if (strcmp(keyName, "F6") == 0) return kVK_F6;
    if (strcmp(keyName, "F7") == 0) return kVK_F7;
    if (strcmp(keyName, "F8") == 0) return kVK_F8;
    if (strcmp(keyName, "F9") == 0) return kVK_F9;
    if (strcmp(keyName, "F10") == 0) return kVK_F10;
    if (strcmp(keyName, "F11") == 0) return kVK_F11;
    if (strcmp(keyName, "F12") == 0) return kVK_F12;
    switch (keyName[0]) {
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
        default: return kVK_ANSI_V;
    }
}

// Physical ANSI keyCode -> lowercase ASCII so shortcuts work regardless of Korean/English IME
static NSString *physicalKeyCharForEvent(NSEvent *event) {
    switch (event.keyCode) {
        case kVK_ANSI_0: case kVK_ANSI_Keypad0: return @"0";
        case kVK_ANSI_1: case kVK_ANSI_Keypad1: return @"1";
        case kVK_ANSI_2: case kVK_ANSI_Keypad2: return @"2";
        case kVK_ANSI_3: case kVK_ANSI_Keypad3: return @"3";
        case kVK_ANSI_4: case kVK_ANSI_Keypad4: return @"4";
        case kVK_ANSI_5: case kVK_ANSI_Keypad5: return @"5";
        case kVK_ANSI_6: case kVK_ANSI_Keypad6: return @"6";
        case kVK_ANSI_7: case kVK_ANSI_Keypad7: return @"7";
        case kVK_ANSI_8: case kVK_ANSI_Keypad8: return @"8";
        case kVK_ANSI_9: case kVK_ANSI_Keypad9: return @"9";
        case kVK_ANSI_A: return @"a";
        case kVK_ANSI_B: return @"b";
        case kVK_ANSI_C: return @"c";
        case kVK_ANSI_D: return @"d";
        case kVK_ANSI_E: return @"e";
        case kVK_ANSI_F: return @"f";
        case kVK_ANSI_G: return @"g";
        case kVK_ANSI_H: return @"h";
        case kVK_ANSI_I: return @"i";
        case kVK_ANSI_J: return @"j";
        case kVK_ANSI_K: return @"k";
        case kVK_ANSI_L: return @"l";
        case kVK_ANSI_M: return @"m";
        case kVK_ANSI_N: return @"n";
        case kVK_ANSI_O: return @"o";
        case kVK_ANSI_P: return @"p";
        case kVK_ANSI_Q: return @"q";
        case kVK_ANSI_R: return @"r";
        case kVK_ANSI_S: return @"s";
        case kVK_ANSI_T: return @"t";
        case kVK_ANSI_U: return @"u";
        case kVK_ANSI_V: return @"v";
        case kVK_ANSI_W: return @"w";
        case kVK_ANSI_X: return @"x";
        case kVK_ANSI_Y: return @"y";
        case kVK_ANSI_Z: return @"z";
        default: return nil;
    }
}

static OSStatus hotKeyHandler(EventHandlerCallRef nextHandler, EventRef theEvent, void *userData) {
    (void)nextHandler;
    (void)theEvent;
    (void)userData;
    goOnHotkey();
    return noErr;
}

@interface UCPopupEntry : NSObject
@property (nonatomic, assign) int tag;
@property (nonatomic, copy) NSString *title;
@property (nonatomic, copy) NSString *keyEquiv;
@property (nonatomic, assign) BOOL enabled;
@property (nonatomic, assign) BOOL isSeparator;
@property (nonatomic, strong) NSImage *thumb;
@property (nonatomic, strong) NSMutableArray<UCPopupEntry *> *children;
- (BOOL)isSubmenu;
@end

@implementation UCPopupEntry
- (BOOL)isSubmenu {
    return self.children != nil;
}
@end

@class UCPopupPanel;
@class UCAppDelegate;
static UCAppDelegate *gDelegate = nil;

@interface UCPopupMenuView : NSView
@property (nonatomic, weak) UCPopupPanel *ownerPanel;
@property (nonatomic, strong) NSArray<UCPopupEntry *> *entries;
@property (nonatomic, strong) NSMutableArray<NSValue *> *rowRects;
@property (nonatomic, assign) NSInteger highlightedIndex;
@property (nonatomic, assign) CGFloat preferredWidth;
@property (nonatomic, assign) CGFloat preferredHeight;
@property (nonatomic, strong) NSTrackingArea *trackingArea;
- (instancetype)initWithEntries:(NSArray<UCPopupEntry *> *)entries owner:(UCPopupPanel *)owner;
- (void)moveHighlightBy:(NSInteger)delta;
- (NSRect)rectForRow:(NSInteger)idx;
@end

@interface UCPopupPanel : NSPanel
@property (nonatomic, assign) NSInteger levelIndex;
@property (nonatomic, strong) UCPopupMenuView *menuView;
@property (nonatomic, strong) NSScrollView *scrollView;
@end

@interface UCAppDelegate : NSObject <NSApplicationDelegate, NSWindowDelegate, WKUIDelegate>
@property (nonatomic, strong) NSStatusItem *statusItem;
@property (nonatomic, strong) NSTimer *pbTimer;
@property (nonatomic, assign) NSInteger lastChangeCount;
@property (nonatomic, strong) NSRunningApplication *lastExternalApp;
@property (nonatomic, strong) NSWindow *viewerWindow;
@property (nonatomic, strong) WKWebView *webView;

@property (nonatomic, strong) NSMutableArray<UCPopupEntry *> *rootEntries;
@property (nonatomic, strong) NSMutableArray<NSMutableArray<UCPopupEntry *> *> *entryBuildStack;
@property (nonatomic, strong) NSMutableArray<UCPopupPanel *> *panelStack;
@property (nonatomic, strong) id globalClickMonitor;
@property (nonatomic, assign) BOOL axPromptedOnce;

@property (nonatomic, assign) int onlinePeers;
@property (nonatomic, assign) int totalPeers;
@property (nonatomic, assign) BOOL universal;
@property (nonatomic, assign) BOOL paused;
@property (nonatomic, copy) NSString *hotkeyLabel;

@property (nonatomic, assign) EventHotKeyRef hotKeyRef;
@property (nonatomic, assign) EventHandlerRef hotKeyHandlerRef;

- (void)dismissAllPopupsAndRestoreFocus:(BOOL)restore;
- (void)openSubmenuForEntry:(UCPopupEntry *)entry atRowRect:(NSRect)rowScreenRect fromLevel:(NSInteger)level;
- (void)closePanelsAboveLevel:(NSInteger)level;
- (void)triggerLeafEntry:(UCPopupEntry *)entry;
@end

static NSString *stripShortcutPrefix(NSString *title, NSString *keyEquiv) {
    if (!title) return @"";
    if (keyEquiv.length > 0) {
        NSString *prefix = [NSString stringWithFormat:@"%@  ", [keyEquiv uppercaseString]];
        if ([title hasPrefix:prefix]) {
            return [title substringFromIndex:prefix.length];
        }
    }
    return title;
}

@implementation UCPopupMenuView

- (BOOL)isFlipped { return YES; }

- (instancetype)initWithEntries:(NSArray<UCPopupEntry *> *)entries owner:(UCPopupPanel *)owner {
    self = [super initWithFrame:NSZeroRect];
    if (self) {
        _entries = entries ?: @[];
        _ownerPanel = owner;
        _highlightedIndex = -1;
        _rowRects = [NSMutableArray arrayWithCapacity:_entries.count];

        NSDictionary *titleAttrs = @{NSFontAttributeName: [NSFont systemFontOfSize:13 weight:NSFontWeightRegular]};
        CGFloat maxTitleW = 140;
        BOOL hasAnyKey = NO;
        BOOL hasAnyThumb = NO;
        BOOL hasAnySub = NO;
        for (UCPopupEntry *e in _entries) {
            if (e.isSeparator) continue;
            if (e.keyEquiv.length > 0) hasAnyKey = YES;
            if (e.thumb) hasAnyThumb = YES;
            if (e.isSubmenu) hasAnySub = YES;
            NSString *disp = stripShortcutPrefix(e.title, e.keyEquiv);
            NSSize sz = [disp sizeWithAttributes:titleAttrs];
            if (sz.width > maxTitleW) maxTitleW = sz.width;
        }
        if (maxTitleW > 300) maxTitleW = 300;

        CGFloat w = 14 + (hasAnyKey ? 22 : 0) + (hasAnyThumb ? 26 : 0) + maxTitleW + (hasAnySub ? 22 : 0) + 16;
        if (w < 240) w = 240;
        if (w > 430) w = 430;
        _preferredWidth = w;

        CGFloat y = 6;
        for (UCPopupEntry *e in _entries) {
            CGFloat rh = e.isSeparator ? 11.0 : 28.0;
            [_rowRects addObject:[NSValue valueWithRect:NSMakeRect(6, y, w - 12, rh)]];
            y += rh;
        }
        _preferredHeight = y + 6;
        self.frame = NSMakeRect(0, 0, _preferredWidth, _preferredHeight);

        for (NSInteger i = 0; i < (NSInteger)_entries.count; i++) {
            UCPopupEntry *e = _entries[i];
            if (!e.isSeparator && e.enabled) {
                _highlightedIndex = i;
                break;
            }
        }
    }
    return self;
}

- (void)updateTrackingAreas {
    [super updateTrackingAreas];
    if (self.trackingArea) {
        [self removeTrackingArea:self.trackingArea];
    }
    self.trackingArea = [[NSTrackingArea alloc] initWithRect:self.bounds
                                                     options:(NSTrackingMouseMoved | NSTrackingMouseEnteredAndExited | NSTrackingActiveAlways | NSTrackingInVisibleRect)
                                                       owner:self
                                                    userInfo:nil];
    [self addTrackingArea:self.trackingArea];
}

- (NSRect)rectForRow:(NSInteger)idx {
    if (idx < 0 || idx >= (NSInteger)self.rowRects.count) return NSZeroRect;
    return [self.rowRects[idx] rectValue];
}

- (NSInteger)rowAtPoint:(NSPoint)pt {
    for (NSInteger i = 0; i < (NSInteger)self.rowRects.count; i++) {
        if (NSPointInRect(pt, [self.rowRects[i] rectValue])) {
            return i;
        }
    }
    return -1;
}

- (void)moveHighlightBy:(NSInteger)delta {
    NSInteger count = (NSInteger)self.entries.count;
    if (count == 0) return;
    NSInteger idx = self.highlightedIndex;
    for (NSInteger step = 0; step < count; step++) {
        if (idx < 0) {
            idx = (delta > 0) ? 0 : count - 1;
        } else {
            idx = (idx + delta + count) % count;
        }
        UCPopupEntry *e = self.entries[idx];
        if (!e.isSeparator && e.enabled) {
            self.highlightedIndex = idx;
            [self scrollRectToVisible:[self rectForRow:idx]];
            [self setNeedsDisplay:YES];
            return;
        }
    }
}

- (void)mouseMoved:(NSEvent *)event {
    NSPoint pt = [self convertPoint:event.locationInWindow fromView:nil];
    NSInteger idx = [self rowAtPoint:pt];
    if (idx != self.highlightedIndex) {
        if (idx >= 0) {
            UCPopupEntry *e = self.entries[idx];
            if (!e.isSeparator && e.enabled) {
                self.highlightedIndex = idx;
                [self setNeedsDisplay:YES];
                if (e.isSubmenu) {
                    NSRect r = [self rectForRow:idx];
                    NSRect winR = [self convertRect:r toView:nil];
                    NSRect scrR = [self.ownerPanel convertRectToScreen:winR];
                    [gDelegate openSubmenuForEntry:e atRowRect:scrR fromLevel:self.ownerPanel.levelIndex];
                } else {
                    [gDelegate closePanelsAboveLevel:self.ownerPanel.levelIndex];
                }
            }
        }
    }
}

- (void)mouseDown:(NSEvent *)event {
    (void)event;
}

- (void)mouseUp:(NSEvent *)event {
    NSPoint pt = [self convertPoint:event.locationInWindow fromView:nil];
    NSInteger idx = [self rowAtPoint:pt];
    if (idx < 0 || idx >= (NSInteger)self.entries.count) return;
    UCPopupEntry *e = self.entries[idx];
    if (e.isSeparator || !e.enabled) return;

    if (e.isSubmenu) {
        self.highlightedIndex = idx;
        [self setNeedsDisplay:YES];
        NSRect r = [self rectForRow:idx];
        NSRect winR = [self convertRect:r toView:nil];
        NSRect scrR = [self.ownerPanel convertRectToScreen:winR];
        [gDelegate openSubmenuForEntry:e atRowRect:scrR fromLevel:self.ownerPanel.levelIndex];
    } else {
        [gDelegate triggerLeafEntry:e];
    }
}

- (void)drawRect:(NSRect)dirtyRect {
    (void)dirtyRect;
    for (NSInteger i = 0; i < (NSInteger)self.entries.count; i++) {
        UCPopupEntry *e = self.entries[i];
        NSRect r = [self.rowRects[i] rectValue];
        if (e.isSeparator) {
            [[NSColor separatorColor] setFill];
            NSRectFill(NSMakeRect(r.origin.x + 6, NSMidY(r), r.size.width - 12, 1));
            continue;
        }

        BOOL active = (i == self.highlightedIndex && e.enabled);
        if (active) {
            [[NSColor selectedContentBackgroundColor] setFill];
            [[NSBezierPath bezierPathWithRoundedRect:r xRadius:6 yRadius:6] fill];
        }

        NSColor *textColor = active
            ? [NSColor alternateSelectedControlTextColor]
            : (e.enabled ? [NSColor labelColor] : [NSColor disabledControlTextColor]);
        NSColor *keyColor = active
            ? [[NSColor alternateSelectedControlTextColor] colorWithAlphaComponent:0.85]
            : [NSColor secondaryLabelColor];

        CGFloat x = r.origin.x + 8;
        if (e.keyEquiv.length > 0) {
            NSString *kStr = [e.keyEquiv uppercaseString];
            NSDictionary *kAttrs = @{
                NSFontAttributeName: [NSFont monospacedSystemFontOfSize:11.5 weight:NSFontWeightSemibold],
                NSForegroundColorAttributeName: keyColor
            };
            NSSize kSz = [kStr sizeWithAttributes:kAttrs];
            [kStr drawAtPoint:NSMakePoint(x + (14 - kSz.width) * 0.5, NSMidY(r) - kSz.height * 0.5) withAttributes:kAttrs];
            x += 20;
        }

        if (e.thumb) {
            NSRect thumbRect = NSMakeRect(x, NSMidY(r) - 10, 20, 20);
            [NSGraphicsContext saveGraphicsState];
            [[NSBezierPath bezierPathWithRoundedRect:thumbRect xRadius:3 yRadius:3] addClip];
            [e.thumb drawInRect:thumbRect fromRect:NSZeroRect operation:NSCompositingOperationSourceOver fraction:1.0 respectFlipped:YES hints:nil];
            [NSGraphicsContext restoreGraphicsState];
            x += 26;
        }

        CGFloat rightPad = e.isSubmenu ? 22.0 : 8.0;
        CGFloat availW = NSMaxX(r) - rightPad - x;
        if (availW < 20) availW = 20;

        NSString *disp = stripShortcutPrefix(e.title, e.keyEquiv);
        NSMutableParagraphStyle *ps = [[NSMutableParagraphStyle alloc] init];
        ps.lineBreakMode = NSLineBreakByTruncatingTail;
        NSDictionary *tAttrs = @{
            NSFontAttributeName: [NSFont systemFontOfSize:13 weight:NSFontWeightRegular],
            NSForegroundColorAttributeName: textColor,
            NSParagraphStyleAttributeName: ps
        };
        NSSize tSz = [disp sizeWithAttributes:tAttrs];
        [disp drawInRect:NSMakeRect(x, NSMidY(r) - tSz.height * 0.5, availW, tSz.height + 2) withAttributes:tAttrs];

        if (e.isSubmenu) {
            NSString *arrow = @"▸";
            NSDictionary *aAttrs = @{
                NSFontAttributeName: [NSFont systemFontOfSize:12 weight:NSFontWeightSemibold],
                NSForegroundColorAttributeName: keyColor
            };
            NSSize aSz = [arrow sizeWithAttributes:aAttrs];
            [arrow drawAtPoint:NSMakePoint(NSMaxX(r) - 15, NSMidY(r) - aSz.height * 0.5) withAttributes:aAttrs];
        }
    }
}

@end

@implementation UCPopupPanel

- (BOOL)canBecomeKeyWindow { return YES; }
- (BOOL)canBecomeMainWindow { return NO; }
- (BOOL)acceptsFirstResponder { return YES; }

- (void)keyDown:(NSEvent *)event {
    if (event.keyCode == kVK_Escape) {
        [gDelegate dismissAllPopupsAndRestoreFocus:YES];
        return;
    }
    if (event.keyCode == kVK_UpArrow) {
        [self.menuView moveHighlightBy:-1];
        return;
    }
    if (event.keyCode == kVK_DownArrow) {
        [self.menuView moveHighlightBy:1];
        return;
    }
    if (event.keyCode == kVK_LeftArrow || event.keyCode == kVK_Delete) {
        if (self.levelIndex > 0) {
            [gDelegate closePanelsAboveLevel:self.levelIndex - 1];
            if (gDelegate.panelStack.lastObject) {
                [gDelegate.panelStack.lastObject makeKeyAndOrderFront:nil];
            }
        }
        return;
    }
    if (event.keyCode == kVK_RightArrow) {
        NSInteger hi = self.menuView.highlightedIndex;
        if (hi >= 0 && hi < (NSInteger)self.menuView.entries.count) {
            UCPopupEntry *e = self.menuView.entries[hi];
            if (e.enabled && e.isSubmenu) {
                NSRect r = [self.menuView rectForRow:hi];
                NSRect winR = [self.menuView convertRect:r toView:nil];
                NSRect scrR = [self convertRectToScreen:winR];
                [gDelegate openSubmenuForEntry:e atRowRect:scrR fromLevel:self.levelIndex];
            }
        }
        return;
    }
    if (event.keyCode == kVK_Return || event.keyCode == kVK_ANSI_KeypadEnter) {
        NSInteger hi = self.menuView.highlightedIndex;
        if (hi >= 0 && hi < (NSInteger)self.menuView.entries.count) {
            UCPopupEntry *e = self.menuView.entries[hi];
            if (!e.isSeparator && e.enabled) {
                if (e.isSubmenu) {
                    NSRect r = [self.menuView rectForRow:hi];
                    NSRect winR = [self.menuView convertRect:r toView:nil];
                    NSRect scrR = [self convertRectToScreen:winR];
                    [gDelegate openSubmenuForEntry:e atRowRect:scrR fromLevel:self.levelIndex];
                } else {
                    [gDelegate triggerLeafEntry:e];
                }
            }
        }
        return;
    }

    NSEventModifierFlags badMods = NSEventModifierFlagCommand | NSEventModifierFlagControl | NSEventModifierFlagOption;
    if ((event.modifierFlags & badMods) != 0) {
        return;
    }

    NSString *phys = physicalKeyCharForEvent(event);
    if (!phys) return;

    // Match shortcut ONLY within THIS active panel's entries
    NSArray<UCPopupEntry *> *entries = self.menuView.entries;
    for (NSInteger i = 0; i < (NSInteger)entries.count; i++) {
        UCPopupEntry *e = entries[i];
        if (!e.isSeparator && e.enabled && e.keyEquiv.length > 0 && [e.keyEquiv caseInsensitiveCompare:phys] == NSOrderedSame) {
            if (e.isSubmenu) {
                self.menuView.highlightedIndex = i;
                [self.menuView setNeedsDisplay:YES];
                NSRect r = [self.menuView rectForRow:i];
                NSRect winR = [self.menuView convertRect:r toView:nil];
                NSRect scrR = [self convertRectToScreen:winR];
                [gDelegate openSubmenuForEntry:e atRowRect:scrR fromLevel:self.levelIndex];
            } else {
                [gDelegate triggerLeafEntry:e];
            }
            return;
        }
    }
}

@end

@implementation UCAppDelegate

- (void)applicationDidFinishLaunching:(NSNotification *)notification {
    (void)notification;
    [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
    [self setupMainMenu];

    self.entryBuildStack = [NSMutableArray array];
    self.panelStack = [NSMutableArray array];
    self.hotkeyLabel = @"Option+V";
    self.universal = YES;

    pid_t selfPID = [[NSProcessInfo processInfo] processIdentifier];
    NSRunningApplication *front = [[NSWorkspace sharedWorkspace] frontmostApplication];
    if (front && front.processIdentifier != selfPID) {
        self.lastExternalApp = front;
    }
    [[[NSWorkspace sharedWorkspace] notificationCenter] addObserver:self
                                                           selector:@selector(onWorkspaceAppActivated:)
                                                               name:NSWorkspaceDidActivateApplicationNotification
                                                             object:nil];

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

- (void)onWorkspaceAppActivated:(NSNotification *)note {
    NSRunningApplication *app = note.userInfo[NSWorkspaceApplicationKey];
    if (!app) return;
    pid_t selfPID = [[NSProcessInfo processInfo] processIdentifier];
    if (app.processIdentifier != selfPID) {
        self.lastExternalApp = app;
    }
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

    NSString *popupTitle = [NSString stringWithFormat:@"클립보드 히스토리 열기 (%@)", self.hotkeyLabel ?: @"Option+V"];
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

    NSString *str = [pb stringForType:NSPasteboardTypeString];
    if (str.length > 0) {
        goOnClipboardChange(1, (char *)[str UTF8String], NULL, 0, NULL, (char *)fgCStr, sensitive);
    }
}

- (UCPopupPanel *)createPanelWithEntries:(NSArray<UCPopupEntry *> *)entries level:(NSInteger)level anchor:(NSPoint)anchor parentRect:(NSRect)parentRect {
    UCPopupPanel *panel = [[UCPopupPanel alloc] initWithContentRect:NSMakeRect(0, 0, 280, 200)
                                                          styleMask:NSWindowStyleMaskBorderless
                                                            backing:NSBackingStoreBuffered
                                                              defer:NO];
    panel.level = NSStatusWindowLevel + level;
    panel.opaque = NO;
    panel.backgroundColor = [NSColor clearColor];
    panel.hasShadow = YES;
    panel.collectionBehavior = NSWindowCollectionBehaviorCanJoinAllSpaces | NSWindowCollectionBehaviorFullScreenAuxiliary;
    panel.levelIndex = level;

    UCPopupMenuView *mv = [[UCPopupMenuView alloc] initWithEntries:entries owner:panel];
    panel.menuView = mv;

    NSScreen *screen = [NSScreen mainScreen];
    for (NSScreen *sc in [NSScreen screens]) {
        if (NSPointInRect(anchor, sc.frame)) {
            screen = sc;
            break;
        }
    }
    NSRect vis = screen ? screen.visibleFrame : NSMakeRect(0, 0, 1440, 900);
    CGFloat maxH = vis.size.height - 24;
    CGFloat pw = mv.preferredWidth;
    CGFloat ph = mv.preferredHeight;
    if (ph > maxH) ph = maxH;

    NSVisualEffectView *vev = [[NSVisualEffectView alloc] initWithFrame:NSMakeRect(0, 0, pw, ph)];
    vev.material = NSVisualEffectMaterialMenu;
    vev.state = NSVisualEffectStateActive;
    vev.blendingMode = NSVisualEffectBlendingModeBehindWindow;
    vev.wantsLayer = YES;
    vev.layer.cornerRadius = 10.0;
    vev.layer.masksToBounds = YES;
    vev.layer.borderWidth = 0.5;
    vev.layer.borderColor = [[NSColor separatorColor] CGColor];

    NSScrollView *sv = [[NSScrollView alloc] initWithFrame:NSMakeRect(0, 0, pw, ph)];
    sv.drawsBackground = NO;
    sv.hasVerticalScroller = (mv.preferredHeight > ph);
    sv.borderType = NSNoBorder;
    sv.documentView = mv;
    panel.scrollView = sv;
    [vev addSubview:sv];
    panel.contentView = vev;

    CGFloat x, y;
    if (level == 0) {
        x = anchor.x;
        y = anchor.y - ph;
    } else {
        x = NSMaxX(parentRect) - 2;
        if (x + pw > NSMaxX(vis)) {
            x = NSMinX(parentRect) - pw + 2;
        }
        y = NSMaxY(parentRect) - ph + 6;
    }
    if (x + pw > NSMaxX(vis)) x = NSMaxX(vis) - pw - 6;
    if (x < NSMinX(vis)) x = NSMinX(vis) + 6;
    if (y < NSMinY(vis)) y = NSMinY(vis) + 6;
    if (y + ph > NSMaxY(vis)) y = NSMaxY(vis) - ph - 6;

    [panel setFrame:NSMakeRect(x, y, pw, ph) display:YES];
    return panel;
}

- (void)closePanelsAboveLevel:(NSInteger)level {
    while ((NSInteger)self.panelStack.count > level + 1) {
        UCPopupPanel *p = self.panelStack.lastObject;
        [self.panelStack removeLastObject];
        [p orderOut:nil];
    }
}

- (void)openSubmenuForEntry:(UCPopupEntry *)entry atRowRect:(NSRect)rowScreenRect fromLevel:(NSInteger)level {
    [self closePanelsAboveLevel:level];
    if (!entry.children || entry.children.count == 0) return;

    UCPopupPanel *sub = [self createPanelWithEntries:entry.children
                                               level:level + 1
                                              anchor:rowScreenRect.origin
                                          parentRect:rowScreenRect];
    [self.panelStack addObject:sub];
    [sub makeKeyAndOrderFront:nil];
}

- (void)dismissAllPopupsAndRestoreFocus:(BOOL)restore {
    if (self.globalClickMonitor) {
        [NSEvent removeMonitor:self.globalClickMonitor];
        self.globalClickMonitor = nil;
    }
    for (UCPopupPanel *p in self.panelStack) {
        [p orderOut:nil];
    }
    [self.panelStack removeAllObjects];
    if (restore && self.lastExternalApp && !self.lastExternalApp.terminated) {
        [self.lastExternalApp activateWithOptions:NSApplicationActivateIgnoringOtherApps];
    }
}

- (void)triggerLeafEntry:(UCPopupEntry *)entry {
    int tag = entry.tag;
    [self dismissAllPopupsAndRestoreFocus:YES];
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

bool uc_register_hotkey(bool ctrl, bool alt, bool shift, bool cmd, const char *keyName) {
    NSString *nsKey = keyName ? [NSString stringWithUTF8String:keyName] : @"V";
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
        OSStatus st = RegisterEventHotKey(carbonKeyCodeForName([nsKey UTF8String]), mods, hkID, GetApplicationEventTarget(), 0, &hkRef);
        if (st == noErr) {
            gDelegate.hotKeyRef = hkRef;
            ok = true;
        }
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
        gDelegate.rootEntries = [NSMutableArray array];
        [gDelegate.entryBuildStack removeAllObjects];
        [gDelegate.entryBuildStack addObject:gDelegate.rootEntries];
    });
}

void uc_popup_add_item(int tag, const char *title, const void *thumbPNG, int thumbLen, const char *keyEquiv, bool enabled) {
    NSString *nsTitle = title ? [NSString stringWithUTF8String:title] : @"";
    NSString *nsKey = keyEquiv ? [NSString stringWithUTF8String:keyEquiv] : @"";
    NSData *imgData = (thumbPNG && thumbLen > 0) ? [NSData dataWithBytes:thumbPNG length:(NSUInteger)thumbLen] : nil;
    runOnMainSync(^{
        NSMutableArray<UCPopupEntry *> *cur = gDelegate.entryBuildStack.lastObject ?: gDelegate.rootEntries;
        UCPopupEntry *e = [[UCPopupEntry alloc] init];
        e.tag = tag;
        e.title = nsTitle;
        e.keyEquiv = nsKey;
        e.enabled = enabled;
        if (imgData) {
            e.thumb = [[NSImage alloc] initWithData:imgData];
        }
        [cur addObject:e];
    });
}

void uc_popup_add_separator(void) {
    runOnMainSync(^{
        NSMutableArray<UCPopupEntry *> *cur = gDelegate.entryBuildStack.lastObject ?: gDelegate.rootEntries;
        UCPopupEntry *e = [[UCPopupEntry alloc] init];
        e.isSeparator = YES;
        [cur addObject:e];
    });
}

void uc_popup_begin_submenu(const char *title, const char *keyEquiv) {
    NSString *nsTitle = title ? [NSString stringWithUTF8String:title] : @"";
    NSString *nsKey = keyEquiv ? [NSString stringWithUTF8String:keyEquiv] : @"";
    runOnMainSync(^{
        NSMutableArray<UCPopupEntry *> *parent = gDelegate.entryBuildStack.lastObject ?: gDelegate.rootEntries;
        UCPopupEntry *e = [[UCPopupEntry alloc] init];
        e.title = nsTitle;
        e.keyEquiv = nsKey;
        e.enabled = YES;
        e.children = [NSMutableArray array];
        [parent addObject:e];
        [gDelegate.entryBuildStack addObject:e.children];
    });
}

void uc_popup_end_submenu(void) {
    runOnMainSync(^{
        if (gDelegate.entryBuildStack.count > 1) {
            [gDelegate.entryBuildStack removeLastObject];
        }
    });
}

void uc_popup_show(void) {
    runOnMainAsync(^{
        [gDelegate dismissAllPopupsAndRestoreFocus:NO];

        pid_t selfPID = [[NSProcessInfo processInfo] processIdentifier];
        NSRunningApplication *front = [[NSWorkspace sharedWorkspace] frontmostApplication];
        if (front && front.processIdentifier != selfPID) {
            gDelegate.lastExternalApp = front;
        }

        [NSApp activateIgnoringOtherApps:YES];

        NSPoint loc = [NSEvent mouseLocation];
        UCPopupPanel *rootPanel = [gDelegate createPanelWithEntries:gDelegate.rootEntries
                                                              level:0
                                                             anchor:loc
                                                         parentRect:NSZeroRect];
        [gDelegate.panelStack addObject:rootPanel];
        [rootPanel makeKeyAndOrderFront:nil];

        gDelegate.globalClickMonitor = [NSEvent addGlobalMonitorForEventsMatchingMask:(NSEventMaskLeftMouseDown | NSEventMaskRightMouseDown)
                                                                              handler:^(NSEvent *ev) {
            (void)ev;
            NSPoint scrPt = [NSEvent mouseLocation];
            for (UCPopupPanel *p in gDelegate.panelStack) {
                if (NSPointInRect(scrPt, p.frame)) {
                    return;
                }
            }
            [gDelegate dismissAllPopupsAndRestoreFocus:NO];
        }];
    });
}

void uc_send_paste(void) {
    runOnMainAsync(^{
        NSRunningApplication *target = gDelegate.lastExternalApp;
        if (target && !target.terminated) {
            [target activateWithOptions:NSApplicationActivateIgnoringOtherApps];
        }
        dispatch_async(dispatch_get_global_queue(QOS_CLASS_USER_INTERACTIVE, 0), ^{
            for (int i = 0; i < 12; i++) {
                if (!target || target.terminated || target.isActive) {
                    break;
                }
                usleep(20000);
            }
            usleep(45000);

            if (!AXIsProcessTrusted()) {
                dispatch_async(dispatch_get_main_queue(), ^{
                    if (!gDelegate.axPromptedOnce) {
                        gDelegate.axPromptedOnce = YES;
                        NSDictionary *opts = @{(__bridge NSString *)kAXTrustedCheckOptionPrompt: @YES};
                        AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)opts);
                    }
                });
                return;
            }

            CGEventSourceRef src = CGEventSourceCreate(kCGEventSourceStateHIDSystemState);
            if (!src) return;

            CGEventRef cmdDown = CGEventCreateKeyboardEvent(src, (CGKeyCode)kVK_Command, true);
            CGEventRef vDown = CGEventCreateKeyboardEvent(src, (CGKeyCode)kVK_ANSI_V, true);
            CGEventRef vUp = CGEventCreateKeyboardEvent(src, (CGKeyCode)kVK_ANSI_V, false);
            CGEventRef cmdUp = CGEventCreateKeyboardEvent(src, (CGKeyCode)kVK_Command, false);

            CGEventSetFlags(cmdDown, kCGEventFlagMaskCommand);
            CGEventSetFlags(vDown, kCGEventFlagMaskCommand);
            CGEventSetFlags(vUp, kCGEventFlagMaskCommand);
            CGEventSetFlags(cmdUp, 0);

            CGEventPost(kCGHIDEventTap, cmdDown);
            CGEventPost(kCGHIDEventTap, vDown);
            usleep(18000);
            CGEventPost(kCGHIDEventTap, vUp);
            CGEventPost(kCGHIDEventTap, cmdUp);

            CFRelease(cmdDown);
            CFRelease(vDown);
            CFRelease(vUp);
            CFRelease(cmdUp);
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
    NSString *hk = hotkey ? [NSString stringWithUTF8String:hotkey] : @"Option+V";
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
