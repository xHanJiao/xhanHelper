package activity

import (
	"net/url"
	"path/filepath"
	"strings"
)

// 资源 kind（附表 B event_resources.resource_kind）。
const (
	ResourceKindURL      = "url"
	ResourceKindRepo     = "repo"
	ResourceKindFile     = "file"
	ResourceKindDoc      = "doc"
	ResourceKindMemory   = "memory"
	ResourceKindBill     = "bill"
	ResourceKindReminder = "reminder"
)

// NormalizeRuleVersion 是归一规则的版本号；规则一变就 +1，并经 RebuildResources 写进
// meta.normalize_rule_version，供"按版本重跑"判定（设计篇 6.1）。
const NormalizeRuleVersion = 1

// trackingParams 是要剥掉的跟踪/营销参数（精确名）。前缀类见 utmPrefix。
// 名单取自设计篇 6.1（可抄 ClearURLs），够用即可，后续按需增补（增补即改规则版本）。
var trackingParams = map[string]struct{}{
	"fbclid": {}, "gclid": {}, "gclsrc": {}, "dclid": {}, "msclkid": {}, "yclid": {},
	"igshid": {}, "mc_eid": {}, "_ga": {}, "spm": {}, "scm": {},
	"xsec_token": {}, "xsec_source": {}, "share_token": {}, "share_source": {},
	"share_medium": {}, "from_source": {},
}

// sensitiveParams 是凭证类参数名（服务端兜底：客户端已脱敏，但服务端产生的事件也可能带）。
// 归一时整条丢弃，保证规范 id 里不含敏感值。
var sensitiveParams = map[string]struct{}{
	"token": {}, "access_token": {}, "refresh_token": {}, "id_token": {},
	"api_key": {}, "apikey": {}, "api-key": {}, "x-api-key": {},
	"key": {}, "password": {}, "passwd": {}, "pwd": {}, "secret": {}, "client_secret": {},
	"sig": {}, "signature": {}, "auth": {}, "authorization": {}, "bearer": {}, "jwt": {},
	"code": {}, "credential": {}, "session": {}, "sessionid": {},
}

const utmPrefix = "utm_"

// NormalizeURL 做 RFC 3986 语法级归一：小写 host、去默认端口、去 fragment、删跟踪与凭证参数、
// 参数按名排序、去末尾斜杠。只认 http/https，其它 scheme 返回 ok=false（不当作网页资源）。
// 归一失败（解析不出）时返回 ok=false，由调用方按"非资源"处理（不静默编造）。
func NormalizeURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", false
	}
	// IPv6 的 Hostname() 去掉方括号，重组时必须补回，否则 http://::1:8080 非法。
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port := u.Port(); port != "" && !isDefaultPort(scheme, port) {
		host = host + ":" + port
	}
	// 用 EscapedPath：u.Path 是解码后的值，重组会把 %2F 变 /、%20 变空格（资源被静默换掉）。
	path := stripTrailingSlash(u.EscapedPath())
	q := u.Query()
	for k := range q {
		lower := strings.ToLower(k)
		if _, ok := trackingParams[lower]; ok || strings.HasPrefix(lower, utmPrefix) {
			q.Del(k)
			continue
		}
		if _, ok := sensitiveParams[lower]; ok {
			q.Del(k)
		}
	}
	canonical := scheme + "://" + host + path
	if enc := q.Encode(); enc != "" {
		canonical += "?" + enc
	}
	return canonical, true
}

func isDefaultPort(scheme, port string) bool {
	return (scheme == "http" && port == "80") || (scheme == "https" && port == "443")
}

