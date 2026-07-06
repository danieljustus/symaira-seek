import Foundation
import Observation
import SymairaToolKit

/// Manages the embedded symseek engine process on macOS.
///
/// NOTE (DaemonKit v0.2 requirements): this is a long-running daemon
/// supervisor — start/stop lifecycle, streaming log capture via
/// readabilityHandler, terminate-then-interrupt escalation, state machine.
/// Together with symaira-memory's daemon start this defines the scope of
/// corekit-appkit's future SymairaDaemonKit; only binary discovery is
/// shared for now.
@Observable
@MainActor
public final class EngineManager {
    public enum State: Sendable {
        case stopped
        case starting
        case running(port: Int)
        case failed(String)
    }

    public private(set) var state: State = .stopped
    public private(set) var logs: [String] = []

    public var isRunning: Bool {
        if case .running = state { return true }
        return false
    }

    public var port: Int? {
        if case .running(let p) = state { return p }
        return nil
    }

    private final class ProcessHandle {
        var process: Process?

        deinit {
            process?.terminate()
        }
    }

    private let processHandle = ProcessHandle()
    private var stdoutFH: FileHandle?
    private var stderrFH: FileHandle?

    private let maxLogs = 500

    public init() {}

    public func start(port: Int = 8080) async {
        guard !isRunning else { return }

        state = .starting
        appendLog("[engine] Starting symseek REST server on port \(port)…")

        guard let binaryURL = locateBinary() else {
            state = .failed("symseek binary not found in app bundle Resources")
            appendLog("[engine] ERROR: symseek binary not found")
            return
        }

        guard FileManager.default.isExecutableFile(atPath: binaryURL.path) else {
            state = .failed("symseek binary is not executable")
            appendLog("[engine] ERROR: binary not executable at \(binaryURL.path)")
            return
        }

        let proc = Process()
        proc.executableURL = binaryURL
        proc.arguments = [
            "serve",
            "--port", "\(port)",
        ]

        let stdoutPipe = Pipe()
        let stderrPipe = Pipe()
        proc.standardOutput = stdoutPipe
        proc.standardError = stderrPipe

        var env = ProcessInfo.processInfo.environment
        if let path = env["PATH"] {
            env["PATH"] = "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:\(path)"
        } else {
            env["PATH"] = "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin"
        }
        proc.environment = env

        let outFH = stdoutPipe.fileHandleForReading
        let errFH = stderrPipe.fileHandleForReading
        self.stdoutFH = outFH
        self.stderrFH = errFH

        outFH.readabilityHandler = { [weak self] handle in
            let data = handle.availableData
            guard !data.isEmpty, let text = String(data: data, encoding: .utf8) else { return }
            Task { @MainActor [weak self] in
                self?.processOutput(text, source: "stdout")
            }
        }

        errFH.readabilityHandler = { [weak self] handle in
            let data = handle.availableData
            guard !data.isEmpty, let text = String(data: data, encoding: .utf8) else { return }
            Task { @MainActor [weak self] in
                self?.processOutput(text, source: "stderr")
            }
        }

        proc.terminationHandler = { [weak self] proc in
            Task { @MainActor [weak self] in
                guard let self else { return }
                let exitCode = proc.terminationStatus
                if exitCode != 0 {
                    self.state = .failed("Process exited with code \(exitCode)")
                    self.appendLog("[engine] Process exited with code \(exitCode)")
                } else {
                    self.state = .stopped
                    self.appendLog("[engine] Process stopped cleanly")
                }
                self.cleanup()
            }
        }

        do {
            try proc.run()
            self.processHandle.process = proc
            appendLog("[engine] Process started (PID \(proc.processIdentifier))")
            
            // Mark as running. Since symseek starts the server immediately,
            // we will transition to running state quickly.
            // In processOutput, we will also scan for "HTTP REST Server implementation starting" message.
            state = .running(port: port)
        } catch {
            state = .failed(error.localizedDescription)
            appendLog("[engine] Failed to start: \(error.localizedDescription)")
            cleanup()
        }
    }

    public func stop() {
        guard let proc = processHandle.process, proc.isRunning else {
            state = .stopped
            return
        }

        appendLog("[engine] Stopping…")
        proc.terminate()

        Task {
            try? await Task.sleep(for: .seconds(3))
            if proc.isRunning {
                appendLog("[engine] Force killing process")
                proc.interrupt()
            }
        }
    }

    private func cleanup() {
        stdoutFH?.readabilityHandler = nil
        stderrFH?.readabilityHandler = nil
        stdoutFH = nil
        stderrFH = nil
        processHandle.process = nil
    }

    private func processOutput(_ text: String, source: String) {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }

        for line in trimmed.components(separatedBy: .newlines) {
            let trimmedLine = line.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !trimmedLine.isEmpty else { continue }

            appendLog("[\(source)] \(trimmedLine)")
        }
    }

    private func appendLog(_ message: String) {
        logs.append(message)
        if logs.count > maxLogs {
            logs.removeFirst(logs.count - maxLogs)
        }
    }

    private func locateBinary() -> URL? {
        // Repo root (../symseek) as extra fallback keeps the pre-AppKit dev
        // workflow working when running without a bundled binary.
        let projectRoot = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent() // SymseekApp/
            .deletingLastPathComponent() // Sources/
            .deletingLastPathComponent() // client/
            .deletingLastPathComponent() // repo root
        let locator = BinaryLocator(extraDirectories: ["/opt/homebrew/bin", "/usr/local/bin", projectRoot.path])
        return locator.locate("symseek")?.url
    }
}
