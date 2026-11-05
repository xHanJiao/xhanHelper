package controller

import (
	"strconv"

	"github.com/gin-gonic/gin"
)

// listLimitMax 用户列表接口单页上限，防止 ?limit=100000 把整表拉出来。
const listLimitMax = 200

// parseListLimit 解析 ?limit=：非法/<=0 用默认值，超过上限夹到 listLimitMax。
func parseListLimit(c *gin.Context, def int) int {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", strconv.Itoa(def)))
	if err != nil || limit <= 0 {
		return def
	}
	return clampListLimit(limit)
}

// parseListLimitAllowZero 同上，但保留 limit=0（文档/回收站用 0 表示只取 total）。
func parseListLimitAllowZero(c *gin.Context, def int) int {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", strconv.Itoa(def)))
	if err != nil || limit < 0 {
		return def
	}
	return clampListLimit(limit)
}

func clampListLimit(limit int) int {
	if limit > listLimitMax {
		return listLimitMax
	}
	return limit
}
