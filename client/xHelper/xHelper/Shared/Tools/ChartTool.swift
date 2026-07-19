#if os(macOS)
import Foundation

final class ChartTool: @unchecked Sendable {
    let namespace = "chart"
}

extension ChartTool: LocalToolProvider {

    func toolSchemas() -> [LocalToolSchema] {
        [LocalToolSchema(
            name: "create_chart",
            description: "根据数据创建可视化图表并展示给用户。支持折线图(line)、柱状图(bar)、饼图(pie)。传入的 labels 长度必须与每个 dataset 的 data 长度一致。",
            inputSchema: [
                "type": "object",
                "properties": [
                    "chart_type": [
                        "type": "string",
                        "enum": ["line", "bar", "pie"],
                        "description": "图表类型",
                    ],
                    "title": [
                        "type": "string",
                        "description": "图表标题，可选",
                    ],
                    "labels": [
                        "type": "array",
                        "items": ["type": "string"],
                        "description": "X 轴标签或饼图扇区名称",
                    ],
                    "datasets": [
                        "type": "array",
                        "items": [
                            "type": "object",
                            "properties": [
                                "label": ["type": "string", "description": "数据系列名称（用于图例）"],
                                "data": ["type": "array", "items": ["type": "number"], "description": "数值数组，长度需与 labels 一致"],
                            ],
                            "required": ["label", "data"],
                        ],
                        "description": "数据集。折线图/柱状图可多条，饼图只用第一个 dataset",
                    ],
                ],
                "required": ["chart_type", "labels", "datasets"],
            ]
        )]
    }

    func execute(tool: String, arguments: [String: Any], context: ExecuteContext) async throws -> LocalToolResult {
        guard tool == "create_chart" else {
            return LocalToolResult(serverResult: nil, serverError: "未知函数: \(tool)", clientAction: nil)
        }

        guard let chartTypeRaw = arguments["chart_type"] as? String,
              let chartType = ChartType(rawValue: chartTypeRaw) else {
            return LocalToolResult(serverResult: nil, serverError: "缺少或无效的 chart_type，可选: line, bar, pie", clientAction: nil)
        }

        guard let labels = arguments["labels"] as? [String], !labels.isEmpty else {
            return LocalToolResult(serverResult: nil, serverError: "labels 不能为空", clientAction: nil)
        }

        let datasetDicts = arguments["datasets"] as? [[String: Any]] ?? []
        guard !datasetDicts.isEmpty else {
            return LocalToolResult(serverResult: nil, serverError: "datasets 不能为空", clientAction: nil)
        }

        var datasets: [ChartDataset] = []
        for ds in datasetDicts {
            guard let label = ds["label"] as? String else {
                return LocalToolResult(serverResult: nil, serverError: "dataset 缺少 label", clientAction: nil)
            }
            let data = (ds["data"] as? [Any])?.compactMap { ($0 as? NSNumber)?.doubleValue } ?? []
            if data.count != labels.count {
                return LocalToolResult(serverResult: nil, serverError: "dataset \"\(label)\" 的 data 长度(\(data.count))与 labels 长度(\(labels.count))不一致", clientAction: nil)
            }
            datasets.append(ChartDataset(label: label, data: data))
        }

        let chartData = ChartData(
            type: chartType,
            title: arguments["title"] as? String,
            labels: labels,
            datasets: datasets
        )

        let payload = (try? JSONEncoder().encode(chartData)) ?? Data()
        return LocalToolResult(
            serverResult: Data(#"{"success":true}"#.utf8),
            serverError: nil,
            clientAction: ClientAction(type: "chart", payload: payload)
        )
    }
}

#endif
