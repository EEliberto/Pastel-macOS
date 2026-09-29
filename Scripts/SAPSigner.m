#import <Foundation/Foundation.h>
#import <dispatch/dispatch.h>
#import <dlfcn.h>

// Apple now protects StoreServices authentication with a SAP action signature.
// Keep all private-framework interaction in this small subprocess so the Node
// login code never needs native FFI and credentials remain on stdin only.
@interface PastelCKSigningSession : NSObject
- (instancetype)initWithStoreClient:(id)storeClient;
- (void)openSessionWithCompletionHandler:(void (^)(void))completionHandler;
- (NSData *)signData:(NSData *)data error:(NSError **)error;
- (void)closeSession;
- (BOOL)isSessionOpen;
@end

static void writeError(NSString *message) {
    NSData *data = [[message ?: @"unknown CommerceKit error"
        stringByAppendingString:@"\n"] dataUsingEncoding:NSUTF8StringEncoding];
    [[NSFileHandle fileHandleWithStandardError] writeData:data];
}

static void writeStdout(NSString *message) {
    NSData *data = [[message stringByAppendingString:@"\n"] dataUsingEncoding:NSUTF8StringEncoding];
    [[NSFileHandle fileHandleWithStandardOutput] writeData:data];
}

static NSString *readLine(NSFileHandle *input) {
    NSMutableData *line = [NSMutableData data];
    while (true) {
        NSData *byte = [input readDataOfLength:1];
        if (byte.length == 0) return line.length ? [[NSString alloc] initWithData:line encoding:NSUTF8StringEncoding] : nil;
        const uint8_t value = ((const uint8_t *)byte.bytes)[0];
        if (value == '\n') return [[NSString alloc] initWithData:line encoding:NSUTF8StringEncoding];
        [line appendData:byte];
    }
}

int main(int argc, const char *argv[]) {
    (void)argc;
    (void)argv;

    @autoreleasepool {
        BOOL serverMode = argc > 1 && strcmp(argv[1], "--server") == 0;
        NSData *input = serverMode ? nil : [[NSFileHandle fileHandleWithStandardInput] readDataToEndOfFile];
        if (!serverMode && input.length == 0) {
            writeError(@"SAP signing input is empty");
            return 2;
        }

        void *commerceKit = dlopen(
            "/System/Library/PrivateFrameworks/CommerceKit.framework/CommerceKit",
            RTLD_LAZY | RTLD_LOCAL
        );
        if (commerceKit == NULL) {
            const char *detail = dlerror();
            writeError(detail != NULL
                ? [NSString stringWithUTF8String:detail]
                : @"failed to load CommerceKit");
            return 3;
        }

        Class signingSessionClass = NSClassFromString(@"CKSigningSession");
        NSArray<NSString *> *requiredSelectors = @[
            @"initWithStoreClient:",
            @"openSessionWithCompletionHandler:",
            @"signData:error:",
            @"closeSession"
        ];
        if (signingSessionClass == Nil) {
            writeError(@"CKSigningSession is unavailable");
            return 3;
        }
        for (NSString *selectorName in requiredSelectors) {
            if (![signingSessionClass instancesRespondToSelector:NSSelectorFromString(selectorName)]) {
                writeError([NSString stringWithFormat:@"CommerceKit is missing %@", selectorName]);
                return 3;
            }
        }

        PastelCKSigningSession *session = [(id)[signingSessionClass alloc] initWithStoreClient:nil];
        if (session == nil) {
            writeError(@"CommerceKit could not create a SAP signing session");
            return 4;
        }

        dispatch_semaphore_t opened = dispatch_semaphore_create(0);
        [session openSessionWithCompletionHandler:^{
            dispatch_semaphore_signal(opened);
        }];
        long waitResult = dispatch_semaphore_wait(
            opened,
            dispatch_time(DISPATCH_TIME_NOW, 30 * NSEC_PER_SEC)
        );
        if (waitResult != 0) {
            [session closeSession];
            writeError(@"timed out opening the Apple SAP signing session");
            return 4;
        }

        if ([session respondsToSelector:@selector(isSessionOpen)] && ![session isSessionOpen]) {
            [session closeSession];
            writeError(@"Apple SAP signing session did not open");
            return 4;
        }

        if (serverMode) {
            writeStdout(@"READY");
            NSFileHandle *stdinHandle = [NSFileHandle fileHandleWithStandardInput];
            NSString *line = nil;
            while ((line = readLine(stdinHandle)) != nil) {
                if ([line isEqualToString:@"CLOSE"]) break;
                NSData *requestData = [[NSData alloc] initWithBase64EncodedString:line options:0];
                if (requestData.length == 0) {
                    writeStdout(@"ERROR invalid signing input");
                    continue;
                }
                NSError *signingError = nil;
                NSData *signature = [session signData:requestData error:&signingError];
                if (signature.length == 0) {
                    writeStdout([@"ERROR " stringByAppendingString:signingError.localizedDescription ?: @"empty signature"]);
                    continue;
                }
                writeStdout([signature base64EncodedStringWithOptions:0]);
            }
            [session closeSession];
        } else {
            NSError *signingError = nil;
            NSData *signature = [session signData:input error:&signingError];
            [session closeSession];
            if (signature.length == 0) {
                writeError(signingError.localizedDescription ?: @"CommerceKit returned an empty SAP signature");
                return 5;
            }
            writeStdout([signature base64EncodedStringWithOptions:0]);
        }
        return 0;
    }
}
