package activity

import "testing"

func TestNormalizeURL(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		want   string
		wantOK bool
	}{
		{"lower host, drop default port, drop fragment", "HTTPS://Example.COM:443/a/b/#frag", "https://example.com/a/b", true},
		{"http default port", "http://Example.com:80", "http://example.com", true},
		{"strip tracking + sort params", "https://x.com/a/?utm_source=t&b=2&a=1&xsec_token=zzz", "https://x.com/a?a=1&b=2", true},
		{"drop sensitive param", "https://x.com/p?token=abc&q=1", "https://x.com/p?q=1", true},
		{"trailing slash root", "https://x.com/", "https://x.com", true},
		{"keep non-default port", "https://x.com:8443/p/", "https://x.com:8443/p", true},
		{"preserve percent-encoding in path", "https://x.com/a%2Fb", "https://x.com/a%2Fb", true},
		{"preserve encoded space", "https://x.com/a%20b", "https://x.com/a%20b", true},
		{"ipv6 host keeps brackets", "http://[::1]:8080/p", "http://[::1]:8080/p", true},
		{"non-http scheme ignored", "about:blank", "", false},
		{"empty ignored", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := NormalizeURL(c.in)
			if ok != c.wantOK || got != c.want {
				t.Errorf("NormalizeURL(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.wantOK)
			}
		})
	}
}

func TestNormalizeGitRemote(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"git@GitHub.com:Owner/Repo.git", "github.com/owner/repo"},
		{"https://github.com/Owner/Repo.git", "github.com/owner/repo"},
		{"ssh://git@github.com/o/r.git", "github.com/o/r"},
		{"git@github.com:o/r", "github.com/o/r"},
	}
	for _, c := range cases {
		got, ok := NormalizeGitRemote(c.in)
		if !ok || got != c.want {
			t.Errorf("NormalizeGitRemote(%q) = (%q,%v), want %q", c.in, got, ok, c.want)
		}
	}
	if _, ok := NormalizeGitRemote("not a remote"); ok {
		t.Errorf("plain path must not parse as git remote")
	}
}

func TestNormalizeRepoPathAndFile(t *testing.T) {
	home := "/Users/me"
	if got := NormalizeRepoPath("/Users/me/x/r", home); got != "~/x/r" {
		t.Errorf("repo path = %q, want ~/x/r", got)
	}
	if got := NormalizeRepoPath("/opt/other", home); got != "/opt/other" {
		t.Errorf("outside home = %q", got)
	}

	// 仓库内 → 锚 + 相对路径
	got, ok := NormalizeFilePath("/Users/me/x/r/a/b.go", "/Users/me/x/r", "github.com/me/r", home)
	if !ok || got != "github.com/me/r/a/b.go" {
		t.Errorf("file under repo = %q ok=%v", got, ok)
	}
	// 仓库外 → 折叠家目录
	got, ok = NormalizeFilePath("/Users/me/other/c.go", "/Users/me/x/r", "github.com/me/r", home)
	if !ok || got != "~/other/c.go" {
		t.Errorf("file outside repo = %q ok=%v", got, ok)
	}
}

func TestNormalizeRepoFallback(t *testing.T) {
	// 无 remote 的仓：按路径折叠家目录
	if got, ok := NormalizeRepo("/Users/me/x/r", "/Users/me"); !ok || got != "~/x/r" {
		t.Errorf("repo fallback = %q ok=%v", got, ok)
	}
	// 空白输入不产出 "." 资源
	if got, ok := NormalizeRepo("  ", "/Users/me"); ok {
		t.Errorf("blank repo_path must not produce a resource, got %q ok=%v", got, ok)
	}
	// 仓库内文件但 repo_path 是 git remote（非绝对路径）→ 不静默假装锚上，按仓库外处理
	got, ok := NormalizeFilePath("/Users/me/x/r/a.go", "git@github.com:me/r.git", "github.com/me/r", "/Users/me")
	if !ok || got != "~/x/r/a.go" {
		t.Errorf("file with non-abs repo root = %q ok=%v, want ~/x/r/a.go", got, ok)
	}
}

func TestScrubbedURLKeepsEncoding(t *testing.T) {
	got := ScrubbedURL("https://x.com/a%2Fb?token=abc&q=1")
	if got != "https://x.com/a%2Fb?q=1&token=REDACTED" {
		t.Errorf("ScrubbedURL = %q", got)
	}
}

func TestScrubbedURLDropsFragmentAndUserinfo(t *testing.T) {
	// fragment 里塞 token 必须整段丢
	if got := ScrubbedURL("https://x.com/cb#access_token=xxx"); got != "https://x.com/cb" {
		t.Errorf("fragment not dropped: %q", got)
	}
	// userinfo 也丢
	if got := ScrubbedURL("https://user:pass@host/p"); got != "https://host/p" {
		t.Errorf("userinfo not dropped: %q", got)
	}
}

func TestNormalizeFilePathInputEqualsRoot(t *testing.T) {
	// 输入就是仓库根：应得锚本身，不能拼出 "anchor/."
	got, ok := NormalizeFilePath("/Users/me/x/r", "/Users/me/x/r", "github.com/me/r", "/Users/me")
	if !ok || got != "github.com/me/r" {
		t.Errorf("file == repoRoot = %q ok=%v, want github.com/me/r", got, ok)
	}
}