// SCRubbedURL 把 URL 里的敏感参数值换成 `REDACTED`（其余不动），用于落进别名/边的原始值。
// canonical 已整条丢弃敏感参数；但 raw_value/alias 是原样保留，服务端将来发带 url 的事件时
// 不能把 token 落进 events.db（附表 A 硬规则：URL 里的 token 不进事件）。
// 与客户端 ActivityUrls.sanitized 同一意图；服务端多此一层兜底。
func ScrubbedURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	q := u.Query()
	for k := range q {
		if _, ok := sensitiveParams[strings.ToLower(k)]; ok {
			q.Set(k, "REDACTED")
		}
	}
	u.RawQuery = q.Encode()
	// fragment 里也常塞 token（`#access_token=xxx`）；userinfo（`user:pass@`）同理。整段丢弃，
	// 与客户端 ActivityUrls.sanitized（去 fragment）对齐。附表 A：URL 里的 token 不进事件。
	u.Fragment = ""
	u.RawFragment = ""
	u.User = nil
	return u.String()
}

func stripTrailingSlash(p string) string {
	if p == "/" {
		return ""
	}
	return strings.TrimRight(p, "/")
}

// NormalizeGitRemote 把 git remote 的 ssh 与 https 写法归一到同一形态：
// 去掉 scheme 与 user、host 与 path 小写、去 `.git` 后缀。例：
//
//	git@GitHub.com:Owner/Repo.git   → github.com/owner/repo
//	https://github.com/Owner/Repo.git → github.com/owner/repo
//	ssh://git@github.com/o/r.git     → github.com/o/r
func NormalizeGitRemote(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	var host, path string
	if !strings.Contains(raw, "://") {
		// scp 式：user@host:path（冒号在第一个斜杠之前）
		at := strings.LastIndex(raw, "@")
		colon := strings.Index(raw, ":")
		if at < 0 || colon < 0 || colon < at {
			return "", false
		}
		host = raw[at+1 : colon]
		path = raw[colon+1:]
	} else {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			return "", false
		}
		host = u.Hostname()
		if port := u.Port(); port != "" {
			host += ":" + port
		}
		path = u.Path
	}
	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")
	if host == "" || path == "" {
		return "", false
	}
	return strings.ToLower(host) + "/" + strings.ToLower(path), true
}

// NormalizeRepoPath 把仓库绝对路径折叠家目录前缀（`/Users/me/x/r` → `~/x/r`）。
// 空白输入返回空串（不把 filepath.Clean("") 的 "." 当路径）。
func NormalizeRepoPath(path, home string) string {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || path == "." {
		return ""
	}
	if home != "" {
		home = filepath.Clean(home)
		if path == home {
			return "~"
		}
		if strings.HasPrefix(path, home+string(filepath.Separator)) {
			return "~" + path[len(home):]
		}
	}
	return path
}

// NormalizeRepo 优先按 git remote 归一；远程不像 remote 时按绝对路径（折叠家目录）。
func NormalizeRepo(raw, home string) (string, bool) {
	if strings.Contains(raw, "://") || looksLikeSCP(raw) {
		if canonical, ok := NormalizeGitRemote(raw); ok {
			return canonical, true
		}
	}
	path := NormalizeRepoPath(raw, home)
	if path == "" {
		return "", false
	}
	return path, true
}

func looksLikeSCP(raw string) bool {
	at := strings.LastIndex(raw, "@")
	colon := strings.Index(raw, ":")
	return at > 0 && colon > at
}

// NormalizeFilePath 归一文件路径：仓库内 → 仓库锚 + 仓库内相对路径；仓库外 → 绝对路径（折叠家目录）。
// repoRoot 为空、或不是绝对路径（相对路径/git remote 归不出仓库根）时按仓库外处理——不静默假装锚上了。
func NormalizeFilePath(path, repoRoot, repoAnchor, home string) (string, bool) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || path == "." {
		return "", false
	}
	if repoRoot != "" && repoAnchor != "" && filepath.IsAbs(filepath.Clean(repoRoot)) {
		root := filepath.Clean(repoRoot)
		if rel, err := filepath.Rel(root, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			if rel == "." {
				return repoAnchor, true // 输入就是仓库根本身
			}
			return strings.TrimSuffix(repoAnchor, "/") + "/" + filepath.ToSlash(rel), true
		}
	}
	return NormalizeRepoPath(path, home), true
}
