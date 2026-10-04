import CoreGraphics
import ImageIO
import UniformTypeIdentifiers
import XCTest

@testable import ImageCompressorCore

/// 窗口底图导入路径的回归钉：`downscaledJPEG` 必须把大图压到长边 ≤ 目标、且输出是 JPEG。
final class ImageCompressorCoreTests: XCTestCase {

    private func makeJPEG(width: Int, height: Int) -> Data {
        let space = CGColorSpaceCreateDeviceRGB()
        let ctx = CGContext(
            data: nil, width: width, height: height,
            bitsPerComponent: 8, bytesPerRow: 0, space: space,
            bitmapInfo: CGImageAlphaInfo.noneSkipLast.rawValue
        )!
        ctx.setFillColor(CGColor(red: 0.2, green: 0.5, blue: 0.8, alpha: 1))
        ctx.fill(CGRect(x: 0, y: 0, width: width, height: height))
        let image = ctx.makeImage()!

        let out = NSMutableData()
        let dest = CGImageDestinationCreateWithData(out, UTType.jpeg.identifier as CFString, 1, nil)!
        CGImageDestinationAddImage(dest, image, nil)
        XCTAssertTrue(CGImageDestinationFinalize(dest))
        return out as Data
    }

    func testDownscaledJPEGClampsLongEdgeAndStaysJPEG() {
        let input = makeJPEG(width: 4000, height: 3000)
        let output = ImageCompressor.downscaledJPEG(input, maxPixel: 192)
        let data = try! XCTUnwrap(output)

        let source = try! XCTUnwrap(CGImageSourceCreateWithData(data as CFData, nil))
        XCTAssertEqual(CGImageSourceGetType(source), UTType.jpeg.identifier as CFString)

        let props = try! XCTUnwrap(
            CGImageSourceCopyPropertiesAtIndex(source, 0, nil) as? [CFString: Any]
        )
        let width = (props[kCGImagePropertyPixelWidth] as? NSNumber)?.doubleValue ?? 0
        let height = (props[kCGImagePropertyPixelHeight] as? NSNumber)?.doubleValue ?? 0
        XCTAssertLessThanOrEqual(max(width, height), 192)
        XCTAssertEqual(max(width, height), 192, accuracy: 1)
        // 压完必然比 4000px 的输入小得多
        XCTAssertLessThan(data.count, input.count)
    }

    func testDownscaledJPEGIsDeterministic() {
        let input = makeJPEG(width: 800, height: 600)
        let a = ImageCompressor.downscaledJPEG(input, maxPixel: 192)
        let b = ImageCompressor.downscaledJPEG(input, maxPixel: 192)
        XCTAssertNotNil(a)
        XCTAssertEqual(a, b, "同一输入两次编码应稳定——否则文件名里的内容哈希去重会失效")
    }
}
