import SwiftUI
import Charts

struct ChartBubble: View {
    let chartData: ChartData

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            if let title = chartData.title {
                Text(title)
                    .font(.headline)
                    .padding(.horizontal, 14)
                    .padding(.top, 10)
            }

            GeometryReader { geo in
                let minPointWidth: CGFloat = 50
                let idealWidth = max(geo.size.width, CGFloat(chartData.labels.count) * minPointWidth)
                let needsScroll = (chartData.type != .pie) && idealWidth > geo.size.width

                if needsScroll {
                    ScrollView(.horizontal, showsIndicators: true) {
                        chartContent(frameWidth: idealWidth)
                    }
                } else {
                    chartContent(frameWidth: geo.size.width)
                }
            }
            .frame(height: 250)
        }
        .padding(.bottom, 10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 18))
        .overlay(RoundedRectangle(cornerRadius: 18).stroke(.white.opacity(0.35), lineWidth: 0.6))
        .shadow(color: .black.opacity(0.06), radius: 4, y: 2)
        .padding(.horizontal, Layout.chatHorizontalPadding)
    }

    @ViewBuilder
    private func chartContent(frameWidth: CGFloat) -> some View {
        Chart {
            if chartData.type == .pie {
                if let ds = chartData.datasets.first {
                    ForEach(Array(zip(chartData.labels, ds.data)), id: \.0) { label, value in
                        SectorMark(
                            angle: .value("值", value)
                        )
                        .foregroundStyle(by: .value("分类", label))
                    }
                }
            } else if chartData.type == .line {
                ForEach(chartData.datasets, id: \.label) { ds in
                    ForEach(Array(zip(Array(chartData.labels.enumerated()), ds.data)), id: \.0.offset) { idx, value in
                        LineMark(
                            x: .value("X", idx.element),
                            y: .value("Y", value)
                        )
                    }
                    .foregroundStyle(by: .value("系列", ds.label))
                }
            } else {
                ForEach(chartData.datasets, id: \.label) { ds in
                    ForEach(Array(zip(Array(chartData.labels.enumerated()), ds.data)), id: \.0.offset) { idx, value in
                        BarMark(
                            x: .value("X", idx.element),
                            y: .value("Y", value)
                        )
                    }
                    .foregroundStyle(by: .value("系列", ds.label))
                }
            }
        }
        .frame(width: frameWidth, height: 250)
        .chartLegend(position: .bottom, alignment: .center)
        .chartXAxis {
            AxisMarks { _ in
                AxisValueLabel()
                    .font(.caption2)
            }
        }
        .chartYAxis {
            AxisMarks { _ in
                AxisValueLabel()
                    .font(.caption2)
            }
        }
        .padding(.horizontal, 14)
    }
}
