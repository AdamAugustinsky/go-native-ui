// Throwaway phase-2 probe: the native floor for "exec -> first frame on glass".
// A minimal Objective-C AppKit + Metal app doing exactly what cmd/spike does
// (NSApplication, NSWindow 960x600, CAMetalLayer view, finishLaunching, order
// front, draw first, clear + present with a presented handler) and printing
// the same timeline, so the Go stack can be compared against plain AppKit.
//
//	clang -O2 -fobjc-arc -framework AppKit -framework Metal -framework QuartzCore \
//	    -o bin/nativebaseline probes/nativebaseline/main.m
//	./bin/nativebaseline            # prints JSON and exits after the frame is on glass
#import <AppKit/AppKit.h>
#import <Metal/Metal.h>
#import <QuartzCore/QuartzCore.h>
#include <libproc.h>
#include <mach/mach_time.h>
#include <sys/sysctl.h>
#include <unistd.h>

static mach_timebase_info_data_t tb;
static double nowMs(void) { return (double)mach_absolute_time() * tb.numer / tb.denom / 1e6; }

// Process start in the same clock: age from proc_pid_rusage's ri_proc_start_abstime.
static double procStartMs(void) {
	struct rusage_info_v4 ri;
	if (proc_pid_rusage(getpid(), RUSAGE_INFO_V4, (rusage_info_t *)&ri) != 0) return -1;
	return (double)ri.ri_proc_start_abstime * tb.numer / tb.denom / 1e6;
}

int main(int argc, const char **argv) {
	mach_timebase_info(&tb);
	double tMain = nowMs(), t0 = procStartMs();
	@autoreleasepool {
		NSApplication *app = [NSApplication sharedApplication];
		[app setActivationPolicy:NSApplicationActivationPolicyRegular];
		double tApp = nowMs();
		NSWindow *win = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, 960, 600)
		                                            styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable |
		                                                      NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable
		                                              backing:NSBackingStoreBuffered
		                                                defer:NO];
		double tWin = nowMs();
		NSView *view = [[NSView alloc] initWithFrame:NSMakeRect(0, 0, 960, 600)];
		[win setContentView:view];
		[win setTitle:@"native baseline"];
		[win center];
		id<MTLDevice> dev = MTLCreateSystemDefaultDevice();
		id<MTLCommandQueue> q = [dev newCommandQueue];
		CAMetalLayer *layer = [CAMetalLayer layer];
		layer.device = dev;
		layer.pixelFormat = MTLPixelFormatBGRA8Unorm;
		layer.framebufferOnly = YES;
		layer.maximumDrawableCount = 2;
		layer.opaque = YES;
		[view setLayer:layer];
		[view setWantsLayer:YES];
		CGFloat scale = win.backingScaleFactor;
		layer.contentsScale = scale;
		layer.drawableSize = CGSizeMake(960 * scale, 600 * scale);
		double tSetup = nowMs();
		[app finishLaunching];
		double tFinish = nowMs();
		[win makeKeyAndOrderFront:nil];
		[win orderFrontRegardless];
		[app activate];
		double tFront = nowMs();

		__block double tGlass = 0;
		id<CAMetalDrawable> d = [layer nextDrawable];
		[d addPresentedHandler:^(id<MTLDrawable> dr) {
			double pt = dr.presentedTime;
			tGlass = pt > 0 ? pt * 1000 : nowMs();
		}];
		MTLRenderPassDescriptor *rpd = [MTLRenderPassDescriptor renderPassDescriptor];
		rpd.colorAttachments[0].texture = d.texture;
		rpd.colorAttachments[0].loadAction = MTLLoadActionClear;
		rpd.colorAttachments[0].storeAction = MTLStoreActionStore;
		rpd.colorAttachments[0].clearColor = MTLClearColorMake(0.11, 0.12, 0.14, 1);
		id<MTLCommandBuffer> cb = [q commandBuffer];
		[[cb renderCommandEncoderWithDescriptor:rpd] endEncoding];
		[cb presentDrawable:d];
		[cb commit];
		[CATransaction flush];
		double tPresent = nowMs();

		// Pump events until the frame is on glass (or 3 s).
		while (tGlass == 0 && nowMs() - tPresent < 3000) {
			NSEvent *ev = [app nextEventMatchingMask:NSEventMaskAny
			                               untilDate:[NSDate dateWithTimeIntervalSinceNow:0.005]
			                                  inMode:NSDefaultRunLoopMode
			                                 dequeue:YES];
			if (ev) [app sendEvent:ev];
		}
		printf("{\"ExecToMainMs\":%.1f,\"NSApplicationMs\":%.1f,\"NSWindowMs\":%.1f,\"SetupMs\":%.1f,"
		       "\"FinishLaunchingMs\":%.1f,\"OrderFrontMs\":%.1f,\"FirstPresentMs\":%.1f,"
		       "\"ExecToFirstPresentMs\":%.1f,\"ExecToOnGlassMs\":%.1f}\n",
		       tMain - t0, tApp - tMain, tWin - tApp, tSetup - tWin, tFinish - tSetup, tFront - tFinish,
		       tPresent - tFront, tPresent - t0, tGlass ? tGlass - t0 : -1);
	}
	return 0;
}
