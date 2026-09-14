package kafka

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
)

// defaults.go：Kafka 参数默认值补全（原 api/stack/stack_topo.go 的 randomKafkaClusterID
// 与 stack_create.go 的 kafka 特判原样迁入）。
//
// KRaft 模式必须显式指定集群 ID（首次 format 存储时写入），未填则自动生成一个
// 与 `kafka-storage.sh random-uuid` 同构的 ID（16 字节 base64url 去填充），
// 使多台 broker 使用同一 cluster id 组成集群。

// Defaults 实现 stackkit.DefaultsProvider：就地写入参数默认值。
func (d *Driver) Defaults(op, mode string, params map[string]string) error {
	if op != "create" || mode != "kraft" || strings.TrimSpace(params["cluster_id"]) != "" {
		return nil
	}
	id, err := randomClusterID()
	if err != nil {
		return fmt.Errorf("生成 Kafka 集群 ID 失败: %w", err)
	}
	params["cluster_id"] = id
	return nil
}

// randomClusterID 生成与 kafka-storage.sh random-uuid 同构的集群 ID。
func randomClusterID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
