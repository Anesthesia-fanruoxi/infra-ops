//go:build ignore

package main

import (
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"
)

func main() {
	// 1. 读取凭据密文
	db, err := sql.Open("sqlite", "data/infra-ops.db")
	if err != nil {
		fmt.Println("open db err:", err)
		os.Exit(1)
	}
	defer db.Close()

	var username, encSecret string
	err = db.QueryRow("SELECT username, encrypted_secret FROM credentials WHERE id=1").Scan(&username, &encSecret)
	if err != nil {
		fmt.Println("query credential err:", err)
		os.Exit(1)
	}

	// 2. 解密（复刻 crypto.Service.Decrypt），密钥从 config.yaml 读取
	cfgData, _ := os.ReadFile("config.yaml")
	var cfgFile struct {
		Security struct {
			SecretKey string `yaml:"secret_key"`
		} `yaml:"security"`
	}
	yaml.Unmarshal(cfgData, &cfgFile)
	secretKeyB64 := cfgFile.Security.SecretKey
	key, _ := base64.StdEncoding.DecodeString(secretKeyB64)
	// encrypted_secret 存的是原始密文字节（nonce+ciphertext+tag），非 base64
	raw := []byte(encSecret)
	if len(raw) < 13 {
		fmt.Println("raw 数据过短:", len(raw))
		os.Exit(1)
	}
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	nonce := raw[:12]
	ciphertext := raw[12:]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		fmt.Println("decrypt err:", err)
		os.Exit(1)
	}
	fmt.Printf("解密密码: %s\n", string(plain))

	// 3. 读取已保存的 host key 指纹（TOFU）
	var savedFP string
	db.QueryRow("SELECT fingerprint FROM host_keys WHERE addr='192.168.6.2:22'").Scan(&savedFP)
	fmt.Printf("数据库指纹: %s\n", savedFP)

	// 4. TOFU 校验 + SSH 连接
	cfg := &ssh.ClientConfig{
		User:    username,
		Auth:    []ssh.AuthMethod{ssh.Password(string(plain))},
		Timeout: 8 * time.Second,
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			fp := ssh.FingerprintSHA256(key)
			fmt.Printf("服务器实际指纹: %s\n", fp)
			if savedFP == "" {
				fmt.Println("首次连接，保存指纹")
				return nil
			}
			if savedFP != fp {
				return fmt.Errorf("host key mismatch: expected %s, got %s", savedFP, fp)
			}
			fmt.Println("指纹匹配 OK")
			return nil
		},
	}

	conn, err := ssh.Dial("tcp", "192.168.6.2:22", cfg)
	if err != nil {
		fmt.Println("SSH FAIL:", err)
		os.Exit(1)
	}
	defer conn.Close()
	fmt.Println("SSH OK")

	session, err := conn.NewSession()
	if err != nil {
		fmt.Println("new session FAIL:", err)
		os.Exit(1)
	}
	defer session.Close()
	out, _ := session.Output("hostname")
	fmt.Println("hostname:", string(out))
}
