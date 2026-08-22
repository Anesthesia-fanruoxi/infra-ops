# 02 — 技术选型

> 每项给出结论与理由。结论一经确认不再反复；更换须更新本文档。

## 1. 语言：Go

| 维度 | Go | Python |
|------|-----|--------|
| 并发长连接（SSH/WS） | goroutine 原生适配 | 需 async 栈，混写复杂 |
| 部署 | 单二进制 + embed 前端 | 需 venv/依赖/gunicorn |
| 已有积累 | ops-platform Go Agent 可复用 | Flask 已踩过多坑 |

**结论：Go 1.23。** 无 cgo 依赖，保证 Windows 开发 / Linux 交叉编译一致。

## 2. Web 框架：Gin

理由：生态成熟、中间件机制清晰（登录/审计/recovery 全部走中间件）、
团队熟悉度高、性能足够。备选 chi（更轻但生态弱）、echo（与 Gin 等价，无理由切换）。

## 3. SSH：golang.org/x/crypto/ssh

官方扩展库，无第三方封装依赖。封装为 `common/sshx`：
`Dial`（建连）/ `Run`（执行单命令取输出）/ `Collect`（标准信息采集）/
后续 `Interactive`（Web SSH 预留，三期）。统一超时、host key TOFU、错误归类。

## 4. 数据库驱动：modernc.org/sqlite

**纯 Go 实现，无 cgo**，这是选型硬约束（保证交叉编译）。
不用 mattn/go-sqlite3（依赖 cgo 与 gcc）。不引入 ORM，
用 `database/sql` + 轻量手写查询，SQL 可控、符合分层约束。

## 5. 前端：Vue3 + Element Plus（无构建链 / cdn-vue 方式）

- 采用 Vue3 与 Element Plus 的**全局构建版（IIFE/global build）**，
  `<script>` 直接引入，无 npm/webpack/vite。
- 文件 **vendor 本地化**放入 `template/static/vendor/`，go:embed 打包，
  离线可用、不依赖外部 CDN 稳定性。
- 版本：Vue 3.4.x、Element Plus 2.x、axios 1.x（含中文 locale）。
- 交互复杂度足够支撑表单/表格/弹窗/消息提示；无需组件编译。

## 6. 配置：viper 或标准库

采用 **env 优先、config.yaml 兜底**（与 ops-platform 约定一致，便于容器化）。
轻量实现：gopkg.in/yaml.v3 读 yaml + os.Getenv 覆盖，不引入 viper 以减少依赖面。

## 7. 加密：标准库

AES-256-GCM（crypto/aes + cipher），bcrypt（golang.org/x/crypto/bcrypt）存登录口令。
不引入第三方加密库。

## 8. 依赖清单（预期 go.mod）

```
github.com/gin-gonic/gin
golang.org/x/crypto            # ssh + bcrypt
modernc.org/sqlite
gopkg.in/yaml.v3
```

## 9. 构建与运行

- 开发：`go run .`（Windows，main.go 位于根目录）。
- 发布：`GOOS=linux GOARCH=amd64 go build`，产出单二进制。
- 端口：默认 8090，可经 env `INFRA_OPS_PORT` 或 yaml 覆盖。
