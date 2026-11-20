import XCTest
@testable import DraftAutosaveCore

final class DraftAutosaveStoreTests: XCTestCase {
    private var defaults: UserDefaults!
    private var suiteName: String!

    override func setUp() {
        super.setUp()
        suiteName = "DraftAutosaveStoreTests.\(UUID().uuidString)"
        defaults = UserDefaults(suiteName: suiteName)
    }

    override func tearDown() {
        defaults.removePersistentDomain(forName: suiteName)
        defaults = nil
        suiteName = nil
        super.tearDown()
    }

    func testSaveAndLoadPerKey() {
        XCTAssertTrue(DraftAutosaveStore.save("甲", for: "c1", defaults: defaults))
        XCTAssertTrue(DraftAutosaveStore.save("乙", for: DraftAutosaveStore.blankKey, defaults: defaults))
        XCTAssertEqual(DraftAutosaveStore.load(for: "c1", defaults: defaults), "甲")
        XCTAssertEqual(DraftAutosaveStore.load(for: DraftAutosaveStore.blankKey, defaults: defaults), "乙")
        XCTAssertNil(DraftAutosaveStore.load(for: "c2", defaults: defaults))
    }

    func testEmptyTextDeletesEntry() {
        DraftAutosaveStore.save("要删掉", for: "c1", defaults: defaults)
        DraftAutosaveStore.save("", for: "c1", defaults: defaults)
        XCTAssertNil(DraftAutosaveStore.load(for: "c1", defaults: defaults))
    }

    func testExpiredEntryIsDropped() {
        let old = Date().addingTimeInterval(-31 * 24 * 60 * 60)
        DraftAutosaveStore.save("31 天前", for: "old", now: old, defaults: defaults)
        XCTAssertNil(DraftAutosaveStore.load(for: "old", defaults: defaults))
    }

    func testOversizedTextIsRejected() {
        let huge = String(repeating: "字", count: 200_001)
        XCTAssertFalse(DraftAutosaveStore.save(huge, for: "c1", defaults: defaults))
        XCTAssertNil(DraftAutosaveStore.load(for: "c1", defaults: defaults))
    }

    func testRemoveDeletesOnlyThatKey() {
        DraftAutosaveStore.save("甲", for: "c1", defaults: defaults)
        DraftAutosaveStore.save("乙", for: "c2", defaults: defaults)
        DraftAutosaveStore.remove(for: "c1", defaults: defaults)
        XCTAssertNil(DraftAutosaveStore.load(for: "c1", defaults: defaults))
        XCTAssertEqual(DraftAutosaveStore.load(for: "c2", defaults: defaults), "乙")
    }
}
