import Foundation
import SymairaCLIRunner
import SymairaToolKit

public struct IngestJob: Codable, Identifiable, Sendable {
    public let id: Int64
    public let documentId: Int64
    public let kind: String
    public let status: String
    public let attempts: Int
    public let lastError: String?
    public let createdAt: String
    public let updatedAt: String
    public let sourcePath: String

    enum CodingKeys: String, CodingKey {
        case id
        case documentId = "document_id"
        case kind
        case status
        case attempts
        case lastError = "last_error"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
        case sourcePath = "source_path"
    }
}

public struct SwiftRule: Codable, Identifiable, Sendable {
    public let id: Int64
    public let pattern: String
    public let kind: String
    public let value: String
    public let createdAt: String

    enum CodingKeys: String, CodingKey {
        case id
        case pattern
        case kind
        case value
        case createdAt = "created_at"
    }
}

public struct DependencyReport: Sendable {
    public let symingestPath: String?
    public let tesseractPath: String?
    public let pdftoppmPath: String?
    public let sipsPath: String?
    public let textutilPath: String?
    public let pandocPath: String?
    public let libreOfficePath: String?
    public let sofficePath: String?

    public var isComplete: Bool {
        return symingestPath != nil && tesseractPath != nil && pdftoppmPath != nil
    }
}

public struct CLIConfigSnapshot: Sendable {
    public let vault: String
    public let ocrLang: String
    public let dbPath: String
    public let archivePath: String
    public let customBinaryPath: String

    @MainActor
    public init(config: ConfigStore) {
        self.vault = config.vault
        self.ocrLang = config.ocrLang
        self.dbPath = config.dbPath
        self.archivePath = config.archivePath
        self.customBinaryPath = config.customBinaryPath
    }
}

public final class CLIClient: Sendable {
    public static let shared = CLIClient()

    // OCR runs over large PDFs can take a while; keep the timeout generous.
    private let runner = CLIRunner(defaultTimeout: 600)

    private init() {}

    public func locateBinary(customPath: String) -> URL? {
        let override = customPath.isEmpty ? nil : URL(fileURLWithPath: customPath)
        return BinaryLocator(userOverride: override).locate("symingest")?.url
    }

    /// Shared discovery (bundle → exe dir → PATH → Homebrew prefixes) —
    /// also used for the external helpers (tesseract, pdftoppm, sips).
    /// Replaces the former `/usr/bin/which` subprocess.
    private func searchPathFor(_ executable: String) -> String? {
        BinaryLocator().locate(executable)?.url.path
    }

    public func checkDependencies(customPath: String) async -> DependencyReport {
        let sym = locateBinary(customPath: customPath)?.path
        let tess = searchPathFor("tesseract")
        let pdf = searchPathFor("pdftoppm")
        let sips = searchPathFor("sips")
        let textutil = searchPathFor("textutil")
        let pandoc = searchPathFor("pandoc")
        let libreOffice = searchPathFor("libreoffice")
        let soffice = searchPathFor("soffice")
        return DependencyReport(
            symingestPath: sym,
            tesseractPath: tess,
            pdftoppmPath: pdf,
            sipsPath: sips,
            textutilPath: textutil,
            pandocPath: pandoc,
            libreOfficePath: libreOffice,
            sofficePath: soffice
        )
    }

    private func applyConfigEnvironment(_ config: CLIConfigSnapshot, to env: inout [String: String]) {
        if !config.vault.isEmpty { env["SYMINGEST_VAULT"] = config.vault }
        if !config.archivePath.isEmpty { env["SYMINGEST_ARCHIVE_PATH"] = config.archivePath }
        if !config.dbPath.isEmpty { env["SYMINGEST_DB_PATH"] = config.dbPath }
        if !config.ocrLang.isEmpty { env["SYMINGEST_OCR_LANG"] = config.ocrLang }
    }

    public func runIngestCommand(args: [String], config: ConfigStore, environment: [String: String] = [:]) async throws -> (stdout: String, stderr: String) {
        let snapshot = await CLIConfigSnapshot(config: config)
        guard let binary = locateBinary(customPath: snapshot.customBinaryPath) else {
            throw NSError(domain: "symingest", code: 404, userInfo: [NSLocalizedDescriptionKey: "symingest binary not found"])
        }

        // Config env vars (SYMINGEST_*) merged over the PATH-augmented
        // environment by CLIRunner.
        var env: [String: String] = [:]
        applyConfigEnvironment(snapshot, to: &env)
        environment.forEach { key, value in env[key] = value }

        // Pre-existing contract: non-zero exits are NOT thrown here —
        // callers inspect stdout/stderr themselves.
        let result = try await runner.run(binary, arguments: args, environment: env)
        return (
            String(data: result.stdout, encoding: .utf8) ?? "",
            String(data: result.stderr, encoding: .utf8) ?? ""
        )
    }

