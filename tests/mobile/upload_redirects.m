// Real NSURLSession/uploader; file lookup only uses a synthetic existing fixture.
#import <Cocoa/Cocoa.h>
#import <objc/runtime.h>
#import "../../Logan/iOS/Logan.m"
int clogan_init(const char*a,const char*b,int c,const char*d,const char*e){return 0;}
int clogan_open(const char*a){return 0;}
int clogan_write(int a,char*b,long long c,char*d,long long e,int f){return 0;}
int clogan_flush(void){return 0;}
void clogan_debug(int a){}
static NSString *fixture;
@interface RedirectFileLookup : NSObject
@end
@implementation RedirectFileLookup
- (void)filePathForDate:(NSString *)date block:(LoganFilePathBlock)block { block(fixture); }
@end
static id redirectLookup(id self, SEL cmd) { return [RedirectFileLookup new]; }
int main(int argc, const char **argv) { @autoreleasepool {
    if (argc != 5) return 2;
    fixture = [NSString stringWithUTF8String:argv[2]];
    method_setImplementation(class_getClassMethod([Logan class], @selector(logan)), (IMP)redirectLookup);
    __block BOOL done = NO, accepted = NO;
    NSInteger expected = atoi(argv[4]);
    LoganUploadResultBlock callback = ^(NSData *data, NSURLResponse *response, NSError *error) {
        accepted = !error && [(NSHTTPURLResponse *)response statusCode] == expected;
        done = YES;
    };
    NSString *url = [NSString stringWithUTF8String:argv[1]];
    if (strcmp(argv[3], "credential") == 0) {
        loganUploadWithHeaders(url, @"2026-10-22", @"app", nil, @"device",
            @{@"X-Logan-Upload-Token": @"synthetic-redirect-token"}, callback);
    } else {
        loganUpload(url, @"2026-10-22", @"app", nil, @"device", callback);
    }
    NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:10];
    while (!done && [deadline timeIntervalSinceNow] > 0)
        [[NSRunLoop currentRunLoop] runUntilDate:[NSDate dateWithTimeIntervalSinceNow:0.01]];
    if (!done || !accepted) { fputs("unexpected iOS redirect result\n", stderr); return 1; }
} return 0; }
