// macOS 事件处理：再次打开程序、clash:// 链接。由 appevents_darwin.go 调用。
#import <Cocoa/Cocoa.h>
#import <CoreServices/CoreServices.h>

extern void goOnReopen(void);
extern void goOnOpenURL(char *u);

@interface ECEventHandler : NSObject
@end

@implementation ECEventHandler
- (void)handleReopen:(NSAppleEventDescriptor *)event withReplyEvent:(NSAppleEventDescriptor *)reply {
	goOnReopen();
}
- (void)handleURL:(NSAppleEventDescriptor *)event withReplyEvent:(NSAppleEventDescriptor *)reply {
	NSString *u = [[event paramDescriptorForKeyword:keyDirectObject] stringValue];
	if (u != nil) {
		goOnOpenURL((char *)[u UTF8String]);
	}
}
@end

static ECEventHandler *ecHandler = nil;

static void ecRegister(void) {
	if (ecHandler == nil) {
		ecHandler = [[ECEventHandler alloc] init];
	}
	NSAppleEventManager *m = [NSAppleEventManager sharedAppleEventManager];
	[m setEventHandler:ecHandler
	       andSelector:@selector(handleReopen:withReplyEvent:)
	     forEventClass:kCoreEventClass
	        andEventID:kAEReopenApplication];
	[m setEventHandler:ecHandler
	       andSelector:@selector(handleURL:withReplyEvent:)
	     forEventClass:kInternetEventClass
	        andEventID:kAEGetURL];
}

// 在菜单栏程序启动之前调用（主线程）。
// 系统自己的默认处理是在程序启动过程中登记的，所以要等它登记完再覆盖：
//   - 「即将完成启动」时登记一次：点 clash:// 链接把程序启动起来时，随启动送来的链接也能收到（苹果文档推荐的时机）
//   - 启动后再登记一次兜底
void ecInstallEventHandlers(void) {
	[[NSNotificationCenter defaultCenter] addObserverForName:NSApplicationWillFinishLaunchingNotification
	                                                  object:nil
	                                                   queue:nil
	                                              usingBlock:^(NSNotification *note) {
		ecRegister();
	}];
	dispatch_async(dispatch_get_main_queue(), ^{
		ecRegister();
	});
}

#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
void ecClaimURLSchemes(void) {
	NSString *bid = [[NSBundle mainBundle] bundleIdentifier];
	if (bid == nil) {
		return;
	}
	LSSetDefaultHandlerForURLScheme(CFSTR("clash"), (CFStringRef)bid);
	LSSetDefaultHandlerForURLScheme(CFSTR("clashmeta"), (CFStringRef)bid);
}
#pragma clang diagnostic pop
