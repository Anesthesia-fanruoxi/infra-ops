package sshx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"infra-ops/model"
)

// collectCmds 标准只读采集命令组。
var collectCmds = []string{
	"hostname -s",
	"cat /etc/os-release | grep PRETTY_NAME | cut -d= -f2 | tr -d '\"'",
	"uname -r",
	"awk '{print $1}' /proc/uptime",
	"nproc",
	"cat /proc/loadavg | awk '{print $1}'",
	"free -m | awk '/^Mem:/{print $2}'",
	"free | awk '/^Mem:/{printf \"%.0f\", $3/$2*100}'",
	`df -BG --output=target,size,pcent | awk 'NR>1 && $1!~/^\/(boot|run)/{gsub(/G/,"",$2); gsub(/%/,"",$3); printf "{\"mount\":\"%s\",\"size_gb\":%s,\"used_percent\":%s}\n",$1,$2,$3}'`,
}

// CollectResult 采集结果。
type CollectResult struct {
	Reachable bool            `json:"reachable"`
	LatencyMs int64           `json:"latency_ms"`
	Info      *model.HostInfo `json:"info"`
}

// Collect 连接目标机器并执行标准采集命令组。
func Collect(client *ssh.Client) (*CollectResult, error) {
	start := time.Now()

	var results []string
	for _, cmd := range collectCmds {
		output, err := runCmd(client, cmd)
		if err != nil {
			return nil, fmt.Errorf("collect cmd failed: %w", err)
		}
		results = append(results, strings.TrimSpace(output))
	}

	latency := time.Since(start).Milliseconds()

	info := &model.HostInfo{
		Hostname: results[0],
		OS:       results[1],
		Kernel:   results[2],
	}

	// uptime
	if v, err := parseFloat(results[3]); err == nil {
		info.UptimeHours = v / 3600
	}
	// cpu cores
	if v, err := parseInt(results[4]); err == nil {
		info.CPUCores = v
	}
	// load1
	if v, err := parseFloat(results[5]); err == nil {
		info.Load1 = v
	}
	// mem total
	if v, err := parseInt(results[6]); err == nil {
		info.MemTotalMB = v
	}
	// mem used percent
	if v, err := parseInt(results[7]); err == nil {
		info.MemUsedPercent = v
	}

	// disk: 每行一个 JSON 对象
	info.Disk = []model.DiskInfo{}
	if results[8] != "" {
		for _, line := range strings.Split(results[8], "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var d model.DiskInfo
			if err := json.Unmarshal([]byte(line), &d); err == nil {
				info.Disk = append(info.Disk, d)
			}
		}
	}

	return &CollectResult{
		Reachable: true,
		LatencyMs: latency,
		Info:      info,
	}, nil
}

// RunSingle 在 SSH 连接上执行单条命令。
func RunSingle(client *ssh.Client, cmd string) (string, error) {
	return runCmd(client, cmd)
}

func runCmd(client *ssh.Client, cmd string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("new session: %w", err)
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	done := make(chan error, 1)
	go func() {
		done <- session.Run(cmd)
	}()

	select {
	case err := <-done:
		if err != nil {
			return "", fmt.Errorf("run cmd: %w (stderr: %s)", err, stderr.String())
		}
	case <-ctx.Done():
		return "", fmt.Errorf("cmd timeout: %s", cmd)
	}

	return stdout.String(), nil
}

func parseFloat(s string) (float64, error) {
	var v float64
	_, err := fmt.Sscanf(s, "%f", &v)
	return v, err
}

func parseInt(s string) (int, error) {
	var v int
	_, err := fmt.Sscanf(s, "%d", &v)
	return v, err
}
