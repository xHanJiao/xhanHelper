// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "ElkSwift",
    platforms: [
        .iOS(.v15),
        .macOS(.v12),
        .macCatalyst(.v15),
        .visionOS(.v1)
    ],
    products: [
        .library(
            name: "ElkSwift",
            targets: ["ElkSwift"]
        )
    ],
    targets: [
        .target(
            name: "ElkSwift"
        ),
        .testTarget(
            name: "ElkSwiftTests",
            dependencies: ["ElkSwift"]
        )
    ]
)
