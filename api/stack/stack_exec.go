package stack

import (
	"log"
	"strings"
	"sync"

	"infra-ops/api/deploy"
	"infra-ops/api/shared"
	"infra-ops/common/sysutil"
	"infra-ops/model"
)

// probeDocker 探测主机上是否已安装 Docker。
func (h *stackHandler) probeDocker(hostID int64) (bool, error) {
	out, err := deploy.ExecHostWith(h.hostRepo, h.credRepo, h.cryptoS, h.sshC, hostID,
		`if command -v docker >/dev/null 2>&1; then echo INFRAOPS_DOCKER=yes; else echo INFRAOPS_DOCKER=no; fi`, nil)
	if err != nil {
		return false, err
	}
	return strings.Contains(out, "INFRAOPS_DOCKER=yes"), nil
}

// execScript 在目标主机执行脚本并按行回推日志。
func (h *stackHandler) execScript(host *model.StackRunHost, script, phase string) (string, error) {
	onLog := func(chunk string) {
		if chunk == "" {
			return
		}
		lines := shared.SplitLogLines(chunk)
		if len(lines) == 0 {
			return
		}
		rows := make([]model.StackRunLog, 0, len(lines))
		for _, ln := range lines {
			rows = append(rows, model.StackRunLog{Phase: phase, HostID: host.HostID, HostIP: host.HostIP, Text: ln})
		}
		persisted, err := h.repo.AppendLogs(host.RunID, rows)
		if err != nil {
			log.Printf("stack: 写日志失败 run=%d host=%d: %v", host.RunID, host.HostID, err)
			return
		}
		h.publishLogs(persisted)
	}
	return deploy.ExecHostWith(h.hostRepo, h.credRepo, h.cryptoS, h.sshC, host.HostID, script, onLog)
}

// forEachHost 并发遍历主机执行回调。
func (h *stackHandler) forEachHost(hosts []model.StackRunHost, fn func(*model.StackRunHost)) {
	conc := h.conc
	if conc <= 0 {
		conc = sysutil.AdaptiveConcurrency(len(hosts))
	}
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i := range hosts {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			mu.Lock()
			host := hosts[idx]
			mu.Unlock()
			fn(&host)
			mu.Lock()
			hosts[idx] = host
			mu.Unlock()
		}(i)
	}
	wg.Wait()
}
