import Foundation

enum LocalToolResultType: String, Codable {
    case chart
    case docRefs
}

struct LocalToolResultData: Codable {
    let type: LocalToolResultType
    let payload: Data
}
