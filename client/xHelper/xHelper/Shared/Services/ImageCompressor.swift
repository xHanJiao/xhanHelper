import CoreGraphics
import Foundation
import ImageIO
import UniformTypeIdentifiers

/// 选图后、上传前把图缩到长边 2048 再重编码。
///
/// 为什么不省 token：模型侧单图上限 1024 token、会自动缩到约 1300×1300，所以 2048 已经把
/// 模型能看到的都留下了（再大是纯浪费）。省下的是磁盘、上传/下载耗时、以及列表解码占的内存
/// —— 十几兆的照片解码后约 48MB 一张，列表里十张就是几百兆，NSCache 会被压到回收。
///
/// 顺带把 HEIC 转成 JPEG：服务端只认 png/jpg/gif/webp，iPhone 照片原本会被拒。
enum ImageCompressor {
    /// 长边上限。留一点余量给"想放大看细节"，同时远小于常见相机原图
    static let maxPixel = 2048

    private static let jpegQuality: CGFloat = 0.85
    /// 已经够小的 JPEG/PNG 不重复压：省一次代损，也省 CPU
    private static let skipBelowBytes = 2 << 20

    /// 需要压缩/换格式时返回新字节；GIF、已经够小、处理失败都返回 nil（调用方用原字节）
    nonisolated static func shrink(_ data: Data) -> Data? {
        guard let source = CGImageSourceCreateWithData(data as CFData, nil),
              let type = CGImageSourceGetType(source) else { return nil }

        // 动图不碰：重编码只会剩第一帧
        if type == UTType.gif.identifier as CFString { return nil }

        let isJPEG = type == UTType.jpeg.identifier as CFString
        let isPNG = type == UTType.png.identifier as CFString
        let size = pixelSize(source)
        if (isJPEG || isPNG), max(size.width, size.height) <= CGFloat(maxPixel),
           data.count <= skipBelowBytes {
            return nil
        }
        // 服务端只认 png/jpg/gif/webp：HEIC 之类必须换格式，哪怕压完更大也得换
        let mustReencode = !(isJPEG || isPNG || type == UTType.webP.identifier as CFString)

        guard let cg = downsample(source, maxPixel: maxPixel) else { return nil }
        guard let out = encode(cg, as: wantsAlpha(source, cg: cg, isPNG: isPNG) ? .png : .jpeg) else {
            return nil
        }
        // 压完反而更大（小图转格式偶尔如此）就保留原图，除非本来就得换格式
        return out.count < data.count || mustReencode ? out : nil
    }

    /// 是否需要保留 alpha。以源属性为准：JPEG/HEIC 不报这个属性，而 HEIC 解码出来是
    /// premultipliedLast（像素格式带 alpha 通道、照片本身并不透明），按像素格式判会误走 PNG。
    /// 属性缺失时只对 PNG 保守保留，其余当不透明。
    nonisolated private static func wantsAlpha(_ source: CGImageSource, cg: CGImage, isPNG: Bool) -> Bool {
        if let reported = CGImageSourceCopyPropertiesAtIndex(source, 0, nil)
            .flatMap({ ($0 as? [CFString: Any])?[kCGImagePropertyHasAlpha] as? Bool }) {
            return reported
        }
        switch cg.alphaInfo {
        case .none, .noneSkipFirst, .noneSkipLast: return false
        default: return isPNG
        }
    }

    nonisolated private static func pixelSize(_ source: CGImageSource) -> CGSize {
        guard let props = CGImageSourceCopyPropertiesAtIndex(source, 0, nil) as? [CFString: Any] else {
            return .zero
        }
        let w = (props[kCGImagePropertyPixelWidth] as? NSNumber)?.doubleValue ?? 0
        let h = (props[kCGImagePropertyPixelHeight] as? NSNumber)?.doubleValue ?? 0
        return CGSize(width: w, height: h)
    }

    nonisolated private static func downsample(_ source: CGImageSource, maxPixel: Int) -> CGImage? {
        let options: [CFString: Any] = [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceCreateThumbnailWithTransform: true, // 按 EXIF 方向转正
            kCGImageSourceThumbnailMaxPixelSize: maxPixel,
            kCGImageSourceShouldCacheImmediately: true,
        ]
        return CGImageSourceCreateThumbnailAtIndex(source, 0, options as CFDictionary)
    }

    /// 降采样成缩略图（图片库网格用）：解码阶段就按目标尺寸解，不把全尺寸图读进内存。
    /// 复用 `shrink` 的同一套 ImageIO 参数（含 EXIF 转正）。
    nonisolated static func thumbnail(_ data: Data, maxPixel: Int) -> CGImage? {
        guard let source = CGImageSourceCreateWithData(data as CFData, nil) else { return nil }
        return downsample(source, maxPixel: maxPixel)
    }

    nonisolated private static func encode(_ image: CGImage, as type: UTType) -> Data? {
        let out = NSMutableData()
        guard let dest = CGImageDestinationCreateWithData(
            out, type.identifier as CFString, 1, nil
        ) else { return nil }
        let options: [CFString: Any] = type == .jpeg
            ? [kCGImageDestinationLossyCompressionQuality: jpegQuality]
            : [:]
        CGImageDestinationAddImage(dest, image, options as CFDictionary)
        guard CGImageDestinationFinalize(dest) else { return nil }
        return out as Data
    }
}
