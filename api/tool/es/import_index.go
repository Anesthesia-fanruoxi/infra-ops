// import_index.go 新建索引时按用户确认过的字段类型建 mapping。
//
// 为什么不全靠动态映射：ES 的 dynamic mapping 首次出现定终身 —— 第一批文档
// 把 status 建成 long 之后，后面出现的 "-" 会让那些文档**整条被拒**，
// 而用户此时已经在预览里确认过类型，照他的来才不会出这种意外。
package es

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// ensureIndexWithMapping 「不存在时创建」的字面语义：索引已存在就跳过创建，返回是否真的新建。
// 必须容忍已存在 —— 重复往同一索引导入是常态（第一批日志建好索引，第二批直接追加），
// 无条件 PUT 会让第二次导入在第一条数据之前就因 already exists 中止，而确认框上写的
// 却是「已存在，数据追加」，用户看到的是自相矛盾的失败。
func ensureIndexWithMapping(ctx context.Context, client *escClient, index string, types map[string]string) (bool, error) {
	exists, _ := client.indexExists(ctx, index)
	if exists {
		return false, nil
	}
	// 探测失败按「不存在」处理：照常尝试创建，让 PUT 的真实报错（权限/连接）浮出来
	return true, createIndexWithMapping(ctx, client, index, types)
}

// createIndexWithMapping 新建索引；types 为空时等价于纯动态映射。
func createIndexWithMapping(ctx context.Context, client *escClient, index string, types map[string]string) error {
	var payload []byte
	if len(types) > 0 {
		var err error
		payload, err = json.Marshal(map[string]any{
			"mappings": map[string]any{"properties": buildMappingProps(types)},
		})
		if err != nil {
			return err
		}
	}
	status, respBody, err := client.do(ctx, http.MethodPut, "/"+pathEscape(index), payload)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return esErr(status, respBody, "创建索引失败")
	}
	return nil
}

// buildMappingProps 把 "a.b" 这类字段名展开成嵌套对象。
//
// ES 的字段名口径里点号是路径分隔符：直接写 "a.b" 建出来的结构，
// 与写入时 {"a.b": ...} 被 ES 展开后的结构对不上，查询会查不到。
func buildMappingProps(types map[string]string) map[string]any {
	props := map[string]any{}
	for field, typ := range types {
		if strings.TrimSpace(field) == "" || strings.TrimSpace(typ) == "" {
			continue
		}
		setProp(props, strings.Split(field, "."), typ)
	}
	return props
}

func setProp(level map[string]any, path []string, typ string) {
	head := path[0]
	if head == "" {
		return
	}
	if len(path) == 1 {
		level[head] = mappingProp(typ)
		return
	}
	child, ok := level[head].(map[string]any)
	if !ok {
		child = map[string]any{"properties": map[string]any{}}
		level[head] = child
	}
	sub, ok := child["properties"].(map[string]any)
	if !ok {
		sub = map[string]any{}
		child["properties"] = sub
	}
	setProp(sub, path[1:], typ)
}

// mappingProp 单个字段的映射定义。date 带上宽松 format：日志时间戳形式五花八门，
// ES 默认只认 ISO8601，不显式给 format 会让空格分隔 / 逗号小数 / nginx 这些值整条被拒。
func mappingProp(typ string) map[string]any {
	if typ == typeDate {
		return map[string]any{"type": typ, "format": dateMappingFormat}
	}
	return map[string]any{"type": typ}
}
