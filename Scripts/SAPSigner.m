#import <Foundation/Foundation.h>
#import <dispatch/dispatch.h>
#import <dlfcn.h>

// CommerceKit's CKSigningSession stopped completing on current macOS 27
// releases. Prime AppleMediaServices' Mescal session directly with the SAP
// endpoints advertised in Apple's current Store bag instead.
@interface PastelAMSMescalFairPlay : NSObject
- (instancetype)initWithMescalType:(long long)type;
- (instancetype)initWithMescalType:(long long)type logKey:(NSString *)key error:(NSError **)error;
- (NSData *)exchangeData:(NSData *)data error:(NSError **)error;
- (NSData *)signData:(NSData *)data error:(NSError **)error;
- (NSData *)primingSignatureForData:(NSData *)data error:(NSError **)error;
- (BOOL)isComplete;
@end

static const NSUInteger PastelMaximumSAPResponseSize = 1024 * 1024;

static void writeError(NSString *message) {
    NSData *data = [[message ?: @"unknown Apple SAP error"
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
        if (byte.length == 0) {
            return line.length ? [[NSString alloc] initWithData:line encoding:NSUTF8StringEncoding] : nil;
        }
        const uint8_t value = ((const uint8_t *)byte.bytes)[0];
        if (value == '\n') return [[NSString alloc] initWithData:line encoding:NSUTF8StringEncoding];
        [line appendData:byte];
    }
}

static BOOL allowedEndpoint(NSURL *url, BOOL certificate) {
    if (![url.scheme.lowercaseString isEqualToString:@"https"] || url.user.length || url.password.length || url.port) {
        return NO;
    }
    NSString *host = url.host.lowercaseString;
    if (certificate) {
        return [host isEqualToString:@"s.mzstatic.com"] && [url.path isEqualToString:@"/sap/setupCert.plist"];
    }
    BOOL allowedHost = [host isEqualToString:@"fpinit.itunes.apple.com"]
        || [host isEqualToString:@"play.itunes.apple.com"];
    return allowedHost && ([url.path hasPrefix:@"/v1/signSapSetup/"]
        || [url.path isEqualToString:@"/WebObjects/MZPlay.woa/wa/signSapSetup"]);
}

static NSData *sendRequest(NSURLRequest *request, NSError **outputError) {
    NSURLSessionConfiguration *configuration = [NSURLSessionConfiguration ephemeralSessionConfiguration];
    configuration.timeoutIntervalForRequest = 30;
    configuration.timeoutIntervalForResource = 35;
    NSURLSession *session = [NSURLSession sessionWithConfiguration:configuration];
    dispatch_semaphore_t completed = dispatch_semaphore_create(0);
    __block NSData *responseData = nil;
    __block NSError *responseError = nil;
    __block NSInteger statusCode = 0;

    NSURLSessionDataTask *task = [session dataTaskWithRequest:request
        completionHandler:^(NSData *data, NSURLResponse *response, NSError *error) {
            responseData = data;
            responseError = error;
            if ([response isKindOfClass:[NSHTTPURLResponse class]]) {
                statusCode = ((NSHTTPURLResponse *)response).statusCode;
            }
            dispatch_semaphore_signal(completed);
        }];
    [task resume];

    long waitResult = dispatch_semaphore_wait(completed, dispatch_time(DISPATCH_TIME_NOW, 36 * NSEC_PER_SEC));
    [session finishTasksAndInvalidate];
    if (waitResult != 0) {
        [task cancel];
        responseError = [NSError errorWithDomain:@"PastelSAP" code:1
            userInfo:@{NSLocalizedDescriptionKey: @"Apple SAP request timed out"}];
    } else if (!responseError && statusCode != 200) {
        responseError = [NSError errorWithDomain:@"PastelSAP" code:statusCode
            userInfo:@{NSLocalizedDescriptionKey: [NSString stringWithFormat:@"Apple SAP service returned HTTP %ld", (long)statusCode]}];
    } else if (!responseError && (responseData.length == 0 || responseData.length > PastelMaximumSAPResponseSize)) {
        responseError = [NSError errorWithDomain:@"PastelSAP" code:2
            userInfo:@{NSLocalizedDescriptionKey: @"Apple SAP response has an invalid size"}];
    }
    if (outputError) *outputError = responseError;
    return responseError ? nil : responseData;
}

static NSData *plistData(NSData *document, NSString *key, NSError **error) {
    NSDictionary *values = [NSPropertyListSerialization propertyListWithData:document
        options:NSPropertyListImmutable format:nil error:error];
    NSData *value = [values isKindOfClass:[NSDictionary class]] ? values[key] : nil;
    if (![value isKindOfClass:[NSData class]] || value.length == 0) {
        if (error && !*error) {
            *error = [NSError errorWithDomain:@"PastelSAP" code:3
                userInfo:@{NSLocalizedDescriptionKey: [NSString stringWithFormat:@"Apple SAP response is missing %@", key]}];
        }
        return nil;
    }
    return value;
}

static PastelAMSMescalFairPlay *openSession(NSURL *setupURL, NSURL *certificateURL, NSError **error) {
    Class fairPlayClass = NSClassFromString(@"AMSMescalFairPlay");
    if (fairPlayClass == Nil) {
        if (error) *error = [NSError errorWithDomain:@"PastelSAP" code:4
            userInfo:@{NSLocalizedDescriptionKey: @"AMSMescalFairPlay is unavailable"}];
        return nil;
    }

    PastelAMSMescalFairPlay *fairPlay = nil;
    id allocated = [fairPlayClass alloc];
    if ([allocated respondsToSelector:@selector(initWithMescalType:logKey:error:)]) {
        fairPlay = [allocated initWithMescalType:2 logKey:@"Pastel" error:error];
    } else if ([allocated respondsToSelector:@selector(initWithMescalType:)]) {
        fairPlay = [allocated initWithMescalType:2];
    }
    if (!fairPlay) return nil;

    NSData *certificateDocument = sendRequest([NSURLRequest requestWithURL:certificateURL], error);
    NSData *certificate = certificateDocument ? plistData(certificateDocument, @"sign-sap-setup-cert", error) : nil;
    if (!certificate) return nil;

    NSData *setupMessage = [fairPlay exchangeData:certificate error:error];
    if (!setupMessage.length) return nil;
    NSData *setupBody = [NSPropertyListSerialization dataWithPropertyList:@{@"sign-sap-setup-buffer": setupMessage}
        format:NSPropertyListXMLFormat_v1_0 options:0 error:error];
    if (!setupBody) return nil;

    NSMutableURLRequest *setupRequest = [NSMutableURLRequest requestWithURL:setupURL];
    setupRequest.HTTPMethod = @"POST";
    setupRequest.HTTPBody = setupBody;
    [setupRequest setValue:@"application/x-plist" forHTTPHeaderField:@"Content-Type"];
    [setupRequest setValue:@"Configurator/2.17 (Macintosh; OS X 15.2; 24C5089c) AppleWebKit/0620.1.16.11.6"
        forHTTPHeaderField:@"User-Agent"];
    NSData *setupDocument = sendRequest(setupRequest, error);
    NSData *setupReply = setupDocument ? plistData(setupDocument, @"sign-sap-setup-buffer", error) : nil;
    if (!setupReply) return nil;
    NSData *completion = [fairPlay exchangeData:setupReply error:error];
    if (completion == nil || !fairPlay.isComplete) return nil;
    return fairPlay;
}

int main(int argc, const char *argv[]) {
    @autoreleasepool {
        BOOL serverMode = argc > 1 && strcmp(argv[1], "--server") == 0;
        int endpointIndex = serverMode ? 2 : 1;
        if (argc < endpointIndex + 2) {
            writeError(@"Apple SAP setup and certificate endpoints are required");
            return 2;
        }
        NSString *setupString = [NSString stringWithUTF8String:argv[endpointIndex]];
        NSString *certificateString = [NSString stringWithUTF8String:argv[endpointIndex + 1]];
        NSURL *setupURL = [NSURL URLWithString:setupString];
        NSURL *certificateURL = [NSURL URLWithString:certificateString];
        if (!allowedEndpoint(setupURL, NO) || !allowedEndpoint(certificateURL, YES)) {
            writeError(@"Apple SAP endpoints are invalid");
            return 2;
        }

        NSData *input = serverMode ? nil : [[NSFileHandle fileHandleWithStandardInput] readDataToEndOfFile];
        if (!serverMode && input.length == 0) {
            writeError(@"SAP signing input is empty");
            return 2;
        }

        void *appleMediaServices = dlopen(
            "/System/Library/PrivateFrameworks/AppleMediaServices.framework/AppleMediaServices",
            RTLD_LAZY | RTLD_LOCAL
        );
        if (appleMediaServices == NULL) {
            const char *detail = dlerror();
            writeError(detail ? [NSString stringWithUTF8String:detail] : @"failed to load AppleMediaServices");
            return 3;
        }

        NSError *sessionError = nil;
        PastelAMSMescalFairPlay *fairPlay = openSession(setupURL, certificateURL, &sessionError);
        if (!fairPlay) {
            writeError(sessionError.localizedDescription ?: @"Apple SAP signing session did not open");
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
                NSData *signature = [fairPlay signData:requestData error:&signingError];
                if (signature.length == 0) {
                    writeStdout([@"ERROR " stringByAppendingString:signingError.localizedDescription ?: @"empty signature"]);
                    continue;
                }
                writeStdout([signature base64EncodedStringWithOptions:0]);
            }
        } else {
            NSError *signingError = nil;
            NSData *signature = [fairPlay signData:input error:&signingError];
            if (signature.length == 0) {
                writeError(signingError.localizedDescription ?: @"AppleMediaServices returned an empty SAP signature");
                return 5;
            }
            writeStdout([signature base64EncodedStringWithOptions:0]);
        }
        return 0;
    }
}
