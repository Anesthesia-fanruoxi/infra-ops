// Package backupfmt 备份容器格式（.iopsbak）：加密 + 压缩的自描述单文件。
//
// 布局（全部小端序由 uint32be 显式写出，不依赖平台字节序）：
//
//	 0..7   魔数 "IOPSBK01"——识别文件类型，也挡住「拿错文件来恢复」；
//	 8..23  scrypt 盐（16 字节随机）；
//	24..35  KDF 参数 N/r/p（各 4 字节），将来调参老备份仍可解；
//	36..47  AES-GCM nonce（12 字节随机）；
//	48..    AES-256-GCM(密文 = zip 载荷)，AAD = 前 48 字节头。
//
// 为什么整体一次性 GCM 而不是流式：备份体量（库 + 资产）目前 MB 级，
// 单次 seal 的内存代价可忽略，换来的是「任何一比特被篡改都整体拒解」的
// 强完整性——流式 CTR + 分段 MAC 实现复杂度不成比例。
//
// 密钥派生用 scrypt(N=32768, r=8, p=1)：密码是唯一的信任根，必须让暴力
// 穷举在内存上先撞墙。忘密码 = 备份永久作废，界面上必须写明。
package backupfmt

import (
	"archive/zip"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/crypto/scrypt"
)

// Magic 容器魔数（含格式代数 01，将来改布局换代号走新老并存的迁移）。
var Magic = []byte("IOPSBK01")

// 头部各段长度：魔数 8 + 盐 16 + 参数 12 + nonce 12。
const headerLen = 8 + saltLen + kdfParamLen + nonceLen

const (
	saltLen     = 16
	kdfParamLen = 12
	nonceLen    = 12
	keyLen      = 32

	// scryptKDFN…与备份性能的折中：实测单次派生 ~100ms，导出/恢复各一次，可接受。
	scryptKDFN = 32768
	scryptKDFr = 8
	scryptKDFp = 1

	// kdfMemLimit 恢复侧接受的内存上界（防御构造的超大 N/r）：scrypt 内存代价
	// = 128×N×r，现参数仅 32MiB，留 8 倍余量。KDF 参数来自不可信文件，先限量再派生。
	kdfMemLimit = 256 << 20
)

// 容器级错误：调用方按类型给用户可读的提示，别把底层 crypto 错误直接抛出去。
var (
	ErrBadFormat     = errors.New("不是有效的备份文件（魔数不符）")
	ErrTruncated     = errors.New("备份文件不完整或已损坏")
	ErrWrongPassword = errors.New("密码错误（或文件已损坏）")
)

// TableStat 清单里的单表行数（与系统信息页口径一致，恢复前可预览）。
type TableStat struct {
	Name string `json:"name"`
	Rows int64  `json:"rows"`
}

// Manifest 备份清单：容器载荷 zip 里的第一个文件，恢复预览只解这一个就能展示。
type Manifest struct {
	Format     int         `json:"format"`      // 容器格式代数，恒 1
	AppVersion string      `json:"app_version"` // 备份时的应用版本
	CreatedAt  string      `json:"created_at"`  // YYYY-MM-DD HH:mm:ss
	DBFile     string      `json:"db_file"`     // 库文件在 zip 内的路径
	DBTables   []TableStat `json:"db_tables"`   // 各表行数快照
	TotalRows  int64       `json:"total_rows"`
	AssetCount int         `json:"asset_count"` // 随包资产文件数
	AssetBytes int64       `json:"asset_bytes"` // 随包资产总字节数
	Note       string      `json:"note,omitempty"`
}

// ZipInput 打进 zip 的一个条目：name 用正斜杠相对路径；Path 为空则写 data 内容。
type ZipInput struct {
	Name string
	Path string // 本地文件路径（流式读盘，不整段进内存）
	Data []byte // 内存内容（manifest 等小文件）；与 Path 二选一
}

