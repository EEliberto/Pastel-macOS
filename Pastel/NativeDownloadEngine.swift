import CryptoKit
import Foundation

// The Store adapter owns Apple protocol compatibility only. Scheduling, bytes,
// retries and partial-file ownership belong to this native engine.
struct NativeDownloadSpec: Codable, Sendable {
  let id: String
  let directory: URL
  let workspace: URL
  let node: URL
  let project: URL
  let environment: [String: String]
  let credentials: Data
  let email: String
  let removeUpdates: Bool
}

enum NativeDownloadEvent: Sendable {
  case waiting, authenticating
  case transferring(Int64, Int64)
  case packaging
}

struct NativeDownloadError: LocalizedError, Sendable {
  let code: String
  let message: String
  var errorDescription: String? { message }
  init(code: String, message: String) {
    self.code = code
    self.message = NSLocalizedString(message, comment: "Native download error")
  }
}

// FIFO, cancellation-aware permits. A cancelled owner retains its permit until
// its operation has actually stopped; resume cannot overlap an old writer.
actor DownloadPermitPool {
  private var available: Int
  private var waiters: [(UUID, CheckedContinuation<Void, Error>)] = []
  init(limit: Int) { available = limit }
  func withPermit<T: Sendable>(_ operation: @Sendable () async throws -> T) async throws -> T {
    let id = UUID()
    try await withTaskCancellationHandler {
      try Task.checkCancellation()
      try await withCheckedThrowingContinuation {
        (continuation: CheckedContinuation<Void, Error>) in
        if available > 0 {
          available -= 1
          continuation.resume()
        } else {
          waiters.append((id, continuation))
        }
      }
    } onCancel: {
      Task { await self.cancel(id) }
    }
    defer { release() }
    try Task.checkCancellation()
    return try await operation()
  }
  private func cancel(_ id: UUID) {
    guard let index = waiters.firstIndex(where: { $0.0 == id }) else { return }
    waiters.remove(at: index).1.resume(throwing: CancellationError())
  }
  private func release() {
    if waiters.isEmpty { available += 1 } else { waiters.removeFirst().1.resume() }
  }
}

// Safety: process/cancel state is protected by lock. Pipe I/O and waiting run
// on a utility dispatch queue, never on MainActor or a cooperative executor.
private final class NativeWorker: @unchecked Sendable {
  private let lock = NSLock()
  private var process: Process?
  private var cancelled = false
  func stop() {
    lock.lock()
    cancelled = true
    let child = process
    if child?.isRunning == true { child?.terminate() }
    lock.unlock()
    if let child {
      DispatchQueue.global(qos: .utility).asyncAfter(deadline: .now() + 4) {
        self.lock.lock()
        defer { self.lock.unlock() }
        if self.process === child, child.isRunning { kill(child.processIdentifier, SIGKILL) }
      }
    }
  }
  func run(
    executable: URL, arguments: [String], directory: URL, environment: [String: String], input: Data
  ) async throws -> Data {
    try await withTaskCancellationHandler {
      try await withCheckedThrowingContinuation { continuation in
        DispatchQueue.global(qos: .utility).async { [self] in
          let child = Process()
          let stdin = Pipe()
          let stdout = Pipe()
          child.executableURL = executable
          child.arguments = arguments
          child.currentDirectoryURL = directory
          child.environment = environment
          child.standardInput = stdin
          child.standardOutput = stdout
          child.standardError = FileHandle.nullDevice
          let deadline = DispatchWorkItem { [weak self] in self?.stop() }
          defer { deadline.cancel() }
          do {
            self.lock.lock()
            if self.cancelled {
              self.lock.unlock()
              throw CancellationError()
            }
            do { try child.run() } catch {
              self.lock.unlock()
              throw error
            }
            self.process = child
            self.lock.unlock()
            DispatchQueue.global(qos: .utility).asyncAfter(
              deadline: .now() + 720, execute: deadline)
            // Write concurrently: a large SINF request must not deadlock
            // against a helper that is already producing output.
            DispatchQueue.global(qos: .utility).async {
              try? stdin.fileHandleForWriting.write(contentsOf: input)
              try? stdin.fileHandleForWriting.close()
            }
            var output = Data()
            while let chunk = try stdout.fileHandleForReading.read(upToCount: 65_536),
              !chunk.isEmpty
            {
              guard output.count + chunk.count <= 16 * 1024 * 1024 else {
                self.stop()
                throw NativeDownloadError(code: "INVALID_RESPONSE", message: "下载组件返回了过大的响应。")
              }
              output.append(chunk)
            }
            child.waitUntilExit()
            self.lock.lock()
            self.process = nil
            let stopped = self.cancelled
            self.lock.unlock()
            if stopped {
              throw NativeDownloadError(code: "INTERRUPTED", message: "下载组件等待超时或已停止，请重试。")
            }
            continuation.resume(returning: output)
          } catch {
            self.stop()
            child.waitUntilExitIfLaunched()
            continuation.resume(throwing: error)
          }
        }
      }
    } onCancel: {
      self.stop()
    }
  }
}

