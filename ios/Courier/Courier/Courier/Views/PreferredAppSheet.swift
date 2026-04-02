import SwiftUI

struct PreferredAppSheet: View {
    let collectionPrefix: String
    let currentAppURL: String?
    let did: String
    let onSelect: (AppInfo) -> Void
    @Environment(\.dismiss) private var dismiss

    @State private var alternatives: [AppInfo] = []
    @State private var isLoading = true

    var body: some View {
        NavigationStack {
            Group {
                if isLoading {
                    ProgressView()
                } else if alternatives.isEmpty {
                    Text("No alternative apps found")
                        .foregroundStyle(.secondary)
                } else {
                    List(alternatives) { app in
                        Button {
                            onSelect(app)
                            dismiss()
                        } label: {
                            HStack(spacing: 12) {
                                if let favicon = app.faviconUrl, !favicon.isEmpty,
                                   let url = URL(string: favicon) {
                                    AsyncImage(url: url) { image in
                                        image.resizable().scaledToFit()
                                    } placeholder: {
                                        RoundedRectangle(cornerRadius: 8)
                                            .fill(.quaternary)
                                    }
                                    .frame(width: 40, height: 40)
                                    .clipShape(RoundedRectangle(cornerRadius: 8))
                                } else {
                                    RoundedRectangle(cornerRadius: 8)
                                        .fill(.quaternary)
                                        .frame(width: 40, height: 40)
                                        .overlay {
                                            Text(String(app.appName.prefix(1)))
                                                .font(.headline)
                                                .foregroundStyle(.secondary)
                                        }
                                }

                                VStack(alignment: .leading, spacing: 2) {
                                    Text(app.appName)
                                        .font(.body)
                                        .foregroundStyle(.primary)
                                    if let desc = app.description, !desc.isEmpty {
                                        Text(desc)
                                            .font(.caption)
                                            .foregroundStyle(.secondary)
                                            .lineLimit(2)
                                    }
                                }

                                Spacer()

                                if app.appUrl == currentAppURL {
                                    Image(systemName: "checkmark.circle.fill")
                                        .foregroundStyle(.green)
                                }
                            }
                        }
                    }
                }
            }
            .navigationTitle("Choose App")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
            }
            .task {
                await loadAlternatives()
            }
        }
    }

    private func loadAlternatives() async {
        do {
            alternatives = try await APIClient.shared.getAlternatives(prefix: collectionPrefix)
        } catch {
        }
        isLoading = false
    }
}
