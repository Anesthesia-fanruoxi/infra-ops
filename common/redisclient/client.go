// Package redisclient 是一个只覆盖查询所需命令的 RESP2 客户端。
//
// 为什么不引 go-redis：本工具只用到 SCAN / TYPE / TTL / GET / LRANGE 这一小撮读命令，
// 一次请求一条连接、不需要连接池，也不需要集群 / 哨兵 / Pub-Sub。
// 协议本身只有五种前缀（+ - : $ *），自研换来的是零依赖与和 common/aiopenai、
// common/xlsx 一致的代码形态。
package redisclient

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// RESP2 行首前缀，同时也是 Reply.Kind 的取值。
const (
	KindStatus  byte = '+' // 简单字符串：+OK
	KindError   byte = '-' // 错误：-ERR ...
	KindInteger byte = ':' // 整数：:1000
	KindBulk    byte = '$' // 批量字符串（可空）
	KindArray   byte = '*' // 数组（可空）
)

const (
	defaultTimeout = 30 * time.Second
	dialTimeout    = 10 * time.Second
	readBufSize    = 16 << 10
)

// Reply 一条命令的应答。按 Kind 取对应字段。
type Reply struct {
	Kind  byte
	Str   string  // Status / Error 的正文
	Int   int64   // Integer
	Bulk  []byte  // Bulk 内容（二进制安全，也可能是非法 UTF-8）
	Array []Reply // Array 元素
	Null  bool    // Bulk / Array 为 nil 应答（$-1 / *-1）
}

// Err 把错误应答转成 error；其余类型返回 nil。
func (r Reply) Err() error {
	if r.Kind == KindError {
		return errors.New(r.Str)
	}
	return nil
}

// Text Bulk / Status / Integer 取文本形式；数组与空值返回空串。
func (r Reply) Text() string {
	switch r.Kind {
	case KindBulk:
		return string(r.Bulk)
	case KindStatus, KindError:
		return r.Str
	case KindInteger:
		return strconv.FormatInt(r.Int, 10)
	}
	return ""
}

// Texts 数组应答逐元素取文本（SCAN 的 [游标, [key…]] 用得上）。
func (r Reply) Texts() []string {
	out := make([]string, 0, len(r.Array))
	for _, e := range r.Array {
		out = append(out, e.Text())
	}
	return out
}

// Options 建连参数。
type Options struct {
	Addr     string // host:port
	Username string // ACL 用户名（Redis 6+），可空
	Password string
	DB       int
	Timeout  time.Duration // 单条命令的读写超时；<=0 用 30s
}

// Client 单条 TCP 连接上的 RESP 会话，非并发安全（一次请求一个实例）。
type Client struct {
	conn    net.Conn
	br      *bufio.Reader
	bw      *bufio.Writer
	timeout time.Duration
}

// Dial 建连并完成 AUTH / SELECT 握手，最后 PING 探活。
func Dial(ctx context.Context, opt Options) (*Client, error) {
	timeout := opt.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	d := net.Dialer{Timeout: dialTimeout}
	conn, err := d.DialContext(ctx, "tcp", opt.Addr)
	if err != nil {
		return nil, err
	}
	c := &Client{
		conn:    conn,
		br:      bufio.NewReaderSize(conn, readBufSize),
		bw:      bufio.NewWriter(conn),
		timeout: timeout,
	}
	if err := c.handshake(opt); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return c, nil
}

// handshake AUTH → SELECT → PING。
func (c *Client) handshake(opt Options) error {
	if opt.Password != "" {
		authed := false
		if opt.Username != "" {
			if _, err := c.Do("AUTH", opt.Username, opt.Password); err == nil {
				authed = true
			} else if !strings.Contains(err.Error(), "wrong number of arguments") {
				return fmt.Errorf("认证失败: %w", err)
			}
			// Redis 5 及更早只认单参数 AUTH：指定了 ACL 用户名却报参数个数错误时退回再试。
		}
		if !authed {
			if _, err := c.Do("AUTH", opt.Password); err != nil {
				return fmt.Errorf("认证失败: %w", err)
			}
		}
	}
	if opt.DB > 0 {
		if _, err := c.Do("SELECT", opt.DB); err != nil {
			return fmt.Errorf("选择库 %d 失败: %w", opt.DB, err)
		}
	}
	if _, err := c.Do("PING"); err != nil {
		return fmt.Errorf("探活失败: %w", err)
	}
	return nil
}

// Close 关闭连接。
func (c *Client) Close() error { return c.conn.Close() }

// Do 执行一条命令，并把错误应答（-ERR）转成 error 返回。
func (c *Client) Do(args ...any) (Reply, error) {
	_ = c.conn.SetDeadline(time.Now().Add(c.timeout))
	if err := c.writeCommand(args); err != nil {
		return Reply{}, err
	}
	if err := c.bw.Flush(); err != nil {
		return Reply{}, err
	}
	r, err := c.readReply()
	if err != nil {
		return Reply{}, err
	}
	return r, r.Err()
}

