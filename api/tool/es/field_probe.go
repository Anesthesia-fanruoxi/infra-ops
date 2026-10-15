// field_probe.go ES「系统自带字段」的探测：一次三步，全部只读、不碰业务数据。
//
//  1. GET /                                  → 集群版本（清单随版本走，版本是缓存的依据）
//  2. GET /_component_template/ecs@mappings  → 该版本 ECS 定义的**顶层对象名**
//  3. GET /_field_caps?fields=<候选池>        → 这些字段在集群里**真实存在**哪些
//
// 结果 = 候选池 ∩ 步骤2 ∩ 步骤3。三层缺一不可：
//   - 只有候选池 → 会删掉集群里压根没有的字段（无害但脏，用户看着莫名其妙）；
//   - 少了步骤2 → 新版本才引入的字段（如 data_stream）永远进不来，等于又写死了一份清单；
//   - 少了步骤3 → 同上，删的是「理论上存在」而不是「这个集群里真的采到了」。
//
// 步骤 2 拿不到（低于 7.14、或权限不足）时不报错：退化为「候选池 ∩ 实际存在」，状态标 no_ecs
// 让界面说明原因。删字段这件事宁可少删，不能因为探测失败把整个功能卡住。
package es

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// 探测状态。
const (
	esFieldStatusOK     = "ok"     // 依据 ECS 定义 + 集群实际字段算出
	esFieldStatusNoECS  = "no_ecs" // 该集群没有 ECS 组件模板，退化为内置候选池
	esFieldStatusFailed = "failed" // 连集群都读不到
	esFieldNoMatchExtra = "该集群里没有这些字段，或还没有任何索引"
)

// esMetaCandidateTop 「采集元数据」候选池：ECS 顶层字段里描述「这条日志是怎么被采集进来的」那几个。
//
// 为什么不把 ECS 全部顶层字段都当候选：ECS 里还有 url / http / user / source / destination /
// process / error / message 这些**业务内容**对象 —— 它们同样会出现在集群里，一旦进了
// remove_field，就是把刚解析出来的数据删掉（不报错、只丢数据）。所以这里只留元数据性质的，
// 再由「该版本 ECS 里有」+「集群里真实存在」两层裁剪。
var esMetaCandidateTop = []string{
	"agent", "cloud", "container", "data_stream", "ecs", "event",
	"host", "input", "log", "observer", "orchestrator",
}

// probeESDefaultFields 执行一次完整探测。任何一步失败都不中断：把原因装进 errMsg 带回去，
// 由界面说明「这份清单是怎么来的 / 为什么少了什么」。
func probeESDefaultFields(ctx context.Context, client *escClient) (version string, fields []string, status, errMsg string) {
	version, err := esClusterVersion(ctx, client)
	if err != nil {
		return "", []string{}, esFieldStatusFailed, err.Error()
	}

	ecs, ecsErr := ecsTopLevel(ctx, client)
	live, liveErr := liveFields(ctx, client, esMetaCandidateTop)

	out := make([]string, 0, len(esMetaCandidateTop))
	for _, name := range esMetaCandidateTop {
		if ecs != nil && !ecs[name] {
			continue // 这个版本的 ECS 定义里没有它（老版本没有 data_stream 之类）
		}
		if live != nil && !live[name] {
			continue // 集群里没人采到这个字段
		}
		out = append(out, name)
	}

	var notes []string
	status = esFieldStatusOK
	if ecs == nil {
		status = esFieldStatusNoECS
		notes = append(notes, "该集群没有 ECS 组件模板（"+errText(ecsErr)+"），已退化为内置候选清单")
	}
	if liveErr != nil {
		notes = append(notes, "未能核对集群实际字段（"+errText(liveErr)+"），已按 ECS 定义给出")
	} else if len(out) == 0 {
		notes = append(notes, esFieldNoMatchExtra)
	}
	return version, out, status, strings.Join(notes, "；")
}

// esClusterVersion 读集群版本号。用裸 GET / 而不是 overview()：后者还会去拉健康与节点信息，
// 这里只需要 version.number，多打的请求都是白花的。
func esClusterVersion(ctx context.Context, client *escClient) (string, error) {
	code, body, err := client.do(ctx, "GET", "/", nil)
	if err != nil {
		return "", fmt.Errorf("连接集群失败: %w", err)
	}
	if code != 200 {
		return "", fmt.Errorf("读取集群信息失败（HTTP %d）", code)
	}
	var resp struct {
		Version struct {
			Number string `json:"number"`
		} `json:"version"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("解析集群信息失败: %w", err)
	}
	return resp.Version.Number, nil
}

// ecsTopLevel 从 ECS 组件模板里取顶层对象名集合。
// 名字随版本变过：7.14+ 是 ecs@mappings，更早的版本叫 ecs-mappings —— 两个都试。
func ecsTopLevel(ctx context.Context, client *escClient) (map[string]bool, error) {
	var lastErr error
	for _, name := range []string{"ecs@mappings", "ecs-mappings"} {
		code, body, err := client.do(ctx, "GET", "/_component_template/"+name, nil)
		if err != nil {
			lastErr = fmt.Errorf("读取组件模板 %s 失败: %w", name, err)
			continue
		}
		if code != 200 {
			lastErr = fmt.Errorf("组件模板 %s 不可用（HTTP %d）", name, code)
			continue
		}
		var resp struct {
			ComponentTemplates []struct {
				ComponentTemplate struct {
					Template struct {
						Mappings struct {
							Properties map[string]json.RawMessage `json:"properties"`
						} `json:"mappings"`
					} `json:"template"`
				} `json:"component_template"`
			} `json:"component_templates"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			lastErr = fmt.Errorf("解析组件模板 %s 失败: %w", name, err)
			continue
		}
		top := map[string]bool{}
		for _, ct := range resp.ComponentTemplates {
			for k := range ct.ComponentTemplate.Template.Mappings.Properties {
				top[k] = true
			}
		}
		if len(top) > 0 {
			return top, nil
		}
		lastErr = fmt.Errorf("组件模板 %s 里没有字段定义", name)
	}
	return nil, lastErr
}

// liveFields 问集群「候选池里这些字段真的存在吗」。
// 用 _field_caps 而不是拉全量 mapping：候选池只有十来个名字，请求体积恒定，
// 不会随集群里索引数量膨胀。返回的键可能是 host.name 这种嵌套路径，按顶层名归并。
func liveFields(ctx context.Context, client *escClient, candidates []string) (map[string]bool, error) {
	code, body, err := client.do(ctx, "GET", "/_field_caps?fields="+strings.Join(candidates, ","), nil)
	if err != nil {
		return nil, err
	}
	if code != 200 {
		// 没有任何索引时这里会 404：不是错误，交给调用方按「跳过这层」处理
		return nil, fmt.Errorf("_field_caps 返回 HTTP %d", code)
	}
	var resp struct {
		Fields map[string]json.RawMessage `json:"fields"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("解析 _field_caps 失败: %w", err)
	}
	out := map[string]bool{}
	for k := range resp.Fields {
		top := k
		if i := strings.IndexByte(top, '.'); i > 0 {
			top = top[:i]
		}
		out[top] = true
	}
	return out, nil
}

func errText(err error) string {
	if err == nil {
		return "未知原因"
	}
	return err.Error()
}
