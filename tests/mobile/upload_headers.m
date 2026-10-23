// Host behavior test: transport and file lookup are intercepted; CLogan is not executed.
#import <Cocoa/Cocoa.h>
#import <objc/runtime.h>
#import "../../Logan/iOS/Logan.m"
int clogan_init(const char*a,const char*b,int c,const char*d,const char*e){return 0;}
int clogan_open(const char*a){return 0;}
int clogan_write(int a,char*b,long long c,char*d,long long e,int f){return 0;}
int clogan_flush(void){return 0;}
void clogan_debug(int a){}
static NSURLRequest *captured;
static NSURL *capturedFile;
static BOOL resumed, guarded, invalidated, delayFileLookup;
static LoganFilePathBlock pendingLookup;
static id<NSURLSessionTaskDelegate> capturedDelegate;
static void (^capturedCompletion)(NSData *, NSURLResponse *, NSError *);
@interface UploadDouble : NSObject
@end
@implementation UploadDouble
- (void)filePathForDate:(NSString *)date block:(LoganFilePathBlock)block {
    if (delayFileLookup) pendingLookup = [block copy];
    else block(@"/tmp/synthetic-clogan-original");
}
- (id)uploadTaskWithRequest:(NSURLRequest *)request fromFile:(NSURL *)file completionHandler:(id)handler {
    captured = request; capturedFile = file; capturedCompletion = handler; return self;
}
- (void)resume { resumed = YES; }
- (void)finishTasksAndInvalidate { invalidated = YES; }
@end
static id fakeInstance(id self, SEL cmd) { static UploadDouble *instance; if (!instance) instance = [UploadDouble new]; return instance; }
static id fakeSession(id self, SEL cmd, id configuration, id delegate, id queue) {
    guarded = YES; capturedDelegate = delegate; return fakeInstance(self, cmd);
}
#define CHECK(x) do { if (!(x)) { NSLog(@"check failed at line %d", __LINE__); return 1; } } while (0)
int main(void) { @autoreleasepool {
    method_setImplementation(class_getClassMethod([Logan class], @selector(logan)), (IMP)fakeInstance);
    method_setImplementation(class_getClassMethod([NSURLSession class], @selector(sharedSession)), (IMP)fakeInstance);
    method_setImplementation(class_getClassMethod([NSURLSession class], @selector(sessionWithConfiguration:delegate:delegateQueue:)), (IMP)fakeSession);
    loganUpload(@"https://example.invalid/intake", @"2026-10-22", @"app", @"user", @"device", nil);
    CHECK(resumed); CHECK(!guarded); CHECK([captured.HTTPMethod isEqual:@"POST"]);
    CHECK([[captured valueForHTTPHeaderField:@"Content-Type"] isEqual:@"binary/octet-stream"]);
    CHECK([[captured valueForHTTPHeaderField:@"appId"] isEqual:@"app"]);
    CHECK([[captured valueForHTTPHeaderField:@"deviceId"] isEqual:@"device"]);
    CHECK([[captured valueForHTTPHeaderField:@"unionId"] isEqual:@"user"]);
    CHECK([[captured valueForHTTPHeaderField:@"platform"] isEqual:@"2"]);
    CHECK([[captured valueForHTTPHeaderField:@"fileDate"] isEqual:@"2026-10-22"]);
    CHECK([captured valueForHTTPHeaderField:@"X-Logan-Upload-Token"] == nil);
    CHECK([capturedFile.path isEqual:@"/tmp/synthetic-clogan-original"]);
    NSDictionary *legacy = captured.allHTTPHeaderFields;
    loganUploadWithHeaders(@"https://example.invalid/intake", @"2026-10-22", @"app", @"user", @"device",
        @{@"x-logan-upload-token": @"synthetic-upload-only", @"X-Logan-Upload-ID": @"stable-retry-1"}, nil);
    CHECK([[captured valueForHTTPHeaderField:@"X-Logan-Upload-Token"] isEqual:@"synthetic-upload-only"]);
    CHECK([[captured valueForHTTPHeaderField:@"X-Logan-Upload-ID"] isEqual:@"stable-retry-1"]);
    CHECK(guarded); CHECK(!invalidated);
    __block BOOL redirectRefused = NO;
    [capturedDelegate URLSession:(NSURLSession *)fakeInstance(nil, NULL) task:(NSURLSessionTask *)fakeInstance(nil, NULL) willPerformHTTPRedirection:[[NSHTTPURLResponse alloc] initWithURL:captured.URL statusCode:302 HTTPVersion:nil headerFields:nil] newRequest:captured completionHandler:^(NSURLRequest *request) { redirectRefused = request == nil; }];
    CHECK(redirectRefused);
    capturedCompletion(nil, nil, nil);
    CHECK(invalidated);
    for (NSString *key in legacy) CHECK([[captured valueForHTTPHeaderField:key] isEqual:legacy[key]]);
    for (NSDictionary *bad in @[@{@"APPID": @"spoof"}, @{@"Content-Type": @"text/plain"}, @{@"Authorization": @"synthetic-admin"}, @{@"X-Workbench-Token": @"synthetic-admin"}, @{@"X-Logan-Upload-Token": @"bad\r\nheader"}]) {
        resumed = NO; __block BOOL rejected = NO;
        loganUploadWithHeaders(@"https://example.invalid/intake", @"2026-10-22", @"app", nil, @"device", bad,
            ^(NSData *data, NSURLResponse *response, NSError *error) { rejected = error != nil; });
        NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:1];
        while (!rejected && [deadline timeIntervalSinceNow] > 0) [[NSRunLoop currentRunLoop] runUntilDate:[NSDate dateWithTimeIntervalSinceNow:0.01]];
        CHECK(rejected); CHECK(!resumed);
    }
    delayFileLookup = YES; resumed = NO;
    NSMutableString *mutableToken = [@"synthetic-snapshot-token" mutableCopy];
    NSMutableString *mutableID = [@"stable-snapshot-id" mutableCopy];
    NSMutableDictionary *mutableHeaders = [@{@"X-Logan-Upload-Token": mutableToken,
        @"X-Logan-Upload-ID": mutableID} mutableCopy];
    loganUploadWithHeaders(@"https://example.invalid/intake", @"2026-10-22", @"app", nil, @"device", mutableHeaders, nil);
    CHECK(pendingLookup != nil); CHECK(!resumed);
    [mutableToken setString:@"changed-after-validation"];
    [mutableID setString:@"changed-id-after-validation"];
    [mutableHeaders removeAllObjects];
    pendingLookup(@"/tmp/synthetic-clogan-original"); pendingLookup = nil;
    CHECK(resumed);
    CHECK([[captured valueForHTTPHeaderField:@"X-Logan-Upload-Token"] isEqual:@"synthetic-snapshot-token"]);
    CHECK([[captured valueForHTTPHeaderField:@"X-Logan-Upload-ID"] isEqual:@"stable-snapshot-id"]);
    capturedCompletion(nil, nil, nil);
    puts("mobile upload headers: passed");
} return 0; }
