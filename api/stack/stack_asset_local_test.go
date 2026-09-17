package stack

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"infra-ops/model"
)

// 资产就绪检测以「本地文件」为唯一判据：套件声明的 key/version/file_name 在资产目录里命中即就绪。
// 这条口径让手工放进 data/assets 的离线包同样被认（缺登记时由 localAsset 补登记，使部署分发取得到）。
func TestScanLocalAsset(t *testing.T) {
	base := t.TempDir()
	need := model.StackAssetNeed{
		Key: "rabbitmq_delayed_message_exchange", Version: "3.13.0",
		FileName: "rabbitmq_delayed_message_exchange-3.13.0.ez",
	}
	dir := filepath.Join(base, need.Key, need.Version)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	// 1. 文件不存在 → 未命中（未就绪）
	if _, err := scanLocalAsset(base, need); err == nil {
		t.Fatal("文件缺失时应返回错误（未就绪）")
	}

	// 2. 空文件 → 未命中（0 字节残留不算可用资产）
	if err := os.WriteFile(filepath.Join(dir, need.FileName), nil, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := scanLocalAsset(base, need); err == nil {
		t.Fatal("空文件不应视为就绪")
	}

	// 3. 同名目录 → 未命中
	_ = os.Remove(filepath.Join(dir, need.FileName))
	if err := os.Mkdir(filepath.Join(dir, need.FileName), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := scanLocalAsset(base, need); err == nil {
		t.Fatal("同名目录不应视为就绪")
	}
	_ = os.Remove(filepath.Join(dir, need.FileName))

	// 4. 正常文件 → 命中，size/sha256 按内容现场计算
	body := []byte("PK\x03\x04 fake ez payload")
	if err := os.WriteFile(filepath.Join(dir, need.FileName), body, 0o640); err != nil {
		t.Fatal(err)
	}
	a, err := scanLocalAsset(base, need)
	if err != nil {
		t.Fatalf("文件存在时应就绪: %v", err)
	}
	sum := sha256.Sum256(body)
	if a.AssetKey != need.Key || a.Version != need.Version || a.FileName != need.FileName {
		t.Fatalf("元数据不符: %+v", a)
	}
	if a.SizeBytes != int64(len(body)) || a.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("大小/摘要不符: size=%d sha=%s", a.SizeBytes, a.SHA256)
	}
	if a.Source != "local" {
		t.Fatalf("来源应为 local（本地目录已有），实际 %q", a.Source)
	}

	// 5. 内容变化 → 摘要与大小随之重算（防拿旧记录冒充本地文件）
	body2 := append(body, 'x')
	if err := os.WriteFile(filepath.Join(dir, need.FileName), body2, 0o640); err != nil {
		t.Fatal(err)
	}
	a2, err := scanLocalAsset(base, need)
	if err != nil {
		t.Fatal(err)
	}
	if a2.SHA256 == a.SHA256 || a2.SizeBytes != int64(len(body2)) {
		t.Fatal("文件内容变化后摘要/大小未重算")
	}

	// 6. 非法资产标识（路径穿越）→ 拒绝
	if _, err := scanLocalAsset(base, model.StackAssetNeed{Key: "../etc", Version: "1", FileName: "p"}); err == nil {
		t.Fatal("非法资产标识应被拒绝")
	}
}
