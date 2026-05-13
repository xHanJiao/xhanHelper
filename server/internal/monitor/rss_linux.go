package monitor

import (
	"os"
	"strconv"
	"strings"
)

// getRSSMB 从 /proc/self/status 读取 VmRSS
func getRSSMB() float64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, err := strconv.ParseInt(fields[1], 10, 64)
				if err == nil {
					return float64(kb) / 1024 // KB → MB
				}
			}
		}
	}
	return -1
}
