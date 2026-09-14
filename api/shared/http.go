package shared

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// ParseID 从 gin 路径参数解析 ID；无法解析返回 0。
func ParseID(c *gin.Context) int64 {
	return ParseIDParam(c, "id")
}

// ParseIDParam 从 gin 路径参数按名解析 ID；无法解析返回 0。
func ParseIDParam(c *gin.Context, name string) int64 {
	id, _ := strconv.ParseInt(c.Param(name), 10, 64)
	return id
}

// ParsePage 解析分页参数，返回归一化后的 (page, pageSize)。
func ParsePage(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	return page, pageSize
}

// IsDuplicateErr 判断错误是否为「名称重复」类错误。
func IsDuplicateErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate name")
}
