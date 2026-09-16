import XCTest
@testable import FsCore

final class ShellCommandParserTests: XCTestCase {

    /// 授权一个临时工作目录（用 realpath，避免 /var → /private/var 的 symlink 前缀不匹配）。
    private var workdir = "/tmp"

    override func setUp() {
        super.setUp()
        let dir = NSTemporaryDirectory() + "shellparser-\(UUID().uuidString)"
        try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        workdir = FsPath.resolve(dir)
        DirectoryAuthorizationManager.shared.authorize(workdir, permanent: false)
    }

    override func tearDown() {
        try? FileManager.default.removeItem(atPath: workdir)
        super.tearDown()
    }

    private func risk(_ command: String, workdir: String? = nil) -> String {
        let e = ShellCommandParser.evaluate(command: command, workdir: workdir ?? self.workdir)
        switch e.risk {
        case .allow: return "allow"
        case .l4: return "l4"
        case .deny: return "deny"
        }
    }

    // MARK: - allow（只读，且在授权目录内）

    func testAllowReadOnlyCommands() {
        XCTAssertEqual(risk("ls -la"), "allow")
        XCTAssertEqual(risk("grep -r TODO ."), "allow")
        XCTAssertEqual(risk("rg TODO src"), "allow")
        XCTAssertEqual(risk("ls | head -5"), "allow")
        XCTAssertEqual(risk("ls ; pwd"), "allow")
        XCTAssertEqual(risk("/bin/ls"), "allow")
        XCTAssertEqual(risk("ls --version"), "allow")
        XCTAssertEqual(risk("git --version"), "allow")
    }

    func testGitReadOnly() {
        XCTAssertEqual(risk("git status"), "allow")
        XCTAssertEqual(risk("git log --oneline --max-count=20"), "allow")
        XCTAssertEqual(risk("git diff"), "allow")
        XCTAssertEqual(risk("git branch"), "allow")
        XCTAssertEqual(risk("git branch --list"), "allow")
        XCTAssertEqual(risk("git remote -v"), "allow")
        XCTAssertEqual(risk("git remote show origin"), "allow")
        XCTAssertEqual(risk("git tag -l"), "allow")
        XCTAssertEqual(risk("git tag -n5"), "allow")
        XCTAssertEqual(risk("git stash list"), "allow")
        XCTAssertEqual(risk("git stash show"), "allow")
    }

    // MARK: - 授权目录约束（A：路径参数必须落在已授权目录内）

    func testPathInsideAuthorizedDirAllowed() throws {
        let file = (workdir as NSString).appendingPathComponent("sample.txt")
        try "hello".write(toFile: file, atomically: true, encoding: .utf8)
        XCTAssertEqual(risk("head sample.txt"), "allow")
        XCTAssertEqual(risk("head \(file)"), "allow")
        XCTAssertEqual(risk("grep -r hello ."), "allow")
    }

    func testPathOutsideAuthorizedDirDenied() {
        // 授权目录外、确实存在的路径 → 拒绝
        XCTAssertEqual(risk("head /etc/hosts"), "deny")
        XCTAssertEqual(risk("head /"), "deny")
        XCTAssertEqual(risk("ls /usr"), "deny")
        XCTAssertEqual(risk("grep -r root /etc"), "deny")
    }

    func testPatternWithSlashNotFalsePositive() {
        // 含 / 但不存在的"路径"（其实是正则/普通参数）不应被误杀
        XCTAssertEqual(risk("grep /api/v1 ."), "allow")
        XCTAssertEqual(risk("rg /no-such-dir-xyz/foo ."), "allow")
    }

    // MARK: - Agent 工作区（隐式读写根）

    func testAgentWorkspaceIsImplicitlyAuthorized() {
        let sub = (AgentWorkspace.rootPath as NSString).appendingPathComponent("sub/file.txt")
        XCTAssertTrue(DirectoryAuthorizationManager.shared.isAuthorized(sub))
        XCTAssertTrue(DirectoryAuthorizationManager.shared.isReadAuthorized(sub))
    }

