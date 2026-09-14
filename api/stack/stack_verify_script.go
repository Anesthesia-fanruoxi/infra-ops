// 套件探活脚本生成：通用 shell 骨架（Docker 探测 / compose 状态 / 容器 inspect）
// + 套件专属片段（stackkit.ProbePlugin 的 Containers 与 ScriptTail）。
package stack

import (
	"strings"

	"infra-ops/store"
	"infra-ops/store/stackkit"
)

func buildStackVerifyScript(stackKey, mode, role, hostIP string, params map[string]string) string {
	home := bashQuote(pickParam(params, "home_dir", ""))
	pass := bashQuote(strings.TrimSpace(params["password"]))
	var b strings.Builder
	b.WriteString("#!/bin/bash\nset +e\n")
	b.WriteString("echo __IO_BEGIN__\n")
	b.WriteString("HOME_DIR=" + home + "\n")
	b.WriteString("PASS=" + pass + "\n")
	b.WriteString("ROLE=" + bashQuote(role) + "\n")
	b.WriteString(`if ! command -v docker >/dev/null 2>&1; then echo __IO_ERR__=no-docker; echo __IO_END__; exit 0; fi
# 单 compose（Redis 等）+ 多目录 compose（大数据底座 home/*/compose.yml）
echo __IO_COMPOSE_BEGIN__
if [ -n "$HOME_DIR" ]; then
  for cf in "$HOME_DIR/compose.yml" "$HOME_DIR/docker-compose.yml" "$HOME_DIR"/*/compose.yml; do
    [ -f "$cf" ] || continue
    docker compose -f "$cf" ps -a --format '{{.Name}}={{.State}}' 2>/dev/null || true
  done
fi
echo __IO_COMPOSE_END__
inspect_ctr() {
  local n="$1"
  local st
  st=$(docker inspect -f '{{.State.Status}}|{{if .State.Running}}1{{else}}0{{end}}|{{if .State.Health}}{{.State.Health.Status}}{{end}}|{{.Config.Image}}|{{.State.StartedAt}}' "$n" 2>/dev/null)
  if [ -z "$st" ]; then echo "__IO_CTR__${n}__=missing"; else echo "__IO_CTR__${n}__=$st"; fi
}
`)
	ctx := stackkit.ProbeCtx{Mode: mode, Role: role, HostIP: hostIP, Params: params}
	var ctrs []string
	tail := ""
	if p, ok := store.FindStackDriver(stackKey).(stackkit.ProbePlugin); ok {
		ctrs = p.Containers(ctx)
		tail = p.ScriptTail(ctx)
	}
	for _, c := range ctrs {
		b.WriteString("inspect_ctr " + bashQuote(c) + "\n")
	}
	b.WriteString(tail)
	b.WriteString("echo __IO_END__\nexit 0\n")
	return b.String()
}
