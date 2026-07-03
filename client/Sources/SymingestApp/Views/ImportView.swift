import SwiftUI

struct ImportView: View {
    @Environment(ConfigStore.self) private var configStore
    
    // Paperless host details
    @State private var baseURL = ""
    @State private var apiToken = ""
    
    // Filters
    @State private var useDateFilter = false
    @State private var sinceDate = Date()
    
    @State private var useLimitFilter = false
    @State private var limitValue = 10
    
    @State private var useIdsFilter = false
    @State private var idsText = ""
    
    // Mode options
    @State private var preserveStoragePaths = false
    @State private var dryRun = false
    @State private var verifyOnly = false
    @State private var statusOnly = false
    
    // Run state
    @State private var isRunning = false
    @State private var logs: [String] = []
    @State private var runStatusText: String?
    
    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            // Header
            HStack {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Paperless-ngx Import")
                        .font(Font.title.bold())
                        .foregroundStyle(Theme.textPrimary)
                    Text("Migrate or verify documents from a Paperless-ngx instance into your local markdown vault.")
                        .font(.subheadline)
                        .foregroundStyle(Theme.textSecondary)
                }
                Spacer()
            }
            
            HStack(alignment: .top, spacing: 20) {
                // Settings Form (Left Panel)
                ScrollView {
                    VStack(alignment: .leading, spacing: 16) {
                        // Host details
                        VStack(alignment: .leading, spacing: 10) {
                            Text("Connection Details")
                                .font(.headline)
                                .foregroundStyle(Theme.goldPrimary)
                            
                            VStack(alignment: .leading, spacing: 4) {
                                Text("Paperless Instance URL")
                                    .font(.caption)
                                    .foregroundStyle(Theme.textSecondary)
                                TextField("e.g. http://192.168.1.100:8000", text: $baseURL)
                                    .textFieldStyle(.roundedBorder)
                            }
                            
                            VStack(alignment: .leading, spacing: 4) {
                                Text("API Token")
                                    .font(.caption)
                                    .foregroundStyle(Theme.textSecondary)
                                TextField("Enter your API token", text: $apiToken)
                                    .textFieldStyle(.roundedBorder)
                            }
                        }
                        
                        Divider().background(Theme.borderGlass)
                        
                        // Filters
                        VStack(alignment: .leading, spacing: 12) {
                            Text("Filters")
                                .font(.headline)
                                .foregroundStyle(Theme.goldPrimary)
                            
                            // Since date filter
                            Toggle(isOn: $useDateFilter) {
                                Text("Only import since date")
                                    .font(.body)
                            }
                            if useDateFilter {
                                DatePicker("Since Date", selection: $sinceDate, displayedComponents: .date)
                                    .datePickerStyle(.compact)
                                    .labelsHidden()
                                    .padding(.leading, 20)
                            }
                            
                            // Limit filter
                            Toggle(isOn: $useLimitFilter) {
                                Text("Limit documents count")
                                    .font(.body)
                            }
                            if useLimitFilter {
                                HStack {
                                    Stepper("Limit: \(limitValue)", value: $limitValue, in: 1...1000)
                                        .foregroundStyle(Theme.textPrimary)
                                }
                                .padding(.leading, 20)
                            }
                            
                            // Ids filter
                            Toggle(isOn: $useIdsFilter) {
                                Text("Filter by Document IDs")
                                    .font(.body)
                            }
                            if useIdsFilter {
                                TextField("e.g. 1, 5, 23 (comma-separated)", text: $idsText)
                                    .textFieldStyle(.roundedBorder)
                                    .padding(.leading, 20)
                            }
                        }
                        
                        Divider().background(Theme.borderGlass)
                        
                        // Action Modes
                        VStack(alignment: .leading, spacing: 10) {
                            Text("Migration Options")
                                .font(.headline)
                                .foregroundStyle(Theme.goldPrimary)
                            
                            Toggle("Preserve Storage Paths", isOn: $preserveStoragePaths)
                            Toggle("Dry Run (Simulate)", isOn: $dryRun)
                            Toggle("Verify Existing Import Only", isOn: $verifyOnly)
                            Toggle("Status Only (List local status)", isOn: $statusOnly)
                        }
                        
                        // Action Buttons
                        Button {
                            Task {
                                await runImport()
                            }
                        } label: {
                            if isRunning {
                                ProgressView("Running...")
                                    .controlSize(.small)
                            } else {
                                Label(verifyOnly ? "Verify Migration" : (statusOnly ? "Check Import Status" : "Start Import"), systemImage: "arrow.down.doc.fill")
                                    .font(.headline)
                                    .foregroundStyle(Theme.bgDark)
                                    .frame(maxWidth: .infinity)
                                    .padding(.vertical, 8)
                            }
                        }
                        .buttonStyle(.borderedProminent)
                        .tint(Theme.goldPrimary)
                        .disabled(baseURL.isEmpty || (apiToken.isEmpty && !statusOnly) || isRunning)
                        
                        if let runStatus = runStatusText {
                            Text(runStatus)
                                .font(.caption)
                                .foregroundStyle(Theme.goldSecondary)
                                .padding(8)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .background(Theme.goldPrimary.opacity(0.1))
                                .clipShape(RoundedRectangle(cornerRadius: 6))
                        }
                    }
                    .padding(.trailing, 10)
                }
                .frame(width: 320)
                
                // Logging Console (Right Panel)
                VStack(alignment: .leading, spacing: 10) {
                    HStack {
                        Label("Migration Logs", systemImage: "terminal.fill")
                            .font(.headline)
                            .foregroundStyle(Theme.textPrimary)
                        Spacer()
                        Button("Clear") {
                            logs.removeAll()
                        }
                        .buttonStyle(.plain)
                        .foregroundStyle(Theme.goldPrimary)
                        .font(.caption)
                    }
                    
                    ScrollViewReader { proxy in
                        ScrollView {
                            LazyVStack(alignment: .leading, spacing: 4) {
                                if logs.isEmpty {
                                    Text("Console idle. Configure connections and start migration.")
                                        .foregroundStyle(Theme.textMuted)
                                        .font(.caption.monospaced())
                                } else {
                                    ForEach(logs.indices, id: \.self) { idx in
                                        Text(logs[idx])
                                            .font(.system(.caption, design: .monospaced))
                                            .foregroundStyle(Theme.textSecondary)
                                            .id(idx)
                                    }
                                }
                            }
                            .padding(12)
                        }
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                        .background(Color.black.opacity(0.55))
                        .clipShape(RoundedRectangle(cornerRadius: 8))
                        .overlay(
                            RoundedRectangle(cornerRadius: 8)
                                .stroke(Theme.borderGlass, lineWidth: 1)
                        )
                        .onChange(of: logs.count) {
                            if let lastIdx = logs.indices.last {
                                proxy.scrollTo(lastIdx, anchor: .bottom)
                            }
                        }
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .onAppear {
            // Load saved settings
            baseURL = UserDefaults.standard.string(forKey: "PAPERLESS_URL") ?? ""
            apiToken = UserDefaults.standard.string(forKey: "PAPERLESS_TOKEN") ?? ""
            preserveStoragePaths = UserDefaults.standard.bool(forKey: "PAPERLESS_PRESERVE_STORAGE_PATHS")
        }
    }
    
    private func runImport() async {
        isRunning = true
        runStatusText = nil
        logs.removeAll()
        
        // Save host configuration
        UserDefaults.standard.set(baseURL, forKey: "PAPERLESS_URL")
        UserDefaults.standard.set(apiToken, forKey: "PAPERLESS_TOKEN")
        UserDefaults.standard.set(preserveStoragePaths, forKey: "PAPERLESS_PRESERVE_STORAGE_PATHS")
        
        appendLog("[app] Preparing import arguments...")
        
        var args = ["import", "paperless"]
        
        args.append("--base-url")
        args.append(baseURL)
        
        if !apiToken.isEmpty {
            args.append("--token")
            args.append(apiToken)
        }
        
        if statusOnly {
            args.append("--status")
        } else {
            if verifyOnly {
                args.append("--verify")
            }
            if preserveStoragePaths {
                args.append("--preserve-storage-paths")
            }
            if dryRun {
                args.append("--dry-run")
            }
            
            // Apply filters
            if useDateFilter {
                let formatter = DateFormatter()
                formatter.dateFormat = "yyyy-MM-dd"
                let dateStr = formatter.string(from: sinceDate)
                args.append("--since")
                args.append(dateStr)
            }
            
            if useLimitFilter {
                args.append("--limit")
                args.append("\(limitValue)")
            }
            
            if useIdsFilter && !idsText.isEmpty {
                args.append("--ids")
                args.append(idsText)
            }
        }
        
        appendLog("[app] Executing: symingest \(args.joined(separator: " "))")
        
        do {
            let status = try await CLIClient.shared.runIngestCommandStreaming(args: args, config: configStore) { text in
                Task { @MainActor in
                    self.appendLog(text)
                }
            }
            
            if status == 0 {
                runStatusText = "Process completed successfully (Exit code 0)"
                appendLog("[app] SUCCESS: Process finished.")
            } else {
                runStatusText = "Process exited with failure code \(status)"
                appendLog("[app] ERROR: Process failed with code \(status).")
            }
        } catch {
            runStatusText = "Failed to execute: \(error.localizedDescription)"
            appendLog("[app] EXCEPTION: \(error.localizedDescription)")
        }
        
        isRunning = false
    }
    
    private func appendLog(_ text: String) {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }
        for line in trimmed.components(separatedBy: .newlines) {
            let l = line.trimmingCharacters(in: .whitespacesAndNewlines)
            if !l.isEmpty {
                logs.append(l)
            }
        }
    }
}