    func testShellCanReadAgentWorkspace() throws {
        AgentWorkspace.ensureExists()
        let file = (AgentWorkspace.rootPath as NSString).appendingPathComponent("shellparser-test-\(UUID().uuidString).txt")
        try "hi".write(toFile: file, atomically: true, encoding: .utf8)
        defer { try? FileManager.default.removeItem(atPath: file) }
        XCTAssertEqual(risk("head \"\(file)\""), "allow")
    }

    // MARK: - 工作区范围判定（供风险分级：工作区内 → L3，其余 → L4）

    private func scoped(_ command: String, workdir: String? = nil) -> Bool {
        ShellCommandParser.isScopedToAgentWorkspace(command: command, workdir: workdir ?? self.workdir)
    }

    func testIsWithinAgentWorkspaceComponentBoundary() {
        let ws = AgentWorkspace.rootPath
        XCTAssertTrue(DirectoryAuthorizationManager.shared.isWithinAgentWorkspace(ws))
        XCTAssertTrue(DirectoryAuthorizationManager.shared.isWithinAgentWorkspace((ws as NSString).appendingPathComponent("a/b")))
        // 前缀相同但非子路径（workspace2）不应命中
        let sibling = ((ws as NSString).deletingLastPathComponent as NSString).appendingPathComponent("workspace2")
        XCTAssertFalse(DirectoryAuthorizationManager.shared.isWithinAgentWorkspace(sibling))
    }

    func testWorkspaceScopedTrue() {
        let ws = AgentWorkspace.rootPath
        XCTAssertTrue(scoped("ls", workdir: ws))
        XCTAssertTrue(scoped("git status", workdir: ws))
        XCTAssertTrue(scoped("ls .", workdir: ws))
        XCTAssertTrue(scoped("ls | head -5", workdir: ws))
        XCTAssertTrue(scoped("grep /api/v1 .", workdir: ws), "正则含 / 不应误判越界")
        XCTAssertTrue(scoped("ls ..", workdir: (ws as NSString).appendingPathComponent("sub")), "上一级仍是工作区")
    }

    func testWorkspaceScopedFalseWhenEscaping() {
        let ws = AgentWorkspace.rootPath
        XCTAssertFalse(scoped("ls ..", workdir: ws), "父目录在工作区外")
        XCTAssertFalse(scoped("ls /tmp", workdir: ws), "存在的路径在工作区外")
        XCTAssertFalse(scoped("ls | head /etc/hosts", workdir: ws), "管道任一段越界即非工作区")
        XCTAssertFalse(scoped("ls", workdir: self.workdir), "workdir 不在工作区（临时授权目录）")
    }

    // MARK: - B：shell 不得读 tool_output（该只读例外只给 fs）

    func testShellCannotReadToolOutputDir() throws {
        let url = try XCTUnwrap(ToolOutputStore.shared.save("shell spill content"))
        XCTAssertFalse(DirectoryAuthorizationManager.shared.isAuthorized(url.path), "tool_output 不是读写授权根")
        XCTAssertTrue(DirectoryAuthorizationManager.shared.isReadAuthorized(url.path), "fs 只读例外仍在")
        XCTAssertEqual(risk("head \"\(url.path)\""), "deny")
    }

    // MARK: - l4 集合为空：shell 整体由 registry 定级 L4，解析层只区分 allow/deny

    func testCurlIsDenied() {
        // curl 已移出白名单（联网交给浏览器），解析层直接拒绝
        XCTAssertEqual(risk("curl https://example.com"), "deny")
        XCTAssertEqual(risk("curl -s https://example.com | head -20"), "deny")
    }

    // MARK: - 递归命令 + 硬性拦截路径（防 `grep -r x ~` 把 .ssh 私钥打印出来）

    func testRecursiveCommandIntoHardPathDenied() {
        let home = NSHomeDirectory()
        XCTAssertEqual(risk("grep -r TODO \(home)"), "deny")
        XCTAssertEqual(risk("grep -R TODO \(home)"), "deny")
        XCTAssertEqual(risk("grep -rn TODO \"/\""), "deny")
        XCTAssertEqual(risk("rg TODO \(home)"), "deny")
        XCTAssertEqual(risk("rg TODO /"), "deny")
    }

    func testRecursiveCommandInProjectAllowed() {
        XCTAssertEqual(risk("grep -r TODO ."), "allow")
        XCTAssertEqual(risk("grep -rn TODO ."), "allow")
        XCTAssertEqual(risk("rg TODO ."), "allow")
        XCTAssertEqual(risk("grep -r TODO \(workdir)"), "allow")
    }

