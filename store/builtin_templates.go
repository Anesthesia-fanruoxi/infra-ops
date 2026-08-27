// 内置部署模板元数据与加载。
// 脚本统一存于 builtin/*.sh（经 go:embed 嵌入），避免在 Go 字符串字面量中维护 shell，
// 彻底规避转义/缩进污染；非 shell 资源（如 nginx.conf）经占位符 @@KEY@@ 注入脚本。
package store

import (
	"database/sql"
	"embed"
	"errors"
	"strings"
)

//go:embed builtin
var builtinFS embed.FS

type builtinTemplate struct {
	name        string
	description string
	variables   string            // JSON: [{name,label,default,required}]
	path        string            // builtinFS 内相对脚本路径
	assets      map[string]string // 资源路径 -> 注入脚本占位符 @@KEY@@ 的内容
}

var builtinTemplates = []builtinTemplate{
	{
		name:        "新增 Linux 用户",
		description: "创建带家目录和 bash 的普通用户，已存在则跳过。",
		variables:   `[{"name":"username","label":"用户名","default":"","required":true}]`,
		path:        "builtin/add-linux-user.sh",
	},
	{
		name:        "修改主机名",
		description: "为当前主机设置新主机名：在第三步「自定义变量」中为每台主机分别填写各自的新名字（留空会校验失败）。持久化 hostname、更新 /etc/hosts，并自动同步平台台账中的主机名。",
		variables:   `[{"name":"new_name","label":"新主机名","default":"","required":true}]`,
		path:        "builtin/set-hostname.sh",
	},
	{
		name:        "安装 Docker",
		description: "适配 Rocky/CentOS 9/10：阿里云源安装最新版 Docker CE（含 buildx/compose 插件），写入 daemon.json（镜像加速/日志轮转/数据目录）。已安装则跳过。",
		variables:   `[]`,
		path:        "builtin/install-docker.sh",
	},
	{
		name:        "安装 OpenResty",
		description: "OpenResty 官方源安装最新版（Rocky/EL 8/9；Rocky 10 自动回退 el9 源），并落盘生产级 nginx.conf 主配置（含 cert / conf.d 等目录自动创建、语法校验与 reload）。已安装则跳过安装、仅重写配置。",
		variables:   `[]`,
		path:        "builtin/install-openresty.sh",
		assets:      map[string]string{"NGINX_CONF": "builtin/openresty-nginx.conf"},
	},
	{
		name:        "内核网络参数调优",
		description: "开启 BBR 拥塞控制并批量应用生产级内核/网络参数：覆盖 TCP 连接生命周期、连接队列与收发缓冲、TCP 特性、文件句柄/inotify/内存，配置与每项作用注释写入 /etc/sysctl.d/99-tuning.conf，逐条容错应用。",
		variables:   `[]`,
		path:        "builtin/kernel-tuning.sh",
	},
}

// loadBuiltinScript 读取模板脚本并注入其引用的资源占位符。
func loadBuiltinScript(t builtinTemplate) (string, error) {
	b, err := builtinFS.ReadFile(t.path)
	if err != nil {
		return "", err
	}
	script := string(b)
	if script == "" {
		return "", errors.New("empty builtin script: " + t.path)
	}
	for key, asset := range t.assets {
		c, err := builtinFS.ReadFile(asset)
		if err != nil {
			return "", err
		}
		script = strings.ReplaceAll(script, "@@"+key+"@@", string(c))
	}
	return script, nil
}

// seedBuiltinTemplates 预置内置模板（幂等，按 name 去重）。
// 内置模板随版本演进：脚本有变化时原地刷新（内置模板 UI 只读，不存在覆盖用户修改的问题）；
// 已不在当前内置列表中的历史内置模板会被清理。
func seedBuiltinTemplates(db *sql.DB) error {
	scripts := make(map[string]string, len(builtinTemplates))
	for _, t := range builtinTemplates {
		s, err := loadBuiltinScript(t)
		if err != nil {
			return err
		}
		scripts[t.name] = s
	}

	// 清理历史版本遗留的内置模板。
	// 已被部署任务/定时任务引用的模板受外键约束不可硬删，降级为普通模板保留（历史可追溯）；
	// 无引用的直接删除。
	names := make([]string, len(builtinTemplates))
	args := make([]interface{}, len(builtinTemplates))
	for i, t := range builtinTemplates {
		names[i] = t.name
		args[i] = t.name
	}
	if _, err := db.Exec(
		`UPDATE deploy_templates SET is_builtin=0
		WHERE is_builtin=1 AND name NOT IN (`+strings.Repeat("?,", len(args)-1)+`?)
		AND (EXISTS(SELECT 1 FROM deploy_tasks t WHERE t.template_id=deploy_templates.id)
		  OR EXISTS(SELECT 1 FROM deploy_schedules s WHERE s.template_id=deploy_templates.id))`, args...,
	); err != nil {
		return err
	}
	if _, err := db.Exec(
		`DELETE FROM deploy_templates WHERE is_builtin=1 AND name NOT IN (`+strings.Repeat("?,", len(args)-1)+"?)", args...,
	); err != nil {
		return err
	}

	for _, t := range builtinTemplates {
		var exists int
		if err := db.QueryRow(`SELECT COUNT(*) FROM deploy_templates WHERE name=?`, t.name).Scan(&exists); err != nil {
			return err
		}
		script := scripts[t.name]
		if exists > 0 {
			var curScript, curVars string
			if err := db.QueryRow(`SELECT script, variables FROM deploy_templates WHERE name=? AND is_builtin=1`, t.name).Scan(&curScript, &curVars); err == nil && (curScript != script || curVars != t.variables) {
				if _, err := db.Exec(
					`UPDATE deploy_templates SET description=?, script=?, variables=?, updated_at=datetime('now','localtime') WHERE name=? AND is_builtin=1`,
					t.description, script, t.variables, t.name,
				); err != nil {
					return err
				}
			}
			continue
		}
		_, err := db.Exec(
			`INSERT INTO deploy_templates(name, description, script, variables, is_builtin) VALUES(?,?,?,?,1)`,
			t.name, t.description, script, t.variables,
		)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
	}
	return nil
}
