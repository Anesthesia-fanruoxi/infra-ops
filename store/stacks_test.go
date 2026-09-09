package store

import (
	"strings"
	"testing"
)

func TestRedisStackScriptsEmbed(t *testing.T) {
	s := FindBuiltinStack("redis")
	if s == nil {
		t.Fatal("missing redis stack")
	}
	repl, err := s.LoadPhase("replication", "node")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(repl, "@@REDIS_MASTER_CONF@@") || strings.Contains(repl, "@@REDIS_REPLICA_CONF@@") {
		t.Fatal("conf assets not injected")
	}
	if !strings.Contains(repl, "replicaof {{__master_ip}}") {
		t.Fatal("replica conf missing")
	}
	node, err := s.LoadPhase("cluster", "node")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(node, "@@REDIS_CLUSTER_CONF@@") {
		t.Fatal("cluster conf not injected")
	}
	if !strings.Contains(node, "cluster-enabled yes") {
		t.Fatal("cluster conf missing")
	}
	boot, err := s.LoadPhase("cluster", "bootstrap")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(boot, "--cluster create") {
		t.Fatal("bootstrap script missing create")
	}
	add, err := s.LoadPhase("cluster", "scale_out")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(add, "--cluster add-node") {
		t.Fatal("scale-out script missing add-node")
	}
	sent, err := s.LoadPhase("sentinel", "node")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sent, "@@REDIS_SENTINEL_") {
		t.Fatal("sentinel assets not injected")
	}
	if !strings.Contains(sent, "sentinel monitor mymaster {{__master_ip}}") {
		t.Fatal("sentinel conf missing monitor")
	}
	if !strings.Contains(sent, "replicaof {{__master_ip}}") {
		t.Fatal("sentinel replica conf missing")
	}
	for _, name := range []string{"replication", "cluster", "sentinel"} {
		node, err := s.LoadPhase(name, "node")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(node, "chown 999:999") || !strings.Contains(node, "chmod 644") {
			t.Fatalf("%s node script must chown/chmod redis.conf for image uid 999", name)
		}
	}
}

func TestAllBuiltinStacksWired(t *testing.T) {
	seen := map[string]bool{}
	for _, bp := range ListStackBlueprints() {
		if seen[bp.Key] {
			t.Fatalf("duplicate stack key %s", bp.Key)
		}
		seen[bp.Key] = true
		s := FindBuiltinStack(bp.Key)
		if s == nil {
			t.Fatalf("missing builtin %s", bp.Key)
		}
		if len(bp.Modes) == 0 {
			t.Fatalf("%s has no modes", bp.Key)
		}
		if bp.Category != "service" && bp.Category != "platform" {
			t.Fatalf("%s missing category, got %q", bp.Key, bp.Category)
		}
		wantDocker, ok := map[string]bool{
			"redis": true, "bigdata": true, "kafka": true, "elasticsearch": true,
			"rabbitmq": true, "rocketmq": true, "nacos": true, "powerjob": true,
		}[bp.Key]
		if !ok {
			t.Fatalf("%s missing requires_docker expectation (container vs host)", bp.Key)
		}
		if bp.RequiresDocker != wantDocker {
			t.Fatalf("%s requires_docker=%v, want %v", bp.Key, bp.RequiresDocker, wantDocker)
		}
		for _, m := range bp.Modes {
			script, err := s.LoadPhase(m.Key, "node")
			if err != nil {
				t.Fatalf("%s/%s node: %v", bp.Key, m.Key, err)
			}
			if strings.Contains(script, "@@") {
				t.Fatalf("%s/%s leftover @@ placeholder", bp.Key, m.Key)
			}
			needChmod := map[string]bool{
				"redis": true, "elasticsearch": true, "rabbitmq": true, "kafka": true, "rocketmq": true,
			}
			if needChmod[bp.Key] && !strings.Contains(script, "chmod 644") {
				t.Fatalf("%s/%s must chmod 644 mounted config for non-root image user", bp.Key, m.Key)
			}
			if m.HasBootstrap {
				boot, err := s.LoadPhase(m.Key, "bootstrap")
				if err != nil {
					t.Fatalf("%s/%s bootstrap: %v", bp.Key, m.Key, err)
				}
				if strings.Contains(boot, "@@") {
					t.Fatalf("%s/%s bootstrap leftover @@", bp.Key, m.Key)
				}
			}
		}
	}
	for _, key := range []string{"redis", "bigdata", "kafka", "elasticsearch", "rabbitmq", "rocketmq", "nacos", "powerjob"} {
		if !seen[key] {
			t.Fatalf("missing expected stack %s", key)
		}
	}
}
