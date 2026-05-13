package monitor

// getRSSMB macOS 无法简单获取 OS RSS，返回 -1
func getRSSMB() float64 {
	return -1
}
