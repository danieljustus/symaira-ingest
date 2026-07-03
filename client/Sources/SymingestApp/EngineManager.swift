import Foundation
import Observation

@Observable
@MainActor
public final class EngineManager {
    public enum State: Sendable, Equatable {
        case stopped
        case starting
        case running
        case failed(String)
    }

    public private(set) var state: State = .stopped
    public private(set) var logs: [String] = []

    public var isRunning: Bool {
        return state == .running
    }

    nonisolated(unsafe) private var process: Process?
    private var stdoutFH: FileHandle?
    private var stderrFH: FileHandle?

    private let maxLogs = 500

    public init() {}

    nonisolated deinit {
        process?.terminate()
    }

    public func start(config: ConfigStore) async {
        guard !isRunning else { return }

        state = .starting
        appendLog("[watcher] Starting symingest watch…")

        guard !config.inboxPath.isEmpty else {
            state = .failed("No inbox directory configured")
            appendLog("[watcher] ERROR: Inbox path is empty. Please set it in Settings.")
            return
        }
        
        guard !config.vault.isEmpty else {
            state = .failed("No vault directory configured")
            appendLog("[watcher] ERROR: Vault path is empty. Please set it in Settings.")
            return
        }

        guard let binaryURL = CLIClient.shared.locateBinary(customPath: config.customBinaryPath) else {
            state = .failed("symingest binary not found")
            appendLog("[watcher] ERROR: symingest binary not found")
            return
        }

        guard FileManager.default.isExecutableFile(atPath: binaryURL.path) else {
            state = .failed("symingest binary is not executable")
            appendLog("[watcher] ERROR: binary not executable at \(binaryURL.path)")
            return
        }

        let proc = Process()
        proc.executableURL = binaryURL
        proc.arguments = [
            "watch",
            config.inboxPath
        ]

        let stdoutPipe = Pipe()
        let stderrPipe = Pipe()
        proc.standardOutput = stdoutPipe
        proc.standardError = stderrPipe

        // Set Environment
        var env = ProcessInfo.processInfo.environment
        env["SYMINGEST_VAULT"] = config.vault
        env["SYMINGEST_ARCHIVE"] = config.archivePath
        env["SYMINGEST_DB"] = config.dbPath
        env["SYMINGEST_OCR_LANG"] = config.ocrLang
        
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
                    self.appendLog("[watcher] Process exited with code \(exitCode)")
                } else {
                    self.state = .stopped
                    self.appendLog("[watcher] Process stopped cleanly")
                }
                self.cleanup()
            }
        }

        do {
            try proc.run()
            self.process = proc
            state = .running
            appendLog("[watcher] Process started (PID \(proc.processIdentifier))")
        } catch {
            state = .failed(error.localizedDescription)
            appendLog("[watcher] Failed to start: \(error.localizedDescription)")
            cleanup()
        }
    }

    public func stop() {
        guard let proc = process, proc.isRunning else {
            state = .stopped
            return
        }

        appendLog("[watcher] Stopping…")
        proc.terminate()

        Task {
            try? await Task.sleep(for: .seconds(3))
            if proc.isRunning {
                appendLog("[watcher] Force killing process")
                proc.interrupt()
            }
        }
    }

    private func cleanup() {
        stdoutFH?.readabilityHandler = nil
        stderrFH?.readabilityHandler = nil
        stdoutFH = nil
        stderrFH = nil
        process = nil
    }

    private func processOutput(_ text: String, source: String) {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }

        for line in trimmed.components(separatedBy: .newlines) {
            let trimmedLine = line.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !trimmedLine.isEmpty else { continue }
            appendLog(trimmedLine)
        }
    }

    private func appendLog(_ message: String) {
        logs.append(message)
        if logs.count > maxLogs {
            logs.removeFirst(logs.count - maxLogs)
        }
    }
}