    // MARK: - Business Operations

    public func listJobs(config: ConfigStore) async throws -> [IngestJob] {
        let (out, _) = try await runIngestCommand(args: ["jobs", "--json"], config: config)
        let decoder = JSONDecoder()
        return try decoder.decode([IngestJob].self, from: out.data(using: .utf8) ?? Data())
    }

    public func retryJob(id: Int64, config: ConfigStore) async -> (success: Bool, message: String) {
        do {
            let (out, err) = try await runIngestCommand(args: ["retry", "\(id)"], config: config)
            if out.contains("pending") {
                return (true, out.trimmingCharacters(in: .whitespacesAndNewlines))
            } else {
                return (false, err.isEmpty ? out : err)
            }
        } catch {
            return (false, error.localizedDescription)
        }
    }

    public func listRules(config: ConfigStore) async throws -> [SwiftRule] {
        let (out, _) = try await runIngestCommand(args: ["rules", "--json", "list"], config: config)
        let decoder = JSONDecoder()
        return try decoder.decode([SwiftRule].self, from: out.data(using: .utf8) ?? Data())
    }

    public func addRule(pattern: String, kind: String, value: String, config: ConfigStore) async -> (success: Bool, message: String) {
        do {
            let (out, err) = try await runIngestCommand(args: ["rules", "add", pattern, kind, value], config: config)
            if out.contains("Added") {
                return (true, out.trimmingCharacters(in: .whitespacesAndNewlines))
            } else {
                return (false, err.isEmpty ? out : err)
            }
        } catch {
            return (false, error.localizedDescription)
        }
    }

    public func deleteRule(id: Int64, config: ConfigStore) async -> (success: Bool, message: String) {
        do {
            let (out, err) = try await runIngestCommand(args: ["rules", "delete", "\(id)"], config: config)
            if out.contains("Deleted") {
                return (true, out.trimmingCharacters(in: .whitespacesAndNewlines))
            } else {
                return (false, err.isEmpty ? out : err)
            }
        } catch {
            return (false, error.localizedDescription)
        }
    }

    public func ingestFile(filePath: String, config: ConfigStore) async -> (success: Bool, message: String) {
        do {
            let (out, err) = try await runIngestCommand(args: ["ingest", filePath], config: config)
            if out.contains("ingested") || out.contains("already ingested") {
                return (true, out.trimmingCharacters(in: .whitespacesAndNewlines))
            } else {
                return (false, err.isEmpty ? out : err)
            }
        } catch {
            return (false, error.localizedDescription)
        }
    }

    public func runIngestCommandStreaming(
        args: [String],
        config: ConfigStore,
        environment: [String: String] = [:],
        onOutput: @escaping @Sendable (String) -> Void
    ) async throws -> Int32 {
        let snapshot = await CLIConfigSnapshot(config: config)
        guard let binary = locateBinary(customPath: snapshot.customBinaryPath) else {
            throw NSError(domain: "symingest", code: 404, userInfo: [NSLocalizedDescriptionKey: "symingest binary not found"])
        }

        let process = Process()
        process.executableURL = binary
        process.arguments = args

        // Environment variables matching CLI/XDG
        var env = ProcessInfo.processInfo.environment
        applyConfigEnvironment(snapshot, to: &env)
        environment.forEach { key, value in env[key] = value }

        // Add homebrew paths to process PATH if missing
        if let path = env["PATH"] {
            env["PATH"] = "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:\(path)"
        } else {
            env["PATH"] = "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin"
        }
        process.environment = env

        let outPipe = Pipe()
        let errPipe = Pipe()
        process.standardOutput = outPipe
        process.standardError = errPipe

        let outHandle = outPipe.fileHandleForReading
        let errHandle = errPipe.fileHandleForReading

        outHandle.readabilityHandler = { handle in
            let data = handle.availableData
            guard !data.isEmpty, let text = String(data: data, encoding: .utf8) else { return }
            onOutput(text)
        }

        errHandle.readabilityHandler = { handle in
            let data = handle.availableData
            guard !data.isEmpty, let text = String(data: data, encoding: .utf8) else { return }
            onOutput(text)
        }

        try process.run()
        process.waitUntilExit()

        outHandle.readabilityHandler = nil
        errHandle.readabilityHandler = nil

        return process.terminationStatus
    }
}
