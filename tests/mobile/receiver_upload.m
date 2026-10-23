// Actual Objective-C uploader and NSURLSession. File discovery only is replaced
// with an existing fixture produced by actual CLogan (not plaintext/stub encoding).
#import <Cocoa/Cocoa.h>
#import <objc/runtime.h>
#import "../../Logan/iOS/Logan.m"
int clogan_init(const char*a,const char*b,int c,const char*d,const char*e){return 0;}
int clogan_open(const char*a){return 0;}
int clogan_write(int a,char*b,long long c,char*d,long long e,int f){return 0;}
int clogan_flush(void){return 0;}
void clogan_debug(int a){}
@interface FixtureFileLookup : NSObject
@end
@implementation FixtureFileLookup
- (void)filePathForDate:(NSString *)date block:(LoganFilePathBlock)block {
 block([[[NSProcessInfo processInfo] environment] objectForKey:@"LOGAN_RECEIVER_FIXTURE"]);
}
@end
static id fixtureLookup(id self, SEL cmd) { static FixtureFileLookup *instance; if (!instance) instance=[FixtureFileLookup new]; return instance; }
int main(void) { @autoreleasepool {
 NSDictionary *env=[[NSProcessInfo processInfo] environment];
 method_setImplementation(class_getClassMethod([Logan class],@selector(logan)),(IMP)fixtureLookup);
 __block BOOL finished=NO, accepted=NO;
 loganUploadWithHeaders(env[@"LOGAN_RECEIVER_URL"],@"2026-10-19",@"com.hxl.xlb.test",nil,@"triad-ios-host",
  @{@"X-Logan-Upload-Token":env[@"WORKBENCH_SDK_TOKEN"],@"X-Logan-Upload-ID":@"triad-ios-host-1"},
  ^(NSData *data, NSURLResponse *response, NSError *error) {
   accepted=error==nil && [(NSHTTPURLResponse*)response statusCode]==200;
   if (accepted) accepted=[data writeToFile:env[@"LOGAN_RECEIVER_REPLY"] atomically:YES];
   finished=YES;
  });
 NSDate *deadline=[NSDate dateWithTimeIntervalSinceNow:15];
 while(!finished && [deadline timeIntervalSinceNow]>0) [[NSRunLoop currentRunLoop] runUntilDate:[NSDate dateWithTimeIntervalSinceNow:0.01]];
 if(!finished || !accepted) {fputs("iOS host uploader failed\n",stderr);return 1;}
 puts("iOS host uploader -> Go native receiver: accepted");
 } return 0; }
