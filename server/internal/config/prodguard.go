package config

import (
	"log"
	"os"
	"strings"
)

// GuardProdWrite 破坏性写入前的护栏：回显解析后的 DB 路径；若疑似 prod 库
// （路径不含 "-dev"）则要求 CONFIRM_PROD=yes，否则退出。
//
// 本地 prod 库 = ~/Library/Application Support/xhanHelper/xhan_helper.db；
// dev 沙盒 = .../xhanHelper-dev/...（判据：路径带 "-dev"）。
// 供 entity-gc / topic-clean 等本地维护工具统一调用，避免各写一份、口径漂移。
func GuardProdWrite(dbPath string) {
	log.Printf("[prodguard] --write 目标 DB：%s", dbPath)
	if strings.Contains(dbPath, "-dev") {
		return
	}
	if os.Getenv("CONFIRM_PROD") == "yes" {
		log.Printf("[prodguard] 已确认 CONFIRM_PROD=yes，继续写入（疑似 prod 库）")
		return
	}
	log.Fatalf("[prodguard] 目标疑似 prod 库（路径不含 -dev）：%s\n"+
		"        确认无误请加 CONFIRM_PROD=yes 重跑；prod 库禁止误操作。", dbPath)
}
