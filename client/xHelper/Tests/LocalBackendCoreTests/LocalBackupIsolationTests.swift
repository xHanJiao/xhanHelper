import XCTest
@testable import LocalBackendCore

/// 钉住「dev 永远不能碰 prod 备份/数据」这条核心安全属性（跨 env 隔离）。
final class LocalBackupIsolationTests: XCTestCase {

    // MARK: - 归档名解析

    func testEnvOfParsesEnvironment() {
        XCTAssertEqual(LocalBackup.envOf(name: "xhanHelper-dev-20260929-221722.tar.gz"), "dev")
        XCTAssertEqual(LocalBackup.envOf(name: "xhanHelper-prod-20260929-205513.tar.gz"), "prod")
        XCTAssertNil(LocalBackup.envOf(name: "xhanHelper-20260929-205513.tar.gz"), "缺 env 段应解析失败")
        XCTAssertNil(LocalBackup.envOf(name: "other-dev-20260929-205513.tar.gz"), "非本前缀应解析失败")
        XCTAssertNil(LocalBackup.envOf(name: "xhanHelper-dev-20260929.txt"), "非 tar.gz 应解析失败")
    }

    // MARK: - 归档删除守卫

    func testDeleteRefusesForeignEnv() {
        let foreign = LocalBackup.env == "dev" ? "prod" : "dev"
        let name = "xhanHelper-\(foreign)-20260101-000000.tar.gz"
        let file = LocalBackupFile(
            name: name,
            url: URL(fileURLWithPath: "/tmp/\(name)"),
            bytes: 0,
            date: .distantPast
        )
        XCTAssertThrowsError(try LocalBackup.delete(file)) { error in
            guard case LocalBackupError.foreignEnvDelete(let env) = error else {
                return XCTFail("应抛 foreignEnvDelete，实际：\(error)")
            }
            XCTAssertEqual(env, foreign)
        }
    }

    func testDeleteAllowsOwnEnv() throws {
        let name = "xhanHelper-\(LocalBackup.env)-20260101-000000.tar.gz"
        let url = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try Data("x".utf8).write(to: url)
        let file = LocalBackupFile(name: name, url: url, bytes: 1, date: Date())
        XCTAssertNoThrow(try LocalBackup.delete(file))
        XCTAssertFalse(FileManager.default.fileExists(atPath: url.path), "本环境归档应被删除")
    }

    // MARK: - 安全副本删除守卫

    func testDeleteSafetyRefusesForeignEnv() {
        let other = LocalPaths.isDev ? LocalPaths.prodDataRootName : LocalPaths.devDataRootName
        let url = URL(fileURLWithPath: "/tmp/\(other).pre-restore-20260101-000000")
        let safety = LocalBackupSafety(url: url, bytes: 0, date: .distantPast)
        XCTAssertThrowsError(try LocalBackup.deleteSafety(safety)) { error in
            guard case LocalBackupError.foreignEnvSafety = error else {
                return XCTFail("应抛 foreignEnvSafety，实际：\(error)")
            }
        }
    }

    func testDeleteSafetyAllowsOwnEnv() throws {
        let parent = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: parent, withIntermediateDirectories: true)
        let url = parent.appendingPathComponent("\(LocalPaths.dataRootName).pre-restore-20260101-000000")
        try FileManager.default.createDirectory(at: url, withIntermediateDirectories: true)
        let safety = LocalBackupSafety(url: url, bytes: 0, date: Date())
        XCTAssertNoThrow(try LocalBackup.deleteSafety(safety))
        XCTAssertFalse(FileManager.default.fileExists(atPath: url.path), "本环境安全副本应被删除")
    }

    // MARK: - 前缀互不包含（隐式耦合的回归网）

    func testSafetyPrefixesAreDisjoint() {
        let devPrefix = LocalBackup.safetyPrefix(dataRoot: URL(fileURLWithPath: "/tmp/\(LocalPaths.devDataRootName)"))
        let prodPrefix = LocalBackup.safetyPrefix(dataRoot: URL(fileURLWithPath: "/tmp/\(LocalPaths.prodDataRootName)"))
        XCTAssertFalse(devPrefix.hasPrefix(prodPrefix))
        XCTAssertFalse(prodPrefix.hasPrefix(devPrefix))

        // 真实目录名（含时间戳）不得误配到另一个环境，否则会被误列/误删。
        let devCopy = "\(LocalPaths.devDataRootName).pre-restore-20260101-000000"
        let prodCopy = "\(LocalPaths.prodDataRootName).pre-restore-20260101-000000"
        XCTAssertTrue(devCopy.hasPrefix(devPrefix))
        XCTAssertTrue(prodCopy.hasPrefix(prodPrefix))
        XCTAssertFalse(devCopy.hasPrefix(prodPrefix), "dev 安全副本不能被 prod 前缀匹配")
        XCTAssertFalse(prodCopy.hasPrefix(devPrefix), "prod 安全副本不能被 dev 前缀匹配")
    }

    // MARK: - 同秒二次操作不撞名

    func testUniqueSafetyURLAvoidsCollision() throws {
        let parent = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: parent, withIntermediateDirectories: true)
        let root = parent.appendingPathComponent(LocalPaths.dataRootName)
        let date = Date(timeIntervalSince1970: 0)

        let first = LocalBackup.uniqueSafetyURL(dataRoot: root, date: date)
        try FileManager.default.createDirectory(at: first, withIntermediateDirectories: true)
        let second = LocalBackup.uniqueSafetyURL(dataRoot: root, date: date)

        XCTAssertNotEqual(first.path, second.path)
        XCTAssertTrue(second.lastPathComponent.hasPrefix(first.lastPathComponent))
    }
}
