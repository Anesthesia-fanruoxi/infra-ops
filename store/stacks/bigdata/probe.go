package bigdata

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"infra-ops/store/stackkit"
)

// probe.go：大数据底座探活（期望容器 / 实际运行清单 / 容器分组检查）。
// 原 api/stack 的 stack_verify_script.go（bigdata 分支）与 stack_verify_parse.go（bigdata 分支）
// 原样迁入，检查项顺序与文案逐字节一致。

// Containers 按组件与角色规划推断本机应存在的容器名（与 node.sh/ha.sh 对齐）。
func (d *Driver) Containers(ctx stackkit.ProbeCtx) []string {
	comps := ParseComponentsCSV(ctx.Params["components"])
	hostIP, role := ctx.HostIP, ctx.Role
	masters := map[string]string{}
	_ = json.Unmarshal([]byte(ctx.Params["masters"]), &masters)
	ha := strings.EqualFold(strings.TrimSpace(ctx.Params["ha"]), "true")
	isPrimary := func(comp string) bool {
		if ip := strings.TrimSpace(masters[comp]); ip != "" {
			return ip == hostIP
		}
		return role == "master"
	}
	// inList 判断 hostIP 是否落在逗号列表角色（如 hdfs_nn2 单值 / hdfs_jns 多值）
	inList := func(key string) bool {
		for _, p := range strings.Split(strings.TrimSpace(masters[key]), ",") {
			if strings.TrimSpace(p) == hostIP {
				return true
			}
		}
		return false
	}
	has := func(k string) bool { return containsString(comps, k) }
	var out []string
	if has("zookeeper") {
		out = append(out, "bigdata-zookeeper")
	}
	// HA 模式：严格对齐 ha.sh / node.sh 的落点。要点：
	//   1) JournalNode 部署在 JNS 全部主机（含 NN1/NN2），见 ha.sh「JNS 内全部主机（含 NN1/NN2）都部署」；
	//   2) DataNode 全节点部署（deploy_hdfs_ha_dn 不按角色排除），NN1/NN2 主机同样跑 DataNode；
	//   3) 备主/备实例与主实例可同主机并存（如 NN2 主机同时是 DataNode + JournalNode），
	//      所以这里是「按角色逐条追加」，不是非主即工作的二选一。
	if ha {
		if has("hdfs") {
			if isPrimary("hdfs") {
				out = append(out, "hadoop-namenode", "hadoop-zkfc")
			}
			if inList("hdfs_nn2") {
				out = append(out, "hadoop-namenode2", "hadoop-zkfc2")
			}
			if inList("hdfs_jns") {
				out = append(out, "hadoop-journalnode")
			}
			out = append(out, "hadoop-datanode")
		}
		if has("yarn") {
			switch {
			case isPrimary("yarn"):
				out = append(out, "hadoop-resourcemanager")
			case inList("yarn_rm2"):
				out = append(out, "hadoop-resourcemanager2")
			default:
				out = append(out, "hadoop-nodemanager")
			}
		}
		if has("spark") {
			switch {
			case isPrimary("spark"):
				out = append(out, "spark-master")
			case inList("spark_m2"):
				out = append(out, "spark-master2")
			default:
				out = append(out, "spark-worker")
			}
		}
		if has("flink") {
			switch {
			case isPrimary("flink"):
				out = append(out, "flink-jobmanager")
			case inList("flink_jm2"):
				out = append(out, "flink-jobmanager2")
			default:
				out = append(out, "flink-taskmanager")
			}
		}
		if has("hbase") {
			switch {
			case isPrimary("hbase"):
				out = append(out, "hbase-master")
			case inList("hbase_hm2"):
				out = append(out, "hbase-backup-master")
			default:
				out = append(out, "hbase-regionserver")
			}
		}
		if has("hive") {
			if isPrimary("hive") {
				out = append(out, "hive-metastore", "hive-hiveserver2")
			}
			if inList("hive_ms2") {
				out = append(out, "hive-metastore2")
			}
			if inList("hive_hs2b") {
				out = append(out, "hive-hiveserver2-2")
			}
			if inList("hive_db") {
				out = append(out, "hive-metastore-db")
			}
		}
		if has("trino") {
			out = append(out, "trino-node")
		}
		return out
	}
	if has("hdfs") {
		if isPrimary("hdfs") {
			out = append(out, "hadoop-namenode")
		} else {
			out = append(out, "hadoop-datanode")
		}
	}
	if has("yarn") {
		if isPrimary("yarn") {
			out = append(out, "hadoop-resourcemanager")
		} else {
			out = append(out, "hadoop-nodemanager")
		}
	}
	if has("spark") {
		if isPrimary("spark") {
			out = append(out, "spark-master")
		} else {
			out = append(out, "spark-worker")
		}
	}
	if has("flink") {
		if isPrimary("flink") {
			out = append(out, "flink-jobmanager")
		} else {
			out = append(out, "flink-taskmanager")
		}
	}
	if has("hive") && isPrimary("hive") {
		out = append(out, "hive-metastore", "hive-hiveserver2")
	}
	if has("hbase") {
		if isPrimary("hbase") {
			out = append(out, "hbase-master")
		} else {
			out = append(out, "hbase-regionserver")
		}
	}
	if has("trino") {
		out = append(out, "trino-node")
	}
	return out
}

// ScriptTail 实际运行的大数据容器清单：用于如实回显本机实例清单
// （期望集之外的容器只做提示，不算失败）。
func (d *Driver) ScriptTail(stackkit.ProbeCtx) string {
	return `echo __IO_LIVE_BEGIN__
docker ps --format '{{.Names}}' 2>/dev/null | grep -E '^(bigdata-|hadoop-|spark-|flink-|hive-|hbase-|trino-)' | sort | tr '\n' ','
echo
echo __IO_LIVE_END__
`
}

