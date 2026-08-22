# boundaries/rules.md — 编码边界与强制约定

> 本文件是硬性约束，任何代码提交前对照检查。新增规则须先写入本文件再实施。

## 1. 代码规模与文件边界

1. **单文件 ≤ 300 行**（含空行注释）。超限必须按职责拆分，拆分优先按
   "资源维度"（如 api/host.go / api/credential.go）而非机械切行。
2. 每个包（package）必须能用一句话说清职责，写在包注释里；说不清说明拆分有问题。

## 2. 分层约束（强制）

```
main.go → router → api → store(SQLite)
                     ↘ common/sshx · common/crypto
common/probe（巡检）→ store · sshx · crypto（由 main 注入依赖）
```

1. **api 层负责参数绑定校验、业务逻辑与对 store/common 的编排**，按资源分文件
   （api/host.go、api/credential.go 等），单文件 ≤300 行。
2. **api 三条红线**：禁止直接写 SQL（必须调用 store）；禁止直连 SSH（必须经
   common/sshx）；禁止直接加解密（必须经 common/crypto）。明文密钥材料不在
   api 多做停留。
3. **router 只负责路由表与中间件装配**，不得包含业务逻辑。
4. **store 只做数据存取**：返回 model 结构体或 error，禁止包含业务判断
   （如"凭据被引用则不可删"属于 api 层逻辑）。
5. **common/sshx 是纯通道层**：只暴露 Dial/Run/Collect/Interactive 四类能力，
   不感知主机、凭据等业务概念，入参全部显式传入（地址、认证、超时）。
6. **common/crypto 独立**：仅 api 层（及 main 注入的 probe）调用加解密，
   router/store/sshx 禁止接触明文密钥材料。
7. **common/resp、common/middleware 为公共基建**：统一响应、错误码映射、
   会话/审计/recovery 集中于中间件维护，禁止散落在业务代码里。

## 3. 安全红线（违反即打回）

1. 凭据明文（私钥/密码）**只允许存在于**：请求入参、内存变量、加密前的瞬间。
   落库必须经 crypto.Encrypt；任何 GET 接口禁止回传 secret 字段。
2. **日志脱敏**：error 日志禁止携带 secret、Authorization、完整命令行中的密钥内容；
   SSH 错误只记录 host:port 与错误类型。
3. 主密钥（secret_key）**禁止写入仓库任何文件**；config.yaml.example 中只留占位空串。
4. SQL 全部使用占位符参数，禁止字符串拼接。
5. SSH host key 策略：首次连接记录指纹（TOFU），后续连接指纹不一致必须失败并告警，
   禁止默认 InsecureIgnoreHostKey 上线（仅允许配置显式声明 insecure 且日志 WARN）。

## 4. API 约定

1. 前缀 `/api/v1`；路由、JSON 字段一律 snake_case；Go 结构体用 tag 映射。
2. 统一响应结构：`{ "code": 0, "message": "ok", "data": ... }`；
   code=0 成功，非 0 时 data 为 null。HTTP 状态码同步语义（401/404/409/500）。
3. 错误码分段：0 成功；40x 鉴权参数类；1001-1099 SSH 通道类；2001-2099 凭据类；
   3001-3099 主机类。新增错误码必须登记在 `docs/04-api.md`。
4. 列表接口统一分页参数 `page`（默认 1）、`page_size`（默认 20，上限 100），
   返回 `{ list, total, page, page_size }`。
5. **写操作必须落审计日志**（auth.*、host.*、credential.*），由中间件统一拦截，
   禁止在业务代码里零散手写。

## 5. 数据库约定

1. 表名 snake_case 复数；必备列：id、created_at、updated_at（TEXT，localtime）。
2. schema 变更只允许"新增表/新增列/新增索引"，启动时自动迁移
   （schema_migrations 版本号机制）；禁止在迁移中做破坏性 DROP/改类型。
3. 时间统一 `datetime('now','localtime')` 存储；JSON 快照列命名 `*_json`。

## 6. 前端约定

1. **无构建链**：Vue3 + Element Plus 采用全局构建版（vendor 本地化，不依赖外部 CDN），
   go:embed 随二进制发布。禁止引入 npm/webpack/vite。
2. 页面与路由结构见 `docs/07-frontend.md`；所有请求经统一 axios 封装（401 跳登录）。
3. 界面留白充足：表格行高 ≥52px、卡片间距 ≥20px、页面边距 ≥32px（用户明确偏好）。

## 7. Git 约定

1. 提交信息格式：`<type>(<scope>): <中文描述>`，type ∈ feat/fix/docs/refactor/test/chore。
2. 按 workflow 步骤粒度提交（一步一提交或一步多提交），禁止一次性倾倒式提交。
3. 每个阶段完成后打 tag：`v0.<阶段号>.0`。

## 8. 验证与测试约定

1. 对真实服务器仅允许只读验证命令（uptime/df/free/uname/hostname 级别）。
2. 任何"安装/卸载/重启"类能力的验证，先在专用测试机（standby-01）执行，
   测试机操作也需人工确认后由页面发起，禁止脚本直连生产。
3. 单元测试覆盖 common/crypto 与 api 业务逻辑的核心分支；SSH 相关逻辑用接口注入
   mock，测试不发起真实网络连接。
