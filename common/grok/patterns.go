// patterns.go 内置 grok Pattern 库（命名与 Logstash 对齐，正则改写为 RE2 方言）。
//
// 为什么是「子集 + 改写」而不是照搬：
//   - Logstash 官方 grok-patterns 是 Java 正则，含原子组 (?>...)、后顾 (?<=...)、
//     命名组 (?<name>...) 写法；Go 的 regexp 是 RE2，这几类都不支持（会直接编译失败）。
//   - 因此这里把日志解析最常用的那批 pattern 重写为等价或语义更宽的 RE2 写法，
//     并由引擎统一生成命名捕获组（定义里不必、也不要写 (?P<name>...)）。
//   - 覆盖范围是「常见日志够用的那批」（约 70 个）；缺的用自定义 pattern 补，
//     用户定义优先于内置同名项。
package grok

// Builtin 内置 pattern 定义（键为 Pattern 名，值为 RE2 正则或对其它 Pattern 的引用）。
var Builtin = map[string]string{
	// ---- 通用 ----
	"WORD":            `\b\w+\b`,
	"NOTSPACE":        `\S+`,
	"SPACE":           `\s*`,
	"DATA":            `.*?`,
	"GREEDYDATA":      `.*`,
	"GREEDYMULTILINE": `(?s:.*)`,
	// QUOTEDSTRING：Logstash 版用原子组 + 后顾，RE2 不可用；这里用等价的两分支写法
	"QUOTEDSTRING": `"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'`,
	"QS":           `%{QUOTEDSTRING}`,

	// ---- 数值 ----
	"NUMBER":    `[+-]?(?:[0-9]+(?:\.[0-9]+)?|\.[0-9]+)`,
	"BASE10NUM": `%{NUMBER}`,
	"FLOAT":     `[+-]?(?:[0-9]+(?:\.[0-9]+)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?`,
	"INT":       `[+-]?[0-9]+`,
	"POSINT":    `\b[1-9][0-9]*\b`,
	"NONNEGINT": `\b[0-9]+\b`,
	"BASE16NUM": `0[xX][0-9a-fA-F]+`,
	"BOOL":      `(?:true|false|TRUE|FALSE|True|False)`,

	// ---- 网络 ----
	"IPV4":       `(?:(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)`,
	"IPV6":       `(?:(?:[0-9A-Fa-f]{1,4}:){7}[0-9A-Fa-f]{1,4}|(?:[0-9A-Fa-f]{1,4}:){1,7}:|(?:[0-9A-Fa-f]{1,4}:){1,6}:[0-9A-Fa-f]{1,4}|(?:[0-9A-Fa-f]{1,4}:){1,5}(?::[0-9A-Fa-f]{1,4}){1,2}|::(?:[0-9A-Fa-f]{1,4}:){0,5}[0-9A-Fa-f]{1,4}|::)`,
	"IP":         `(?:%{IPV6}|%{IPV4})`,
	"HOSTNAME":   `\b(?:[0-9A-Za-z][0-9A-Za-z-]{0,62})(?:\.(?:[0-9A-Za-z][0-9A-Za-z-]{0,62}))*(?:\.?)\b`,
	"IPORHOST":   `(?:%{IP}|%{HOSTNAME})`,
	"HOSTPORT":   `%{IPORHOST}:%{POSINT}`,
	"PORT":       `\b(?:[0-9]{1,5})\b`,
	"CISCOMAC":   `(?:(?:[A-Fa-f0-9]{4}\.){2}[A-Fa-f0-9]{4})`,
	"WINDOWSMAC": `(?:(?:[A-Fa-f0-9]{2}-){5}[A-Fa-f0-9]{2})`,
	"COMMONMAC":  `(?:(?:[A-Fa-f0-9]{2}:){5}[A-Fa-f0-9]{2})`,
	"MAC":        `(?:%{CISCOMAC}|%{WINDOWSMAC}|%{COMMONMAC})`,
	"UUID":       `[A-Fa-f0-9]{8}-(?:[A-Fa-f0-9]{4}-){3}[A-Fa-f0-9]{12}`,

	// ---- 身份 ----
	"EMAILLOCALPART": `[a-zA-Z0-9._%+-]+`,
	"EMAILADDRESS":   `%{EMAILLOCALPART}@%{HOSTNAME}`,
	"EMAIL":          `%{EMAILADDRESS}`,
	"USERNAME":       `[a-zA-Z0-9._-]+`,
	"USER":           `%{USERNAME}`,
	"HTTPDUSER":      `%{EMAILADDRESS}|%{USER}`,

	// ---- 时间 ----
	"MONTH":              `\b(?:Jan(?:uary)?|Feb(?:ruary)?|Mar(?:ch)?|Apr(?:il)?|May|Jun(?:e)?|Jul(?:y)?|Aug(?:ust)?|Sep(?:tember)?|Oct(?:ober)?|Nov(?:ember)?|Dec(?:ember)?)\b`,
	"MONTHNUM":           `(?:0?[1-9]|1[0-2])`,
	"MONTHNUM2":          `(?:0[1-9]|1[0-2])`,
	"MONTHDAY":           `(?:(?:0[1-9])|(?:[12][0-9])|(?:3[01])|[1-9])`,
	"DAY":                `(?:Mon(?:day)?|Tue(?:sday)?|Wed(?:nesday)?|Thu(?:rsday)?|Fri(?:day)?|Sat(?:urday)?|Sun(?:day)?)`,
	"YEAR":               `(?:\d\d){1,2}`,
	"HOUR":               `(?:2[0123]|[01]?[0-9])`,
	"MINUTE":             `(?:[0-5][0-9])`,
	"SECOND":             `(?:(?:[0-5]?[0-9]|60)(?:[:.,][0-9]+)?)`,
	"TIME":               `%{HOUR}:%{MINUTE}(?::%{SECOND})?`,
	"TIME12":             `(?:1[012]|0?[1-9]):%{MINUTE}(?::%{SECOND})?(?: ?[AaPp][Mm])?`,
	"TZ":                 `[A-Z]{3}`,
	"ISO8601_TIMEZONE":   `(?:Z|[+-]%{HOUR}(?::?%{MINUTE}))`,
	"TIMESTAMP_ISO8601":  `%{YEAR}-%{MONTHNUM}-%{MONTHDAY}[T ]%{HOUR}:?%{MINUTE}(?::?%{SECOND})?%{ISO8601_TIMEZONE}?`,
	"HTTPDATE":           `%{MONTHDAY}/%{MONTH}/%{YEAR}:%{TIME} %{INT}`,
	"SYSLOGTIMESTAMP":    `%{MONTH} +%{MONTHDAY} %{TIME}`,
	"DATESTAMP":          `%{YEAR}-%{MONTHNUM}-%{MONTHDAY}[- ]%{TIME}`,
	"DATESTAMP_RFC822":   `%{DAY} %{MONTH} %{MONTHDAY} %{YEAR} %{TIME} %{TZ}`,
	"DATESTAMP_RFC2822":  `%{DAY}, %{MONTHDAY} %{MONTH} %{YEAR} %{TIME} %{ISO8601_TIMEZONE}`,
	"DATESTAMP_OTHER":    `%{DAY} %{MONTH} %{MONTHDAY} %{TIME} %{TZ} %{YEAR}`,
	"DATESTAMP_EVENTLOG": `%{YEAR}%{MONTHNUM2}%{MONTHDAY}%{HOUR}%{MINUTE}%{SECOND}`,
	"HTTPDERROR_DATE":    `%{DAY} %{MONTH} %{MONTHDAY} %{TIME} %{YEAR}`,

	// ---- 日志行 ----
	"LOGLEVEL":          `(?:TRACE|DEBUG|INFO|WARN|WARNING|ERROR|FATAL|CRITICAL|NOTICE|SEVERE|ALERT|EMERG|EMERGENCY|AUDIT|CONFIG|FINE|FINER|FINEST)`,
	"PROG":              `[\x21-\x5a\x5c\x5e-\x7e]+`,
	"SYSLOGHOST":        `%{IPORHOST}`,
	"SYSLOGFACILITY":    `<%{NONNEGINT:facility}.%{NONNEGINT:priority}>`,
	"PRI":               `%{POSINT}`,
	"SYSLOGPROG":        `%{PROG:program}(?:\[%{POSINT:pid}\])?`,
	"SYSLOGBASE":        `%{SYSLOGTIMESTAMP:timestamp} (?:%{SYSLOGFACILITY} )?%{SYSLOGHOST:logsource} %{SYSLOGPROG}:`,
	"SYSLOGLINE":        `%{SYSLOGBASE} %{GREEDYDATA:message}`,
	"COMMONAPACHELOG":   `%{IPORHOST:clientip} %{HTTPDUSER:ident} %{HTTPDUSER:auth} \[%{HTTPDATE:timestamp}\] "(?:%{WORD:verb} %{NOTSPACE:request}(?: HTTP/%{NUMBER:httpversion})?|%{DATA:rawrequest})" %{NUMBER:response} (?:%{NUMBER:bytes}|-)`,
	"COMBINEDAPACHELOG": `%{COMMONAPACHELOG} %{QS:referrer} %{QS:agent}`,
	"HTTPD_COMMONLOG":   `%{COMMONAPACHELOG}`,

	// ---- 路径与 URI ----
	"UNIXPATH":     `(?:/[\w_%!$@:.,+~-]*)+`,
	"WINPATH":      `(?:[A-Za-z]+:|\\)(?:\\[^\\?*]*)+`,
	"PATH":         `(?:%{UNIXPATH}|%{WINPATH})`,
	"URIPROTO":     `[A-Za-z](?:[A-Za-z0-9+\-.]+)+`,
	"URIHOST":      `%{IPORHOST}(?::%{POSINT})?`,
	"URIPATH":      `(?:/[A-Za-z0-9$.+!*'(){},~:;=@#%&_\-]*)+`,
	"URIPARAM":     `\?[A-Za-z0-9$.+!*'|(){},~@#%&/=:;_\-\[\]<>]*`,
	"URIPATHPARAM": `%{URIPATH}(?:%{URIPARAM})?`,
	"URI":          `%{URIPROTO}://(?:%{USER}(?::[^@]*)?@)?(?:%{URIHOST})?(?:%{URIPATHPARAM})?`,

	// ---- Java 栈 ----
	"JAVACLASS":  `(?:[a-zA-Z$_][a-zA-Z0-9$_]*\.)*[a-zA-Z$_][a-zA-Z0-9$_]*`,
	"JAVAFILE":   `(?:[a-zA-Z0-9$_]+\.java)`,
	"JAVAMETHOD": `(?:[a-zA-Z$_][a-zA-Z0-9$_]*)`,
}