extension Process {
  fileprivate func waitUntilExitIfLaunched() { if processIdentifier > 0 { waitUntilExit() } }
}

private struct NativePlan: Decodable, Sendable {
  let url: String
  let artworkURL: String
  let md5: String
  let fileName: String
  let versionID: String
  let payload: String
}

// One delegate queue owns all file operations. Only task/cancel state crosses
// queues, under lock. Completion is delivered after the partial file is closed.
final class NativeFileTransfer: NSObject, URLSessionDataDelegate, @unchecked Sendable {
  private let lock = NSLock()
  private var task: URLSessionDataTask?
  private var cancelled = false
  private var continuation: CheckedContinuation<Void, Error>?
  private var file: FileHandle?
  private var offset: Int64 = 0
  private var expected: Int64 = 0
  private var failure: Error?
  private var completeRange = false
  private var lastReport = Date.distantPast
  private let destination: URL
  private let validatorURL: URL
  private let progress: @Sendable (Int64, Int64) -> Void
  init(destination: URL, progress: @escaping @Sendable (Int64, Int64) -> Void) {
    self.destination = destination
    validatorURL = destination.appendingPathExtension("validator")
    self.progress = progress
  }
  private func cancel() {
    lock.lock()
    cancelled = true
    task?.cancel()
    lock.unlock()
  }
  func run(url: URL) async throws {
    try await withTaskCancellationHandler {
      try Task.checkCancellation()
      try await withCheckedThrowingContinuation {
        (continuation: CheckedContinuation<Void, Error>) in
        let queue = OperationQueue()
        queue.maxConcurrentOperationCount = 1
        queue.qualityOfService = .utility
        let configuration = URLSessionConfiguration.ephemeral
        configuration.timeoutIntervalForRequest = 45
        configuration.timeoutIntervalForResource = 86_400
        configuration.requestCachePolicy = .reloadIgnoringLocalCacheData
        configuration.httpMaximumConnectionsPerHost = 4
        let session = URLSession(configuration: configuration, delegate: self, delegateQueue: queue)
        queue.addOperation {
          self.continuation = continuation
          self.offset =
            ((try? FileManager.default.attributesOfItem(atPath: self.destination.path)[.size])
            as? NSNumber)?.int64Value ?? 0
          var request = URLRequest(url: url)
          request.setValue("identity", forHTTPHeaderField: "Accept-Encoding")
          if self.offset > 0 {
            request.setValue("bytes=\(self.offset)-", forHTTPHeaderField: "Range")
            if let validator = try? String(contentsOf: self.validatorURL, encoding: .utf8),
              !validator.isEmpty
            {
              request.setValue(validator, forHTTPHeaderField: "If-Range")
            }
          }
          self.lock.lock()
          let task = session.dataTask(with: request)
          self.task = task
          task.resume()
          if self.cancelled { task.cancel() }
          self.lock.unlock()
        }
      }
    } onCancel: {
      self.cancel()
    }
  }
  func urlSession(
    _ session: URLSession, dataTask: URLSessionDataTask, didReceive response: URLResponse,
    completionHandler: @escaping @Sendable (URLSession.ResponseDisposition) -> Void
  ) {
    do {
      guard let http = response as? HTTPURLResponse else {
        throw error("INVALID_RESPONSE", "下载服务器响应无效。")
      }
      let range = http.value(forHTTPHeaderField: "Content-Range") ?? ""
      if http.statusCode == 416, range == "bytes */\(offset)", offset > 0 {
        completeRange = true
        expected = offset
        completionHandler(.cancel)
        return
      }
      guard http.statusCode == 200 || http.statusCode == 206 else {
        throw error(
          "HTTP_\(http.statusCode)", String(localized: "下载服务器返回 HTTP \(http.statusCode)。"))
      }
      if http.statusCode == 206 {
        let parts = range.replacingOccurrences(of: "bytes ", with: "").components(
          separatedBy: CharacterSet(charactersIn: "-/"))
        guard range.hasPrefix("bytes "), parts.count == 3,
          let start = Int64(parts[0]), start == offset,
          let end = Int64(parts[1]), let total = Int64(parts[2]), end >= start, total > end
        else {
          throw error("INVALID_RANGE", "服务器返回的断点位置不一致，请重试。")
        }
        expected = total
        if let saved = try? String(contentsOf: validatorURL, encoding: .utf8),
          let current = http.value(forHTTPHeaderField: "ETag")
            ?? http.value(forHTTPHeaderField: "Last-Modified"), saved != current
        {
          throw error("INVALID_RANGE", "下载文件已变化，正在重新连接。")
        }
      } else {
        offset = 0
        expected = response.expectedContentLength
      }
      if !FileManager.default.fileExists(atPath: destination.path) {
        guard
          FileManager.default.createFile(
            atPath: destination.path, contents: nil, attributes: [.posixPermissions: 0o600])
        else {
          throw error("FILE_FAILED", "无法创建下载文件。")
        }
      }
      file = try FileHandle(forWritingTo: destination)
      try file?.truncate(atOffset: UInt64(offset))
      try file?.seek(toOffset: UInt64(offset))
      if let validator = http.value(forHTTPHeaderField: "ETag")
        ?? http.value(forHTTPHeaderField: "Last-Modified")
      {
        try Data(validator.utf8).write(to: validatorURL, options: .atomic)
      } else {
        try? FileManager.default.removeItem(at: validatorURL)
      }
      progress(offset, expected)
      completionHandler(.allow)
    } catch {
      failure = error
      completionHandler(.cancel)
    }
  }
  func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
    guard failure == nil else { return }
    do {
      try file?.write(contentsOf: data)
      offset += Int64(data.count)
      if Date().timeIntervalSince(lastReport) >= 0.25 {
        progress(offset, expected)
        lastReport = Date()
      }
    } catch {
      failure = error
      dataTask.cancel()
    }
  }
  func urlSession(
    _ session: URLSession, task: URLSessionTask, didCompleteWithError networkError: Error?
  ) {
    do {
      try file?.synchronize()
      try file?.close()
    } catch { if failure == nil { failure = error } }
    file = nil
    lock.lock()
    self.task = nil
    let stopped = cancelled
    lock.unlock()
    let resultError: Error? =
      stopped ? CancellationError() : failure ?? (completeRange ? nil : networkError)
    if let resultError {
      continuation?.resume(throwing: resultError)
    } else if expected > 0 && expected != offset {
      continuation?.resume(throwing: error("INCOMPLETE", "下载连接提前中断，将从已下载的位置重试。"))
    } else {
      progress(offset, expected)
      continuation?.resume()
    }
    continuation = nil
    session.finishTasksAndInvalidate()
  }
  func urlSession(
    _ session: URLSession, task: URLSessionTask,
    willPerformHTTPRedirection response: HTTPURLResponse,
    newRequest request: URLRequest, completionHandler: @escaping @Sendable (URLRequest?) -> Void
  ) {
    // A signed HTTPS download must not silently redirect to cleartext HTTP.
    completionHandler(request.url?.scheme == "https" ? request : nil)
  }
  private func error(_ code: String, _ message: String) -> NativeDownloadError {
    .init(code: code, message: message)
  }
}

