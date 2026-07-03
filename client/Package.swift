// swift-tools-version:6.0
import PackageDescription

let package = Package(
    name: "SymseekClient",
    platforms: [
        .macOS(.v14)
    ],
    products: [
        .executable(name: "symseek-gui", targets: ["SymseekApp"])
    ],
    dependencies: [],
    targets: [
        .executableTarget(
            name: "SymseekApp",
            dependencies: [],
            path: "Sources/SymseekApp",
            resources: []
        )
    ]
)
