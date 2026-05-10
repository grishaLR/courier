import SwiftUI

struct MainTabView: View {
    var body: some View {
        TabView {
            InboxView()
                .tabItem {
                    Label("Inbox", systemImage: "bell.fill")
                }

            PreferencesView()
                .tabItem {
                    Label("Preferences", systemImage: "slider.horizontal.3")
                }
        }
        .tint(Color.accentColor)
    }
}
