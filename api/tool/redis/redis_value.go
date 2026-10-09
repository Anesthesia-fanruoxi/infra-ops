package redis

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/common/redisclient"
	"infra-ops/common/resp"
	"infra-ops/model"
)

const (
	previewEntries = 100      // list / set / zset / hash 预览条数
	previewBytes   = 64 << 10 // string 预览字节上限（超出在服务端用 GETRANGE 截断）
	previewStream  = 10       // stream 预览条数
)

var errShortReply = errors.New("应答不完整")

// Key GET /api/redis/:id/key?key=&db=
//
// 只读预览：按类型取一段内容（字符串截断、集合类取前 100 个），不返回整值，
// 免得一个几十兆的 list 把界面拖垮。
func (h *Handler) Key(c *gin.Context) {
	conn, ok := h.getByID(c)
	if !ok {
		return
	}
	key := c.Query("key")
	if key == "" {
		resp.Fail(c, resp.CodeBadRequest, "缺少 key 参数")
		return
	}
	db := conn.DefaultDB
	if v := c.Query("db"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			db = n
		}
	}
	cli, ss, err := h.dial(conn, db)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "连接失败: "+err.Error())
		return
	}
	defer ss.Close()

	detail, err := readKey(cli, key)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "读取 key 失败: "+err.Error())
		return
	}
	resp.OK(c, detail)
}

// readKey 取类型、TTL 与一段内容。
func readKey(cli *redisclient.Client, key string) (model.RedisKeyDetail, error) {
	start := time.Now()
	det := model.RedisKeyDetail{Key: key, Entries: []model.RedisEntry{}}

	meta, err := cli.Pipeline([][]any{{"TYPE", key}, {"TTL", key}})
	if err != nil {
		return det, err
	}
	if len(meta) < 2 {
		return det, errShortReply
	}
	det.Type = meta[0].Text()
	if n, err := strconv.ParseInt(meta[1].Text(), 10, 64); err == nil {
		det.TTL = n
	}
	if det.Type == "" || det.Type == "none" {
		det.Type = "none"
		det.ElapsedMs = time.Since(start).Milliseconds()
		return det, nil
	}

	switch det.Type {
	case "string":
		err = readString(cli, key, &det)
	case "list":
		err = readList(cli, key, &det)
	case "set":
		err = readSet(cli, key, &det)
	case "zset":
		err = readZSet(cli, key, &det)
	case "hash":
		err = readHash(cli, key, &det)
	case "stream":
		err = readStream(cli, key, &det)
	default:
		err = fmt.Errorf("暂不支持预览 %s 类型", det.Type)
	}
	det.ElapsedMs = time.Since(start).Milliseconds()
	return det, err
}

func readString(cli *redisclient.Client, key string, det *model.RedisKeyDetail) error {
	// GETRANGE 对短字符串等价于 GET，还能顺手把超长值在服务端截断。
	r, err := cli.Pipeline([][]any{{"STRLEN", key}, {"GETRANGE", key, 0, previewBytes - 1}})
	if err != nil {
		return err
	}
	if len(r) < 2 {
		return errShortReply
	}
	if n, err := strconv.ParseInt(r[0].Text(), 10, 64); err == nil {
		det.Size = n
	}
	raw := r[1].Bulk
	det.Preview = string(raw)
	det.Truncated = det.Size > int64(len(raw))
	return nil
}

func readList(cli *redisclient.Client, key string, det *model.RedisKeyDetail) error {
	r, err := cli.Pipeline([][]any{{"LLEN", key}, {"LRANGE", key, 0, previewEntries - 1}})
	if err != nil {
		return err
	}
	if len(r) < 2 {
		return errShortReply
	}
	if n, err := strconv.ParseInt(r[0].Text(), 10, 64); err == nil {
		det.Size = n
	}
	for i, v := range r[1].Array {
		det.Entries = append(det.Entries, model.RedisEntry{Rank: strconv.Itoa(i), Value: v.Text()})
	}
	det.Truncated = det.Size > int64(len(det.Entries))
	return nil
}

func readSet(cli *redisclient.Client, key string, det *model.RedisKeyDetail) error {
	r, err := cli.Pipeline([][]any{{"SCARD", key}, {"SSCAN", key, 0, "COUNT", previewEntries}})
	if err != nil {
		return err
	}
	if len(r) < 2 {
		return errShortReply
	}
	if n, err := strconv.ParseInt(r[0].Text(), 10, 64); err == nil {
		det.Size = n
	}
	for _, v := range cursorPairs(r[1]) {
		det.Entries = append(det.Entries, model.RedisEntry{Value: v})
	}
	det.Truncated = det.Size > int64(len(det.Entries))
	return nil
}

func readZSet(cli *redisclient.Client, key string, det *model.RedisKeyDetail) error {
	r, err := cli.Pipeline([][]any{{"ZCARD", key}, {"ZRANGE", key, 0, previewEntries - 1, "WITHSCORES"}})
	if err != nil {
		return err
	}
	if len(r) < 2 {
		return errShortReply
	}
	if n, err := strconv.ParseInt(r[0].Text(), 10, 64); err == nil {
		det.Size = n
	}
	// WITHSCORES 按 member / score 交替返回：zset 里 rank 位置放分数。
	pair := r[1].Texts()
	for i := 0; i+1 < len(pair); i += 2 {
		det.Entries = append(det.Entries, model.RedisEntry{Rank: pair[i+1], Value: pair[i]})
	}
	det.Truncated = det.Size > int64(len(det.Entries))
	return nil
}

func readHash(cli *redisclient.Client, key string, det *model.RedisKeyDetail) error {
	r, err := cli.Pipeline([][]any{{"HLEN", key}, {"HSCAN", key, 0, "COUNT", previewEntries}})
	if err != nil {
		return err
	}
	if len(r) < 2 {
		return errShortReply
	}
	if n, err := strconv.ParseInt(r[0].Text(), 10, 64); err == nil {
		det.Size = n
	}
	// HSCAN 的载荷是 field / value 交替
	kv := cursorPairs(r[1])
	for i := 0; i+1 < len(kv); i += 2 {
		det.Entries = append(det.Entries, model.RedisEntry{Field: kv[i], Value: kv[i+1]})
	}
	det.Truncated = det.Size > int64(len(det.Entries))
	return nil
}

func readStream(cli *redisclient.Client, key string, det *model.RedisKeyDetail) error {
	r, err := cli.Pipeline([][]any{{"XLEN", key}, {"XRANGE", key, "-", "+", "COUNT", previewStream}})
	if err != nil {
		return err
	}
	if len(r) < 2 {
		return errShortReply
	}
	if n, err := strconv.ParseInt(r[0].Text(), 10, 64); err == nil {
		det.Size = n
	}
	for _, item := range r[1].Array {
		if len(item.Array) < 2 {
			continue
		}
		id := item.Array[0].Text()
		fields := item.Array[1].Texts()
		parts := make([]string, 0, len(fields)/2)
		for i := 0; i+1 < len(fields); i += 2 {
			parts = append(parts, fields[i]+"="+fields[i+1])
		}
		det.Entries = append(det.Entries, model.RedisEntry{Rank: id, Value: strings.Join(parts, " ")})
	}
	det.Truncated = det.Size > int64(len(det.Entries))
	return nil
}

// cursorPairs 从 SSCAN / HSCAN 的 `[游标, [元素…]]` 应答里取元素列表。
func cursorPairs(r redisclient.Reply) []string {
	if r.Kind != redisclient.KindArray || len(r.Array) < 2 {
		return nil
	}
	return r.Array[1].Texts()
}
