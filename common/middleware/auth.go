// Package middleware 提供会话校验、审计拦截等公共中间件。
package middleware

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/common/resp"
)

// SessionCookie Cookie 名称（导出供 api 层引用）。
const SessionCookie = "infra_ops_session"

// sessionTTL 会话有效期。
const sessionTTL = 12 * time.Hour

// SessionStore 无状态签名会话：token 用主密钥 HMAC 签名，服务重启不失效，无需落库。
type SessionStore struct {
	secret []byte
}

// NewSessionStore 创建会话存储，secretKey 复用主密钥。
func NewSessionStore(secretKey string) *SessionStore {
	return &SessionStore{secret: []byte(secretKey)}
}

// Create 签发签名 token，格式 base64(payload).base64(hmac)，payload=username|expireUnix。
func (s *SessionStore) Create(username string) string {
	payload := fmt.Sprintf("%s|%d", username, time.Now().Add(sessionTTL).Unix())
	payloadB64 := base64.RawURLEncoding.EncodeToString([]byte(payload))
	return payloadB64 + "." + s.sign(payloadB64)
}

// Validate 校验签名与有效期，返回 username。
func (s *SessionStore) Validate(token string) (string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return "", false
	}
	payloadB64, sig := parts[0], parts[1]
	if !hmac.Equal([]byte(s.sign(payloadB64)), []byte(sig)) {
		return "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return "", false
	}
	seg := strings.Split(string(payload), "|")
	if len(seg) != 2 {
		return "", false
	}
	expire, err := strconv.ParseInt(seg[1], 10, 64)
	if err != nil || time.Now().Unix() > expire {
		return "", false
	}
	return seg[0], true
}

// Delete 无状态会话无需服务端吊销，登出仅需清除 Cookie。
func (s *SessionStore) Delete(token string) {}

// sign 计算 payload 的 HMAC-SHA256 签名。
func (s *SessionStore) sign(payloadB64 string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(payloadB64))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Auth 登录会话校验中间件。
func Auth(store *SessionStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(SessionCookie)
		if err != nil || token == "" {
			resp.ErrHTTP(c, http.StatusUnauthorized, resp.CodeUnauthorized, "未登录")
			c.Abort()
			return
		}
		username, ok := store.Validate(token)
		if !ok {
			resp.ErrHTTP(c, http.StatusUnauthorized, resp.CodeUnauthorized, "会话已过期")
			c.Abort()
			return
		}
		c.Set("username", username)
		c.Next()
	}
}

func generateToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
