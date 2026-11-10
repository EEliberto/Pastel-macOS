from pathlib import Path
import os, tempfile, subprocess, shutil, time, signal
root=Path(__file__).resolve().parents[2]
os.chdir(root)
node=os.environ.get("PASTEL_TEST_NODE") or shutil.which("node")
if not node: raise SystemExit("Set PASTEL_TEST_NODE to a Node.js 24 executable")
testroot=Path(tempfile.mkdtemp(prefix="pastel-resume-tests-"))
s=Path('Pastel/PastelApp.swift').read_text()
parts=[]
for start,end in [('struct RunConfig: Codable {','private struct NodeCredentialPayload'),('enum StoreOperationPhase:', 'func ipaIsVerificationChallenge'),('func ipaProgressValue(', 'func downloadErrorMessage'),('enum JobStatus:', 'struct DownloadFailure'),('@MainActor\n@Observable\nfinal class DownloadManager', '\nstruct AppSearchResult')]:
 a=s.index(start);b=s.index(end,a);parts.append(s[a:b])
a=s.index('struct DownloadedItem:');b=s.index('struct DownloadedAppGroup:',a)
parts.append(s[a:b])
a=s.index('            let label = job.label',s.index('private var libraryDisplayItems:'))
b=s.index('\n        }\n        return libraryFeature.downloadedItems + pending',a)
parts.append('@MainActor func pendingItem(job: DownloadManager.Job, config: RunConfig, existing: DownloadedItem?) -> DownloadedItem {\n'+s[a:b]+'\n}')
pre='''import Foundation
import Darwin
import Observation
import CryptoKit
let appDisplayName = "PastelResumeTests"
enum AppLanguage { static let effectiveCode = "zh-Hans" }
struct DownloadFailure { let jobID: String; let label: String; let message: String }
struct AppAcquisitionRequest { let id: String; let appName: String }
func ipaIsVerificationChallenge(_ value: String) -> Bool { false }
func downloadErrorMessage(from value: String) -> String { value }
struct StoredAccount { let id: UUID; let appleAccount: String }
struct StoredCredentials { let accounts: [StoredAccount] }
enum CredentialVault {
 static func load() throws -> StoredCredentials? { StoredCredentials(accounts: [StoredAccount(id: UUID(), appleAccount: "test@example.invalid")]) }
 static func loadPassword(for id: UUID) throws -> String { "private-password" }
}
enum DeviceGUIDStore { static func current() throws -> String { "5C9BA65DB0CF" } }
enum NodeRuntime {
 static func locate() throws -> (projectURL: URL, mainURL: URL, nodeURL: URL) {
 let root=URL(fileURLWithPath: "/tmp/pastel-resume/fixture")
 return (root, root.appendingPathComponent("main.js"), URL(fileURLWithPath: "/Applications/Pastel.app/Contents/Resources/node/bin/node"))
 }
 static func credentialInput(appleAccount: String, password: String, code: String) throws -> (pipe: Pipe, data: Data) { (Pipe(), Data("{}".utf8)) }
 static func baseEnvironment() -> [String:String] { ProcessInfo.processInfo.environment }
 static func sendCredentialInput(_ data: Data, through pipe: Pipe) throws { try pipe.fileHandleForWriting.write(contentsOf: data);try pipe.fileHandleForWriting.close() }
}
'''
# Keep the engine interface real; replace only its network worker for deterministic lifecycle tests.
engine=Path('Pastel/NativeDownloadEngine.swift').read_text()
pre += engine[engine.index('struct NativeDownloadSpec:'):engine.index('// FIFO,')]
pre += """
actor NativeDownloadEngine {
 static let shared = NativeDownloadEngine()
 func run(_ spec: NativeDownloadSpec, event: @escaping @Sendable (NativeDownloadEvent) -> Void) async throws -> URL {
  event(.transferring(50,100))
  do { try await Task.sleep(for:.seconds(60)) }
  catch {
   // A cancelled writer takes time to close; an immediate resume must wait.
   await Task.detached { try? await Task.sleep(for:.milliseconds(400)) }.value
   throw CancellationError()
  }
  return spec.directory.appendingPathComponent("test.ipa")
 }
}
"""
post='''
extension DownloadManager {
 @MainActor func seedCompletedJobForTesting(id: String) {
  jobs[id] = Job(id: id, label: "Completed download", status: .done, progress: 1)
 }
 @MainActor func installExitedProcessForTesting(id: String) throws {
  let task = Process(); task.executableURL = URL(fileURLWithPath: "/usr/bin/true")
  try task.run(); task.waitUntilExit()
  processes[id] = task
 }
}
@main struct Harness {
 @MainActor static func main() async throws {
 let root=URL(fileURLWithPath: "/tmp/pastel-resume/fixture")
 let journal=root.appendingPathComponent("journal.json")
 let downloads=root.appendingPathComponent("downloads")
 try FileManager.default.createDirectory(at: downloads, withIntermediateDirectories: true)
 let config=RunConfig(appleAccount:"test@example.invalid",password:"private-password",code:"123456",appID:"123",versionID:"456",downloadDir:downloads.path)
 let pendingJob=DownloadManager.Job(id:"version-task",label:"Douyin 40.9.0",status:.paused,progress:0.03)
 let pending=pendingItem(job:pendingJob,config:config,existing:nil)
 precondition(pending.appId=="123" && pending.groupKey=="123" && pending.version=="40.9.0")
 precondition(pending.appName=="Douyin" && pending.fileURL.scheme=="pastel-download")
 let merged=pendingItem(job:pendingJob,config:config,existing:pending)
 precondition(merged.groupKey==pending.groupKey)
 precondition(Dictionary(grouping:[pending,merged],by: \.groupKey).count==1)
 print("PASS: incomplete versions share app groups and never masquerade as IPA files")
 let manager=DownloadManager(journalURL:journal,automaticallyResume:false)
 if CommandLine.arguments.contains("--recover") {
   let recovered=DownloadManager(journalURL:journal)
   precondition(recovered.job("sample")?.progress==0.5)
   try await Task.sleep(for:.seconds(5))
   precondition(recovered.isRunning("sample"))
   recovered.cancel(id:"sample")
   try await Task.sleep(for:.milliseconds(700))
   precondition(recovered.job("sample")==nil)
   print("PASS: automatic recovery after SIGKILL")
   return
 }
 manager.seedCompletedJobForTesting(id:"redownload")
 manager.start(id:"redownload",label:"Fresh download",config:config)
 precondition(manager.job("redownload")?.progress == nil)
 precondition(manager.job("redownload")?.phase == .waiting)
 manager.cancel(id:"redownload")
 try await Task.sleep(for:.milliseconds(700))
 precondition(manager.job("redownload") == nil)
 print("PASS: fresh download clears a completed task's 100% before first UI update")
 manager.start(id:"sample",label:"Test download",config:config)
 try await Task.sleep(for:.milliseconds(600))
 precondition(manager.job("sample")?.progress==0.5)
 if CommandLine.arguments.contains("--crash") { kill(getpid(), SIGKILL); fatalError() }
 manager.pause(id:"sample")
 manager.resume(id:"sample")
 precondition(manager.job("sample")?.status == .running)
 precondition(manager.job("sample")?.progress == 0.5)
 try await Task.sleep(for:.milliseconds(900))
 precondition(manager.isRunning("sample"))
 precondition(manager.job("sample")?.status == .running)
 manager.pause(id:"sample")
 try await Task.sleep(for:.milliseconds(700))
 precondition(manager.job("sample")?.status == .paused)
 precondition(manager.job("sample")?.progress==0.5)
 let data=try String(contentsOf:journal,encoding:.utf8)
 precondition(!data.contains("private-password") && !data.contains("123456"))
 let restored=DownloadManager(journalURL:journal,automaticallyResume:false)
 precondition(restored.job("sample")?.status == .paused && restored.job("sample")?.progress==0.5)
 try restored.installExitedProcessForTesting(id:"sample")
 restored.resume(id:"sample")
 try await Task.sleep(for:.milliseconds(600))
 precondition(restored.isRunning("sample"))
 let key=SHA256.hash(data:Data("sample\\ntest@example.invalid".utf8)).map{String(format:"%02x",$0)}.joined()
 let partial=downloads.appendingPathComponent(".pastel-partials").appendingPathComponent(key)
 try FileManager.default.createDirectory(at:partial,withIntermediateDirectories:true)
 try Data("partial".utf8).write(to:partial.appendingPathComponent("app.ipa.tmp"))
 restored.cancel(id:"sample")
 try await Task.sleep(for:.milliseconds(700))
 precondition(restored.job("sample")==nil)
 precondition(!FileManager.default.fileExists(atPath:partial.path))
 precondition(try String(contentsOf:journal,encoding:.utf8)=="[]")
 print("PASS: immediate resume, exited-process cleanup, persisted progress, credential exclusion, cancel cleanup")
 }
}
'''
# Swift precondition autoclosure does not throw.
post=post.replace('precondition(try String(contentsOf:journal,encoding:.utf8)=="[]")','let finalJournal = try String(contentsOf:journal,encoding:.utf8); precondition(finalJournal=="[]")')
source=(pre+'\n'.join(parts)+post).replace('/tmp/pastel-resume',str(testroot)).replace('/Applications/Pastel.app/Contents/Resources/node/bin/node',node)
(testroot/'harness.swift').write_text(source)
f=testroot/'fixture';f.mkdir(exist_ok=True);(f/'main.js').write_text(Path("NodeProject/main.js").read_text().split("import 'dotenv/config';")[0]+"require('fs').writeFileSync('worker.pid',String(process.pid)); process.on('SIGTERM',()=>setTimeout(()=>process.exit(1),400)); process.stdin.resume(); console.log('0 MB / 0 MB 50%'); setInterval(()=>{},1000);\n")

try:
    subprocess.run(['xcrun','swiftc','-swift-version','6','-parse-as-library',str(testroot/'harness.swift'),'-o',str(testroot/'harness')],check=True)
    subprocess.run([str(testroot/'harness')],check=True)
    crashed=subprocess.run([str(testroot/'harness'),'--crash'])
    assert crashed.returncode == -9
    subprocess.run([str(testroot/'harness'),'--recover'],check=True)
finally:
    shutil.rmtree(testroot)
