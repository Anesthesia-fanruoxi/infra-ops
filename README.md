# infra-ops

基建运维平台：管理服务器接入、巡检、初始化、中间件安装。

## 特性

- **agentless**：SSH 直连，无需在目标机部署任何组件
- **单二进制**：Go 编译 + 前端 embed，一个文件即完整系统
- **SQLite**：零依赖数据库，WAL 模式，备份 = 拷贝文件
- **凭据安全**：AES-256-GCM 加密落库，主密钥 env 注入
- **操作审计**：所有写操作自动留痕

## 快速开始

### 1. 生成主密钥

```bash
# 方式一：运行 Go 代码
go run -e 'package main; import ("crypto/rand"; "encoding/base64"; "fmt"); func main() { k := make([]byte, 32); rand.Read(k); fmt.Println(base64.StdEncoding.EncodeToString(k)) }'

# 方式二：openssl
openssl rand -base64 32
```

### 2. 生成管理员密码哈希

```bash
# 在 Go 中执行
go run -e 'package main; import ("fmt"; "golang.org/x/crypto/bcrypt"); func main() { h, _ := bcrypt.GenerateFromPassword([]byte("your-password"), bcrypt.DefaultCost); fmt.Println(string(h)) }'
```

### 3. 配置

复制 `config.yaml.example` 为 `config.yaml`，填入主密钥和密码哈希：

```yaml
security:
  secret_key: "<生成的主密钥>"
auth:
  username: admin
  password_hash: "<生成的bcrypt哈希>"
```

也可通过环境变量覆盖：

```bash
export INFRA_OPS_SECRET="<主密钥>"
export INFRA_OPS_PASSWORD_HASH="<bcrypt哈希>"
```

### 4. 运行

```bash
# 开发运行
go run .

# 或编译后运行
go build -o infra-ops .
./infra-ops
```

默认监听 `:8090`，浏览器访问 `http://localhost:8090`。

### 5. 交叉编译 Linux

```bash
bash script/build.sh
```

产出 `dist/infra-ops`（Linux amd64 二进制）。

## 目录结构

```
main.go          # 入口
api/             # HTTP 处理 + 业务逻辑
common/          # 公共基建（crypto/sshx/resp/middleware/probe）
model/           # 纯数据结构
store/           # SQLite 存取
config/          # 配置加载
router/          # 路由装配
template/        # 前端（go:embed）
```

## 技术栈

- Go 1.23 + Gin
- SQLite（modernc.org/sqlite，纯 Go 无 cgo）
- Vue3 + Element Plus（全局构建版，无 npm）
- AES-256-GCM 凭据加密

## API

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | /api/v1/auth/login | 登录 |
| POST | /api/v1/auth/logout | 登出 |
| GET | /api/v1/hosts | 主机列表 |
| POST | /api/v1/hosts | 新增主机 |
| POST | /api/v1/hosts/{id}/test | 连接测试 |
| GET | /api/v1/credentials | 凭据列表 |
| POST | /api/v1/credentials | 新增凭据 |
| GET | /api/v1/overview | 总览 |
| GET | /api/v1/audit-logs | 操作日志 |
| GET | /api/v1/healthz | 存活探针 |
