// 部署变量/自定义配置合并与渲染辅助。
package deploy

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"infra-ops/model"
)

// mergeParams 合并变量：模板默认值 < 任务级默认 < 主机级覆盖。
func mergeParams(rawVars json.RawMessage, taskParams, hostParams map[string]string) (map[string]string, error) {
	vars, err := parseVariables(rawVars)
	if err != nil {
		return nil, err
	}
	merged := make(map[string]string, len(vars))
	for _, v := range vars {
		if v.Default != "" {
			merged[v.Name] = v.Default
		}
	}
	for k, val := range taskParams {
		merged[k] = val
	}
	for k, val := range hostParams {
		merged[k] = val
	}
	return merged, nil
}

// parseConfigs 解析模板 configs 声明。
func parseConfigs(raw json.RawMessage) ([]model.TemplateConfig, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var cfgs []model.TemplateConfig
	if err := json.Unmarshal(raw, &cfgs); err != nil {
		return nil, err
	}
	return cfgs, nil
}

// mergeConfigs 合并自定义配置：任务级 < 主机级覆盖。模板声明 required 且结果为空时报错。
func mergeConfigs(rawConfigs json.RawMessage, taskConfigs, hostConfigs map[string]string) (map[string]string, error) {
	cfgs, err := parseConfigs(rawConfigs)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(cfgs))
	for _, c := range cfgs {
		if c.Key == "" {
			continue
		}
		v, ok := hostConfigs[c.Key]
		if !ok {
			v, ok = taskConfigs[c.Key]
		}
		if !ok || strings.TrimSpace(v) == "" {
			if c.Required {
				return nil, fmt.Errorf("缺少必填自定义配置: %s(%s)", c.Label, c.Key)
			}
			continue
		}
		out[c.Key] = v
	}
	return out, nil
}

// applyConfigOverrides 渲染期覆盖默认配置文件：对提供了非空自定义内容的 config，
// 用 base64 解码安全写入其目标文件。脚本中须用锚点
// # __DEPLOY_CONF__ <key> … # __DEPLOY_CONF_END__ <key> 包裹默认写入块；无锚点则忽略并告警。
func applyConfigOverrides(script string, tpl *model.DeployTemplate, merged map[string]string) string {
	cfgs, err := parseConfigs(tpl.Configs)
	if err != nil || len(cfgs) == 0 {
		return script
	}
	out := script
	for _, c := range cfgs {
		if c.Key == "" {
			continue
		}
		content, ok := merged["__cfg."+c.Key]
		if !ok || strings.TrimSpace(content) == "" {
			continue // 未提供 → 保持脚本默认
		}
		beginTag := "# __DEPLOY_CONF__ " + c.Key
		endTag := "# __DEPLOY_CONF_END__ " + c.Key
		b := strings.Index(out, beginTag)
		if b < 0 {
			log.Printf("deploy: 模板 config %s(%s) 未在脚本中找到锚点，自定义配置已忽略", c.Key, c.Label)
			continue
		}
		e := strings.Index(out[b+len(beginTag):], endTag)
		if e < 0 {
			continue
		}
		ePos := b + len(beginTag) + e + len(endTag)
		file := renderConfigFile(c.File, merged)
		b64 := base64.StdEncoding.EncodeToString([]byte(content))
		quoted := strings.ReplaceAll(file, "'", "'\\''")
		injected := beginTag + "\n" +
			"# 用户自定义配置覆盖：" + c.Label + "\n" +
			"printf '%s\\n' \"$(printf '%s' '" + b64 + "' | base64 -d)\" > '" + quoted + "'\n" +
			endTag + "\n"
		out = out[:b] + injected + out[ePos:]
	}
	return out
}

// renderConfigFile 渲染 config.file 中的 {{var}} 占位（跳过 __cfg.* 保留键）。
func renderConfigFile(f string, merged map[string]string) string {
	for k, v := range merged {
		if strings.HasPrefix(k, "__cfg.") {
			continue
		}
		f = strings.ReplaceAll(f, "{{"+k+"}}", v)
	}
	return f
}

// DedupInt64 去除 int64 切片中的重复元素并保持顺序。
func DedupInt64(in []int64) []int64 {
	seen := map[int64]bool{}
	out := make([]int64, 0, len(in))
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
