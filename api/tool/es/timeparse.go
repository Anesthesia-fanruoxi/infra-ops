// 时间解析：全项目唯一的时间转换入口。仅接受 `YYYY-MM-DD HH:mm:ss`（UTC+8），
// 其余一律返回 error（调用方映射 4010），不静默纠正。
package es

import (
	"errors"
	"regexp"
	"time"
)

// beijing 固定 UTC+8 时区，与后端 datetime('now','localtime') 的口径一致。
var beijing = time.FixedZone("CST", 8*3600)

var reDateTime = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$`)

// ParseTimeRange 将 `start` / `end` 两个时间字符串解析为毫秒时间戳。
// 二者必须均为严格格式 `YYYY-MM-DD HH:mm:ss`（UTC+8），缺秒、`T` 分隔、
// 相对表达式、毫秒数字一律拒绝；并校验 start < end（下半开区间）。
func ParseTimeRange(start, end string) (startMs, endMs int64, err error) {
	if start == "" || end == "" {
		return 0, 0, errors.New("时间参数不能为空")
	}
	startMs, err = parseDateTime(start)
	if err != nil {
		return 0, 0, err
	}
	endMs, err = parseDateTime(end)
	if err != nil {
		return 0, 0, err
	}
	if startMs >= endMs {
		return 0, 0, errors.New("结束时间必须晚于开始时间")
	}
	return startMs, endMs, nil
}

// parseDateTime 解析单个 `YYYY-MM-DD HH:mm:ss`（UTC+8）字符串为毫秒时间戳。
func parseDateTime(s string) (int64, error) {
	if !reDateTime.MatchString(s) {
		return 0, errors.New("时间格式必须为 YYYY-MM-DD HH:mm:ss")
	}
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, beijing)
	if err != nil {
		return 0, err
	}
	return t.UnixMilli(), nil
}
