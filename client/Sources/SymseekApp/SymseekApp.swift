import SwiftUI

@main
struct SymseekApp: App {
    var body: some Scene {
        WindowGroup {
            ContentView()
                .preferredColorScheme(.dark) // Forces dark theme globally
        }
        .windowStyle(.hiddenTitleBar)
    }
}
