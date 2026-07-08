import SwiftUI

struct ContentView: View {
    enum Tab {
        case dashboard
        case search
        case indexing
        case settings
    }
    
    @State private var selectedTab: Tab = .dashboard
    @State private var engineManager = EngineManager()
    
    var body: some View {
        NavigationSplitView {
            // Sidebar Navigation
            VStack(alignment: .leading, spacing: 6) {
                // Branding Logo/Title
                HStack(spacing: 8) {
                    Image(systemName: "bolt.ring")
                        .font(.title2)
                        .foregroundStyle(Color.symairaGold)
                    
                    VStack(alignment: .leading, spacing: 0) {
                        Text("SYMAIRA")
                            .font(.system(.subheadline, design: .monospaced))
                            .bold()
                            .foregroundStyle(Color.symairaGold)
                        Text("SEEK")
                            .font(.system(.caption, design: .monospaced))
                            .foregroundStyle(Color.symairaTextSecondary)
                    }
                }
                .padding(.horizontal)
                .padding(.vertical, 24)
                
                // Navigation Items
                VStack(spacing: 4) {
                    SidebarButton(title: "Dashboard", icon: "square.grid.2x2.fill", isSelected: selectedTab == .dashboard) {
                        selectedTab = .dashboard
                    }
                    
                    SidebarButton(title: "Search Matches", icon: "magnifyingglass", isSelected: selectedTab == .search) {
                        selectedTab = .search
                    }
                    
                    SidebarButton(title: "Index & Watch", icon: "folder.fill.badge.plus", isSelected: selectedTab == .indexing) {
                        selectedTab = .indexing
                    }
                    
                    SidebarButton(title: "Settings", icon: "gearshape.fill", isSelected: selectedTab == .settings) {
                        selectedTab = .settings
                    }
                }
                
                Spacer()
                
                // Bottom Info
                VStack(alignment: .leading, spacing: 4) {
                    HStack {
                        Circle()
                            .fill(engineManager.isRunning ? Color.symairaGold : Color.gray)
                            .frame(width: 6, height: 6)
                        Text(engineManager.isRunning ? "Daemon Active" : "Daemon Offline")
                            .font(.system(.caption2, design: .monospaced))
                            .foregroundStyle(Color.symairaTextSecondary)
                    }
                    Text("v2.3.1")
                        .font(.system(.caption2, design: .monospaced))
                        .foregroundStyle(Color.symairaMuted)
                }
                .padding()
            }
            .frame(minWidth: 200, maxWidth: 240)
            .background(Color.symairaBgDarker)
        } detail: {
            // Main Display Pane
            Group {
                switch selectedTab {
                case .dashboard:
                    DashboardView(engineManager: engineManager)
                case .search:
                    SearchView(engineManager: engineManager)
                case .indexing:
                    IndexView(engineManager: engineManager)
                case .settings:
                    SettingsView(engineManager: engineManager)
                }
            }
            .frame(minWidth: 500, minHeight: 600)
            .background(Color.symairaBg)
        }
        .frame(minWidth: 800, minHeight: 600)
        .task {
            // Auto-start server on app load
            await engineManager.start()
        }
    }
}

struct SidebarButton: View {
    let title: String
    let icon: String
    let isSelected: Bool
    let action: () -> Void
    
    @State private var isHovered = false
    
    var body: some View {
        Button(action: action) {
            HStack(spacing: 12) {
                Image(systemName: icon)
                    .font(.body)
                    .foregroundStyle(isSelected ? Color.symairaBg : (isHovered ? Color.symairaGoldSecondary : Color.symairaTextSecondary))
                    .frame(width: 20)
                
                Text(title)
                    .font(.body)
                    .foregroundStyle(isSelected ? Color.symairaBg : (isHovered ? Color.symairaText : Color.symairaTextSecondary))
                
                Spacer()
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 10)
            .background(
                isSelected ? Color.symairaGold : (isHovered ? Color.symairaCardHover : Color.clear)
            )
            .cornerRadius(8)
        }
        .buttonStyle(.plain)
        .padding(.horizontal, 8)
        .onHover { hover in
            isHovered = hover
        }
    }
}
