package mysql

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// 导出时把数据库值翻译成目标格式的写法。
//
// 驱动返回的形态与列类型强相关：开了 parseTime 后时间列是 time.Time，其余几乎都是
// []byte（含数值列），所以「要不要加引号」不能只看 Go 类型，必须结合 DatabaseTypeName。

// numericTypes 走数字字面量的类型（不加引号）。
// 用精确集合而不是 Contains 匹配：POINT 这类几何类型含 "INT"，Contains 会误判。
var numericTypes = map[string]bool{
	"TINYINT": true, "SMALLINT": true, "MEDIUMINT": true, "INT": true, "INTEGER": true,
	"BIGINT": true, "DECIMAL": true, "NUMERIC": true, "NEWDECIMAL": true,
	"FLOAT": true, "DOUBLE": true, "REAL": true, "YEAR": true,
}

// binaryPrefixes 走十六进制字面量的类型。用子串匹配而不是前缀匹配：
// TINYBLOB / MEDIUMBLOB / LONGBLOB 都不以 "BLOB" 开头。
var binaryPrefixes = []string{"BLOB", "BINARY", "BIT", "GEOMETRY", "POINT",
	"LINESTRING", "POLYGON", "MULTIPOINT", "MULTILINESTRING", "MULTIPOLYGON", "GEOMETRYCOLLECTION"}

func isNumericType(t string) bool {
	u := strings.ToUpper(strings.TrimSpace(t))
	u = strings.TrimPrefix(u, "UNSIGNED ")
	u = strings.TrimSuffix(u, " UNSIGNED")
	return numericTypes[u]
}

func isBinaryType(t string) bool {
	u := strings.ToUpper(strings.TrimSpace(t))
	u = strings.TrimPrefix(u, "UNSIGNED ")
	for _, p := range binaryPrefixes {
		if strings.Contains(u, p) {
			return true
		}
	}
	return false
}

// isPlainNumber 判断字符串是不是能直接当数字字面量的十进制写法。
//
// 「列类型是数值」不代表值一定可写成裸数字（驱动在某些路径下会给出意外文本），
// 直接裸写会拼出语法错误的 SQL，所以再多一道校验，不通过就退化成带引号的字符串。
func isPlainNumber(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	i, digits, dots, exp := 0, 0, 0, false
	if s[0] == '-' || s[0] == '+' {
		i = 1
	}
	for ; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			digits++
		case c == '.':
			dots++
			if dots > 1 {
				return false
			}
		case (c == 'e' || c == 'E') && digits > 0 && !exp:
			exp = true
			if i+1 < len(s) && (s[i+1] == '-' || s[i+1] == '+') {
				i++
			}
		default:
			return false
		}
	}
	return digits > 0
}

// appendSQLValue 把一个值拼成 INSERT 里的字面量。
//
// 字符串按 MySQL 标准转义（依赖 sql_mode 未开 NO_BACKSLASH_ESCAPES，这是 MySQL 默认）；
// 二进制列走 X'…' 十六进制，避免非法字节在转义/解码途中被改坏。
func appendSQLValue(b []byte, v interface{}, dbType string) []byte {
	switch t := v.(type) {
	case nil:
		return append(b, "NULL"...)
	case bool:
		if t {
			return append(b, '1')
		}
		return append(b, '0')
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return append(b, fmt.Sprint(t)...)
	case float32, float64:
		return append(b, fmt.Sprint(t)...)
	case time.Time:
		return appendQuoted(b, t.Format("2006-01-02 15:04:05"))
	case []byte:
		if isBinaryType(dbType) {
			return appendHex(b, t)
		}
		if isNumericType(dbType) && isPlainNumber(string(t)) {
			return append(b, t...)
		}
		return appendQuoted(b, string(t))
	case string:
		if isNumericType(dbType) && isPlainNumber(t) {
			return append(b, t...)
		}
		return appendQuoted(b, t)
	default:
		return appendQuoted(b, fmt.Sprint(v))
	}
}

// appendQuoted 按 MySQL 规则转义并加单引号。
//
// 覆盖 \0 \n \r \\ \' \" \Z 六种转义 + 反斜杠本身；漏掉任何一个都会让还原出的
// 数据与原文不一致（尤其是 \Z 与 NUL，肉眼看不出来）。
func appendQuoted(b []byte, s string) []byte {
	b = append(b, '\'')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case 0:
			b = append(b, '\\', '0')
		case '\n':
			b = append(b, '\\', 'n')
		case '\r':
			b = append(b, '\\', 'r')
		case '\\':
			b = append(b, '\\', '\\')
		case '\'':
			b = append(b, '\\', '\'')
		case '"':
			b = append(b, '\\', '"')
		case 0x1a:
			b = append(b, '\\', 'Z')
		default:
			b = append(b, c)
		}
	}
	return append(b, '\'')
}

const hexDigits = "0123456789ABCDEF"

func appendHex(b []byte, raw []byte) []byte {
	b = append(b, 'X', '\'')
	for _, c := range raw {
		b = append(b, hexDigits[c>>4], hexDigits[c&0x0f])
	}
	return append(b, '\'')
}

// appendCSVCell 按 RFC 4180 拼一个 CSV 字段。
func appendCSVCell(b []byte, s string) []byte {
	if !strings.ContainsAny(s, ",\"\r\n") {
		return append(b, s...)
	}
	b = append(b, '"')
	for i := 0; i < len(s); i++ {
		if s[i] == '"' {
			b = append(b, '"')
		}
		b = append(b, s[i])
	}
	return append(b, '"')
}

// xlsxValue 把值转成表格里该有的形态：能当数字的写数字单元格（Excel 里可直接求和），
// 超出 double 精确整数范围的整数退回文本——Excel 本身用 double 存数字，
// 硬转会让 19 位主键在文件里就悄悄变值，不如让它以文本保真。
func xlsxValue(v interface{}, dbType string) interface{} {
	switch t := v.(type) {
	case nil, bool, time.Time:
		return t
	case []byte:
		return textOrNumber(string(t), dbType)
	case string:
		return textOrNumber(t, dbType)
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return v
	case float32, float64:
		return v
	default:
		return fmt.Sprint(v)
	}
}

const maxExactInt = 1 << 53

func textOrNumber(s, dbType string) interface{} {
	if isNumericType(dbType) && isPlainNumber(s) {
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			if i <= maxExactInt && i >= -maxExactInt {
				return i
			}
			return s
		}
		// 连 int64 都装不下的纯整数（如 UNSIGNED BIGINT 的上半段）同样退回文本：
		// 转成 float64 会让主键在文件里就变成另一个数
		if !strings.ContainsAny(s, ".eE") {
			return s
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	}
	return s
}

// cellText 把值转成文本（CSV / xlsx 文本单元格共用）。NULL 落成空串。
func cellText(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(t)
	case time.Time:
		return t.Format("2006-01-02 15:04:05")
	default:
		return fmt.Sprint(v)
	}
}
