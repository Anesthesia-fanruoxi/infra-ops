package redisclient

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestWriteCommandEncoding(t *testing.T) {
	var buf bytes.Buffer
	c := &Client{bw: bufio.NewWriter(&buf)}
	if err := c.writeCommand([]any{"SCAN", "0", "MATCH", "*a[b]", "COUNT", 500}); err != nil {
		t.Fatalf("writeCommand: %v", err)
	}
	if err := c.bw.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	want := "*6\r\n$4\r\nSCAN\r\n$1\r\n0\r\n$5\r\nMATCH\r\n$5\r\n*a[b]\r\n$5\r\nCOUNT\r\n$3\r\n500\r\n"
	if buf.String() != want {
		t.Fatalf("命令编码不符\n got %q\nwant %q", buf.String(), want)
	}
}

func TestWriteCommandValueKinds(t *testing.T) {
	var buf bytes.Buffer
	c := &Client{bw: bufio.NewWriter(&buf)}
	// int / int64 / nil / []byte 都要能编码：GETRANGE 的上下标是 int，X 参数是 int64
	if err := c.writeCommand([]any{"GETRANGE", "k", 0, int64(65535), nil, []byte("raw")}); err != nil {
		t.Fatalf("writeCommand: %v", err)
	}
	_ = c.bw.Flush()
	want := "*6\r\n$8\r\nGETRANGE\r\n$1\r\nk\r\n$1\r\n0\r\n$5\r\n65535\r\n$0\r\n\r\n$3\r\nraw\r\n"
	if buf.String() != want {
		t.Fatalf("命令编码不符\n got %q\nwant %q", buf.String(), want)
	}
}

func TestReadReplyKinds(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		kind  byte
		text  string
		num   int64
		null  bool
		extra func(t *testing.T, r Reply)
	}{
		{name: "status", in: "+OK\r\n", kind: KindStatus, text: "OK"},
		{name: "error", in: "-WRONGTYPE bad\r\n", kind: KindError, text: "WRONGTYPE bad"},
		{name: "integer", in: ":42\r\n", kind: KindInteger, text: "42", num: 42},
		{name: "negative integer", in: ":-1\r\n", kind: KindInteger, text: "-1", num: -1},
		{name: "bulk", in: "$3\r\nfoo\r\n", kind: KindBulk, text: "foo"},
		{name: "empty bulk", in: "$0\r\n\r\n", kind: KindBulk, text: ""},
		{name: "null bulk", in: "$-1\r\n", kind: KindBulk, null: true},
		{name: "null array", in: "*-1\r\n", kind: KindArray, null: true},
		{
			name: "array", in: "*2\r\n$1\r\n0\r\n*2\r\n$1\r\na\r\n$1\r\nb\r\n", kind: KindArray,
			extra: func(t *testing.T, r Reply) {
				texts := r.Array[1].Texts()
				if len(texts) != 2 || texts[0] != "a" || texts[1] != "b" {
					t.Fatalf("数组内容不符: %#v", texts)
				}
				if r.Array[0].Text() != "0" {
					t.Fatalf("游标不符: %q", r.Array[0].Text())
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{br: bufio.NewReader(strings.NewReader(tc.in))}
			r, err := c.readReply()
			if err != nil {
				t.Fatalf("readReply: %v", err)
			}
			if r.Kind != tc.kind {
				t.Fatalf("Kind=%q want %q", r.Kind, tc.kind)
			}
			if tc.null != r.Null {
				t.Fatalf("Null=%v want %v", r.Null, tc.null)
			}
			if tc.kind != KindArray && r.Text() != tc.text {
				t.Fatalf("Text=%q want %q", r.Text(), tc.text)
			}
			if tc.kind == KindInteger && r.Int != tc.num {
				t.Fatalf("Int=%d want %d", r.Int, tc.num)
			}
			if tc.extra != nil {
				tc.extra(t, r)
			}
		})
	}
}

