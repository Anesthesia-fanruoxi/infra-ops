package es

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"infra-ops/model"
)

// esNodeStats _nodes/stats 单节点响应体（仅取展示所需字段）。
type esNodeStats struct {
	OS struct {
		CPU struct {
			Percent int `json:"percent"`
		} `json:"cpu"`
	} `json:"os"`
	JVM struct {
		Mem struct {
			HeapUsedPercent int `json:"heap_used_percent"`
		} `json:"mem"`
	} `json:"jvm"`
	FS struct {
		Total struct {
			TotalInBytes     int64 `json:"total_in_bytes"`
			AvailableInBytes int64 `json:"available_in_bytes"`
		} `json:"total"`
	} `json:"fs"`
	Indices struct {
		Docs struct {
			Count int64 `json:"count"`
		} `json:"docs"`
	} `json:"indices"`
}

func (c *escClient) overview(ctx context.Context) (*model.ESOverview, error) {
	status, body, err := c.do(ctx, http.MethodGet, "/", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, esErr(status, body, "读取版本信息失败")
	}
	var info struct {
		ClusterName string `json:"cluster_name"`
		Version     struct {
			Number string `json:"number"`
		} `json:"version"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("解析版本信息失败: %w", err)
	}

	status, body, err = c.do(ctx, http.MethodGet, "/_cluster/health?format=json", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, esErr(status, body, "读取集群健康失败")
	}
	ov := &model.ESOverview{ClusterName: info.ClusterName, Version: info.Version.Number}
	var h struct {
		Status            string `json:"status"`
		TimedOut          bool   `json:"timed_out"`
		NumberOfNodes     int    `json:"number_of_nodes"`
		NumberOfDataNodes int    `json:"number_of_data_nodes"`
		ActiveShards      int    `json:"active_shards"`
		ActivePrimaries   int    `json:"active_primary_shards"`
		Relocating        int    `json:"relocating_shards"`
		Initializing      int    `json:"initializing_shards"`
		Unassigned        int    `json:"unassigned_shards"`
	}
	if err := json.Unmarshal(body, &h); err != nil {
		return nil, fmt.Errorf("解析集群健康失败: %w", err)
	}
	ov.Status = h.Status
	ov.TimedOut = h.TimedOut
	ov.Nodes = h.NumberOfNodes
	ov.DataNodes = h.NumberOfDataNodes
	ov.ActiveShards = h.ActiveShards
	ov.ActivePrim = h.ActivePrimaries
	ov.Relocating = h.Relocating
	ov.Initializing = h.Initializing
	ov.Unassigned = h.Unassigned
	return ov, nil
}

// nodes 合并 _nodes（版本/角色）与 _nodes/stats（资源用量）。
func (c *escClient) nodes(ctx context.Context) ([]model.ESNode, error) {
	status, body, err := c.do(ctx, http.MethodGet, "/_nodes?format=json", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, esErr(status, body, "读取节点信息失败")
	}
	var info struct {
		Nodes map[string]struct {
			Name    string   `json:"name"`
			Version string   `json:"version"`
			Roles   []string `json:"roles"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("解析节点信息失败: %w", err)
	}

	status, body, err = c.do(ctx, http.MethodGet, "/_nodes/stats?format=json", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, esErr(status, body, "读取节点统计失败")
	}
	var stats struct {
		Nodes map[string]esNodeStats `json:"nodes"`
	}
	if err := json.Unmarshal(body, &stats); err != nil {
		return nil, fmt.Errorf("解析节点统计失败: %w", err)
	}

	var nodes []model.ESNode
	for id, n := range info.Nodes {
		s, ok := stats.Nodes[id]
		if !ok {
			s = esNodeStats{}
		}
		nodes = append(nodes, model.ESNode{
			ID:            id,
			Name:          n.Name,
			Version:       n.Version,
			Roles:         n.Roles,
			CPUPercent:    s.OS.CPU.Percent,
			HeapPercent:   s.JVM.Mem.HeapUsedPercent,
			DiskTotal:     s.FS.Total.TotalInBytes,
			DiskAvailable: s.FS.Total.AvailableInBytes,
			DocsCount:     s.Indices.Docs.Count,
		})
	}
	return nodes, nil
}

