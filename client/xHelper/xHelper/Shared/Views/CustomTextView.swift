import SwiftUI
import UIKit

struct CustomTextView: UIViewRepresentable {
    @Binding var text: String
    var dismissKeyboard: Bool
    var isDisabled: Bool
    var placeholder: String
    var onSubmit: (() -> Void)?

    func makeCoordinator() -> Coordinator {
        Coordinator(self)
    }

    func makeUIView(context: Context) -> UITextView {
        let textView = UITextView()
        textView.delegate = context.coordinator
        textView.font = .preferredFont(forTextStyle: .body)
        textView.backgroundColor = .clear
        textView.textContainerInset = .zero
        textView.textContainer.lineFragmentPadding = 0
        textView.returnKeyType = .send
        textView.enablesReturnKeyAutomatically = true
        textView.isScrollEnabled = false
        textView.showsVerticalScrollIndicator = false
        textView.text = text
        textView.setContentCompressionResistancePriority(.defaultHigh, for: .horizontal)

        let placeholderLabel = UILabel()
        placeholderLabel.text = placeholder
        placeholderLabel.font = .preferredFont(forTextStyle: .body)
        placeholderLabel.textColor = .placeholderText
        placeholderLabel.tag = 999
        placeholderLabel.isHidden = !text.isEmpty
        textView.addSubview(placeholderLabel)
        placeholderLabel.translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            placeholderLabel.topAnchor.constraint(equalTo: textView.topAnchor),
            placeholderLabel.leadingAnchor.constraint(equalTo: textView.leadingAnchor),
            placeholderLabel.trailingAnchor.constraint(equalTo: textView.trailingAnchor),
        ])

        return textView
    }

    func sizeThatFits(_ proposal: ProposedViewSize, uiView: UITextView, context: Context) -> CGSize? {
        let width: CGFloat
        if let pw = proposal.width, pw.isFinite {
            width = pw
        } else if uiView.bounds.width > 0 {
            width = uiView.bounds.width
        } else {
            width = 300
        }
        let maxH: CGFloat = 280
        if uiView.isScrollEnabled {
            print(">>> [Size] proposal.width=\(proposal.width?.description ?? "nil") usedWidth=\(Int(width)) -> SCROLLING fixed \(Int(width))x\(Int(maxH))")
            return CGSize(width: width, height: maxH)
        }
        let fitHeight = uiView.sizeThatFits(CGSize(width: width, height: .infinity)).height
        let clamped = min(max(fitHeight, 0), maxH)
        print(">>> [Size] proposal.width=\(proposal.width?.description ?? "nil") usedWidth=\(Int(width)) boundWidth=\(Int(uiView.bounds.width)) fitHeight=\(Int(fitHeight)) -> \(Int(width))x\(Int(clamped))")
        return CGSize(width: width, height: clamped)
    }

    func updateUIView(_ uiView: UITextView, context: Context) {
        if dismissKeyboard && uiView.isFirstResponder {
            print(">>> [Focus] updateUIView: dismissKeyboard=true, resigning first responder")
            uiView.resignFirstResponder()
        }

        if uiView.text != text {
            context.coordinator.isProgrammaticUpdate = true
            uiView.text = text
        }

        uiView.isEditable = !isDisabled

        if let placeholderLabel = uiView.viewWithTag(999) as? UILabel {
            placeholderLabel.isHidden = !uiView.text.isEmpty
        }
    }

    final class Coordinator: NSObject, UITextViewDelegate {
        let parent: CustomTextView
        var isProgrammaticUpdate = false

        init(_ parent: CustomTextView) {
            self.parent = parent
        }

        func textViewDidChange(_ textView: UITextView) {
            guard !isProgrammaticUpdate else {
                isProgrammaticUpdate = false
                return
            }
            parent.text = textView.text
            if let placeholderLabel = textView.viewWithTag(999) as? UILabel {
                placeholderLabel.isHidden = !textView.text.isEmpty
            }

            let maxHeight: CGFloat = 280
            let actualHeight = textView.sizeThatFits(CGSize(width: textView.frame.width, height: .infinity)).height
            let shouldScroll = actualHeight > maxHeight
            if textView.isScrollEnabled != shouldScroll {
                print(">>> [Height] toggling isScrollEnabled -> \(shouldScroll), actualHeight=\(Int(actualHeight)) frameHeight=\(Int(textView.frame.height))")
                textView.isScrollEnabled = shouldScroll
                textView.invalidateIntrinsicContentSize()
            }
        }

        func textViewDidBeginEditing(_ textView: UITextView) {
            print(">>> [Focus] textViewDidBeginEditing")
        }

        func textViewDidEndEditing(_ textView: UITextView) {
            print(">>> [Focus] textViewDidEndEditing")
        }

        func textView(_ textView: UITextView, shouldChangeTextIn range: NSRange, replacementText text: String) -> Bool {
            if text == "\n" {
                parent.onSubmit?()
                return false
            }
            return true
        }

        func textView(_ textView: UITextView, editMenuForTextIn textRange: NSRange, suggestedActions: [UIMenuElement]) -> UIMenu? {
            print(">>> [Menu] editMenuForTextIn CALLED, range=\(textRange), suggestedActions count=\(suggestedActions.count)")
            return buildCustomMenu(from: suggestedActions, textView: textView)
        }

        private func childTitle(_ child: UIMenuElement) -> String {
            (child as? UICommand)?.title ?? (child as? UIKeyCommand)?.title ?? (child as? UIAction)?.title ?? ""
        }

        private func buildCustomMenu(from suggestedActions: [UIMenuElement], textView: UITextView) -> UIMenu {
            print(">>> [Menu] buildCustomMenu START — suggestedActions count=\(suggestedActions.count)")

            var scanTextElement: UIMenuElement?
            var autoFillChildren: [UIMenuElement] = []
            var topActions: [UIMenuElement] = []
            var otherActions: [UIMenuElement] = []

            for (idx, element) in suggestedActions.enumerated() {
                guard let menu = element as? UIMenu else {
                    print(">>> [Menu]   [\(idx)] NOT UIMenu, type=\(type(of: element)) title=\"\(childTitle(element))\"")
                    otherActions.append(element)
                    continue
                }

                print(">>> [Menu]   [\(idx)] UIMenu title=\"\(menu.title)\" children=\(menu.children.count)")
                for (ci, child) in menu.children.enumerated() {
                    let title = childTitle(child)
                    let typeStr = String(describing: type(of: child))
                    if let subMenu = child as? UIMenu {
                        print(">>> [Menu]     child[\(ci)] UIMenu title=\"\(subMenu.title)\" children=\(subMenu.children.count)")
                        for (sci, sc) in subMenu.children.enumerated() {
                            let st = childTitle(sc)
                            print(">>> [Menu]       subchild[\(sci)] \(String(describing: type(of: sc))) title=\"\(st)\"")
                        }
                    } else {
                        print(">>> [Menu]     child[\(ci)] \(typeStr) title=\"\(title)\"")
                    }
                }

                if isAutoFillMenu(menu) {
                    print(">>> [Menu]   [\(idx)] -> isAutoFillMenu = TRUE")
                    for child in menu.children {
                        if let subMenu = child as? UIMenu, isAutoFillMenu(subMenu) {
                            for sc in subMenu.children {
                                if isScanTextElement(sc) {
                                    print(">>> [Menu]     -> ScanText DETECTED: \"\(childTitle(sc))\"")
                                    scanTextElement = sc
                                } else {
                                    autoFillChildren.append(sc)
                                }
                            }
                        } else if isScanTextElement(child) {
                            print(">>> [Menu]     -> ScanText DETECTED (direct): \"\(childTitle(child))\"")
                            scanTextElement = child
                        } else {
                            let title = childTitle(child)
                            if isStandardEditTitle(title) {
                                print(">>> [Menu]     -> StandardEdit MATCH: \"\(title)\"")
                                topActions.append(child)
                            } else {
                                print(">>> [Menu]     -> other (autofill sibling): \"\(title)\"")
                                otherActions.append(child)
                            }
                        }
                    }
                } else {
                    for child in menu.children {
                        let title = childTitle(child)
                        if isStandardEditTitle(title) {
                            print(">>> [Menu]     -> StandardEdit MATCH: \"\(title)\"")
                            topActions.append(child)
                        } else {
                            print(">>> [Menu]     -> other: \"\(title)\"")
                            otherActions.append(child)
                        }
                    }
                }
            }

            var elementByTitle: [String: UIMenuElement] = [:]
            for item in topActions { elementByTitle[childTitle(item)] = item }
            if let scan = scanTextElement { elementByTitle[childTitle(scan)] = scan }

            let orderedTitles = ["Copy", "Paste", "Cut", "Select", "Select All", "Scan Text"]
            var finalActions: [UIMenuElement] = []

            for title in orderedTitles {
                if let el = elementByTitle[title] {
                    finalActions.append(relabeledElement(el, textView: textView))
                }
            }

            let titles = finalActions.map { titleOfElement($0) }
            print(">>> [Menu] buildCustomMenu DONE — returning \(finalActions.count) actions: \(titles)")

            return UIMenu(children: finalActions)
        }

        private func titleOfElement(_ element: UIMenuElement) -> String {
            (element as? UICommand)?.title ?? (element as? UIKeyCommand)?.title ?? (element as? UIAction)?.title ?? (element as? UIMenu)?.title ?? ""
        }

        private let chineseMap: [String: String] = [
            "Copy": "拷贝", "Paste": "粘贴", "Cut": "剪切", "Select All": "全选",
            "Delete": "删除", "Select": "选择", "Scan Text": "扫描文本",
            "AutoFill": "自动填充", "Look Up": "查询", "Translate": "翻译",
            "Replace…": "替换…", "Replace...": "替换…",
            "Share…": "共享…", "Share...": "共享…",
            "Spell": "拼写", "Speech": "语音", "Format": "格式",
            "Bold": "粗体", "Italic": "斜体", "Underline": "下划线",
            "Learn…": "学习…", "Learn...": "学习…",
            "Speak": "朗读", "Speak…": "朗读…", "Pause": "暂停",
            "More…": "更多…", "More...": "更多…",
            "Find Selection": "查找所选",
            "Insert Drawing": "插入绘图",
            "Writing Tools": "写作工具",
            "Right to Left": "从右到左", "Left to Right": "从左到右",
            "Text Style": "文字样式", "Writing Direction": "书写方向",
            "简⇄繁": "简⇄繁",
            "拷贝": "拷贝", "粘贴": "粘贴", "剪切": "剪切", "全选": "全选",
            "删除": "删除", "选择": "选择", "扫描文本": "扫描文本",
            "自动填充": "自动填充", "查询": "查询", "翻译": "翻译",
        ]

        private func isAutoFillMenu(_ menu: UIMenu) -> Bool {
            if menu.title.lowercased().contains("autofill") || menu.title.contains("自动填充") {
                return true
            }
            for child in menu.children {
                if let sub = child as? UIMenu,
                   sub.title.lowercased().contains("autofill") || sub.title.contains("自动填充") {
                    return true
                }
            }
            return false
        }

        private func isScanTextElement(_ element: UIMenuElement) -> Bool {
            let title = childTitle(element).lowercased()
            return title.contains("scan") || title.contains("扫描") || title.contains("文本")
        }

        private func isStandardEditTitle(_ title: String) -> Bool {
            if title.isEmpty { return false }
            return chineseMap[title] != nil
        }

        private func relabeledElement(_ element: UIMenuElement, textView: UITextView) -> UIMenuElement {
            let title = childTitle(element)
            guard !title.isEmpty, let cn = chineseMap[title] else {
                if !title.isEmpty { print(">>> [Relabel] \"\(title)\" SKIP (not in chineseMap)") }
                return element
            }

            let selector: Selector? = {
                if let cmd = element as? UIKeyCommand { return cmd.action }
                if let cmd = element as? UICommand { return cmd.action }
                return nil
            }()

            guard let sel = selector else {
                print(">>> [Relabel] \"\(title)\" -> \"\(cn)\" (no selector, keeping original type=\(String(describing: type(of: element))))")
                return element
            }

            let selName = NSStringFromSelector(sel)
            if selName == "delete:" {
                print(">>> [Relabel] \"\(title)\" SKIP (delete: not supported on UITextView, keeping original)")
                return element
            }

            print(">>> [Relabel] \"\(title)\" -> \"\(cn)\" (UIAction, selector=\(selName))")

            return UIAction(title: cn, image: (element as? UIKeyCommand)?.image ?? (element as? UICommand)?.image) { [weak textView] _ in
                guard let tv = textView else { return }
                UIApplication.shared.sendAction(sel, to: tv, from: nil, for: nil)
            }
        }

        private func relabeledMenuChildren(_ menu: UIMenu, textView: UITextView) -> UIMenu {
            let cnTitle = chineseMap[menu.title] ?? menu.title
            let relabeledChildren = menu.children.map { relabeledElement($0, textView: textView) }
            return UIMenu(title: cnTitle, children: relabeledChildren)
        }
    }
}
