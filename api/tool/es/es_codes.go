// ES 控制台专属业务错误码（§14.9 错误码表）。HTTP 状态与码段由各 handler 决定。
package es

// 400 段：视图 /检索 /分析输入错误
const (
	esCodeKQLSyntax         = 4001 // KQL 语法错误（message 含位置与片段）
	esCodeFieldNotExist     = 4002 // KQL 语义错误：字段不存在
	esCodeOpNotSupported    = 4002 // KQL 语义错误：算子与字段类型不匹配（与 4002 同段）
	esCodeViewNotExist      = 4003
	esCodeNoTimeField       = 4004
	esCodePageOutOfRange    = 4005
	esCodeIndexNoMatch      = 4006
	esCodeViewNameDup       = 4007
	esCodeViewSyncing       = 4008
	esCodeTimeFieldConflict = 4009
	esCodeTimeInvalid       = 4010
	esCodeResultStale       = 4011
	esCodeAnalyzeInvalid    = 4012
	esCodeGrokInvalid       = 4016 // Grok：pattern 编译失败 / 输入非法（message 带 pattern 名与原因）
	esCodeImportInvalid     = 4017 // 导入：文件不可读 / 索引名非法 / 规则编译失败
	esCodeImportCanceled    = 4018 // 导入被用户取消（已写入的部分保留）
)

// 403/409 段：写操作白名单与冲突
const (
	esCodeManagedReadonly = 4013
	esCodePolicyInUse     = 4014
	esCodeBadResourceName = 4015
)