    func testGitNoIndexDiffDenied() {
        // git diff --no-index 可读任意文件，绕过仓库与路径检查
        XCTAssertEqual(risk("git diff --no-index ~/.zshrc /dev/null"), "deny")
        XCTAssertEqual(risk("git diff --no-index --stat a b"), "deny")
        XCTAssertEqual(risk("git diff"), "allow")
    }

    // MARK: - deny：写操作 / git 写 / 脚本语言 / 构建链 / 未命中

    func testDenyWriteCommands() {
        for command in ["rm -rf x", "mv a b", "cp a b", "mkdir d", "rmdir d", "touch f",
                        "tee f", "truncate -s 0 f", "chmod +x f", "chown u f", "ln -s a b",
                        "rsync -a a/ b/", "tar czf a.tgz d", "zip a.zip f", "unzip a.zip", "gzip f"] {
            XCTAssertEqual(risk(command), "deny", "expected deny: \(command)")
        }
    }

    func testDenyScriptAndBuildChains() {
        for command in ["sed -i s/a/b/ f", "awk '' f", "perl -e 1", "xargs rm",
                        "find . -name x", "node -e 1", "python3 x.py", "go run .",
                        "npm install", "npx foo", "brew install x", "make", "cargo build",
                        "xcodebuild", "sudo ls", "cat file.txt"] {
            XCTAssertEqual(risk(command), "deny", "expected deny: \(command)")
        }
        XCTAssertEqual(risk("node --version"), "deny")
    }

    func testDenyNetworkAndProcessCommands() {
        for command in ["wget https://example.com", "ssh host", "scp a b", "sftp host",
                        "nc -l 1234", "kill 123", "pkill x", "killall x", "open .",
                        "osascript -e 1", "docker ps", "kubectl get pods", "gh pr list"] {
            XCTAssertEqual(risk(command), "deny", "expected deny: \(command)")
        }
    }

    func testGitWriteIsDenied() {
        for command in ["git commit -m x", "git push", "git pull", "git add .",
                        "git config user.name x", "git clean -fd", "git reset --hard",
                        "git branch new", "git branch -d old", "git remote add x y",
                        "git tag v1", "git stash push", "git stash"] {
            XCTAssertEqual(risk(command), "deny", "expected deny: \(command)")
        }
    }

    func testDenyUnsupportedSyntax() {
        XCTAssertEqual(risk("sleep 1 &"), "deny")
        XCTAssertEqual(risk("echo hi > f"), "deny")
        XCTAssertEqual(risk("ls ; ; pwd"), "deny")
        XCTAssertEqual(risk("ls |"), "deny")
        XCTAssertEqual(risk("| ls"), "deny")
        XCTAssertEqual(risk(""), "deny")
        XCTAssertEqual(risk("ls && sudo rm -rf /"), "deny")
    }

    // MARK: - 绝对路径 argv[0]

    func testAbsolutePathMustBeInTrustedDir() {
        XCTAssertEqual(risk("/tmp/evil/git status"), "deny")
        XCTAssertEqual(risk("/bin/ls"), "allow")
        XCTAssertEqual(risk("./script.sh"), "deny")
    }

    // MARK: - 敏感路径

    func testSensitivePathBlocked() {
        XCTAssertEqual(risk("head ~/.ssh/id_rsa"), "deny")
    }

    // MARK: - no-shell：元字符只作字面量

    func testMetacharactersAreLiteral() throws {
        let e = ShellCommandParser.evaluate(command: "echo $(whoami)", workdir: workdir)
        XCTAssertEqual(e.stages.first?.argv, ["echo", "$(whoami)"])
    }

    func testPipelineGrouping() {
        let e = ShellCommandParser.evaluate(command: "ls | grep x && pwd", workdir: workdir)
        XCTAssertEqual(e.stages.count, 3)
        XCTAssertEqual(e.stages[0].argv, ["ls"])
        XCTAssertEqual(e.stages[1].argv, ["grep", "x"])
        XCTAssertEqual(e.stages[2].argv, ["pwd"])
    }
}
