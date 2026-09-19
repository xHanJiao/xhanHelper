import XCTest
@testable import LocalArtifactsCore

final class LocalArtifactTests: XCTestCase {
    func testChartRoundTrip() throws {
        let chart = ChartData(
            type: .bar,
            title: "月度收支",
            labels: ["1月", "2月"],
            datasets: [ChartDataset(label: "支出", data: [1.5, 2.5])]
        )
        let artifact = LocalArtifact.chart(chart)
        XCTAssertEqual(artifact.type, .chart)
        XCTAssertEqual(try LocalArtifact(type: .chart, payload: artifact.payload()), artifact)
    }

    func testDocRefsRoundTrip() throws {
        let refs = [
            DocumentRef(id: "d1", title: "回归测试文档 v2", tags: ["v2", "回归测试"], preview: "标题《回归测试文档 v2》，变更记录…"),
            DocumentRef(id: "d2", title: "无标签", tags: [], preview: nil),
        ]
        let artifact = LocalArtifact.docRefs(refs)
        XCTAssertEqual(artifact.type, .docRefs)
        XCTAssertEqual(try LocalArtifact(type: .docRefs, payload: artifact.payload()), artifact)
    }

    func testEmptyDocRefsRoundTrip() throws {
        let artifact = LocalArtifact.docRefs([])
        XCTAssertEqual(try LocalArtifact(type: .docRefs, payload: artifact.payload()), artifact)
    }

    func testUnicodePreviewAndTagsSurviveRoundTrip() throws {
        let ref = DocumentRef(id: "d", title: "标题😀", tags: ["标签", "a b"], preview: "换行 与 emoji 保留")
        let decoded = try LocalArtifact(type: .docRefs, payload: LocalArtifact.docRefs([ref]).payload())
        XCTAssertEqual(decoded, .docRefs([ref]))
    }

    func testMalformedPayloadReturnsNil() {
        XCTAssertNil(LocalArtifact(type: .docRefs, payload: Data("{not json".utf8)))
        XCTAssertNil(LocalArtifact(type: .chart, payload: Data("{not json".utf8)))
    }

    func testWrongShapePayloadReturnsNil() {
        XCTAssertNil(LocalArtifact(type: .docRefs, payload: Data(#"{"id":"x"}"#.utf8)))
        XCTAssertNil(LocalArtifact(type: .chart, payload: Data("[1,2,3]".utf8)))
    }
}