actor NativeDownloadEngine {
  static let shared = NativeDownloadEngine()
  private let authentication = DownloadPermitPool(limit: 1)
  private let transfers = DownloadPermitPool(limit: 3)
  private let packaging = DownloadPermitPool(limit: 1)

  func run(_ spec: NativeDownloadSpec, event: @escaping @Sendable (NativeDownloadEvent) -> Void)
    async throws -> URL
  {
    event(.waiting)
    var prepared = try await prepare(spec, event: event)
    var plan = try decodePlan(prepared)
    let raw = spec.workspace.appendingPathComponent("download.ipa.tmp")
    try adoptLegacyPartial(spec.workspace, destination: raw)
    var refreshed = false
    while true {
      let currentPlan = plan
      event(.waiting)
      do {
        try await transfers.withPermit {
          var attempt = 0
          while true {
            try Task.checkCancellation()
            do {
              let url = try Self.secureURL(currentPlan.url)
              try await NativeFileTransfer(destination: raw) { received, total in
                event(.transferring(received, total))
              }.run(url: url)
              break
            } catch {
              try Task.checkCancellation()
              let code = (error as? NativeDownloadError)?.code ?? ""
              // Refresh an expired signed URL outside the transfer permit;
              // never mislabel CDN rejection as a password error.
              if ["HTTP_401", "HTTP_403"].contains(code) {
                throw NativeDownloadError(code: "URL_EXPIRED", message: "下载地址已过期，请重试。")
              }
              guard attempt < 3, Self.retryable(error) else { throw error }
              attempt += 1
              if code == "INVALID_RANGE" || code == "HTTP_416" {
                try? FileManager.default.removeItem(at: raw)
                try? FileManager.default.removeItem(at: raw.appendingPathExtension("validator"))
              }
              try await Task.sleep(for: .seconds(1 << attempt))
            }
          }
        }
        break
      } catch let error as NativeDownloadError where error.code == "URL_EXPIRED" && !refreshed {
        refreshed = true
        prepared = try await prepare(spec, event: event)
        plan = try decodePlan(prepared)
      }
    }
    try Task.checkCancellation()
    // Hash and ZIP work are never performed on the UI actor.
    try await Self.verify(raw, md5: plan.md5)
    event(.waiting)
    let planForPackaging = plan
    let preparedForPackaging = prepared
    let result = try await packaging.withPermit {
      try Task.checkCancellation()
      event(.packaging)
      let artwork = spec.workspace.appendingPathComponent("artwork")
      if !planForPackaging.artworkURL.isEmpty {
        try await NativeFileTransfer(destination: artwork) { _, _ in }.run(
          url: Self.secureURL(planForPackaging.artworkURL))
      }
      let name = planForPackaging.fileName
      guard !name.isEmpty, name == URL(fileURLWithPath: name).lastPathComponent,
        name.lowercased().hasSuffix(".ipa")
      else {
        throw NativeDownloadError(code: "INVALID_PATH", message: "下载组件返回了无效文件名。")
      }
      let filename = spec.removeUpdates ? String(name.dropLast(4)) + "_no-update.ipa" : name
      let staging = spec.workspace.appendingPathComponent(filename)
      guard
        let preparedObject = try JSONSerialization.jsonObject(with: preparedForPackaging)
          as? [String: Any]
      else {
        throw NativeDownloadError(code: "INVALID_RESPONSE", message: "授权响应无效。")
      }
      let request: [String: Any] = [
        "command": "finalizeNative", "nativePlan": preparedObject["nativePlan"]!,
        "account": preparedObject["account"] ?? [:], "email": spec.email,
        "rawPath": raw.path, "artworkPath": planForPackaging.artworkURL.isEmpty ? "" : artwork.path,
        "outputPath": staging.path, "removeAppStoreUpdateMetadata": spec.removeUpdates,
      ]
      let input = try JSONSerialization.data(withJSONObject: request)
      let output = try await NativeWorker().run(
        executable: spec.project.deletingLastPathComponent().appendingPathComponent("store-helper"),
        arguments: [], directory: spec.project, environment: spec.environment, input: input)
      _ = try Self.response(output)
      try Task.checkCancellation()
      let destination = spec.directory.appendingPathComponent(filename)
      // rename is atomic on this same filesystem, also replacing a prior
      // completed copy. Never remove the destination before a successful rename.
      guard rename(staging.path, destination.path) == 0 else {
        throw CocoaError(.fileWriteUnknown)
      }
      try? FileManager.default.removeItem(at: spec.workspace)
      return destination
    }
    // Authorization data lives only in this task's memory and private pipes.
    return result
  }

  private func prepare(
    _ spec: NativeDownloadSpec, event: @escaping @Sendable (NativeDownloadEvent) -> Void
  ) async throws -> Data {
    try await authentication.withPermit {
      event(.authenticating)
      let output = try await NativeWorker().run(
        executable: spec.node, arguments: ["main.js"], directory: spec.project,
        environment: spec.environment, input: spec.credentials)
      return try Self.response(output)
    }
  }
  private static func response(_ output: Data) throws -> Data {
    // The final JSON line is a private protocol record. Earlier adapter
    // diagnostics are discarded and never copied into the user's task log.
    guard let line = output.split(separator: 10).last(where: { $0.first == 123 }),
      let object = try JSONSerialization.jsonObject(with: Data(line)) as? [String: Any]
    else {
      throw NativeDownloadError(code: "INVALID_RESPONSE", message: "下载组件没有返回有效响应，请重试。")
    }
    guard object["ok"] as? Bool == true else {
      throw NativeDownloadError(
        code: object["code"] as? String ?? "STORE_FAILED",
        message: object["message"] as? String ?? "Apple 认证失败。")
    }
    return Data(line)
  }
  private func decodePlan(_ data: Data) throws -> NativePlan {
    struct Reply: Decodable { let nativePlan: NativePlan }
    return try JSONDecoder().decode(Reply.self, from: data).nativePlan
  }
  private static func secureURL(_ string: String) throws -> URL {
    guard var components = URLComponents(string: string), let host = components.host?.lowercased()
    else {
      throw NativeDownloadError(code: "INVALID_URL", message: "服务器未提供安全的下载地址。")
    }
    // Older Apple catalogs still return http URLs for HTTPS-capable CDN hosts.
    if components.scheme == "http", host.hasSuffix(".apple.com") || host.hasSuffix(".mzstatic.com")
    {
      components.scheme = "https"
    }
    guard components.scheme == "https", let url = components.url else {
      throw NativeDownloadError(code: "INVALID_URL", message: "服务器未提供安全的下载地址。")
    }
    return url
  }
  private static func retryable(_ error: Error) -> Bool {
    if let error = error as? NativeDownloadError {
      return [
        "HTTP_408", "HTTP_429", "HTTP_500", "HTTP_502", "HTTP_503", "HTTP_504", "HTTP_416",
        "INVALID_RANGE", "INCOMPLETE",
      ].contains(error.code)
    }
    let code = (error as NSError).code
    return (error as NSError).domain == NSURLErrorDomain
      && [
        NSURLErrorTimedOut, NSURLErrorNetworkConnectionLost, NSURLErrorNotConnectedToInternet,
        NSURLErrorCannotConnectToHost, NSURLErrorDNSLookupFailed,
      ].contains(code)
  }
  private func adoptLegacyPartial(_ workspace: URL, destination: URL) throws {
    guard !FileManager.default.fileExists(atPath: destination.path) else { return }
    // Earlier releases used the helper's name, optionally under .pastel-no-update.
    let folders = [workspace, workspace.appendingPathComponent(".pastel-no-update")]
    for folder in folders {
      let files =
        (try? FileManager.default.contentsOfDirectory(at: folder, includingPropertiesForKeys: nil))
        ?? []
      if let old = files.first(where: { $0.lastPathComponent.hasSuffix(".ipa.tmp") }) {
        try FileManager.default.moveItem(at: old, to: destination)
        return
      }
    }
  }
  private static func verify(_ file: URL, md5: String) async throws {
    guard !md5.isEmpty else { return }
    try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
      DispatchQueue.global(qos: .utility).async {
        do {
          let handle = try FileHandle(forReadingFrom: file)
          defer { try? handle.close() }
          var hash = Insecure.MD5()
          while let data = try handle.read(upToCount: 1_048_576), !data.isEmpty {
            hash.update(data: data)
          }
          let actual = hash.finalize().map { String(format: "%02x", $0) }.joined()
          guard actual.caseInsensitiveCompare(md5) == .orderedSame else {
            // A corrupt completed raw archive must not remain stuck in
            // an endless 416 / checksum-failure loop on resume.
            try? FileManager.default.removeItem(at: file)
            throw NativeDownloadError(code: "CHECKSUM_FAILED", message: "下载文件校验失败，请重试。")
          }
          continuation.resume()
        } catch { continuation.resume(throwing: error) }
      }
    }
  }
}
