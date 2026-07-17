import SwiftUI
import MarkdownUI

struct DocumentDetailView: View {
    let document: Document

    var body: some View {
        ScrollView {
            Markdown(document.content)
                .padding()
        }
        .navigationTitle(document.title)
#if os(iOS)
        .navigationBarTitleDisplayMode(.inline)
#endif
    }
}
