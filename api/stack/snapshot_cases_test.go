package stack

// 任务 09（套件目录化拆分）B1 行为快照用例表。
// 每个用例固定「套件 + 模式 + 成员主机 + 参数」，产出确定性的角色计划 / 登记服务 / 校验端点。
// 与 snapshot_test.go（骨架与比对逻辑）同包，便于直接调用内部函数。

import "infra-ops/model"

var snapCases = []snapCase{
	// ---------- redis ----------
	{
		Name: "redis_replication", Stack: "redis", Mode: "replication", ManualMaster: true,
		Hosts: []model.StackRunHost{
			snHost(1, "10.0.0.1", "master", 1, ""),
			snHost(2, "10.0.0.2", "worker", 2, ""),
			snHost(3, "10.0.0.3", "worker", 3, ""),
		},
	},
	{
		Name: "redis_sentinel", Stack: "redis", Mode: "sentinel", ManualMaster: true,
		Hosts: []model.StackRunHost{
			snHost(1, "10.0.0.1", "master", 1, ""),
			snHost(2, "10.0.0.2", "worker", 2, ""),
			snHost(3, "10.0.0.3", "worker", 3, ""),
		},
	},
	{
		Name: "redis_cluster_3", Stack: "redis", Mode: "cluster",
		Params: map[string]string{"replicas": "0"},
		Hosts: []model.StackRunHost{
			snHost(1, "10.0.0.1", "node", 1, ""),
			snHost(2, "10.0.0.2", "node", 2, ""),
			snHost(3, "10.0.0.3", "node", 3, ""),
		},
	},
	{
		Name: "redis_cluster_6", Stack: "redis", Mode: "cluster",
		Params: map[string]string{"replicas": "1"},
		Hosts: []model.StackRunHost{
			snHost(1, "10.0.0.1", "node", 1, ""), snHost(2, "10.0.0.2", "node", 2, ""),
			snHost(3, "10.0.0.3", "node", 3, ""), snHost(4, "10.0.0.4", "node", 4, ""),
			snHost(5, "10.0.0.5", "node", 5, ""), snHost(6, "10.0.0.6", "node", 6, ""),
		},
	},
	{
		Name: "redis_replication_scale_out", Stack: "redis", Mode: "replication", Op: "scale_out", ManualMaster: true,
		Hosts: []model.StackRunHost{
			snHost(1, "10.0.0.1", "master", 1, ""), snHost(2, "10.0.0.2", "worker", 2, ""),
			snHost(3, "10.0.0.3", "worker", 3, ""), snHost(4, "10.0.0.4", "worker", 4, ""),
		},
	},
	{
		// 负例：哨兵低于 3 台 → 报错文案冻结
		Name: "redis_sentinel_min_hosts_err", Stack: "redis", Mode: "sentinel", PlanOnly: true,
		Hosts: []model.StackRunHost{
			snHost(1, "10.0.0.1", "master", 1, ""), snHost(2, "10.0.0.2", "worker", 2, ""),
		},
	},

	// ---------- kafka ----------
	// enable_ui 为布尔开关（Type=bool），真实提交值是 "true"/"false"；
	// 历史实例里的 "yes"/"no" 仍由 stackkit.IsYes 与脚本归一化兼容。
	{
		Name: "kafka_kraft_ui", Stack: "kafka", Mode: "kraft",
		Params: map[string]string{"enable_ui": "true"},
		Hosts: []model.StackRunHost{
			snHost(1, "10.0.1.1", "node", 1, ""), snHost(2, "10.0.1.2", "node", 2, ""), snHost(3, "10.0.1.3", "node", 3, ""),
		},
	},
	{
		Name: "kafka_kraft", Stack: "kafka", Mode: "kraft",
		Params: map[string]string{"enable_ui": "false"},
		Hosts: []model.StackRunHost{
			snHost(1, "10.0.1.1", "node", 1, ""), snHost(2, "10.0.1.2", "node", 2, ""), snHost(3, "10.0.1.3", "node", 3, ""),
		},
	},
	{
		Name: "kafka_zk_ui", Stack: "kafka", Mode: "zk",
		Params: map[string]string{"enable_ui": "true"},
		Hosts: []model.StackRunHost{
			snHost(1, "10.0.1.1", "node", 1, ""), snHost(2, "10.0.1.2", "node", 2, ""), snHost(3, "10.0.1.3", "node", 3, ""),
		},
	},

	// ---------- rabbitmq / rocketmq / nacos / powerjob ----------
	{
		Name: "rabbitmq_cluster", Stack: "rabbitmq", Mode: "cluster", ManualMaster: true,
		Hosts: []model.StackRunHost{
			snHost(1, "10.0.2.1", "master", 1, ""), snHost(2, "10.0.2.2", "worker", 2, ""), snHost(3, "10.0.2.3", "worker", 3, ""),
		},
	},
	{
		Name: "rocketmq_cluster", Stack: "rocketmq", Mode: "cluster", ManualMaster: true,
		Hosts: []model.StackRunHost{
			snHost(1, "10.0.3.1", "master", 1, ""), snHost(2, "10.0.3.2", "worker", 2, ""), snHost(3, "10.0.3.3", "worker", 3, ""),
		},
	},
	{
		Name: "nacos_cluster", Stack: "nacos", Mode: "cluster", ManualMaster: true,
		Hosts: []model.StackRunHost{
			snHost(1, "10.0.4.1", "master", 1, ""), snHost(2, "10.0.4.2", "worker", 2, ""), snHost(3, "10.0.4.3", "worker", 3, ""),
		},
	},
	{
		Name: "nacos_cluster_extdb", Stack: "nacos", Mode: "cluster", ManualMaster: true,
		Params: map[string]string{"db_host": "10.0.9.9"},
		Hosts: []model.StackRunHost{
			snHost(1, "10.0.4.1", "master", 1, ""), snHost(2, "10.0.4.2", "worker", 2, ""), snHost(3, "10.0.4.3", "worker", 3, ""),
		},
	},
	{
		Name: "powerjob_cluster", Stack: "powerjob", Mode: "cluster", ManualMaster: true,
		Hosts: []model.StackRunHost{
			snHost(1, "10.0.5.1", "master", 1, ""), snHost(2, "10.0.5.2", "worker", 2, ""),
		},
	},
	{
		Name: "powerjob_cluster_extdb", Stack: "powerjob", Mode: "cluster", ManualMaster: true,
		Params: map[string]string{"db_host": "10.0.9.9"},
		Hosts: []model.StackRunHost{
			snHost(1, "10.0.5.1", "master", 1, ""), snHost(2, "10.0.5.2", "worker", 2, ""),
		},
	},

	// ---------- elasticsearch ----------
	{
		Name: "elasticsearch_cluster", Stack: "elasticsearch", Mode: "cluster", ManualMaster: true,
		Hosts: []model.StackRunHost{
			snHost(1, "10.0.6.1", "master", 1, ""), snHost(2, "10.0.6.2", "worker", 2, ""), snHost(3, "10.0.6.3", "worker", 3, ""),
		},
	},
	{
		Name: "elasticsearch_cwh", Stack: "elasticsearch", Mode: "cold_warm_hot", ManualMaster: true,
		// 一机多容器：master+协调同机叠加、单数据层、自动模式（空 roles → 自动分配落点）、数据层叠加
		Hosts: []model.StackRunHost{
			snHost(1, "10.0.6.1", "master", 1, esParams("master,coordinator")),
			snHost(2, "10.0.6.2", "worker", 2, esParams("data_hot")),
			snHost(3, "10.0.6.3", "worker", 3, esParams("")),
			snHost(4, "10.0.6.4", "worker", 4, esParams("data_warm,data_cold")),
		},
	},

	// ---------- elfk ----------
	{
		Name: "elfk_standard", Stack: "elfk", Mode: "standard", ManualMaster: true,
		Hosts: []model.StackRunHost{
			snHost(1, "10.0.7.1", "master", 1, ""), snHost(2, "10.0.7.2", "worker", 2, ""),
			snHost(3, "10.0.7.3", "worker", 3, ""), snHost(4, "10.0.7.4", "worker", 4, ""),
			snHost(5, "10.0.7.5", "worker", 5, ""),
		},
	},
	{
		Name: "elfk_logstash_manual", Stack: "elfk", Mode: "standard", ManualMaster: true,
		Params: map[string]string{"logstash_host": "10.0.7.5"},
		Hosts: []model.StackRunHost{
			snHost(1, "10.0.7.1", "master", 1, ""), snHost(2, "10.0.7.2", "worker", 2, ""),
			snHost(3, "10.0.7.3", "worker", 3, ""), snHost(4, "10.0.7.4", "worker", 4, ""),
			snHost(5, "10.0.7.5", "worker", 5, ""),
		},
	},

	// ---------- bigdata ----------
	{
		Name: "bigdata_plain", Stack: "bigdata", Mode: "cluster", Bigdata: true,
		Params: map[string]string{"components": "hdfs,yarn,zookeeper", "ha": "false"},
		Hosts: []model.StackRunHost{
			snHost(1, "10.0.8.1", "master", 1, bdParams("hdfs,yarn,zookeeper", false, "")),
			snHost(2, "10.0.8.2", "worker", 2, bdParams("hdfs,yarn,zookeeper", false, "")),
			snHost(3, "10.0.8.3", "worker", 3, bdParams("hdfs,yarn,zookeeper", false, "")),
		},
	},
	{
		Name: "bigdata_ha", Stack: "bigdata", Mode: "cluster", Bigdata: true,
		Params: map[string]string{
			"components": "hdfs,yarn,zookeeper,spark,flink,hbase,hive,metastore_db,trino",
			"ha":         "true",
		},
		Hosts: []model.StackRunHost{
			snHost(1, "10.0.8.1", "master", 1, bdParams("hdfs,yarn,zookeeper,spark,flink,hbase,hive,metastore_db,trino", true, "")),
			snHost(2, "10.0.8.2", "worker", 2, bdParams("hdfs,yarn,zookeeper,spark,flink,hbase,hive,metastore_db,trino", true, "")),
			snHost(3, "10.0.8.3", "worker", 3, bdParams("hdfs,yarn,zookeeper,spark,flink,hbase,hive,metastore_db,trino", true, "")),
		},
	},
	{
		// 负例：HA 未勾 ZooKeeper → 报错文案冻结
		Name: "bigdata_ha_no_zk_err", Stack: "bigdata", Mode: "cluster", Bigdata: true, PlanOnly: true,
		Params: map[string]string{"components": "hdfs,yarn", "ha": "true"},
		Hosts: []model.StackRunHost{
			snHost(1, "10.0.8.1", "master", 1, bdParams("hdfs,yarn", true, "")),
			snHost(2, "10.0.8.2", "worker", 2, bdParams("hdfs,yarn", true, "")),
			snHost(3, "10.0.8.3", "worker", 3, bdParams("hdfs,yarn", true, "")),
		},
	},
}
