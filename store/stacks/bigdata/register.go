package bigdata

import (
	"encoding/json"
	"strings"

	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// register.go：大数据底座服务登记（原 api/stack/stack_register_bigdata.go 原样迁入）。

// RegisterServices 按勾选组件 + 主/从角色登记服务入口。
// ctx.Hosts 为本次运行的全部主机（确定性从角色判定）；角色矩阵未指定时从角色自动落点。
func (d *Driver) RegisterServices(ctx stackkit.RegisterCtx) {
	host, params := ctx.Host, ctx.Params
	comps := ParseComponentsCSV(params["components"])
	r := ResolveRole(ctx.Hosts)
	ip := host.HostIP
	// 角色矩阵：组件→主角色主机 IP；未规划时主角色跟随主节点（Role=="master"）
	masters := map[string]string{}
	_ = json.Unmarshal([]byte(params["masters"]), &masters)
	compMaster := func(comp string) string {
		if m := strings.TrimSpace(masters[comp]); m != "" {
			return m
		}
		if r.Nn1 != "" { // primary 首落点即可
			switch comp {
			case "hdfs":
				return r.Nn1
			case "yarn":
				return r.Rm1
			case "spark":
				return r.SparkM1
			case "flink":
				return r.FlinkJM1
			case "hbase":
				return r.HMaster1
			case "hive":
				if len(r.MSs) > 0 {
					return r.MSs[0]
				}
			}
		}
		return ""
	}
	up := func(name, url string, web bool) {
		ctx.Emit(model.HostService{
			HostID: host.HostID, HostIP: host.HostIP, ServiceName: name,
			URL: url, Web: web, TemplateID: 0, InstanceID: ctx.InstanceID,
		})
	}
	hasJN := func() bool {
		for _, jn := range r.JNs {
			if jn == ip {
				return true
			}
		}
		return false
	}
	isMasterAt := func(masterIP string) bool { return masterIP != "" && ip == masterIP }
	inComps := func(c string) bool { return containsString(comps, c) }
	for _, c := range comps {
		switch strings.TrimSpace(c) {
		case "hdfs":
			switch {
			case !r.Ha:
				if isMasterAt(compMaster("hdfs")) {
					up("HDFS NameNode", "http://"+ip+":9870", true)
				} else {
					up("HDFS DataNode", "http://"+ip+":9864", false)
				}
			case ip == r.Nn1:
				up("HDFS NameNode", "http://"+ip+":9870", true)
			case ip == r.Nn2:
				up("HDFS NameNode (Standby)", "http://"+ip+":9870", true)
			case hasJN():
				up("HDFS JournalNode", "http://"+ip+":8484", true)
			default:
				up("HDFS DataNode", "http://"+ip+":9864", false)
			}
		case "spark":
			webui := params["webui_port"]
			if webui == "" {
				webui = "8080"
			}
			if r.Ha && ip == r.SparkM2 {
				up("Spark Master (Standby)", "http://"+ip+":"+webui, true)
			} else if isMasterAt(compMaster("spark")) {
				up("Spark Master", "http://"+ip+":"+webui, true)
			} else {
				up("Spark Worker", "http://"+ip+":8081", true)
			}
		case "flink":
			if r.Ha && ip == r.FlinkJm2 {
				up("Flink JobManager (Standby)", "http://"+ip+":8081", true)
			} else if isMasterAt(compMaster("flink")) {
				up("Flink JobManager", "http://"+ip+":8081", true)
			} else {
				up("Flink TaskManager", "http://"+ip+":8081", false)
			}
		case "hive":
			if !inComps("metastore_db") || !r.Ha {
				if isMasterAt(compMaster("hive")) {
					up("HiveServer2", "jdbc:hive2://"+ip+":10000", false)
					up("Hive Metastore", "thrift://"+ip+":9083", false)
					up("Hive WebUI", "http://"+ip+":10002", true)
				}
				continue
			}
			if len(r.Hs2s) > 1 && ip == r.Hs2s[1] {
				up("HiveServer2 (Standby)", "jdbc:hive2://"+ip+":10000", false)
			} else if len(r.Hs2s) > 0 && ip == r.Hs2s[0] {
				up("HiveServer2", "jdbc:hive2://"+ip+":10000", false)
			}
			if len(r.MSs) > 1 && ip == r.MSs[1] {
				up("Hive Metastore (Standby)", "thrift://"+ip+":9083", false)
			} else if len(r.MSs) > 0 && ip == r.MSs[0] {
				up("Hive Metastore", "thrift://"+ip+":9083", false)
			}
			if ip == r.HiveDB {
				up("Hive MetaDB (MySQL)", "mysql://"+ip+":3306", false)
			}
		case "zookeeper":
			up("ZooKeeper", "zookeeper://"+ip+":2181", false)
		case "yarn":
			if r.Ha && ip == r.Rm2 {
				up("YARN ResourceManager (Standby)", "http://"+ip+":8088", true)
			} else if isMasterAt(compMaster("yarn")) {
				up("YARN ResourceManager", "http://"+ip+":8088", true)
			} else {
				up("YARN NodeManager", "http://"+ip+":8042", true)
			}
		case "hbase":
			if r.Ha && ip == r.HMaster2 {
				up("HBase HMaster (backup)", "http://"+ip+":16010", true)
			} else if isMasterAt(compMaster("hbase")) {
				up("HBase Master", "http://"+ip+":16010", true)
			} else {
				up("HBase RegionServer", "http://"+ip+":16030", true)
			}
		case "trino":
			port := params["trino_http_port"]
			if port == "" {
				port = "8080"
			}
			if isMasterAt(compMaster("trino")) {
				up("Trino Coordinator", "http://"+ip+":"+port, true)
			} else {
				up("Trino Worker", "http://"+ip+":"+port, true)
			}
		}
	}
	ctx.MarkInstall("套件:bigdata/cluster(" + params["components"] + ")")
}
