package stack

import (
	"fmt"
	"strings"
)

// stackComposeDownScript 生成停服/清理 compose 目录的 bash 脚本。
func stackComposeDownScript(home string, comps []string, purge bool) string {
	dirs := []string{}
	if len(comps) == 0 {
		dirs = []string{"", "hadoop", "zookeeper", "yarn", "spark", "flink", "hive", "hbase", "trino"}
	} else {
		for _, c := range comps {
			switch c {
			case "hdfs":
				dirs = append(dirs, "hadoop")
			default:
				dirs = append(dirs, c)
			}
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "HOME_DIR=%q\n", home)
	if purge {
		// 清理残留模式：down -v 连同匿名卷/孤儿容器一并移除，并删除 bind-mount 的数据/配置目录
		b.WriteString("down_one() { if [ -f \"$1\" ]; then docker compose -f \"$1\" down -v --remove-orphans || docker compose -f \"$1\" down || true; echo \"已清理 $1\"; fi; }\n")
	} else {
		b.WriteString("down_one() { if [ -f \"$1\" ]; then docker compose -f \"$1\" down || true; echo \"已停止 $1\"; fi; }\n")
	}
	for _, d := range dirs {
		if d == "" {
			b.WriteString("down_one \"${HOME_DIR}/compose.yml\"\n")
			continue
		}
		fmt.Fprintf(&b, "down_one \"${HOME_DIR}/%s/compose.yml\"\n", d)
	}
	if purge {
		// 目录仅限 HOME_DIR 之下，逐个组件目录删除（metadb 嵌套在 hive 下，随 hive 一起删除）
		b.WriteString("rm -rf")
		for _, d := range dirs {
			if d == "" {
				continue
			}
			fmt.Fprintf(&b, " \"${HOME_DIR}/%s\"", d)
		}
		b.WriteString(" \"${HOME_DIR}/compose.yml\"\n")
		b.WriteString("echo \"[Purge] 残留数据/配置目录已删除\"\n")
	}
	return b.String()
}
