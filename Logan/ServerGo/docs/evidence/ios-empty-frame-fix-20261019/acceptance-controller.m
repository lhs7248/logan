#import "LGViewController.h"
#import <Logan/Logan.h>
@interface LGViewController ()
@property(weak,nonatomic) IBOutlet UITextView *filesInfo;
@property(weak,nonatomic) IBOutlet UITextField *ipText;
@property(nonatomic,strong) UILabel *state;
@property(nonatomic,strong) NSMutableArray *results;
@property(nonatomic,strong) NSString *scenario;
@property(nonatomic,assign) NSUInteger written;
@property(nonatomic,assign) uint32_t rng;
@property(nonatomic,assign) NSTimeInterval generationStart;
@end
@implementation LGViewController
- (NSString *)root { return [NSSearchPathForDirectoriesInDomains(NSDocumentDirectory,NSUserDomainMask,YES)[0] stringByAppendingPathComponent:@"Acceptance"]; }
- (void)viewDidLoad {
 [super viewDidLoad];
 loganInit([@"0123456789012345" dataUsingEncoding:NSUTF8StringEncoding],[@"0123456789012345" dataUsingEncoding:NSUTF8StringEncoding],64ULL*1024*1024);
 loganUseASL(NO);
 self.results=[NSMutableArray array]; self.rng=0x12345678;
 self.view=[[UIView alloc] initWithFrame:self.view.frame];self.view.backgroundColor=UIColor.whiteColor;
 UIStackView *stack=[[UIStackView alloc] init];stack.axis=UILayoutConstraintAxisVertical;stack.spacing=24;stack.translatesAutoresizingMaskIntoConstraints=NO;
 [self.view addSubview:stack];
 [NSLayoutConstraint activateConstraints:@[[stack.leadingAnchor constraintEqualToAnchor:self.view.leadingAnchor constant:24],[stack.trailingAnchor constraintEqualToAnchor:self.view.trailingAnchor constant:-24],[stack.topAnchor constraintEqualToAnchor:self.view.safeAreaLayoutGuide.topAnchor constant:30]]];
 for(NSString *name in @[@"continuous",@"large1",@"large8",@"large32"]){UIButton *b=[UIButton buttonWithType:UIButtonTypeSystem];[b setTitle:name forState:UIControlStateNormal];[b addTarget:self action:@selector(run:) forControlEvents:UIControlEventTouchUpInside];[stack addArrangedSubview:b];}
 self.state=[[UILabel alloc] init];self.state.numberOfLines=0;self.state.text=@"Ready: original iOS SDK, isolated Go";[stack addArrangedSubview:self.state];
 [[NSFileManager defaultManager] createDirectoryAtPath:self.root withIntermediateDirectories:YES attributes:nil error:nil];
}
- (void)save {NSData *d=[NSJSONSerialization dataWithJSONObject:self.results options:NSJSONWritingPrettyPrinted error:nil];[d writeToFile:[self.root stringByAppendingPathComponent:@"results.json"] atomically:YES];}
- (void)run:(UIButton *)sender {
 if(self.scenario)return;self.scenario=sender.currentTitle;self.generationStart=NSDate.date.timeIntervalSince1970;
 [self batch:1];
}
- (void)batch:(NSUInteger)batch {
 NSUInteger rows=[self.scenario isEqualToString:@"continuous"]?100:([self.scenario isEqualToString:@"large1"]?256:([self.scenario isEqualToString:@"large8"]?2048:8192));
 NSUInteger size=[self.scenario isEqualToString:@"continuous"]?256:4096;
 self.state.text=[NSString stringWithFormat:@"Writing %@ batch %lu",self.scenario,(unsigned long)batch];
 dispatch_async(dispatch_get_global_queue(QOS_CLASS_USER_INITIATED,0),^{
  for(NSUInteger j=0;j<rows;j++){@autoreleasepool{
   NSString *prefix=[NSString stringWithFormat:@"P26|%@|%08lu|",self.scenario,(unsigned long)self.written++];
   NSMutableData *d=[NSMutableData dataWithLength:size];char *p=d.mutableBytes;NSData *prefixData=[prefix dataUsingEncoding:NSUTF8StringEncoding];memcpy(p,prefixData.bytes,prefixData.length);
   const char *alphabet="ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
   for(NSUInteger k=prefixData.length;k<size;k++){uint32_t x=self.rng;x^=x<<13;x^=x>>17;x^=x<<5;self.rng=x;p[k]=alphabet[x&63];}
   logan(1,[[NSString alloc] initWithData:d encoding:NSUTF8StringEncoding]);
  }}
  for(int retry=0;retry<100 && ![loganAllFilesInfo() objectForKey:loganTodaysDate()];retry++){[NSThread sleepForTimeInterval:0.1];}
  dispatch_async(dispatch_get_main_queue(),^{
   NSString *path=[[NSSearchPathForDirectoriesInDomains(NSDocumentDirectory,NSUserDomainMask,YES) objectAtIndex:0] stringByAppendingPathComponent:[NSString stringWithFormat:@"LoganLoggerv3/%@.temp",loganTodaysDate()]];
   NSTimeInterval started=NSDate.date.timeIntervalSince1970;
   NSMutableDictionary *r=[@{@"pre_upload_snapshot":@NO,@"scenario":self.scenario,@"batch":@(batch),@"expected_rows":@(self.written),@"content_bytes_per_row":@(size),@"generation_start_unix":@(self.generationStart),@"upload_start_unix":@(started),@"device_id":[@"phase26-ios-20261019-" stringByAppendingString:self.scenario]} mutableCopy];
   [self.results addObject:r];[self save];self.state.text=[NSString stringWithFormat:@"Uploading %@ batch %lu",self.scenario,(unsigned long)batch];
   loganUpload(@"http://localhost:8888/logan-web/logan/upload.json",loganTodaysDate(),@"testAppId",@"testUnionId",r[@"device_id"],^(NSData *data,NSURLResponse *response,NSError *error){
    r[@"upload_ack_unix"]=@(NSDate.date.timeIntervalSince1970);r[@"http_status"]=@([(NSHTTPURLResponse *)response statusCode]);r[@"error"]=error.localizedDescription?:@"";r[@"response"]=data?[[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding]:@"";
    NSString *wire=[self.root stringByAppendingPathComponent:[NSString stringWithFormat:@"upload-%lu.bin",(unsigned long)batch]];
    [[NSFileManager defaultManager] copyItemAtPath:[path stringByAppendingString:@""] toPath:wire error:nil];
    r[@"wire_bytes"]=[[[NSFileManager defaultManager] attributesOfItemAtPath:wire error:nil] objectForKey:NSFileSize]?:@0;[self save];
    NSDictionary *body=data?[NSJSONSerialization JSONObjectWithData:data options:0 error:nil]:nil;
    if(error || [(NSHTTPURLResponse *)response statusCode]!=200 || [body[@"code"] integerValue]!=200){self.state.text=@"FAILED: inspect results.json";return;}
    if([self.scenario isEqualToString:@"continuous"]&&batch<5){self.generationStart=NSDate.date.timeIntervalSince1970;[self batch:batch+1];}else{self.state.text=[NSString stringWithFormat:@"COMPLETE %@: %lu records, %lu uploads",self.scenario,(unsigned long)self.written,(unsigned long)batch];}
   });
  });
 });
}
@end
