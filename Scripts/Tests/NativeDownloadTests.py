"""Exercise production Swift transfer and scheduling with a local HTTP fixture."""
import http.server, threading, tempfile, subprocess, pathlib, time, json
ROOT = pathlib.Path(__file__).resolve().parents[2]
BODY = bytes(range(256)) * 8192
requests = []
class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_): pass
    def do_GET(self):
        start = int(self.headers.get('Range', 'bytes=0-').split('=')[1].split('-')[0])
        requests.append((self.path, start, self.headers.get('If-Range')))
        if self.path == '/blocked': time.sleep(2)
        if self.path == '/ignore': start = 0
        if start >= len(BODY):
            self.send_response(416); self.send_header('Content-Range', f'bytes */{len(BODY)}')
            self.end_headers(); return
        self.send_response(206 if start else 200)
        if start: self.send_header('Content-Range', f'bytes {start}-{len(BODY)-1}/{len(BODY)}')
        self.send_header('Content-Length', str(len(BODY)-start))
        self.send_header('ETag', '"fixture-v1"')
        self.end_headers()
        try:
            for offset in range(start, len(BODY), 16384):
                self.wfile.write(BODY[offset:offset+16384]); self.wfile.flush()
                if self.path == '/slow': time.sleep(.01)
        except (BrokenPipeError, ConnectionResetError): pass
server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
threading.Thread(target=server.serve_forever, daemon=True).start()
harness = r'''
import Foundation
actor Counter {
 var count = 0, peak = 0
 func enter() { count += 1; peak = max(peak, count) }
 func leave() { count -= 1 }
}
@main struct Test {
 static func main() async throws {
  let root = URL(fileURLWithPath: CommandLine.arguments[1])
  let base = URL(string: CommandLine.arguments[2])!
  let expected = try Data(contentsOf: root.appendingPathComponent("expected"))
  let raw = root.appendingPathComponent("raw")
  let first = Task { try await NativeFileTransfer(destination:raw) { _,_ in }.run(url:base.appendingPathComponent("slow")) }
  try await Task.sleep(for:.milliseconds(200)); first.cancel()
  do { try await first.value; fatalError("cancel did not stop transfer") } catch is CancellationError {} 
  let partial = try Data(contentsOf:raw)
  precondition(!partial.isEmpty && partial.count < expected.count)
  try await NativeFileTransfer(destination:raw) { _,_ in }.run(url:base.appendingPathComponent("slow"))
  let complete = try Data(contentsOf:raw); precondition(complete == expected)
  // A completed partial returns 416 and must not be deleted/redownloaded.
  try await NativeFileTransfer(destination:raw) { _,_ in }.run(url:base.appendingPathComponent("slow"))
  try partial.write(to:raw)
  try await NativeFileTransfer(destination:raw) { _,_ in }.run(url:base.appendingPathComponent("ignore"))
  let ignored = try Data(contentsOf:raw); precondition(ignored == expected)
  print("PASS: pause closes writer; Range+If-Range resume; complete 416; ignored Range restarts safely")
  let pool = DownloadPermitPool(limit:3), counter = Counter()
  try await withThrowingTaskGroup(of:Void.self) { group in
   for _ in 0..<12 { group.addTask {
    try await pool.withPermit {
     await counter.enter(); try await Task.sleep(for:.milliseconds(30)); await counter.leave()
    }
   }}
   try await group.waitForAll()
  }
  let peak = await counter.peak; precondition(peak == 3)
  let gate = DownloadPermitPool(limit:1)
  let owner = Task { try await gate.withPermit { try await Task.sleep(for:.milliseconds(300)) } }
  try await Task.sleep(for:.milliseconds(30))
  let queued = Task<Void, Error> { try await gate.withPermit { () -> Void in preconditionFailure("cancelled queued task started") } }
  try await Task.sleep(for:.milliseconds(30)); queued.cancel()
  do { try await queued.value; fatalError("queued cancellation missing") } catch is CancellationError {}
  try await owner.value
  try await gate.withPermit { () }
  print("PASS: bounded concurrency, queued cancellation, no permit leaks")
  let blocked = Task { try await pool.withPermit { try await NativeFileTransfer(destination:root.appendingPathComponent("blocked")) { _,_ in }.run(url:base.appendingPathComponent("blocked")) } }
  try await Task.sleep(for:.milliseconds(40))
  let begin = Date()
  try await pool.withPermit { try await NativeFileTransfer(destination:root.appendingPathComponent("fast")) { _,_ in }.run(url:base.appendingPathComponent("fast")) }
  precondition(Date().timeIntervalSince(begin) < 1.5)
  try await blocked.value
  print("PASS: stalled transfer does not block another download")
 }
}
'''
try:
    with tempfile.TemporaryDirectory(prefix='pastel-native-test-') as d:
        folder = pathlib.Path(d); (folder/'expected').write_bytes(BODY); (folder/'test.swift').write_text(harness)
        subprocess.run(['xcrun','swiftc','-swift-version','6','-strict-concurrency=complete','-parse-as-library',str(ROOT/'Pastel/NativeDownloadEngine.swift'),str(folder/'test.swift'),'-o',str(folder/'test')],check=True)
        subprocess.run([str(folder/'test'),d,f'http://127.0.0.1:{server.server_port}'],check=True,timeout=30)
    assert any(path == '/slow' and 0 < start < len(BODY) and validator == '"fixture-v1"' for path,start,validator in requests)
finally:
    server.shutdown(); server.server_close()
