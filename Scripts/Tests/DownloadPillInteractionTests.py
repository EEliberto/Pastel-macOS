"""Requires a macOS 26 desktop session; only sends events to its own test window."""
from pathlib import Path
import tempfile, subprocess, os
if os.environ.get("PASTEL_UI_ARTIFACTS"):
    Path(os.environ["PASTEL_UI_ARTIFACTS"]).mkdir(parents=True,exist_ok=True)
root = Path(__file__).resolve().parents[2]
os.chdir(root)
s=Path('Pastel/PastelApp.swift').read_text()
pill=s[s.index('struct DownloadProgressPill:'):s.index('struct SelectionSummaryCard:')]
source='''import SwiftUI
import AppKit
import Observation
enum StoreOperationPhase { case waiting; var title:String { "等待认证" }; var message:String { title } }
@Observable @MainActor final class State { var paused=false; var pauses=0; var resumes=0; var cancels=0; var progress=0.67; var selected=true }
struct TestView: View {
 let state:State
 @Namespace var outer
 var body:some View {
  ZStack {
   (state.selected ? Color.blue : Color(nsColor: .controlBackgroundColor))
   GlassEffectContainer(spacing:0) {
    DownloadProgressPill(progress:state.progress,isPaused:state.paused,
      onResume:{state.resumes += 1;state.paused=false},
      onPause:{state.pauses += 1;state.paused=true},onCancel:{state.cancels += 1;state.paused=false})
      .glassEffectID("version-row-action",in:outer)
      .glassEffectTransition(.matchedGeometry)
   }
  }.frame(width:400,height:200)
 }
}
@main struct Test {
 @MainActor static func main() {
 let app=NSApplication.shared;app.setActivationPolicy(.regular)
 let state=State()
 let window=NSWindow(contentRect:NSRect(x:200,y:200,width:400,height:200),styleMask:[.titled,.closable],backing:.buffered,defer:false)
 window.contentView=NSHostingView(rootView:TestView(state:state))
 window.makeKeyAndOrderFront(nil);app.activate(ignoringOtherApps:true)
 func click(_ x:CGFloat) {
 for type in [NSEvent.EventType.leftMouseDown,.leftMouseUp] {
 let event=NSEvent.mouseEvent(with:type,location:NSPoint(x:x,y:100),modifierFlags:[],timestamp:ProcessInfo.processInfo.systemUptime,windowNumber:window.windowNumber,context:nil,eventNumber:0,clickCount:1,pressure:1)!
 window.sendEvent(event)
 }
 }
 func capture(_ name: String) {
  guard let directory=ProcessInfo.processInfo.environment["PASTEL_UI_ARTIFACTS"] else { return }
  let process=Process();process.executableURL=URL(fileURLWithPath:"/usr/sbin/screencapture")
  process.arguments=["-x","-o","-l",String(window.windowNumber),directory+"/"+name+".png"]
  do { try process.run();process.waitUntilExit();precondition(process.terminationStatus==0) }
  catch { preconditionFailure("Unable to capture glass rendering: \(error)") }
  guard let data=try? Data(contentsOf:URL(fileURLWithPath:directory+"/"+name+".png")),
        let bitmap=NSBitmapImageRep(data:data) else {
   preconditionFailure("Unable to read captured pixels")
  }
  var redPixels=0
  // Only inspect the central control, excluding window traffic lights.
  for y in (bitmap.pixelsHigh*2/5)..<(bitmap.pixelsHigh*7/10) {
   for x in (bitmap.pixelsWide*2/5)..<(bitmap.pixelsWide*3/5) {
    if let color=bitmap.colorAt(x:x,y:y)?.usingColorSpace(.deviceRGB),
       color.redComponent>0.6 && color.greenComponent<0.45 && color.blueComponent<0.45 {
     redPixels += 1
    }
   }
  }
  precondition(name == "paused" ? redPixels>0 : redPixels==0,
               "Delete icon leaked into progress, or disappeared from paused controls")
 }
 Task { @MainActor in
  try? await Task.sleep(for:.seconds(1))
  capture("running")
  for delay in [30,60,100,200,350,30,60,100,200,350] {
   state.paused=false
   try? await Task.sleep(for:.milliseconds(400))
   let before=state.resumes
   let pauses=state.pauses
   click(200)
   try? await Task.sleep(for:.milliseconds(delay))
   precondition(state.pauses == pauses + 1 && state.paused, "Pause hit region became stale")
   click(183)
   try? await Task.sleep(for:.milliseconds(400))
   precondition(state.resumes == before + 1 && !state.paused, "Resume did not update the same view")
  }
  capture("resumed-after-ten-cycles")
  state.progress=0.99;state.selected=false
  try? await Task.sleep(for:.milliseconds(350))
  capture("99-percent-unselected")
  state.progress=1
  try? await Task.sleep(for:.milliseconds(350))
  capture("100-percent-unselected")
  state.progress=0.99;state.selected=true
  try? await Task.sleep(for:.milliseconds(350))
  capture("99-percent-selected")
  click(200)
  try? await Task.sleep(for:.milliseconds(350))
  capture("paused")
  click(183)
  try? await Task.sleep(for:.milliseconds(350))
  capture("resumed")
  click(200)
  try? await Task.sleep(for:.milliseconds(60))
  click(217)
  try? await Task.sleep(for:.milliseconds(250))
  precondition(state.cancels == 1 && !state.paused)
  print("PASS: ten rapid pause/resume cycles and cancel through actual SwiftUI mouse hit testing")
  app.terminate(nil)
 }
 app.run()
 }
}
'''
with tempfile.TemporaryDirectory(prefix="pastel-pill-ui-") as directory:
    folder=Path(directory)
    (folder/'test.swift').write_text(source+'\n'+pill)
    subprocess.run(['xcrun','swiftc','-swift-version','6','-target','arm64-apple-macos26.0','-parse-as-library',str(folder/'test.swift'),'-o',str(folder/'test')],check=True)
    subprocess.run([str(folder/'test')],check=True,timeout=30)
