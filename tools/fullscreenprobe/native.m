// Diagnostic control only: AppKit's own run loop, no gnui event pump or Metal.
#import <Cocoa/Cocoa.h>
#import <QuartzCore/QuartzCore.h>

@interface Probe : NSObject <NSApplicationDelegate, NSWindowDelegate>
@property NSWindow *window;
@property NSTimer *timer;
@property double start, last, frameTime, phaseTime, doneAt, lastLog;
@property NSUInteger ticks;
@property int phase, result;
@property BOOL activated, clicked, entered, exited, interfered;
@end

@implementation Probe
- (void)log:(NSString *)event {
    printf("%.3fs %s phase=%d visible=%d active=%d key=%d styleFull=%d ticks=%lu frameTime=%.3f mode=%s\n",
        CACurrentMediaTime()-self.start, event.UTF8String, self.phase,
        !!(self.window.occlusionState & NSWindowOcclusionStateVisible), NSApp.active, self.window.keyWindow,
        !!(self.window.styleMask & NSWindowStyleMaskFullScreen), (unsigned long)self.ticks,
        self.frameTime, (NSRunLoop.currentRunLoop.currentMode ?: @"none").UTF8String);
    fflush(stdout);
}
- (void)applicationDidFinishLaunching:(NSNotification *)note {
    self.start = CACurrentMediaTime();
    self.result = 1;
    self.timer = [NSTimer timerWithTimeInterval:1.0/60 target:self selector:@selector(tick:) userInfo:nil repeats:YES];
    [NSRunLoop.mainRunLoop addTimer:self.timer forMode:NSRunLoopCommonModes];
}
- (void)click {
    [self log:@"click"];
    [[self.window standardWindowButton:NSWindowZoomButton] performClick:nil];
}
- (void)finish:(BOOL)success reason:(NSString *)reason {
    [self log:reason];
    printf("native-result: entered=%d exited=%d windowed=%d interfered=%d success=%d\n",
        self.entered, self.exited, !(self.window.styleMask & NSWindowStyleMaskFullScreen), self.interfered, success);
    self.result = success ? 0 : 1;
    [self.timer invalidate];
    [NSApp stop:nil];
    [NSApp postEvent:[NSEvent otherEventWithType:NSEventTypeApplicationDefined location:NSZeroPoint modifierFlags:0 timestamp:0 windowNumber:0 context:nil subtype:0 data1:0 data2:0] atStart:NO];
}
- (void)tick:(NSTimer *)timer {
    double now = CACurrentMediaTime();
    if (self.last) self.frameTime += MIN(now-self.last, 0.1); // same capped frame-clock policy
    self.last = now;
    self.ticks++;
    if (!self.activated && now-self.start >= 2) {
        self.activated = YES;
        [NSApp activateIgnoringOtherApps:YES];
        [self.window makeKeyAndOrderFront:nil];
        [self.window orderFrontRegardless];
        [self log:@"activation requested"];
    }
    if (!self.clicked && self.activated && NSApp.active && self.window.keyWindow && (self.window.occlusionState & NSWindowOcclusionStateVisible)) {
        self.clicked = YES;
        [self performSelector:@selector(click) withObject:nil afterDelay:0];
    }
    if (now-self.lastLog >= 1) { [self log:@"heartbeat"]; self.lastLog = now; }
    if (self.phase == 4 && now-self.doneAt > 0.3) {
        [self finish:self.entered && self.exited && !(self.window.styleMask & NSWindowStyleMaskFullScreen) reason:@"done"];
    } else if ((self.phase == 1 || self.phase == 3) && self.frameTime-self.phaseTime > 5) {
        [self finish:NO reason:@"transition deadline"];
    } else if (self.frameTime > 15 || now-self.start > 120) {
        [self finish:NO reason:@"overall deadline"];
    }
}
- (void)phase:(int)value name:(NSString *)name { self.phase=value; self.phaseTime=self.frameTime; [self log:name]; }
- (void)windowWillEnterFullScreen:(NSNotification *)n { [self phase:1 name:@"will-enter"]; }
- (void)windowDidEnterFullScreen:(NSNotification *)n {
    self.entered=YES; [self phase:2 name:@"did-enter"];
    [self performSelector:@selector(click) withObject:nil afterDelay:0.5];
}
- (void)windowWillExitFullScreen:(NSNotification *)n { [self phase:3 name:@"will-exit"]; }
- (void)windowDidExitFullScreen:(NSNotification *)n {
    self.exited=YES; self.doneAt=CACurrentMediaTime(); [self phase:4 name:@"did-exit"];
}
- (void)applicationDidBecomeActive:(NSNotification *)n { [self log:@"app-active"]; }
- (void)applicationDidResignActive:(NSNotification *)n { self.interfered=YES; [self log:@"app-resigned"]; }
- (void)windowDidBecomeKey:(NSNotification *)n { [self log:@"key"]; }
- (void)windowDidResignKey:(NSNotification *)n { [self log:@"resign-key"]; }
@end

int main(void) { @autoreleasepool {
    [NSApplication sharedApplication];
    [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
    [NSWindow setAllowsAutomaticWindowTabbing:NO];
    Probe *probe = [Probe new];
    probe.window = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, 400, 300)
        styleMask:NSWindowStyleMaskTitled|NSWindowStyleMaskClosable|NSWindowStyleMaskMiniaturizable|NSWindowStyleMaskResizable
        backing:NSBackingStoreBuffered defer:NO];
    probe.window.title = @"native fullscreen control";
    probe.window.restorable = NO;
    probe.window.releasedWhenClosed = NO;
    probe.window.delegate = probe;
    [probe.window center];
    NSApp.delegate = probe;
    [NSApp run];
    [probe.window close];
    return probe.result;
} }
