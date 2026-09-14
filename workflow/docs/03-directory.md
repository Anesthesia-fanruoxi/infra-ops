# 03 — 目录规划

> 顶层结构为用户既定约定：`main.go` 位于最外层，一级目录依次为
> api / common / model / store / config / router / template / doc / workflow / script。
> 本文档定义各目录职责与依赖方向；新增或变更目录须先更新本文档再落地。

## 1. 目录树

```
infra-ops/
├── main.go                  # 最外层入口：加载配置→初始化DB→装配路由→启动HTTP+巡检
├── api/                     # HTTP 处理 + 业务逻辑（按资源分目录，无独立 service 层）
│   ├── shared/              # 跨模块共享层（主机变量注入 / SSE / 切片等）
│   ├── stack/               # 套件（集群）域：创建/执行/角色计划/探活/校验
│   ├── deploy/              # 部署模板与任务
│   ├── host/                # 主机 CRUD、连接测试、信息采集编排
│   ├── credential/          # 凭据 CRUD
│   ├── auth/                # 登录/登出/当前用户
│   ├── overview/            # 概览 / 审计日志 / healthz / version
│   ├── orchestration/       # 编排定义与执行
│   ├── sse/                 # 事件流
│   └── tool/                # 工具接入（registry / sftp / es）
├── common/                  # 公共基建（不感知业务）
│   ├── crypto/              # AES-256-GCM 加解密、主密钥装载
│   ├── sshx/                # SSH 通道层：Dial/Run/Collect、host key TOFU
│   ├── resp/                # 统一响应结构、错误码常量
│   ├── middleware/          # 登录会话 / 审计拦截 / recovery
│   └── probe/               # 心跳巡检协程（main 注入依赖后启动）
├── model/                   # 纯数据结构（无业务方法）
│   ├── host.go
│   ├── credential.go
│   ├── stack.go             # StackBlueprint / StackMode / StackPhase / RolePlan
│   └── audit.go
├── store/                   # SQLite 存取层 + 套件契约与实现
│   ├── db.go                # 打开连接、WAL、迁移调度
│   ├── migrations.go        # 版本化 DDL
│   ├── embed.go             # 唯一 embed 声明点：StackFS（套件脚本/配置）+ BuiltinFS（内置模板）
│   ├── registry.go          # 套件装配表：9 套件驱动登记 + Find/List + 启动自检（fail fast）
│   ├── builtin_stacks.go    # 套件薄壳：蓝图列表 / 按 key 查驱动 / 阶段脚本渲染（@@ASSET@@ 注入）
│   ├── builtin_templates.go # 内置部署模板（约 30 条定义）
│   ├── setting/             # 设置项存取
│   ├── repo/                # 资源仓储（host / credential / stack / deploy / es / sftp / ...）
│   ├── stackkit/            # 【契约层】Driver 接口 + 可选能力接口 + Roles 助手 + 注册表 + probe 模型
│   └── stacks/              # 【套件目录】每套件一个包，只依赖 model + store/stackkit
│       └── <key>/           #   <key>.go（Driver + 蓝图） plan.go register.go verify.go vars.go
│                            #   + scripts/ configs/（原样保留，经 go:embed 打入二进制）
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
│       │                    # credentials.js / audit.js / stacks.js（挂载壳）
│       ├── stacks/          # 【前端套件目录】registry.js + common/ + <key>/{index.js,style.css}
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
| store/stackkit | 套件契约层：Driver 接口 / 可选能力接口 / Roles 落点助手 / 注册表 / probe 模型 | 不 import store、不 import api/** |
| store/stacks/\<key\> | 单个套件：蓝图 + 阶段脚本 + 角色规划 + 服务登记 + 探活 + 变量注入 | 不 import store 包；套件之间不互相 import |
| config | 配置结构、env/yaml 装载、keygen 辅助 | 不含业务 |
| router | 路由表、中间件挂载顺序 | 无业务逻辑 |
| template | 前端静态资源 + embed 声明 | 不放任务脚本模板 |
| template/static/stacks | 前端套件目录：每套件独立 js/css，通用骨架在 common/ | 纯结构拆分，不改动渲染结果 |

## 3. 依赖方向（强制，对应 rules.md §2）

```
main.go → config / store / router / common(probe)
router  → api + common/middleware + common/resp
api     → store · common/crypto · common/sshx · common/resp · model
probe   → store · common/crypto · common/sshx（由 main 注入）
store   → model          config → 无依赖（被 main 最先加载）
common 子包之间：middleware → resp，其余互不依赖
template → 仅被 main.go 以 embed FS 引入，无 Go 逻辑

