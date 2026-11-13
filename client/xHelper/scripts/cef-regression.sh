#!/bin/bash
# CEF 四层伪装回归（主文档第三章 + 10.2）。**每次升级 CEF 之后、以及改伪装层之后先跑这个。**
#
# 为什么必须跑：伪装是"服务端/页面看到了什么"的事，本地看代码永远是自洽的。
# 用法：
#   scripts/cef-regression.sh [xhanHelper.app 路径]     # 不给就取最新 DerivedData 的 Debug 产物
#   XH_CEF_PROBE_URL=https://postman-echo.com/headers scripts/cef-regression.sh
#
# 不覆盖的部分（要人点，脚本替不了）：
#   - 谷歌登录页输一个假邮箱，看是否照常报"找不到账号"（而不是拒绝/风控页）
#   - 百度三站（首页/地图/百科）各开一次，看有没有验证码
set -uo pipefail

cd "$(dirname "$0")/.." || exit 1

REPORT="${XH_CEF_PROBE_REPORT:-$HOME/Library/Application Support/xhanHelper-dev/cef-probe.json}"
PROBE_URL="${XH_CEF_PROBE_URL:-https://httpbin.org/headers}"
APP="${1:-}"

if [ -z "$APP" ]; then
  APP=$(ls -dt "$HOME"/Library/Developer/Xcode/DerivedData/*/Build/Products/Debug/xhanHelper.app \
        2>/dev/null | head -1)
fi
[ -n "$APP" ] && [ -d "$APP" ] || {
  echo "找不到 xhanHelper.app：先 ⌘B 构建，或把 .app 路径当第一个参数传进来" >&2
  exit 2
}

# 探针用独立 profile（`$TMPDIR/xh-cef-probe`），不碰日常实例正在用的那份，所以可以边用 app 边跑。
# 报告仍落在数据根，方便与 dev 数据对照。
if pgrep -f "$APP/Contents/MacOS/xhanHelper" >/dev/null; then
  echo "注意：xhanHelper 正在运行，探针会另开一个实例（独立 profile，不影响正在用的数据）"
fi

if ! curl -sS -o /dev/null -m 8 -I "$PROBE_URL"; then
  echo "回显地址不可达：${PROBE_URL}（用 XH_CEF_PROBE_URL=... 换一个 echo 服务）" >&2
  exit 2
fi

rm -f "$REPORT"
echo "探针启动：$APP"
echo "回显地址：$PROBE_URL"
open -n "$APP" --args "--cef-probe=$PROBE_URL"

for _ in $(seq 1 60); do
  [ -s "$REPORT" ] && break
  sleep 0.5
done

if [ ! -s "$REPORT" ]; then
  echo "FAIL 30s 内没等到报告：$REPORT" >&2
  echo "     排查：Console 里过滤 [cef] / [cef-probe]；缺 framework 时先跑 scripts/fetch-cef.sh" >&2
  exit 1
fi

if python3 - "$REPORT" "$(cat CEF_VERSION)" <<'PY'
import json, sys

report_path, cef_version = sys.argv[1], sys.argv[2]
report = json.load(open(report_path))

fails = []
def check(name, ok, detail=""):
    print(("PASS " if ok else "FAIL ") + name + ("" if ok or not detail else "\n     实际：" + str(detail)[:300]))
    if not ok:
        fails.append(name)

if "error" in report:
    check("探针拿到结果", False, report["error"])
    print("\n%d 项未过" % len(fails))
    sys.exit(1)

# 内核版本是唯一事实来源（cef_version.h 编进 UA/brands，这里再对一次）
core = cef_version.split("+chromium-")[-1]      # 156.0.8078.17
major = core.split(".")[0]

# ── 第 1 层：UA
# 注意：CEF 默认 UA 与本机伪装**同形**（也是 Chrome/<内核版本> Safari/537.36），所以这几条只能证明
# "没更糟"，删掉 settings.user_agent 也照样 PASS。真正被服务端拿去做判定的是第 2 层的头。
ua = report.get("ua") or ""
check("UA 是同代 Chrome（%s）" % core, ("Chrome/" + core) in ua, ua)
check("UA 不是 Headless/CEF 形态", "Headless" not in ua and "Cef" not in ua, ua)
check("UA 是 macOS 冻结形态（Intel Mac OS X 10_15_7）", "Macintosh; Intel Mac OS X 10_15_7" in ua, ua)

# ── 第 2 层：服务端收到的请求头（echo 服务回显）
body = report.get("body") or ""
try:
    headers = json.loads(body).get("headers", {})
except Exception:
    headers = {}
if not headers:
    check("回显服务返回可解析的 headers JSON", False, body[:200])

def header(name):
    for k, v in headers.items():
        if k.lower() == name.lower():
            return v
    return None

ch_ua = header("Sec-Ch-Ua")
check("服务端收到 sec-ch-ua 且报 Google Chrome 同代",
      bool(ch_ua) and "Google Chrome" in ch_ua and ('v="%s"' % major) in ch_ua, ch_ua)
check("sec-ch-ua-platform = macOS", (header("Sec-Ch-Ua-Platform") or "").strip('"') == "macOS",
      header("Sec-Ch-Ua-Platform"))
check("sec-ch-ua-mobile = ?0", header("Sec-Ch-Ua-Mobile") == "?0", header("Sec-Ch-Ua-Mobile"))
check("请求头 UA 与 navigator.userAgent 一致", header("User-Agent") == ua, header("User-Agent"))

# ── 第 3 层：navigator.userAgentData
uad = report.get("uad") or {}
brands = uad.get("brands") or []
gc = [b for b in brands if b.get("brand") == "Google Chrome"]
check("userAgentData.brands 含 Google Chrome 同代",
      bool(gc) and gc[0].get("version") == major, brands)
check("userAgentData.platform = macOS", uad.get("platform") == "macOS", uad.get("platform"))
check("userAgentData.mobile = false", uad.get("mobile") is False, uad.get("mobile"))
check("userAgentData 伪装成 NavigatorUAData", uad.get("tag") == "[object NavigatorUAData]", uad.get("tag"))

# ── 第 4 层：window.chrome
keys = report.get("chrome")
check("window.chrome 存在", isinstance(keys, list), keys)
keys = keys if isinstance(keys, list) else []
cef_only = [k for k in ("setSyncEncryptionKeys", "setClientEncryptionKeys",
                        "addTrustedSyncEncryptionRecoveryMethod") if k in keys]
check("window.chrome 没有 CEF 专属加密函数", not cef_only, keys)
check("window.chrome 有 app/csi/loadTimes", {"app", "csi", "loadTimes"} <= set(keys), keys)

if fails:
    print("\n%d 项未过：%s" % (len(fails), "、".join(fails)))
    sys.exit(1)
print("\n四层伪装自洽。还差人眼两项：谷歌登录假邮箱、百度三站无验证码。")
PY
then :; else
  echo "回归未过，不写戳（check-cef-version.sh 会继续提示先跑回归）" >&2
  exit 1
fi

# 通过才写戳：check-cef-version.sh 用它判断"装进 app 的这个版本已做过伪装回归"。
# 版本取 app 里 framework 的实际版本（权威来源），读不到就退回 pin —— 但要说清，别假装验过。
STAMP="${XH_CEF_REGRESSION_STAMP:-${CEF_CACHE_DIR:-$HOME/Library/Caches/xhanHelper-cef}/regression-stamp.json}"
SHIPPED="$(/usr/bin/plutil -extract CFBundleShortVersionString raw -o - \
  "$APP/Contents/Frameworks/Chromium Embedded Framework.framework/Versions/Current/Resources/Info.plist" \
  2>/dev/null || true)"
if [ -z "$SHIPPED" ]; then
  SHIPPED="$(cut -d+ -f1 "$(cat CEF_VERSION)")"
  echo "note：读不到 app 里 framework 的版本，戳里记 pin 的 $SHIPPED" >&2
fi
mkdir -p "$(dirname "$STAMP")"
printf '{"version":"%s","pinned":"%s","at":"%s"}\n' \
  "$SHIPPED" "$(cat CEF_VERSION)" "$(date +%Y-%m-%d)" > "$STAMP"
echo "戳已写：${STAMP}（${SHIPPED}）"
