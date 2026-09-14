// 探活通用容器状态解析：实现已统一到 store/stackkit（引擎与套件共用同一份，避免行为漂移）。
package stack

import "infra-ops/store/stackkit"

func parseCtrState(name, st string) (bool, string) { return stackkit.ParseCtrState(name, st) }

func parseCtrStateEx(name, st string) (ok bool, detail, image, started string) {
	return stackkit.ParseCtrStateEx(name, st)
}

func parseComposeStates(raw string) (bool, string) { return stackkit.ParseComposeStates(raw) }
