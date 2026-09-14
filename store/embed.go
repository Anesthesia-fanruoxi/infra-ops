// 内嵌资源的唯一声明点（设计文档 §5.3）。
//
// 收紧要点：套件目录内将出现 .go 源码，若继续用 //go:embed stacks 会把源码当静态资源
// 打进二进制（体积与信息泄露双重问题）。故只嵌入各套件的 scripts/ 与 configs/：
//
//  1. 目录模式的匹配是递归的，stacks/*/scripts 仍然覆盖三级路径
//     （如 stacks/elasticsearch/scripts/cold-warm-hot/node.sh）；
//  2. FS 内保留完整相对路径，StackFS.ReadFile("stacks/redis/scripts/node.sh") 行为不变；
//  3. 无 configs/ 的套件只是该模式项不匹配，不影响编译。
package store

import (
	"embed"
	"strings"
)

// StackFS 套件脚本与配置：stacks/<key>/scripts/**、stacks/<key>/configs/**。
//
//go:embed stacks/*/scripts stacks/*/configs
var StackFS embed.FS

// BuiltinFS 部署模板脚本与资源（builtin/*.sh、builtin/*.conf、builtin/*.sql）。
//
//go:embed builtin
var BuiltinFS embed.FS

// readAsset 按路径前缀路由到对应内嵌 FS：builtin/... 走 BuiltinFS（部署模板体系），
// 其余（stacks/...）走 StackFS（套件体系）。部署模板中有 3 条脚本直接指向套件目录，
// 路由是它们的读取入口（议题④-B：只改入口，模板定义原样保留）。
func readAsset(path string) ([]byte, error) {
	if strings.HasPrefix(path, "builtin/") {
		return BuiltinFS.ReadFile(path)
	}
	return StackFS.ReadFile(path)
}
