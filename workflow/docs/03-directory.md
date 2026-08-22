# 03 — 目录规划

> 顶层结构为用户既定约定：`main.go` 位于最外层，一级目录依次为
> api / common / model / store / config / router / template / doc / workflow / script。
> 本文档定义各目录职责与依赖方向；新增或变更目录须先更新本文档再落地。

## 1. 目录树

```
infra-ops/
├── main.go                  # 最外层入口：加载配置→初始化DB→装配路由→启动HTTP+巡检
├── api/                     # HTTP 处理 + 业务逻辑（按资源分文件，无独立 service 层）
│   ├── auth.go              # 登录/登出/当前用户
│   ├── host.go              # 主机 CRUD、连接测试、信息采集编排
│   ├── credential.go        # 凭据 CRUD
│   └── misc.go              # overview / audit-logs / healthz / version
├── common/                  # 公共基建（不感知业务）
│   ├── crypto/              # AES-256-GCM 加解密、主密钥装载
│   ├── sshx/                # SSH 通道层：Dial/Run/Collect、host key TOFU
│   ├── resp/                # 统一响应结构、错误码常量
│   ├── middleware/          # 登录会话 / 审计拦截 / recovery
│   └── probe/               # 心跳巡检协程（main 注入依赖后启动）
├── model/                   # 纯数据结构（无业务方法）
│   ├── host.go
│   ├── credential.go
│   └── audit.go
├── store/                   # SQLite 存取层（无业务逻辑）
│   ├── db.go                # 打开连接、WAL、迁移调度
│   ├── migrations.go        # 版本化 DDL
│   ├── host_repo.go
│   ├── credential_repo.go
│   └── audit_repo.go
├── config/                  # 配置加载（env 优先、yaml 兜底）
│   └── config.go
├── router/                  # 路由表 + 中间件装配（无业务逻辑）
│   └── router.go
├── template/                # 前端页面资源（go:embed 整体打进二进制）
│   ├── embed.go             # //go:embed index.html static，导出 FS
│   ├── index.html           # 单页入口（hash 路由）
│   └── static/
│       ├── app.js           # 应用装配、路由、axios 封装
│       ├── style.css
│       ├── pages/           # login.js / overview.js / hosts.js /
│       │                    # credentials.js / audit.js
│       └── vendor/          # vue.global.prod.js / element-plus /
│                            # element-plus-zh / axios（本地化，不依赖外网 CDN）
├── doc/                     # 功能描述文档（设计文档 01-07，编码依据）
├── workflow/                # 长任务规划（workflow.md 总规划 + boundaries/rules.md 边界）
├── script/                  # 交叉编译脚本、keygen/gen-password 用法说明
├── config.yaml.example      # 配置样例（真实 config.yaml 不入库）
├── .gitignore
├── go.mod                   # module github.com/Anesthesia-fanruoxi/infra-ops
└── README.md
```

## 2. 各目录职责

| 目录 | 职责 | 禁止事项 |
|------|------|----------|
| main.go | 组装与启动：config→store→依赖注入→router→HTTP+probe | 不写业务逻辑 |
| api | 参数绑定校验 + 业务逻辑 + 编排 store/common | 不写 SQL、不直连 SSH、不加解密 |
| common/crypto | 加解密与主密钥装载 | 不感知凭据业务含义 |
| common/sshx | SSH 通道：Dial/Run/Collect（三期加 Interactive） | 不 import model/store |
| common/resp | 统一响应与错误码 | 无逻辑判断 |
| common/middleware | 会话校验、审计落库、recovery | 审计规则集中于此，不散落 |
| common/probe | 周期巡检：并发探测+采集+刷新状态 | 依赖由 main 注入，不自行装载 |
| model | 纯数据结构 + 常量 | 无业务方法（简单校验除外） |
| store | SQL 存取、迁移 | 无业务判断 |
| config | 配置结构、env/yaml 装载、keygen 辅助 | 不含业务 |
| router | 路由表、中间件挂载顺序 | 无业务逻辑 |
| template | 前端静态资源 + embed 声明 | 不放任务脚本模板 |

## 3. 依赖方向（强制，对应 rules.md §2）

```
main.go → config / store / router / common(probe)
router  → api + common/middleware + common/resp
api     → store · common/crypto · common/sshx · common/resp · model
probe   → store · common/crypto · common/sshx（由 main 注入）
store   → model          config → 无依赖（被 main 最先加载）
common 子包之间：middleware → resp，其余互不依赖
template → 仅被 main.go 以 embed FS 引入，无 Go 逻辑
```

禁止反向 import；common/sshx、common/crypto、store 三者互不依赖。

## 4. 文件行数控制

每文件 ≤300 行（rules.md §1.1）。预计最先触限的是 api/host.go（CRUD+测试+采集）
与 store/migrations.go，届时按"资源 / 迁移版本"维度拆分新文件，不机械切行。

## 5. 命名约定

- Go 包名小写单词；文件 snake_case；api 层按资源单词命名（host.go）。
- 前端页面文件与路由名一致（hosts.js ↔ #/hosts）。
- 配置键 snake_case；环境变量统一前缀 `INFRA_OPS_`（INFRA_OPS_SECRET、
  INFRA_OPS_PORT 等），映射关系集中在 config 包维护。

## 6. 二期任务模板的存放位置

一键初始化/中间件安装的脚本模板**存数据库 templates 表**（带版本与参数 schema，
见 doc/05-database.md §4），不占文件系统目录；template/ 目录为前端资源专用。