func (c *escClient) indices(ctx context.Context) ([]model.ESIndex, error) {
	// expand_wildcards 去掉 hidden（仅 open,closed），并在下面剔除 . 开头系统索引，
	// 使列表只展示业务索引，不展示隐藏与系统索引。
	const path = "/_cat/indices?format=json&h=index,health,status,pri,rep,docs.count,docs.deleted,store.size&expand_wildcards=open,closed&size=100"
	status, body, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, esErr(status, body, "读取索引列表失败")
	}
	var list []map[string]interface{}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("解析索引列表失败: %w", err)
	}
	indices := make([]model.ESIndex, 0, 100)
	for _, m := range list {
		name := strOf(m["index"])
		if strings.HasPrefix(name, ".") {
			continue
		}
		if len(indices) >= 100 {
			break
		}
		indices = append(indices, model.ESIndex{
			Index:       name,
			Health:      strOf(m["health"]),
			Status:      strOf(m["status"]),
			Primaries:   strOf(m["pri"]),
			Replicas:    strOf(m["rep"]),
			DocsCount:   strOf(m["docs.count"]),
			DocsDeleted: strOf(m["docs.deleted"]),
			StoreSize:   strOf(m["store.size"]),
		})
	}
	return indices, nil
}

func (c *escClient) createIndex(ctx context.Context, name string, shards, replicas *int) error {
	bodyMap := map[string]interface{}{}
	if shards != nil || replicas != nil {
		settings := map[string]interface{}{}
		if shards != nil {
			settings["number_of_shards"] = *shards
		}
		if replicas != nil {
			settings["number_of_replicas"] = *replicas
		}
		bodyMap["settings"] = settings
	}
	var body []byte
	var err error
	if len(bodyMap) > 0 {
		body, err = json.Marshal(bodyMap)
		if err != nil {
			return fmt.Errorf("构造请求失败: %w", err)
		}
	}
	status, respBody, err := c.do(ctx, http.MethodPut, "/"+pathEscape(name), body)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return esErr(status, respBody, "创建索引失败")
	}
	return nil
}

func (c *escClient) deleteIndex(ctx context.Context, name string) error {
	status, respBody, err := c.do(ctx, http.MethodDelete, "/"+pathEscape(name), nil)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		return fmt.Errorf("索引不存在(404)")
	}
	if status != http.StatusOK {
		return esErr(status, respBody, "删除索引失败")
	}
	return nil
}

func (c *escClient) search(ctx context.Context, index string, from, size int, rawQuery json.RawMessage) (*model.ESSearchResult, error) {
	q := json.RawMessage(`{"match_all":{}}`)
	if len(rawQuery) > 0 && string(rawQuery) != "null" {
		// 仅允许字面量对象，避免任意 JSON 注入风险
		if !bytes.HasPrefix(bytes.TrimSpace(rawQuery), []byte("{")) {
			return nil, fmt.Errorf("查询必须是 JSON 对象（如 {\"match_all\":{}}）")
		}
		q = rawQuery
	}
	bodyMap := map[string]interface{}{
		"from": from, "size": size, "query": q,
	}
	body, err := json.Marshal(bodyMap)
	if err != nil {
		return nil, fmt.Errorf("构造查询失败: %w", err)
	}
	status, respBody, err := c.do(ctx, http.MethodPost, "/"+pathEscape(index)+"/_search", body)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, esErr(status, respBody, "查询失败")
	}
	var raw struct {
		Took     int  `json:"took"`
		TimedOut bool `json:"timed_out"`
		Hits     struct {
			Total json.RawMessage `json:"total"`
			Hits  []struct {
				Index  string      `json:"_index"`
				ID     string      `json:"_id"`
				Score  interface{} `json:"_score"`
				Source interface{} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return nil, fmt.Errorf("解析查询结果失败: %w", err)
	}
	result := &model.ESSearchResult{From: from, Size: size, Took: raw.Took, TimedOut: raw.TimedOut}
	// total 兼容 number 与 {value}
	var num json.Number
	if err := json.Unmarshal(raw.Hits.Total, &num); err == nil {
		result.Total, _ = num.Int64()
	} else {
		var tv struct {
			Value int64 `json:"value"`
		}
		_ = json.Unmarshal(raw.Hits.Total, &tv)
		result.Total = tv.Value
	}
	for _, h := range raw.Hits.Hits {
		result.Hits = append(result.Hits, model.ESSearchHit{
			Index: h.Index, ID: h.ID, Score: h.Score, Source: h.Source,
		})
	}
	if len(result.Hits) == 0 {
		result.Hits = []model.ESSearchHit{}
	}
	return result, nil
}