// 批量串读完后游标必须停在下一行开头：少读末尾的 \r\n 会让后续应答全部错位。
func TestReadReplyKeepsStreamAligned(t *testing.T) {
	in := "$5\r\nab\r\nc\r\n+PONG\r\n"
	c := &Client{br: bufio.NewReader(strings.NewReader(in))}
	first, err := c.readReply()
	if err != nil {
		t.Fatalf("readReply: %v", err)
	}
	if first.Text() != "ab\r\nc" {
		t.Fatalf("内容不符: %q", first.Text())
	}
	second, err := c.readReply()
	if err != nil {
		t.Fatalf("第二条 readReply: %v", err)
	}
	if second.Kind != KindStatus || second.Str != "PONG" {
		t.Fatalf("流未对齐: kind=%q str=%q", second.Kind, second.Str)
	}
}

func TestReadReplyUnknownPrefix(t *testing.T) {
	c := &Client{br: bufio.NewReader(strings.NewReader("?weird\r\n"))}
	if _, err := c.readReply(); err == nil {
		t.Fatal("未知前缀应当报错")
	}
}

// Dial 要按 AUTH → SELECT → PING 的顺序完成握手。
func TestDialHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	got := make(chan []string, 16)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		for {
			args, err := readTestCommand(br)
			if err != nil {
				return
			}
			got <- args
			switch args[0] {
			case "AUTH", "SELECT":
				fmt.Fprint(conn, "+OK\r\n")
			case "PING":
				fmt.Fprint(conn, "+PONG\r\n")
			default:
				fmt.Fprint(conn, "+OK\r\n")
			}
		}
	}()

	cli, err := Dial(context.Background(), Options{
		Addr:     ln.Addr().String(),
		Username: "app",
		Password: "secret",
		DB:       3,
		Timeout:  2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer cli.Close()

	want := [][]string{{"AUTH", "app", "secret"}, {"SELECT", "3"}, {"PING"}}
	for i, w := range want {
		select {
		case args := <-got:
			if strings.Join(args, " ") != strings.Join(w, " ") {
				t.Fatalf("第 %d 条命令 = %v, want %v", i+1, args, w)
			}
		case <-time.After(time.Second):
			t.Fatalf("等第 %d 条命令超时", i+1)
		}
	}
}

// 老版本 Redis 只认单参数 AUTH：指定了 ACL 用户名时应退回再试一次。
func TestDialAuthFallsBackForLegacyServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	got := make(chan []string, 16)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		for {
			args, err := readTestCommand(br)
			if err != nil {
				return
			}
			got <- args
			switch {
			case args[0] == "AUTH" && len(args) > 2:
				fmt.Fprint(conn, "-ERR wrong number of arguments for 'auth' command\r\n")
			case args[0] == "AUTH":
				fmt.Fprint(conn, "+OK\r\n")
			default:
				fmt.Fprint(conn, "+PONG\r\n")
			}
		}
	}()

	cli, err := Dial(context.Background(), Options{
		Addr:     ln.Addr().String(),
		Username: "app",
		Password: "secret",
		Timeout:  2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Dial 应退回单参数 AUTH: %v", err)
	}
	defer cli.Close()

	seen := []string{}
	for i := 0; i < 3; i++ {
		select {
		case args := <-got:
			seen = append(seen, strings.Join(args, " "))
		case <-time.After(time.Second):
			t.Fatalf("只收到 %d 条命令: %v", i, seen)
		}
	}
	if seen[0] != "AUTH app secret" || seen[1] != "AUTH secret" {
		t.Fatalf("AUTH 退回序列不符: %v", seen)
	}
}

// readTestCommand 解析一条客户端命令（仅测试用）。
func readTestCommand(br *bufio.Reader) ([]string, error) {
	line, err := br.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if !strings.HasPrefix(line, "*") {
		return nil, io.ErrUnexpectedEOF
	}
	n, err := strconv.Atoi(line[1:])
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		head, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(head, "$") {
			return nil, io.ErrUnexpectedEOF
		}
		size, err := strconv.Atoi(strings.TrimRight(head[1:], "\r\n"))
		if err != nil {
			return nil, err
		}
		buf := make([]byte, size+2)
		if _, err := io.ReadFull(br, buf); err != nil {
			return nil, err
		}
		out = append(out, string(buf[:size]))
	}
	return out, nil
}
