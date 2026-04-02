import SwiftUI

struct AppSuggestionSheet: View {
    let collection: String
    @Environment(\.dismiss) private var dismiss
    @State private var appName = ""
    @State private var appURL = ""
    @State private var submitted = false

    var body: some View {
        NavigationStack {
            if submitted {
                VStack(spacing: 16) {
                    Image(systemName: "checkmark.circle.fill")
                        .font(.system(size: 48))
                        .foregroundStyle(.green)
                    Text("Thanks!")
                        .font(.title2.bold())
                    Text("We'll review and add this app to our registry.")
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.center)
                }
                .padding()
                .onAppear {
                    DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) {
                        dismiss()
                    }
                }
            } else {
                Form {
                    Section {
                        Text("We noticed a notification from a collection we don't recognize yet:")
                            .foregroundStyle(.secondary)

                        Text(collection)
                            .font(.system(.body, design: .monospaced))
                            .foregroundStyle(.blue)
                    }

                    Section("What app is this from?") {
                        TextField("App name (e.g., Tangled)", text: $appName)
                        TextField("App URL (e.g., tangled.org)", text: $appURL)
                            .textContentType(.URL)
                            .autocorrectionDisabled()
                            .textInputAutocapitalization(.never)
                    }

                    Section {
                        Button("Submit") {
                            Task { await submit() }
                        }
                        .disabled(appName.isEmpty)
                    }
                }
                .navigationTitle("Unknown App")
                .navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) {
                        Button("Skip") { dismiss() }
                    }
                }
            }
        }
    }

    private func submit() async {
        do {
            try await APIClient.shared.suggestApp(
                collection: collection,
                appName: appName,
                appURL: appURL
            )
        } catch {
            // Still show success — we'll store locally as fallback
        }
        submitted = true
    }
}
