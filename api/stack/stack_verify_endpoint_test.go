package stack

import (
	"testing"

	"infra-ops/api/shared"
	"infra-ops/store/stackkit"
	"infra-ops/store/stacks/bigdata"
)

// 接入端点（EndpointProvider）的 HA 落点校验：41 的入口不应再被标成 YARN NM / Spark Worker / HBase RS。
func TestBigdataVerifyEndpoints_HASecondaryHost(t *testing.T) {
	inst, instParams := bigdataHAFixture()
	merged := mergeParamMaps(instParams, parseJSONMap(inst.Hosts[1].ParamsJSON))
	overrideBigdataMasters(inst, merged)
	comps := parseComponentsCSV(merged["components"])

	eps := bigdata.New().Endpoints(stackkit.EndpointCtx{
		Mode: inst.Mode, Host: inst.Hosts[1], Params: merged, Components: comps,
	})
	names := make([]string, 0, len(eps))
	for _, e := range eps {
		names = append(names, e.Name)
	}
	t.Logf("41 入口 = %v", names)
	for _, want := range []string{"HDFS NameNode-2", "YARN RM-2", "Spark Master-2", "Flink JM-2", "HBase Backup Master", "Hive Metastore-2", "HiveServer2-2"} {
		if !shared.ContainsString(names, want) {
			t.Errorf("41 入口应包含 %s，实际 %v", want, names)
		}
	}
	for _, bad := range []string{"YARN NM", "Spark Worker", "HBase RS"} {
		if shared.ContainsString(names, bad) {
			t.Errorf("41 入口不应包含 %s，实际 %v", bad, names)
		}
	}
}
