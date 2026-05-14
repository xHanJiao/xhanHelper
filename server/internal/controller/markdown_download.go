package controller

import (
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

func downloadMarkdown(dir string) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := filepath.Base(c.Param("filename"))
		if !strings.HasSuffix(name, ".md") {
			c.AbortWithStatus(404)
			return
		}
		c.Header("Content-Disposition", "attachment; filename=\""+name+"\"")
		c.File(filepath.Join(dir, name))
	}
}
