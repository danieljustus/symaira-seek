import Foundation
import Observation
import SymairaToolKit
import SymairaDaemonKit

/// Manages the embedded symseek engine process on macOS.
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

    private let supervisor = DaemonSupervisor()
    private let maxLogs = 500
    private var currentPort: Int = 8080

    public init() {
        setupSupervisor()
    }

    private func setupSupervisor() {
        supervisor.onLog = { [weak self] logLine in
            Task { @MainActor [weak self] in
                self?.appendLog("[\(logLine.isError ? "stderr" : "stdout")] \(logLine.text)")
            }
        }
        supervisor.onStateChange = { [weak self] newState in
            Task { @MainActor [weak self] in
                guard let self else { return }
                switch newState {
                case .stopped:
                    self.state = .stopped
                case .starting:
                    self.state = .starting
                case .running:
                    self.state = .running(port: self.currentPort)
                case .failed(let err):
                    self.state = .failed(err)
                }
            }
        }
    }

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

        self.currentPort = port
        _ = supervisor.start(executable: binaryURL, arguments: ["serve", "--port", "\(port)"])
    }

    public func stop() {
        supervisor.stop()
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
