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

        // First, check if a daemon is already running on the target port.
        // This handles the case where a brew-installed symseek daemon is
        // already serving (e.g. /opt/homebrew/bin/symseek serve).
        if await checkExistingDaemon(port: port) {
            appendLog("[engine] Detected running daemon on port \(port), adopting it")
            return
        }

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

    /// Try to detect an already-running symseek daemon by making a status
    /// request to the given port.  Returns `true` and transitions the
    /// engine state to `.running` if a daemon responds.
    private func checkExistingDaemon(port: Int) async -> Bool {
        let url = URL(string: "http://127.0.0.1:\(port)/status")!
        var request = URLRequest(url: url)
        request.timeoutInterval = 1.5

        do {
            let (data, response) = try await URLSession.shared.data(for: request)
            guard let httpResponse = response as? HTTPURLResponse,
                  httpResponse.statusCode == 200,
                  !data.isEmpty else {
                return false
            }
            // Daemon responded — adopt it.
            currentPort = port
            state = .running(port: port)
            return true
        } catch {
            // No daemon on this port — also try the CLI status command as
            // a fallback in case the daemon listens on a different port.
            return await checkViaCLI()
        }
    }

    /// Fallback: run `symseek status` via CLI to discover a running daemon
    /// whose port we might not have guessed.  Parses the output for
    /// known status indicators.
    private func checkViaCLI() async -> Bool {
        guard let binaryURL = locateBinary() else { return false }

        let proc = Process()
        proc.executableURL = binaryURL
        proc.arguments = ["status"]

        let pipe = Pipe()
        proc.standardOutput = pipe
        proc.standardError = Pipe()

        do {
            try proc.run()
            proc.waitUntilExit()

            guard proc.terminationStatus == 0 else { return false }

            let data = pipe.fileHandleForReading.readDataToEndOfFile()
            guard let output = String(data: data, encoding: .utf8),
                  !output.isEmpty else { return false }

            // A successful "symseek status" with data means the daemon is
            // reachable.  Default to port 8080 (most common for brew).
            currentPort = 8080
            state = .running(port: 8080)
            return true
        } catch {
            return false
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
