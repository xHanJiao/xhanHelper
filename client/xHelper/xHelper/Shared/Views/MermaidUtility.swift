import Foundation

struct ContentSegment: Identifiable {
    let id = UUID()
    let type: SegmentType
    let content: String

    enum SegmentType {
        case markdown
        case mermaid
    }
}

enum MermaidUtility {
    static func splitContent(_ content: String) -> [ContentSegment] {
        let pattern = "```(?:mermaid|mmd|mermaidjs|graph|diagram)\\s*\\n([\\s\\S]*?)\\n```"
        guard let regex = try? NSRegularExpression(pattern: pattern, options: []) else {
            let trimmed = content.trimmingCharacters(in: .whitespacesAndNewlines)
            return trimmed.isEmpty ? [] : [ContentSegment(type: .markdown, content: content)]
        }

        let nsContent = content as NSString
        let matches = regex.matches(in: content, options: [], range: NSRange(location: 0, length: nsContent.length))

        var segments: [ContentSegment] = []
        var currentIndex = 0

        for match in matches {
            let fullRange = match.range
            let codeRange = match.range(at: 1)

            if currentIndex < fullRange.location {
                let textRange = NSRange(location: currentIndex, length: fullRange.location - currentIndex)
                let markdown = nsContent.substring(with: textRange)
                let trimmed = markdown.trimmingCharacters(in: .whitespacesAndNewlines)
                if !trimmed.isEmpty {
                    segments.append(ContentSegment(type: .markdown, content: markdown))
                }
            }

            let code = nsContent.substring(with: codeRange)
            let trimmedCode = code.trimmingCharacters(in: .whitespacesAndNewlines)
            if !trimmedCode.isEmpty {
                segments.append(ContentSegment(type: .mermaid, content: code))
            }

            currentIndex = fullRange.location + fullRange.length
        }

        if currentIndex < nsContent.length {
            let textRange = NSRange(location: currentIndex, length: nsContent.length - currentIndex)
            let markdown = nsContent.substring(with: textRange)
            let trimmed = markdown.trimmingCharacters(in: .whitespacesAndNewlines)
            if !trimmed.isEmpty {
                segments.append(ContentSegment(type: .markdown, content: markdown))
            }
        }

        return segments
    }
}
