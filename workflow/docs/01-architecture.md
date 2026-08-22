# 01 — 系统架构设计

## 1. 架构决策：agentless（SSH 直连）

**结论：一期不部署任何常驻 Agent，管理端通过 SSH 通道完成全部远程操作。**

决策依据：

1. **bootstrap 通道问题**：平台首要场景是新服务器初始化，空机器上无法预置 Agent，
   SSH 是唯一且必然的首发通道。既然必须建 SSH 层，就让它成为唯一通道，
   避免"SSH + Agent"双通道带来的状态不一致与维护成本。
2. **任务形态匹配**：基建任务（初始化、装软件、采集）都是短时、幂等脚本，
   SSH 会话内同步执行 + 实时回显即可；断线失败重跑即可，不需要 Agent 的任务驻留能力。
3. **组件最少化**：管基建的工具自身必须最简单。单二进制 + SQLite，
   目标机零安装、零侵入——新主机录入即用。

**Agent 演进路径（三期备选）**：若后续需要秒级实时指标或抗断线长任务，
新增轻量 Go Agent，且作为"中间件产品库"中的一个产品，通过平台自身的
SSH 安装能力部署（自举），不引入新的分发通道。

## 2. 部署形态

```
管理机（阿里云 ECS 或内网任意 Linux/Windows）
├── infra-ops            # 单二进制，systemd/计划任务托管
├── config.yaml          # 配置（env 优先、yaml 兜底，同 ops-platform 约定）
└── data/infra-ops.db    # SQLite，全部状态（主机/凭据/审计）
```

- 单实例运行，监听一个 HTTP 端口（默认 8090）。
- 前端静态资源 go:embed 打包，浏览器访问即完整系统。
- 备份 = 拷贝 db 文件 + config.yaml。

## 3. 运行时组件

```
┌──────────────────── infra-ops 进程 ────────────────────┐
│                                                        │
│  HTTP Server (Gin)                                     │
│  ├── 静态资源（embed：Vue3 页面）                        │
│  ├── /api/v1/*  REST                                   │
│  └── 中间件：登录会话 / 审计拦截 / recovery              │
│                                                        │
│  业务协程                                               │
│  ├── probe 巡检：周期 SSH 连通性+资源采集（并发上限 5）   │
│  └── （二期）task worker：批量任务执行与日志流            │
│                                                        │
│  基础层                                                 │
│  ├── sshx 通道层：Dial/Run/Collect，连接超时 8s          │
│  ├── crypto：AES-256-GCM 凭据加解密                     │
│  └── store：SQLite（WAL 模式）                          │
└────────────────────────────────────────────────────────┘
        │ SSH:22（密钥认证，平台托管私钥）
   ┌────┴─────┬──────────┬───────────┬─────────────┐
nginx-01/02  harbor-01  k8s-master  k8s-node-01..N  standby-01
```

## 4. 关键运行策略

1. **巡检**：默认 60s 一轮，对全部主机并发探测（上限 5 并发），
   成功则刷新 status=online、latency 与 info_json 快照；失败标记 offline。
   巡检为后台协程，与 API 请求互不阻塞；手动"连接测试"与巡检共用同一采集函数。
2. **SSH 连接不复用长连接**：每次操作独立 Dial（基建操作低频，简单可靠优先），
   避免后台长连接在 NAT/防火墙下的保活复杂度。二期任务引擎再评估连接池。
3. **并发安全**：所有对主机的写操作（二期任务）经任务队列串行化到单主机维度，
   同一主机同时只允许一个执行中任务（一期无任务，此规则预埋）。

## 5. 与 ops-platform 的边界

- infra-ops 不做：构建、镜像推送、K8s 应用发布、Nginx 站点配置下发。
- ops-platform 不做：服务器初始化、软件安装、主机资产管理。
- 交集处理：K8s 节点本身的系统层维护（装 kubelet、系统调优）归 infra-ops；
  集群内资源（Pod/Deployment/Ingress）归 ops-platform。

## 6. 技术栈总览

详见 `docs/02-tech-selection.md`。结论速览：
Go 1.23 + Gin + golang.org/x/crypto/ssh + modernc.org/sqlite（纯 Go 无 cgo）
+ Vue3/Element Plus 全局构建（无构建链）+ SQLite(WAL)。
