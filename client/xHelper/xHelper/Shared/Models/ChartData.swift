import Foundation

enum ChartType: String, Codable {
    case line
    case bar
    case pie
}

struct ChartDataset: Codable {
    let label: String
    let data: [Double]
}

struct ChartData: Codable {
    let type: ChartType
    let title: String?
    let labels: [String]
    let datasets: [ChartDataset]
}
