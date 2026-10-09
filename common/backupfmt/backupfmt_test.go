package backupfmt

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptDecryptRoundtrip(t *testing.T) {
	payload := []byte("manifest+db+assets 二进制载荷 \x00\x01\xff")
	blob, err := Encrypt("correct horse", payload)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// 头部指纹：魔数 + 随机盐/nonce（两次加密同载荷密文必须不同）
	if !bytes.HasPrefix(blob, Magic) {
		t.Fatal("容器缺少魔数")
	}
	blob2, _ := Encrypt("correct horse", payload)
	if bytes.Equal(blob, blob2) {
		t.Fatal("随机盐/nonce 未生效：同密码同载荷两次密文相同")
	}
	got, err := Decrypt("correct horse", blob)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("解密结果与原文不一致")
	}
}

func TestDecryptWrongPassword(t *testing.T) {
	blob, _ := Encrypt("p@ssw0rd123", []byte("secret"))
	if _, err := Decrypt("wrong-password", blob); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("错密码应归为 ErrWrongPassword，实际: %v", err)
	}
	// 篡改密文尾部一个字节（模拟损坏/改动）也必须拒解
	blob[len(blob)-1] ^= 0xff
	if _, err := Decrypt("p@ssw0rd123", blob); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("篡改后应拒解，实际: %v", err)
	}
}

func TestDecryptBadFormat(t *testing.T) {
	if _, err := Decrypt("x", []byte("这不是备份文件，是误选的普通文件内容，长度还必须超过头部........")); !errors.Is(err, ErrBadFormat) {
		t.Fatalf("魔数不符应归为 ErrBadFormat，实际: %v", err)
	}
	if _, err := Decrypt("x", []byte("IOPSBK01")); !errors.Is(err, ErrTruncated) {
		t.Fatalf("截断文件应归为 ErrTruncated，实际: %v", err)
	}
}

// TestDecryptRejectsHugeKDFParams 头部 KDF 参数来自不可信文件：构造超大 N/r
// 必须在密钥派生之前拒解——否则 scrypt 直接巨量分配，Go OOM 是 fatal 兜不住。
func TestDecryptRejectsHugeKDFParams(t *testing.T) {
	blob, err := Encrypt("pw", []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	kdf := blob[8+saltLen : 8+saltLen+kdfParamLen]
	binary.BigEndian.PutUint32(kdf[0:4], 1<<30) // N=2^30 时 scrypt 需 ~1TiB 内存
	binary.BigEndian.PutUint32(kdf[4:8], 8)
	binary.BigEndian.PutUint32(kdf[8:12], 1)
	if _, err := Decrypt("pw", blob); !errors.Is(err, ErrBadFormat) {
		t.Fatalf("超大 KDF 参数应归为 ErrBadFormat，实际: %v", err)
	}
	// 非 2 的幂的 N 同样在派生之前拒绝（统一归口，不透出 crypto 底层报错）
	binary.BigEndian.PutUint32(kdf[0:4], 12345)
	if _, err := Decrypt("pw", blob); !errors.Is(err, ErrBadFormat) {
		t.Fatalf("非法 N 应归为 ErrBadFormat，实际: %v", err)
	}
}

func TestZipRoundtrip(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "infra-ops.db")
	if err := os.WriteFile(big, bytes.Repeat([]byte("sqlite-page"), 4096), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{Format: 1, AppVersion: "test", DBFile: "db/infra-ops.db",
		DBTables: []TableStat{{Name: "hosts", Rows: 3}}}
	mb, _ := json.Marshal(manifest)
	payload, err := BuildZip([]ZipInput{
		{Name: "manifest.json", Data: mb},
		{Name: "db/infra-ops.db", Path: big},
		{Name: "../escape.txt", Data: []byte("x")},
	})
	if err == nil {
		t.Fatal("路径穿越条目必须被拒绝")
	}
	payload, err = BuildZip([]ZipInput{
		{Name: "manifest.json", Data: mb},
		{Name: "db/infra-ops.db", Path: big},
	})
	if err != nil {
		t.Fatalf("BuildZip: %v", err)
	}
	m, entries, err := ReadZip(payload)
	if err != nil {
		t.Fatalf("ReadZip: %v", err)
	}
	if m.DBFile != "db/infra-ops.db" || len(m.DBTables) != 1 || m.DBTables[0].Rows != 3 {
		t.Fatalf("清单往返不符: %+v", m)
	}
	if len(entries["db/infra-ops.db"]) == 0 {
		t.Fatal("库文件条目丢失")
	}
}

func TestBuildZipMissingFile(t *testing.T) {
	if _, err := BuildZip([]ZipInput{{Name: "a", Path: filepath.Join(t.TempDir(), "nope")}}); err == nil {
		t.Fatal("源文件缺失必须报错，不得静默产出缺块备份")
	}
}
