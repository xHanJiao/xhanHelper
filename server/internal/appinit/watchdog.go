package appinit

import (
	"log"
	"os"
	"syscall"
	"time"
)

// startPPID 在包初始化时记录（≈ 进程启动那一刻的父进程 PID）。
// 本地模式由 app 拉起这个 Go 子进程；app 崩溃后子进程会被系统托孤（ppid 变成 1），
// 靠这个值区分「父进程还在」和「已被托孤」。
var startPPID = os.Getppid()

// WatchParent 起一个看门狗：本地模式下父进程退出就发 SIGTERM 优雅退出，
// 避免 app 崩溃后 Go 变成孤儿进程赖在端口上。
//
// 只在 local_single_user 时启用——云端由 systemd/nginx 托管，父进程语义不同，
// 贸然退出会误伤。启动时若本就没有真实父进程（ppid<=1，如直接由 launchd 拉起）也无从判断，跳过。
func WatchParent(localMode bool, interval time.Duration) {
	if !localMode {
		return
	}
	if interval <= 0 {
		interval = 5 * time.Second // time.NewTicker 对 <=0 会 panic，兜底
	}
	if startPPID <= 1 {
		log.Printf("[watchdog] 启动时无真实父进程（ppid=%d），不启用父进程看门狗", startPPID)
		return
	}
	log.Printf("[watchdog] 已启用父进程看门狗（ppid=%d，每 %s 检查一次）", startPPID, interval)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			if ppid := os.Getppid(); shouldExit(startPPID, ppid) {
				log.Printf("[watchdog] 父进程已退出（ppid %d → %d），本地后端随之退出", startPPID, ppid)
				if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
					log.Printf("[watchdog] 发送 SIGTERM 失败，直接退出：%v", err)
					os.Exit(1)
				}
				return
			}
		}
	}()
}

// shouldExit 报告是否因父进程变化而应退出：启动时确有父进程（>1），且当前 ppid 已不同。
//
// 语义取舍（方案 A）：把「任何 ppid 变化」都视为父进程已不在。macOS 上重父化只会是
// pid→1（无主进程统一托孤给 launchd），所以与「== 1」等价且更敏感；不收紧为 ==1，
// 是为了将来若出现别的托孤路径也能及时退出。
func shouldExit(startPPID, currentPPID int) bool {
	return startPPID > 1 && currentPPID != startPPID
}