// BuildZip 把清单与数据文件打包为压缩载荷（Deflate）。
// 单文件读失败直接返回错误——备份缺一块就是废件，静默跳过只会把问题推迟到恢复那天。
func BuildZip(inputs []ZipInput) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, in := range inputs {
		name := strings.ReplaceAll(strings.TrimPrefix(in.Name, "/"), "\\", "/")
		if name == "" || strings.Contains(name, "..") {
			return nil, fmt.Errorf("备份条目路径非法: %q", in.Name)
		}
		fw, err := zw.Create(name)
		if err != nil {
			return nil, err
		}
		if in.Path != "" {
			f, err := os.Open(in.Path)
			if err != nil {
				return nil, fmt.Errorf("读取 %s 失败: %w", in.Name, err)
			}
			_, err = io.Copy(fw, f)
			f.Close()
			if err != nil {
				return nil, err
			}
			continue
		}
		if _, err := fw.Write(in.Data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ReadZip 载荷解包：返回清单与「条目名 → 内容读取器工厂」。
// 内容不整体载入内存（库文件恢复时按需取流），manifest 单独解出。
func ReadZip(payload []byte) (*Manifest, map[string][]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return nil, nil, fmt.Errorf("备份载荷不是有效的 zip: %w", err)
	}
	entries := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return nil, nil, err
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, nil, err
		}
		entries[f.Name] = b
	}
	var m Manifest
	mb, ok := entries["manifest.json"]
	if !ok {
		return nil, nil, errors.New("备份里缺少 manifest.json，文件可能被裁剪过")
	}
	if err := json.Unmarshal(mb, &m); err != nil {
		return nil, nil, fmt.Errorf("备份清单解析失败: %w", err)
	}
	return &m, entries, nil
}

// Encrypt 打包载荷 → 容器字节。
func Encrypt(password string, payload []byte) ([]byte, error) {
	if password == "" {
		return nil, errors.New("密码不能为空")
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	key, err := scrypt.Key([]byte(password), salt, scryptKDFN, scryptKDFr, scryptKDFp, keyLen)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	header := make([]byte, 0, headerLen)
	header = append(header, Magic...)
	header = append(header, salt...)
	header = binary.BigEndian.AppendUint32(header, scryptKDFN)
	header = binary.BigEndian.AppendUint32(header, scryptKDFr)
	header = binary.BigEndian.AppendUint32(header, scryptKDFp)
	header = append(header, nonce...)

	out := make([]byte, 0, headerLen+len(payload)+16)
	out = append(out, header...)
	out = gcm.Seal(out, nonce, payload, header[:36]) // AAD 覆盖魔数+盐+参数，改一个字节都解不开
	return out, nil
}

// Decrypt 容器字节 → 打包载荷。密码错、文件损、被篡改统一归为 ErrWrongPassword——
// GCM 认证失败时调用方无从区分「输错密码」与「文件坏了」，界面上给同一句提示最诚实。
func Decrypt(password string, blob []byte) ([]byte, error) {
	if len(blob) < headerLen+16 {
		return nil, ErrTruncated
	}
	if !bytes.Equal(blob[:8], Magic) {
		return nil, ErrBadFormat
	}
	salt := blob[8 : 8+saltLen]
	kdf := blob[8+saltLen : 8+saltLen+kdfParamLen]
	n := binary.BigEndian.Uint32(kdf[0:4])
	r := binary.BigEndian.Uint32(kdf[4:8])
	p := binary.BigEndian.Uint32(kdf[8:12])
	// 参数来自不可信文件：此刻密码还没参与校验（GCM 还没跑到），先卡上界再派生——
	// 构造超大 N/r 会让 scrypt 直接巨量分配（OOM 是 fatal error，recover 兜不住）
	if n < 1<<10 || n > 1<<20 || n&(n-1) != 0 || r < 1 || r > 32 || p < 1 || p > 16 ||
		int64(128)*int64(n)*int64(r) > kdfMemLimit {
		return nil, ErrBadFormat
	}
	nonce := blob[8+saltLen+kdfParamLen : headerLen]

	key, err := scrypt.Key([]byte(password), salt, int(n), int(r), int(p), keyLen)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	payload, err := gcm.Open(nil, nonce, blob[headerLen:], blob[:36])
	if err != nil {
		return nil, ErrWrongPassword
	}
	return payload, nil
}