// ParseSuite 解析本机应有的大数据容器；全部 running 才算通过。
//
// containers 为空表示「无期望容器」（未勾选组件 / 组件与本机角色无关）：
// 此时与拆分前一致地交给引擎按 compose 状态兜底，本方法不追加任何检查项。
func (d *Driver) ParseSuite(ctx stackkit.ProbeCtx, raw string, containers []string) stackkit.ProbeOutcome {
	var out stackkit.ProbeOutcome
	if len(containers) == 0 {
		return out
	}
	var running []string
	var image, started string
	allOK := true
	for _, name := range containers {
		st := stackkit.ExtractLine(raw, "__IO_CTR__"+name+"__=")
		ok, detail, img, start := stackkit.ParseCtrStateEx(name, st)
		comp := containerComponent(name)
		if img != "" && image == "" {
			image = img
		}
		if start != "" && started == "" {
			started = start
		}
		if !ok {
			allOK = false
			out.Checks = append(out.Checks, stackkit.Check{Name: "容器 " + name, Component: comp, OK: false, Detail: detail})
			continue
		}
		running = append(running, name)
		out.Checks = append(out.Checks, stackkit.Check{Name: "容器 " + name, Component: comp, OK: true, Detail: detail})
	}
	instDetail := strings.Join(running, ", ")
	if instDetail == "" {
		instDetail = "无运行中实例"
	}
	ok := allOK && len(running) > 0
	out.Checks = append(out.Checks, stackkit.Check{Name: "实例", OK: ok, Detail: instDetail})
	// 如实回显本机实际运行的大数据容器；仅当出现「未预期的额外容器」时在明细里点出（不作为失败）
	if live := parseLiveContainers(raw); len(live) > 0 {
		extra := make([]string, 0, len(live))
		for _, n := range live {
			if !containsString(containers, n) {
				extra = append(extra, n)
			}
		}
		detail := strings.Join(live, ", ")
		if len(extra) > 0 {
			detail += "（未预期: " + strings.Join(extra, ", ") + "）"
		}
		out.Checks = append(out.Checks, stackkit.Check{Name: "实际运行", OK: true, Detail: detail})
	}
	verDetail := imageShort(image)
	if started != "" {
		if verDetail != "" {
			verDetail += " · "
		}
		verDetail += formatDockerStarted(started)
	}
	if verDetail != "" {
		out.Checks = append(out.Checks, stackkit.Check{Name: "版本", OK: true, Detail: verDetail})
	}
	out.OK = &ok
	return out
}

// Summary 大数据底座探活说明（无客户端命令）。
func (d *Driver) Summary(stackkit.ProbeCtx, []stackkit.ProbeHostRow) ([]string, string) {
	return []string{"按各组件 compose / 容器运行状态探活（HDFS、ZK、Spark 等分目录部署）。"}, ""
}

// parseLiveContainers 解析本机实际运行的大数据容器（逗号分隔）。
func parseLiveContainers(raw string) []string {
	blk := strings.TrimSpace(stackkit.ExtractBlock(raw, "__IO_LIVE_BEGIN__", "__IO_LIVE_END__"))
	if blk == "" {
		return nil
	}
	out := []string{}
	for _, s := range strings.Split(blk, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func imageShort(image string) string {
	if image == "" {
		return ""
	}
	if i := strings.LastIndex(image, "/"); i >= 0 {
		return image[i+1:]
	}
	return image
}

func formatDockerStarted(iso string) string {
	iso = strings.TrimSpace(iso)
	if iso == "" || iso == "0001-01-01T00:00:00Z" {
		return "-"
	}
	t, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		t, err = time.Parse(time.RFC3339, iso)
	}
	if err != nil {
		if len(iso) >= 19 {
			return strings.ReplaceAll(iso[:19], "T", " ")
		}
		return iso
	}
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	days := int(d.Hours() / 24)
	if days > 0 {
		return fmt.Sprintf("已运行 %d 天", days)
	}
	hours := int(d.Hours())
	if hours > 0 {
		return fmt.Sprintf("已运行 %d 小时", hours)
	}
	return fmt.Sprintf("已运行 %d 分钟", int(d.Minutes()))
}

// containerComponent 由容器名推断所属组件（与 node.sh/ha.sh 命名对齐）。
func containerComponent(name string) string {
	switch {
	case name == "bigdata-zookeeper":
		return "zookeeper"
	case strings.HasPrefix(name, "hadoop-namenode"),
		strings.HasPrefix(name, "hadoop-journalnode"),
		strings.HasPrefix(name, "hadoop-datanode"),
		strings.HasPrefix(name, "hadoop-zkfc"),
		strings.HasPrefix(name, "hadoop-resourcemanager"),
		name == "hadoop-nodemanager":
		// hadoop-* 需区分 hdfs 与 yarn：rm 走 yarn，其余按 hdfs
		if name == "hadoop-nodemanager" || name == "hadoop-resourcemanager" || name == "hadoop-resourcemanager2" {
			return "yarn"
		}
		return "hdfs"
	case strings.HasPrefix(name, "spark-"):
		return "spark"
	case strings.HasPrefix(name, "flink-"):
		return "flink"
	case strings.HasPrefix(name, "hive-"):
		return "hive"
	case strings.HasPrefix(name, "hbase-"):
		return "hbase"
	case name == "trino-node" || strings.HasPrefix(name, "trino-"):
		return "trino"
	}
	return ""
}
