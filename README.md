# infra-ops

基建运维平台：管理服务器接入、巡检、初始化、中间件安装。

## 特性

- **agentless**：SSH 直连，无需在目标机部署任何组件
- **单二进制**：Go 编译 + 前端 embed，一个文件即完整系统
- **SQLite**：零依赖数据库，WAL 模式，备份 = 拷贝文件；全部运行配置持久化于 settings 表
- **零配置启动**：首次启动自动生成主密钥与默认账号，无需手写配置文件
- **凭据安全**：AES-256-GCM 加密落库；首次登录强制修改默认密码
- **操作审计**：所有写操作自动留痕

## 快速开始

### 1. 运行

```bash
# 开发运行
go run .

# 或编译后运行
go build -o infra-ops .
./infra-ops
```

### 2. 首次登录

首次启动自动完成初始化（生成 AES 主密钥、创建默认账号），日志输出：

```
首次启动已完成初始化，默认账号 admin / admin123，请登录后立即修改密码
```

浏览器访问 `http://localhost:8090`，使用 `admin / admin123` 登录后系统会强制要求修改密码，修改完成前其他接口均不可用。

数据目录固定为 `data/infra-ops.db`（SQLite，WAL 模式）。监听端口等全部配置持久化于库内 settings 表，修改后重启生效。

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
| POST | /api/auth/login | 登录 |
| POST | /api/auth/logout | 登出 |
| GET | /api/auth/me | 当前用户 |
| POST | /api/auth/password | 修改密码 |
| POST | /api/hosts/batch | 批量新增主机 |
| POST | /api/hosts/{id}/test | 连接测试 |
| GET | /api/sse/hosts | 主机状态实时推送 |
| GET | /api/healthz | 存活探针 |
