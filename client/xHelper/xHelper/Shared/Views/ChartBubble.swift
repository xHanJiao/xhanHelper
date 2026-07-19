import SwiftUI
import Charts

struct ChartBubble: View {
    let chartData: ChartData
    var isFullScreen = false

    @State private var hoveredIndex: Int?
    @State private var showFullScreen = false

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

                Group {
                    if needsScroll {
                        ScrollView(.horizontal, showsIndicators: true) {
                            chartContent(frameWidth: idealWidth)
                        }
                    } else {
                        chartContent(frameWidth: geo.size.width)
                    }
                }
            }
            .frame(height: isFullScreen ? 400 : 250)
        }
        .padding(.bottom, 10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 18))
        .overlay(RoundedRectangle(cornerRadius: 18).stroke(.white.opacity(0.35), lineWidth: 0.6))
        .shadow(color: .black.opacity(0.06), radius: 4, y: 2)
        .padding(.horizontal, isFullScreen ? 0 : Layout.chatHorizontalPadding)
        .onTapGesture {
            guard !isFullScreen else { return }
            showFullScreen = true
        }
        .sheet(isPresented: $showFullScreen) {
            ScrollView {
                ChartBubble(chartData: chartData, isFullScreen: true)
                    .padding(40)
            }
            .frame(minWidth: 700, idealWidth: 900)
        }
    }

    // MARK: - Chart Content

    @ViewBuilder
    private func chartContent(frameWidth: CGFloat) -> some View {
        Chart {
            if chartData.type == .pie {
                if let ds = chartData.datasets.first {
                    let total = ds.data.reduce(0, +)
                    ForEach(Array(zip(chartData.labels, ds.data)), id: \.0) { label, value in
                        SectorMark(
                            angle: .value("值", value)
                        )
                        .foregroundStyle(by: .value("分类", label))
                        .annotation(position: .overlay, alignment: .center) {
                            let pct = total > 0 ? Int(value / total * 100) : 0
                            if pct >= 5 {
                                VStack(spacing: 0) {
                                    Text(label)
                                        .font(.caption2)
                                    Text("\(pct)%")
                                        .font(.caption2.weight(.bold))
                                }
                                .foregroundColor(.white)
                            }
                        }
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
        .frame(width: frameWidth, height: isFullScreen ? 400 : 250)
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
        .chartOverlay { proxy in
            if chartData.type != .pie {
                #if os(macOS)
                hoverOverlay(proxy: proxy)
                #endif
            }
        }
        .padding(.horizontal, 14)
    }

    // MARK: - Hover Tooltip

    #if os(macOS)
    private func hoverOverlay(proxy: ChartProxy) -> some View {
        GeometryReader { geo in
            let plotRect = geo[proxy.plotFrame]

            ZStack(alignment: .top) {
                Rectangle()
                    .fill(.clear)
                    .contentShape(Rectangle())
                    .onContinuousHover { phase in
                        switch phase {
                        case .active(let location):
                            guard plotRect.minX <= location.x, location.x <= plotRect.maxX else {
                                hoveredIndex = nil
                                return
                            }
                            let relativeX = location.x - plotRect.minX
                            let fraction = relativeX / plotRect.width
                            let idx = Int(fraction * CGFloat(chartData.labels.count))
                            hoveredIndex = min(max(idx, 0), chartData.labels.count - 1)
                        case .ended:
                            hoveredIndex = nil
                        }
                    }

                if let idx = hoveredIndex, idx < chartData.labels.count {
                    tooltipView(for: idx)
                }
            }
        }
    }
    #endif

    #if os(macOS)
    @ViewBuilder
    private func tooltipView(for index: Int) -> some View {
        HStack(spacing: 6) {
            Text(chartData.labels[index])
                .font(.caption.weight(.semibold))
                .foregroundColor(.primary)

            ForEach(chartData.datasets, id: \.label) { ds in
                if index < ds.data.count {
                    HStack(spacing: 3) {
                        Circle()
                            .fill(.secondary)
                            .frame(width: 5, height: 5)
                        Text("\(ds.label) \(ds.data[index].formatted(.number.precision(.fractionLength(0...2))))")
                            .font(.caption2)
                    }
                }
            }
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 5)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 8))
        .overlay(RoundedRectangle(cornerRadius: 8).stroke(.white.opacity(0.3), lineWidth: 0.5))
        .padding(.top, 4)
    }
    #endif
}
