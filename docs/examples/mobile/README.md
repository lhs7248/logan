# Native SDK → workbench

Configure the full receiver URL at runtime: its path is
`/logan-web/api/workbench/v1/sdk/native`. Obtain an application ingestion-only
credential from your service operator. Supply that credential as
`X-Logan-Upload-Token`; never supply a workspace/admin credential. The receiver
binds project, app and environment to the credential. The SDK's device and build
headers are application assertions, not device attestation.

Initialize Logan with your application's existing CLogan key/IV. Keep original
SDK bytes: no ZIP, text conversion or multipart wrapping. The configured receiver
must use the matching decode key/IV. No endpoint, key or secret is embedded in
these examples. Don't print headers, tokens, or credential-bearing configuration.

## Android

Copy [WorkbenchLoganUpload.java](WorkbenchLoganUpload.java) into your application
and call it after normal Logan initialization:

```java
WorkbenchLoganUpload.upload(receiverUrl, logDate, applicationId, installationId,
    appVersion, buildVersion, applicationUploadToken, stableUploadId, callback);
```

The helper invokes the existing `Logan.s(url, date, Map<String,String>, callback)`
API. It supplies native identity headers and available version/build headers;
null or empty version/build values are omitted and remain unknown, never
synthesized. The SDK reads the original
CLogan file and posts its bytes. The callback receives HTTP status and response
bytes on the SDK's background thread. The Android sender uses the platform's default TLS certificate and hostname
verification. A certificate for another hostname fails before the upload is sent.
Credential-bearing uploads do not follow HTTP redirects, so credentials and
encrypted bodies stay at the configured receiver URL. Treat any 3xx response as
a configuration failure and supply the final receiver URL. Legacy uploads
without an upload-token header retain their redirect behavior. The HTTP API and
localhost HTTP development behavior remain available.

## iOS

Use the additive API after normal Logan initialization:

```objc
NSMutableDictionary<NSString *, NSString *> *headers =
    [@{@"X-Logan-Upload-Token": applicationUploadToken} mutableCopy];
if (stableUploadId.length) headers[@"X-Logan-Upload-ID"] = stableUploadId;
loganUploadWithHeaders(receiverUrl, logDate, applicationId, nil, installationId,
    headers, ^(NSData *data, NSURLResponse *response, NSError *error) {
        // Inspect status/receipt without logging credentials. Runs on main queue.
    });
```

`appVersion` and `bundleVersion` come from the application bundle; `platform` is
`2`. The existing `loganUpload` signature and behavior remain available. The new
API permits only upload token and optional upload ID headers, case insensitively.
Identity/content/admin headers, duplicate names and control characters fail
locally (error code `-101`, main queue) before starting an upload. It snapshots
headers before asynchronous file lookup. Header validation errors never include
credential values. The additive API uses a dedicated session that refuses HTTP
redirects and invalidates after completion; the legacy API keeps its shared
session behavior. A 3xx response is returned to the callback for handling.

## Receipts and retry

Use one stable upload ID for retries of the same complete file and metadata.
Do not reuse it after the file changes (today's file can continue growing).
HTTP 200 with response `code: 200` means the original and queued parse receipt
were durably accepted; parsing may still fail. Save the returned asset/run
references and inspect parse status in the workbench. Server rejection must be
handled through the HTTP response, not treated as successful ingestion.

## Local verification

From the repository root, run `sh tests/mobile/test-upload-headers.sh` on macOS
with Xcode. It compiles the actual Objective-C uploader, intercepts file lookup
and NSURLSession, and verifies legacy headers, original file URL, new header
propagation, and local rejection without a transport call. CLogan calls are test
stubs: this test does not establish native encoding, network delivery, simulator
or physical-device behavior. Run `sh tests/mobile/test-android-upload.sh` with JDK 17 for the Android
helper and actual HTTP sender loopback check: all identity/auth headers and exact
synthetic bytes are verified. A temporary trusted localhost certificate verifies
that the real HTTPS sender accepts a matching hostname and rejects a different
hostname before sending the credential-bearing HTTP request. Android scheduling and the public API bridge use
test doubles; this does not establish Android device behavior. A restricted
sandbox may require permission to open the temporary loopback listener. No dependencies or
user Xcode settings are updated.

Run `python3 tests/mobile/test-upload-redirects.py` on macOS with Xcode and JDK 17
to verify both actual transports against two temporary loopback HTTP servers.
Credential uploads must return the initial 302 without contacting the second
server; legacy uploads must retain their existing redirect behavior. This uses
synthetic bytes and credentials, and does not establish Android/iOS device runtime
behavior.
