import SwiftUI
import SymseekFeature

@main
struct SymseekApp: App {
    var body: some Scene {
        WindowGroup {
            SymseekModuleView()
        }
        .windowStyle(.hiddenTitleBar)
    }
}
