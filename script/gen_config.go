//go:build ignore

package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

func main() {
	// 生成主密钥
	key := make([]byte, 32)
	rand.Read(key)
	secretKey := base64.StdEncoding.EncodeToString(key)
	fmt.Println("SECRET_KEY=" + secretKey)

	// 生成密码哈希（默认密码: admin123）
	hash, _ := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)
	fmt.Println("PASSWORD_HASH=" + string(hash))
}
