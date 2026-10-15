package es

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type zipIn struct{ name, content string }

func makeZipFile(t *testing.T, path string, in []zipIn) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for _, e := range in {
		w, err := zw.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func makeTarGzFile(t *testing.T, path string, in []zipIn) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range in {
		if err := tw.WriteHeader(&tar.Header{
			Name: e.name, Mode: 0o644, Size: int64(len(e.content)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
}

func openZipSource(t *testing.T, path, filter string) *zipSource {
	t.Helper()
	src, err := newZipSource(path, filter)
	if err != nil {
		t.Fatalf("打开压缩包失败: %v", err)
	}
	return src
}

// TestZipSourceConcatenatesFiles 包内多个文件的记录要串成一条流，且 Origin 能指回包内路径，
// 否则导入完看到「共 3000 条」却不知道分别来自哪个文件。
func TestZipSourceConcatenatesFiles(t *testing.T) {
	p := filepath.Join(t.TempDir(), "logs.zip")
	makeZipFile(t, p, []zipIn{
		{"a/one.jsonl", "{\"a\":1}\n{\"a\":2}\n"},
		{"two.jsonl", "{\"a\":3}\n"},
	})

	src := openZipSource(t, p, "")
	tmpDir := src.tmpDir
	recs, skips := drainSource(t, src)
	if len(recs) != 3 || len(skips) != 0 {
		t.Fatalf("应读到 3 条、0 跳过，实际 %d / %v", len(recs), skips)
	}
	if recs[0].Origin != "a/one.jsonl" || recs[2].Origin != "two.jsonl" {
		t.Fatalf("Origin 应指回包内路径，实际 %q / %q", recs[0].Origin, recs[2].Origin)
	}
	if !numEq(recs[2].Doc["a"], "3") {
		t.Fatalf("第二个文件的解析结果不对: %+v", recs[2].Doc)
	}
	if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
		t.Fatalf("Close 后临时目录必须清掉，实际仍存在: %s", tmpDir)
	}
}

// TestZipSourceSkipsBinary 包里混进图片：按文本读只会写进索引一堆乱码，必须跳过。
func TestZipSourceSkipsBinary(t *testing.T) {
	p := filepath.Join(t.TempDir(), "mix.zip")
	makeZipFile(t, p, []zipIn{
		{"ok.jsonl", "{\"a\":1}\n"},
		{"logo.png", "PNG\x00\x00\x00binarydata"},
	})

	recs, _ := drainSource(t, openZipSource(t, p, ""))
	if len(recs) != 1 || recs[0].Origin != "ok.jsonl" {
		t.Fatalf("二进制文件应被跳过、只留文本文件，实际 %+v", recs)
	}
}

func TestZipSourceAllBinaryErrors(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bin.zip")
	makeZipFile(t, p, []zipIn{{"a.bin", "\x00\x01\x02"}})
	if _, err := newZipSource(p, ""); err == nil {
		t.Fatal("包里没有可用文本文件时应直接报错，而不是静默导 0 条")
	}
}

// TestZipSourceZipSlip 条目名带 ../ 想写到临时目录之外：必须忽略，且不影响其余文件。
func TestZipSourceZipSlip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "evil.zip")
	makeZipFile(t, p, []zipIn{
		{"../escaped.txt", "gotcha"},
		{"ok.jsonl", "{\"a\":1}\n"},
	})

	recs, _ := drainSource(t, openZipSource(t, p, ""))
	if len(recs) != 1 || recs[0].Origin != "ok.jsonl" {
		t.Fatalf("越界条目应被忽略、正常文件仍要处理，实际 %+v", recs)
	}
	if _, err := os.Stat(filepath.Join(dir, "escaped.txt")); !os.IsNotExist(err) {
		t.Fatal("越界条目绝不能真的写到包外")
	}
}

func TestTarGzSource(t *testing.T) {
	p := filepath.Join(t.TempDir(), "logs.tar.gz")
	makeTarGzFile(t, p, []zipIn{
		{"nested/app.log", "[2026-10-14 01:00:00] GET /a\n[2026-10-14 01:00:01] GET /b\n"},
		{"data.json", "[{\"x\":1}]"},
	})

	recs, skips := drainSource(t, openZipSource(t, p, ""))
	if len(recs) != 3 || len(skips) != 0 {
		t.Fatalf("应读到 3 条、0 跳过，实际 %d / %v", len(recs), skips)
	}
	if recs[0].Text == "" || recs[0].Origin != "nested/app.log" {
		t.Fatalf("文本记录应带原始文本与来源: %+v", recs[0])
	}
	if !numEq(recs[2].Doc["x"], "1") {
		t.Fatalf("包内 JSON 数组文件应被正确识别: %+v", recs[2].Doc)
	}
}

// TestOpenSourceDispatch 入口按扩展名分派：压缩包走解压，其余按内容。
func TestOpenSourceDispatch(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.zip")
	makeZipFile(t, p, []zipIn{{"a.jsonl", "{\"a\":1}\n"}})
	src, err := openSource(p, "")
	if err != nil {
		t.Fatalf("openSource 应分派到压缩包读取器: %v", err)
	}
	if _, ok := src.(*zipSource); !ok {
		t.Fatalf("zip 文件应得到 *zipSource，实际 %T", src)
	}
	src.Close()

	src2, err := openSource(writeTemp(t, "plain.log", "hello\n"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer src2.Close()
	if _, ok := src2.(*textLineSource); !ok {
		t.Fatalf("普通日志应得到 *textLineSource，实际 %T", src2)
	}
}

func TestZipSourceEmptyPackage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "empty.zip")
	makeZipFile(t, p, []zipIn{})
	if _, err := newZipSource(p, ""); err == nil || !strings.Contains(err.Error(), "没有可导入") {
		t.Fatalf("空包应给出明确原因，实际 %v", err)
	}
}

// TestZipEntryMatch 过滤规则语义表：文件名匹配（含 * / ? 用通配符、否则子串）、
// 大小写不敏感、规则带 / 时按完整条目路径匹配。
func TestZipEntryMatch(t *testing.T) {
	cases := []struct {
		name, filter string
		want         bool
	}{
		{"2026-10-13/ccysh_access_2026-10-13.log", "", true},
		{"2026-10-13/ccysh_access_2026-10-13.log", "access", true},
		{"2026-10-13/ccysh_error_2026-10-13.log", "access", false},
		{"2026-10-13/ccysh_error_2026-10-13.log", "*error*.log", true},
		{"2026-10-13/ccysh_error_2026-10-13.log", "*access*.log", false},
		{"A_ACCESS.log", "access", true},          // 大小写不敏感
		{"2026-10-13/a.log", "2026-10-13/*", true}, // 带 / 按全路径
		{"2026-10-14/a.log", "2026-10-13/*", false},
		{"xx/access_error.log", "access", true},
	}
	for _, c := range cases {
		if got := zipEntryMatch(c.name, c.filter); got != c.want {
			t.Errorf("zipEntryMatch(%q, %q) = %v, want %v", c.name, c.filter, got, c.want)
		}
	}
}

// TestZipSourceFilterKeepsAccessOnly 用户场景：包内 access / error 同日混放，
// 只要格式准确的 access 日志 —— 过滤后只保留命中条目，不命中的连解压都不做。
func TestZipSourceFilterKeepsAccessOnly(t *testing.T) {
	p := filepath.Join(t.TempDir(), "logs.zip")
	makeZipFile(t, p, []zipIn{
		{"2026-10-13/ccysh_access_2026-10-13.log", "GET /a\n"},
		{"2026-10-13/ccysh_error_2026-10-13.log", "ERROR boom\n"},
		{"2026-10-13/cmdb_access_2026-10-13.log", "GET /b\n"},
	})
	src := openZipSource(t, p, "*access*.log")
	if src.total != 3 || src.marked != 1 || len(src.files) != 2 {
		t.Fatalf("过滤统计不对: total=%d marked=%d kept=%d", src.total, src.marked, len(src.files))
	}
	recs, _ := drainSource(t, src)
	if len(recs) != 2 || recs[0].Origin != "2026-10-13/ccysh_access_2026-10-13.log" {
		t.Fatalf("应只保留 access 文件: %+v", recs)
	}
}

// TestZipSourceFilterSubstring 不含通配符按子串匹配（大小写不敏感）。
func TestZipSourceFilterSubstring(t *testing.T) {
	p := filepath.Join(t.TempDir(), "logs.zip")
	makeZipFile(t, p, []zipIn{
		{"A_ERROR.log", "e\n"},
		{"b_access.log", "a\n"},
	})
	recs, _ := drainSource(t, openZipSource(t, p, "error"))
	if len(recs) != 1 || recs[0].Origin != "A_ERROR.log" {
		t.Fatalf("子串匹配应只留 ERROR 文件: %+v", recs)
	}
}

// TestZipSourceFilterNoMatch 过滤后一个都不剩：报错要带规则与包内条目样例，方便改规则。
func TestZipSourceFilterNoMatch(t *testing.T) {
	p := filepath.Join(t.TempDir(), "logs.zip")
	makeZipFile(t, p, []zipIn{{"app.log", "x\n"}})
	if _, err := newZipSource(p, "nope*"); err == nil ||
		!strings.Contains(err.Error(), "没有匹配") || !strings.Contains(err.Error(), "app.log") {
		t.Fatalf("无匹配要报错并给出条目样例，实际 %v", err)
	}
}

// TestZipSourceFilterBadGlob 非法通配符（未闭合字符类）要当面报错，不能静默当没命中。
func TestZipSourceFilterBadGlob(t *testing.T) {
	p := filepath.Join(t.TempDir(), "logs.zip")
	makeZipFile(t, p, []zipIn{{"app.log", "x\n"}})
	if _, err := newZipSource(p, "*[abc"); err == nil || !strings.Contains(err.Error(), "通配符") {
		t.Fatalf("非法通配符应报错，实际 %v", err)
	}
}
