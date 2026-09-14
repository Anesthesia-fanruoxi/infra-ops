package bigdata

import (
	"encoding/json"
	"strings"

	"infra-ops/store/stackkit"
)

// endpoint.go：大数据底座接入端点（原 api/stack/stack_verify_endpoints.go 的 bigdata 分支原样迁入）。

// Endpoints 生成某台主机的访问入口。HA 下同一主机可能同时是「主」与「备」
// （如 NN2 主机同时跑 DataNode、JN），因此按角色矩阵逐条追加，而不是非主即工作节点的二选一。
func (d *Driver) Endpoints(ctx stackkit.EndpointCtx) []stackkit.Endpoint {
	h, params := ctx.Host, ctx.Params
	masters := map[string]string{}
	_ = json.Unmarshal([]byte(params["masters"]), &masters)
	comps := ctx.Components

	roleOf := func(comp string) string {
		if ip := strings.TrimSpace(masters[comp]); ip != "" {
			return ip
		}
		if h.Role == "master" {
			return h.HostIP
		}
		return ""
	}
	isMasterOf := func(comp string) bool { return roleOf(comp) == h.HostIP }
	inList := func(key string) bool {
		for _, p := range strings.Split(strings.TrimSpace(masters[key]), ",") {
			if strings.TrimSpace(p) == h.HostIP {
				return true
			}
		}
		return false
	}
	webui := stackkit.PickParam(params, "webui_port", "8080")
	trinoPort := stackkit.PickParam(params, "trino_http_port", "8083")
	jnRpc := stackkit.PickParam(params, "jn_rpc_port", "8485")
	ha := strings.EqualFold(strings.TrimSpace(params["ha"]), "true")
	var out []stackkit.Endpoint
	add := func(comp, name, url string) {
		out = append(out, stackkit.Endpoint{Name: name, Component: comp, URL: url, Role: h.Role})
	}
	for _, c := range comps {
		switch c {
		case "hdfs":
			if isMasterOf("hdfs") {
				add("hdfs", "HDFS NameNode", "http://"+h.HostIP+":9870")
			}
			if inList("hdfs_nn2") {
				add("hdfs", "HDFS NameNode-2", "http://"+h.HostIP+":9870")
			}
			if inList("hdfs_jns") {
				add("hdfs", "HDFS JournalNode", "http://"+h.HostIP+":"+jnRpc)
			}
			// HA 下 DataNode 全节点部署（含 NN1/NN2 主机）；非 HA 仅工作节点
			if ha || !isMasterOf("hdfs") {
				add("hdfs", "HDFS DataNode", "http://"+h.HostIP+":9864")
			}
		case "zookeeper":
			add("zookeeper", "ZooKeeper", "zookeeper://"+h.HostIP+":2181")
		case "yarn":
			if isMasterOf("yarn") {
				add("yarn", "YARN RM", "http://"+h.HostIP+":8088")
			}
			if inList("yarn_rm2") {
				add("yarn", "YARN RM-2", "http://"+h.HostIP+":8088")
			}
			if !isMasterOf("yarn") && !inList("yarn_rm2") {
				add("yarn", "YARN NM", "http://"+h.HostIP+":8042")
			}
		case "spark":
			if isMasterOf("spark") {
				add("spark", "Spark Master", "http://"+h.HostIP+":"+webui)
			}
			if inList("spark_m2") {
				add("spark", "Spark Master-2", "http://"+h.HostIP+":8081")
			}
			if !isMasterOf("spark") && !inList("spark_m2") {
				add("spark", "Spark Worker", "http://"+h.HostIP+":8081")
			}
		case "flink":
			if isMasterOf("flink") {
				add("flink", "Flink JM", "http://"+h.HostIP+":8081")
			}
			if inList("flink_jm2") {
				add("flink", "Flink JM-2", "http://"+h.HostIP+":8081")
			}
		case "hive":
			if isMasterOf("hive") {
				add("hive", "Hive Metastore", "thrift://"+h.HostIP+":9083")
				add("hive", "HiveServer2", "jdbc:hive2://"+h.HostIP+":10000")
			}
			if inList("hive_ms2") {
				add("hive", "Hive Metastore-2", "thrift://"+h.HostIP+":9083")
			}
			if inList("hive_hs2b") {
				add("hive", "HiveServer2-2", "jdbc:hive2://"+h.HostIP+":10000")
			}
		case "hbase":
			if isMasterOf("hbase") {
				add("hbase", "HBase Master", "http://"+h.HostIP+":16010")
			}
			if inList("hbase_hm2") {
				add("hbase", "HBase Backup Master", "http://"+h.HostIP+":16010")
			}
			if !isMasterOf("hbase") && !inList("hbase_hm2") {
				add("hbase", "HBase RS", "http://"+h.HostIP+":16030")
			}
		case "trino":
			if isMasterOf("trino") {
				add("trino", "Trino Coord", "http://"+h.HostIP+":"+trinoPort)
			} else {
				add("trino", "Trino Worker", "http://"+h.HostIP+":"+trinoPort)
			}
		}
	}
	return out
}