套件链路（强制，无环是可行性前提）：
model ← store/stackkit ← { store/stacks/<key>, store/registry.go } ← api/stack
```

禁止反向 import；common/sshx、common/crypto、store 三者互不依赖。
套件可选能力（RolePlanner / ServiceRegistrar / VarInjector / CreateValidator / ScaleGuard /
ScaleInGuard / RoleNamer / TopologyBuilder / DefaultsProvider / VerifyProbe / SelfPlanning /
BootstrapHostResolver / ScaleInDrainer）一律用**类型断言**调用，未实现即走引擎通用默认；
`api/stack` 不得出现任何套件分支（不判 key，也不判 mode）。

## 4. 文件行数控制

每文件尽量 ≤500 行（rules.md §1.1）。预计最先触限的是 api/host/（CRUD+测试+采集）
与 store/migrations.go，届时按"资源 / 迁移版本"维度拆分新文件，不机械切行。

任务 09 目录化拆分后的两个硬上限：`store/builtin_stacks.go` ≤120 行（薄壳：查找 / 渲染 / 转发）、
`template/static/pages/stacks.js` ≤120 行（挂载壳）。

## 5. 命名约定

- Go 包名小写单词；文件 snake_case；api 层按资源单词命名（host.go）。
- 前端页面文件与路由名一致（hosts.js ↔ #/hosts）。
- 配置键 snake_case；环境变量统一前缀 `INFRA_OPS_`（INFRA_OPS_SECRET、
  INFRA_OPS_PORT 等），映射关系集中在 config 包维护。

## 6. 二期任务模板的存放位置

一键初始化/中间件安装的脚本模板**存数据库 templates 表**（带版本与参数 schema，
见 doc/05-database.md §4），不占文件系统目录；template/ 目录为前端资源专用。

## 7. 套件目录（任务 09 目录化拆分终态）

「一个套件 = 一个目录」：后端 `store/stacks/<key>/` + 前端 `template/static/stacks/<key>/`。
9 个内置套件：redis / bigdata / kafka / elasticsearch / rabbitmq / rocketmq / nacos / powerjob / elfk。

### 7.1 后端

```
store/stackkit/            # 契约层：不依赖任何业务包（只 import model）
store/registry.go          # 装配表：9 行 stackkit.Register(<key>.New())，顺序即前端卡片顺序
store/stacks/<key>/        # 套件实现，只依赖 model + store/stackkit
```

`store/builtin_stacks.go` 是唯一薄壳（≤120 行），只做三件事：

- `ListStackBlueprints()`：按装配表顺序返回蓝图；
- `FindBuiltinStack(key) stackkit.Driver`：按 key 查驱动，不存在返回 nil（无兼容包装）；
- `LoadStackPhase(d, mode, phase)`：读内嵌脚本 + `@@ASSET@@` 资源注入（需访问 `StackFS`，故留在 store 包）。

引擎侧取模式走 `stackkit.ModeDef(d.Blueprint(), mode)`；角色规划、服务登记、探活、变量注入、
拓扑校验、缩容保护一律经能力接口断言委托给驱动（见 §3）。新套件接入只需：建目录 + 写驱动 +
在 `registry.go` 加一行。

### 7.2 前端

```
template/static/stacks/
├── registry.js            # 全局注册表与共享助手（P.register / P.splitList / …）
├── common/                # 通用骨架：registry / parts / labels / host-table / role-plan /
│                          #   instance-card / verify-dialog / run-log / wizard / assets / common.css
└── <key>/                 # 每套件一份 index.js + style.css（命名空间 .stk-<key简写>-*）
```

`template/index.html` 按套件分组静态引入（通用骨架 → 各套件 → `pages/stacks.js` 挂载壳）；
`pages/stacks.js` 只做挂载（≤120 行），套件组件在各自文件末尾 `P.register(key, {...})` 自注册，
加载顺序不敏感。ES 控制台（`pages/es_*.js`，9 个文件）是既有的拆分范式，不随本目录化改动。

### 7.3 校验入口

- `script/check_stack_lines.py`：行数与 `api/stack` 套件分支数检查（`--targets` 为终态硬门禁）；
- `go test ./store/ -run TestSnapshot`：蓝图 / 流水线 / 渲染产物三类字节基线；
- `go test ./api/stack/ -run TestSnapshot`：角色计划 / 登记服务 / 校验端点行为快照。
