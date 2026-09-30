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

    // MARK: - deny：未收命令

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

    // MARK: - 只读文本工具（v2 allow）

    func testAllowReadOnlyTextTools() {
        for command in ["sort -n a", "uniq -c a", "cut -d, -f1 a", "tr a b",
                        "nl a", "column -t a", "jq '.a' f"] {
            XCTAssertEqual(risk(command), "allow", "expected allow: \(command)")
        }
    }

    func testSortOutputFlagDenied() {
        XCTAssertEqual(risk("sort -o out a"), "deny")
        XCTAssertEqual(risk("sort -oout a"), "deny")
        XCTAssertEqual(risk("sort --output=out a"), "deny")
        XCTAssertEqual(risk("sort -rn a"), "allow")
    }

    // MARK: - l4：写/执行类（v2 放开，恒 L4）

    func testL4WriteCommands() {
        for command in ["rm -rf x", "mv a b", "cp a b", "mkdir d", "rmdir d", "touch f",
                        "tee f", "truncate -s 0 f", "chmod +x f", "chown u f", "ln -s a b"] {
            XCTAssertEqual(risk(command), "l4", "expected l4: \(command)")
        }
    }

    func testL4WriteOutsideAuthorizedDenied() {
        XCTAssertEqual(risk("rm /etc/hosts"), "deny")
        XCTAssertEqual(risk("rm -rf /"), "deny")
        XCTAssertEqual(risk("rm ~/.ssh/id_rsa"), "deny")
        XCTAssertEqual(risk("mkdir /etc/x"), "deny", "父目录未授权")
        XCTAssertEqual(risk("touch /etc/x"), "deny")
        XCTAssertEqual(risk("cp a /etc/x"), "deny")
    }

    func testL4FindSedAwk() {
        XCTAssertEqual(risk("find . -name x"), "l4")
        XCTAssertEqual(risk("awk '' f"), "l4")
        XCTAssertEqual(risk("sed -n 1,10p f"), "l4")
    }

    func testFindExecFormsDenied() {
        XCTAssertEqual(risk("find . -exec rm {} \\;"), "deny")
        XCTAssertEqual(risk("find . -delete"), "deny")
        XCTAssertEqual(risk("find . -ok rm {} \\;"), "deny")
        XCTAssertEqual(risk("find . -fprint out.txt"), "deny")
    }

    func testSedGuards() {
        XCTAssertEqual(risk("sed -i s/a/b/ f"), "deny")
        XCTAssertEqual(risk("sed -i.bak s/a/b/ f"), "deny")
        XCTAssertEqual(risk("sed 's/a/b/e' f"), "deny")
        XCTAssertEqual(risk("sed 'w out.txt' f"), "deny")
        XCTAssertEqual(risk("sed -f script.sed f"), "deny")
        XCTAssertEqual(risk("sed -n 1,10p f"), "l4")
    }

    func testAwkGuards() {
        XCTAssertEqual(risk("awk 'BEGIN{system(\"id\")}' f"), "deny")
        XCTAssertEqual(risk("awk '{print $1 > \"out\"}' f"), "deny")
        XCTAssertEqual(risk("awk -f prog.awk f"), "deny")
        XCTAssertEqual(risk("awk '$1 > 2 {print}' f"), "l4", "比较符 > 不应误杀")
    }

    func testArchiveCommandsStillDenied() {
        for command in ["rsync -a a/ b/", "tar czf a.tgz d", "zip a.zip f", "unzip a.zip", "gzip f"] {
            XCTAssertEqual(risk(command), "deny", "expected deny: \(command)")
        }
    }

    // MARK: - 人话解释（确认条）

    func testDescriber() {
        func d(_ c: String) -> String {
            ShellCommandDescriber.describe(stages: ShellCommandParser.evaluate(command: c, workdir: workdir).stages)
        }
        XCTAssertTrue(d("rm -rf build").contains("递归强制删除"))
        XCTAssertTrue(d("rm -rf build").contains("build"))
        XCTAssertTrue(d("mv a.txt docs/").contains("移动"))
        XCTAssertTrue(d("git commit -m fix").contains("提交"))
        XCTAssertTrue(d("git commit -m fix").contains("fix"))
        XCTAssertTrue(d("ls | head").contains("|"))
    }

    // MARK: - deny：脚本语言 / 构建链 / 未命中

    func testDenyScriptAndBuildChains() {
        for command in ["sed -i s/a/b/ f", "perl -e 1", "xargs rm",
                        "node -e 1", "python3 x.py", "go run .",
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

    func testGitNetworkAndConfigStillDenied() {
        for command in ["git push", "git pull", "git fetch", "git clone x y",
                        "git config user.name x", "git stash", "git stash bogus"] {
            XCTAssertEqual(risk(command), "deny", "expected deny: \(command)")
        }
    }

    func testGitWriteIsL4() {
        for command in ["git commit -m x", "git add .", "git checkout main", "git switch -c x",
                        "git restore f", "git reset --hard", "git clean -fd", "git merge x",
                        "git rebase main", "git cherry-pick abc", "git revert abc",
                        "git branch new", "git branch -d old", "git remote add x y",
                        "git tag v1", "git stash push"] {
            XCTAssertEqual(risk(command), "l4", "expected l4: \(command)")
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

    // MARK: - peek 作用域（主对话只读探查）

    private func riskPeek(_ command: String, workdir: String? = nil) -> String {
        let e = ShellCommandParser.evaluate(command: command, workdir: workdir ?? self.workdir, scope: .peek)
        switch e.risk {
        case .allow: return "allow"
        case .l4: return "l4"
        case .deny: return "deny"
        }
    }

    func testPeekAllowsReadOnlyProbes() {
        XCTAssertEqual(riskPeek("ls -la"), "allow")
        XCTAssertEqual(riskPeek("pwd"), "allow")
        XCTAssertEqual(riskPeek("stat file.txt"), "allow")
        XCTAssertEqual(riskPeek("du -sh ."), "allow")
        XCTAssertEqual(riskPeek("du -s ."), "allow")
        XCTAssertEqual(riskPeek("which git"), "allow")
        XCTAssertEqual(riskPeek("git status"), "allow")
        XCTAssertEqual(riskPeek("git rev-parse --show-toplevel"), "allow")
        XCTAssertEqual(riskPeek("git branch --show-current"), "allow")
        XCTAssertEqual(riskPeek("git log -n 5 --oneline"), "allow")
        XCTAssertEqual(riskPeek("git log -n5"), "allow")
        XCTAssertEqual(riskPeek("git log --max-count=20"), "allow")
    }

    func testPeekDeniesHeavyCommands() {
        // 扫盘 / 读正文 / 大输入：不在主对话开放
        for command in ["rg TODO .", "grep -r TODO .", "find . -name x", "tree .",
                        "sort a", "uniq a", "cut -d, -f1 a", "head -50 a", "tail a",
                        "cat a", "diff a b", "md5 a", "ls -R", "ls -laR", "ls --recursive"] {
            XCTAssertEqual(riskPeek(command), "deny", "peek should deny: \(command)")
        }
    }

    func testPeekDeniesWriteCommands() {
        for command in ["rm -rf x", "mv a b", "cp a b", "mkdir d", "touch f", "git commit -m x"] {
            XCTAssertEqual(riskPeek(command), "deny", "peek should deny: \(command)")
        }
    }

    func testPeekGitLogNeedsBoundedCount() {
        XCTAssertEqual(riskPeek("git log"), "deny", "无 -n 的完整历史不放行")
        XCTAssertEqual(riskPeek("git log --oneline"), "deny")
        XCTAssertEqual(riskPeek("git log -n 50"), "deny", "超过上限")
        XCTAssertEqual(riskPeek("git log -n 20"), "allow")
        XCTAssertEqual(riskPeek("git show"), "deny")
        XCTAssertEqual(riskPeek("git diff"), "deny")
    }

    func testPeekGitLogNegativeCountDenied() {
        // git 把 max_count<0 当无限制，会吐完整历史 → 必须拒
        XCTAssertEqual(riskPeek("git log -n -1"), "deny")
        XCTAssertEqual(riskPeek("git log --max-count=-1"), "deny")
        XCTAssertEqual(riskPeek("git log -n0"), "deny")
    }

    func testPeekGitLogMultipleCountsTakeLast() {
        // git 多个 max-count 时后者胜出；守卫必须看最后一个，否则被大值绕过
        XCTAssertEqual(riskPeek("git log -n 20 --max-count=99999"), "deny")
        XCTAssertEqual(riskPeek("git log --max-count=99999 -n 5"), "allow", "最后一个才是 5")
    }

    func testPeekDuNeedsSummarize() {
        XCTAssertEqual(riskPeek("du"), "deny", "裸 du 逐目录列出，不放行")
        XCTAssertEqual(riskPeek("du -h ."), "deny")
        XCTAssertEqual(riskPeek("du -sh ."), "allow")
    }

    func testPeekKeepsSafetyNets() {
        XCTAssertEqual(riskPeek("ls ~/.ssh"), "deny", "硬性拦截不因 peek 放开")
        XCTAssertEqual(riskPeek("ls /etc"), "deny", "未授权目录")
        XCTAssertEqual(riskPeek("ls | head -5"), "deny", "head 不在 peek 白名单，管道任一段拒则整条拒")
    }
}
