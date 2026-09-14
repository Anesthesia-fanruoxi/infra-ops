package stack

import (
	"encoding/json"
	"strings"
	"testing"

	"infra-ops/model"
	"infra-ops/store"
)

func bigdataPhaseKeys(t *testing.T, op, paramsJSON string) []string {
	t.Helper()
	bp := store.FindBuiltinStack("bigdata")
	if bp == nil {
		t.Fatal("未找到 bigdata 蓝图")
	}
	run := &model.StackRun{StackKey: "bigdata", Mode: "cluster", Op: op, ParamsJSON: paramsJSON}
	phases := selectPipelinePhases(bp, run, op)
	out := make([]string, 0, len(phases))
	for _, p := range phases {
		out = append(out, p.Key)
	}
	return out
}

func joinKeys(keys []string) string { return strings.Join(keys, ",") }

// 整机部署必须跑全量阶段（含 reset 与 verify）。
func TestSelectPipeline_ReinstallRunsAll(t *testing.T) {
	got := bigdataPhaseKeys(t, "reinstall", `{}`)
	want := "reset,zookeeper,hdfs_boot,hdfs_dn,yarn,spark,flink,hive,hbase,trino,verify"
	if joinKeys(got) != want {
		t.Fatalf("reinstall 阶段不符\n got=%s\nwant=%s", joinKeys(got), want)
	}
}

// 加装 hive：只跑 hive 阶段 + Always 的 verify，绝不能含 reset。
func TestSelectPipeline_AddHive(t *testing.T) {
	p, _ := json.Marshal(map[string]string{"add_components": "hive"})
	got := bigdataPhaseKeys(t, "add_component", string(p))
	want := "hive,verify"
	if joinKeys(got) != want {
		t.Fatalf("加装 hive 阶段不符\n got=%s\nwant=%s", joinKeys(got), want)
	}
	for _, k := range got {
		if k == "reset" {
			t.Fatal("加装组件时执行了 reset —— 会清空既有集群数据")
		}
	}
}

// metastore_db 与 hive 共用 hive 阶段；多选组件时阶段按声明顺序输出且去重。
func TestSelectPipeline_AddHiveAndHbase(t *testing.T) {
	p, _ := json.Marshal(map[string]string{"add_components": "hbase,hive"})
	got := bigdataPhaseKeys(t, "add_component", string(p))
	want := "hive,hbase,verify"
	if joinKeys(got) != want {
		t.Fatalf("加装 hbase+hive 阶段不符\n got=%s\nwant=%s", joinKeys(got), want)
	}

	p2, _ := json.Marshal(map[string]string{"add_components": "metastore_db"})
	got2 := bigdataPhaseKeys(t, "add_component", string(p2))
	if joinKeys(got2) != "hive,verify" {
		t.Fatalf("单独加装 metastore_db 应复用 hive 阶段，got=%s", joinKeys(got2))
	}
}

// 扩容：跑组件阶段，跳过 reset（FullOnly）与 verify（leader 收尾需在元老主节点执行）。
func TestSelectPipeline_ScaleOut(t *testing.T) {
	got := bigdataPhaseKeys(t, "scale_out", `{}`)
	want := "zookeeper,hdfs_boot,hdfs_dn,yarn,spark,flink,hive,hbase,trino"
	if joinKeys(got) != want {
		t.Fatalf("扩容阶段不符\n got=%s\nwant=%s", joinKeys(got), want)
	}
}

// 未记录新增组件时返回空，调用方据此显式失败，避免回落到 node 阶段造成"假成功"。
func TestSelectPipeline_AddWithoutComponents(t *testing.T) {
	if got := bigdataPhaseKeys(t, "add_component", `{}`); len(got) != 0 {
		t.Fatalf("缺少 add_components 时应返回空，got=%v", got)
	}
}

// 无流水线的套件不做阶段裁剪。
func TestSelectPipeline_NoPipelineStack(t *testing.T) {
	bp := store.FindBuiltinStack("redis")
	if bp == nil {
		t.Skip("未找到 redis 蓝图")
	}
	run := &model.StackRun{StackKey: "redis", Mode: "cluster", Op: "create"}
	if got := selectPipelinePhases(bp, run, "create"); len(got) != 0 {
		t.Fatalf("无流水线套件应返回空，got=%v", got)
	}
}

// 渲染 node.sh，确认新增的 mapred-site 资产与 Trino Hive 连接器参数真的被注入（防止占位符拼错/资产漏注册）。
func TestBigdataNodeScriptAssets(t *testing.T) {
	bp := store.FindBuiltinStack("bigdata")
	if bp == nil {
		t.Fatal("未找到 bigdata 蓝图")
	}
	script, err := store.LoadStackPhase(bp, "cluster", "node")
	if err != nil {
		t.Fatalf("加载 node 脚本失败: %v", err)
	}
	wants := []string{
		// mapred-site.xml 资产内容（Hive 默认 MR 引擎，缺则 INSERT 报 No valid local directories）
		"mapreduce.framework.name",
		"mapreduce.cluster.local.dir",
		"hadoop/conf/mapred-site.xml",
		// Trino Hive 连接器：Hadoop 配置 + 放开非托管表写入/删除
		"hive.config.resources=/etc/trino/core-site.xml,/etc/trino/hdfs-site.xml",
		"hive.non-managed-table-writes-enabled=true",
		"hive.allow-drop-table=true",
		// Trino node.id 不能含点
		"trino-${SELF_IP//./-}",
	}
	for _, w := range wants {
		if !strings.Contains(script, w) {
			t.Errorf("渲染后的 node.sh 缺少片段: %s", w)
		}
	}
	for _, ph := range []string{"@@MAPRED_SITE@@", "@@HA_SH@@", "@@CORE_SITE@@"} {
		if strings.Contains(script, ph) {
			t.Errorf("占位符未替换: %s", ph)
		}
	}
}