// pipelineChunkSize 每块命令数。不能一口气把几万条命令全写出去：
// 服务端是「读完一条回一条」，响应很快会填满它的发送缓冲并阻塞在写，
// 而此时客户端的发送缓冲往往也满了——两边互等就成死锁。分块写读即可避免。
const pipelineChunkSize = 256

// Pipeline 批量执行命令并顺序读回应答，省掉 N 次往返（批量取 TYPE/TTL 时用）。
//
// 返回的 error 只表示协议 / IO 层失败；单条命令的错误留在各自 Reply 的 Kind 里，
// 由调用方决定是否忽略——批量取元数据时个别 key 恰好过期不该让整批失败。
func (c *Client) Pipeline(cmds [][]any) ([]Reply, error) {
	if len(cmds) == 0 {
		return nil, nil
	}
	out := make([]Reply, len(cmds))
	for i := 0; i < len(cmds); i += pipelineChunkSize {
		end := i + pipelineChunkSize
		if end > len(cmds) {
			end = len(cmds)
		}
		if err := c.pipelineChunk(cmds[i:end], out[i:end]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (c *Client) pipelineChunk(cmds [][]any, out []Reply) error {
	_ = c.conn.SetDeadline(time.Now().Add(c.timeout))
	for _, cmd := range cmds {
		if err := c.writeCommand(cmd); err != nil {
			return err
		}
	}
	if err := c.bw.Flush(); err != nil {
		return err
	}
	for i := range cmds {
		r, err := c.readReply()
		if err != nil {
			return err
		}
		out[i] = r
	}
	return nil
}

// ---------------- 编解码 ----------------

func (c *Client) writeCommand(args []any) error {
	if _, err := fmt.Fprintf(c.bw, "*%d\r\n", len(args)); err != nil {
		return err
	}
	for _, a := range args {
		var err error
		switch v := a.(type) {
		case string:
			err = writeBulk(c.bw, []byte(v))
		case []byte:
			err = writeBulk(c.bw, v)
		case int:
			err = writeBulk(c.bw, []byte(strconv.Itoa(v)))
		case int64:
			err = writeBulk(c.bw, []byte(strconv.FormatInt(v, 10)))
		case nil:
			err = writeBulk(c.bw, nil)
		default:
			err = writeBulk(c.bw, []byte(fmt.Sprint(v)))
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func writeBulk(bw *bufio.Writer, b []byte) error {
	if _, err := fmt.Fprintf(bw, "$%d\r\n", len(b)); err != nil {
		return err
	}
	if _, err := bw.Write(b); err != nil {
		return err
	}
	_, err := bw.WriteString("\r\n")
	return err
}

func (c *Client) readReply() (Reply, error) {
	line, err := readLine(c.br)
	if err != nil {
		return Reply{}, err
	}
	if len(line) == 0 {
		return Reply{}, errors.New("空应答")
	}
	body := string(line[1:])
	switch line[0] {
	case KindStatus:
		return Reply{Kind: KindStatus, Str: body}, nil
	case KindError:
		return Reply{Kind: KindError, Str: body}, nil
	case KindInteger:
		n, err := strconv.ParseInt(body, 10, 64)
		if err != nil {
			return Reply{}, fmt.Errorf("非法整数应答: %q", line)
		}
		return Reply{Kind: KindInteger, Int: n}, nil
	case KindBulk:
		n, err := strconv.Atoi(body)
		if err != nil {
			return Reply{}, fmt.Errorf("非法批量应答: %q", line)
		}
		if n < 0 {
			return Reply{Kind: KindBulk, Null: true}, nil
		}
		buf := make([]byte, n+2) // 末尾 \r\n 一并读出后截掉，避免污染后续行
		if _, err := io.ReadFull(c.br, buf); err != nil {
			return Reply{}, err
		}
		return Reply{Kind: KindBulk, Bulk: buf[:n]}, nil
	case KindArray:
		n, err := strconv.Atoi(body)
		if err != nil {
			return Reply{}, fmt.Errorf("非法数组应答: %q", line)
		}
		if n < 0 {
			return Reply{Kind: KindArray, Null: true}, nil
		}
		arr := make([]Reply, n)
		for i := 0; i < n; i++ {
			e, err := c.readReply()
			if err != nil {
				return Reply{}, err
			}
			arr[i] = e
		}
		return Reply{Kind: KindArray, Array: arr}, nil
	}
	return Reply{}, fmt.Errorf("未知应答前缀: %q", line)
}

// readLine 读一行并剥掉结尾的 \r\n。
func readLine(br *bufio.Reader) ([]byte, error) {
	line, err := br.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	line = line[:len(line)-1]
	if n := len(line); n > 0 && line[n-1] == '\r' {
		line = line[:n-1]
	}
	return line, nil
}
