import SwiftUI

/// 首页问候区：「中午好」+ 右侧日期 + 下面一行「备忘 / 事件 / 本月支出」。
///
/// 独立成视图是为了能离屏出图对比字号与间距（`scripts/snapshot-homecards.sh`），
/// 尺寸参数默认值就是线上取值，出图脚本显式传别的值来对比档位。
struct HomeGreetingView: View {
    let now: Date
    var pendingReminders: Int?
    var events: Int?
    var monthSpend: Double?
    var titleSize: CGFloat = HomeGreetingView.titleSize
    var statsSize: CGFloat = HomeGreetingView.statsSize
    var spacing: CGFloat = HomeGreetingView.spacing
    var onStatsTap: () -> Void = {}

    /// 线上取值（2026-11-13 用户拍板）：问候语 23、今日行 13、两块间距 8。
    /// 19/12.5/6 时头部被六张 164 高的卡压住，读起来像页眉而不是首页标题。
    static let titleSize: CGFloat = 23
    static let statsSize: CGFloat = 13
    static let spacing: CGFloat = 8

    var body: some View {
        VStack(alignment: .leading, spacing: spacing) {
            HStack(alignment: .firstTextBaseline) {
                Text(Self.greeting(hour: Calendar.current.component(.hour, from: now)))
                    .font(.system(size: titleSize, weight: .semibold))
                Spacer()
                // 日期固定 12pt：跟着问候语一起放大会跟它抢视觉重心
                Text(Self.todayText(now))
                    .font(.system(size: 12))
                    .foregroundColor(.secondary)
            }

            Button(action: onStatsTap) {
                todayLine
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help("查看备忘")
        }
    }

    /// 今日行：备忘 / 事件 / 本月支出。任一路取不到就显示 `—`（不隐藏，免得布局跳动）。
    /// 整行可点进备忘面板——否则「上面有数字、下面没入口」会显得断头。
    /// 用 HStack 而不是 `Text + Text`（macOS 26 已废弃 `+`）。
    private var todayLine: some View {
        HStack(spacing: 7) {
            todayItem("备忘", pendingReminders.map(String.init))
            separatorDot
            todayItem("事件", events.map(String.init))
            separatorDot
            todayItem("本月支出", monthSpend.map(SidebarHomeFormat.money))
        }
        .font(.system(size: statsSize))
        .foregroundColor(.secondary)
        .lineLimit(1)
        .minimumScaleFactor(0.9)
    }

    private func todayItem(_ label: String, _ value: String?) -> some View {
        HStack(spacing: 4) {
            Text(label)
            Text(value ?? "—")
                .fontWeight(.semibold)
                .foregroundColor(Color("assistantText"))
                .monospacedDigit()
        }
    }

    private var separatorDot: some View {
        Text("·")
            .foregroundColor(Color.primary.opacity(0.35))
    }

    static func greeting(hour: Int) -> String {
        SidebarHomeFormat.greeting(hour: hour)
    }

    static func todayText(_ now: Date) -> String {
        let cal = Calendar.current
        let comps = cal.dateComponents([.month, .day, .weekday], from: now)
        let names = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"]
        let index = max(0, min(6, (comps.weekday ?? 1) - 1))
        return "\(comps.month ?? 1)月\(comps.day ?? 1)日 \(names[index])"
    }
}
