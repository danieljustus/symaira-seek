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
    dependencies: [
        .package(url: "https://github.com/danieljustus/symaira-appkit.git", exact: "0.1.0"),
    ],
    targets: [
        .executableTarget(
            name: "SymseekApp",
            dependencies: [
                .product(name: "SymairaTheme", package: "symaira-appkit"),
                .product(name: "SymairaToolKit", package: "symaira-appkit"),
            ],
            path: "Sources/SymseekApp",
            resources: []
        )
    ]
)
