import Foundation

enum ChartType: String, Codable, Equatable {
    case line
    case bar
    case pie
}

struct ChartDataset: Codable, Equatable {
    let label: String
    let data: [Double]
}

struct ChartData: Codable, Equatable {
    let type: ChartType
    let title: String?
    let labels: [String]
    let datasets: [ChartDataset]
}
