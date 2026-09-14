# 04 — 接口设定（阶段一）

## 1. 通用约定

- 前缀 `/api/v1`；JSON 字段 snake_case；除 login/healthz 外全部要求登录会话。
- 统一响应：

```json
{ "code": 0, "message": "ok", "data": { } }
```

- 分页：入参 `page`（默认 1）、`page_size`（默认 20，上限 100）；
  出参 `{ "list": [], "total": 0, "page": 1, "page_size": 20 }`。

## 2. 错误码表

| code | 含义 | HTTP |
|------|------|------|
| 0 | 成功 | 200 |
| 400 | 参数错误 | 400 |
| 401 | 未登录 / 会话失效 | 401 |
| 403 | 禁止操作 | 403 |
| 404 | 资源不存在 | 404 |
| 409 | 冲突（名称重复 / 凭据被引用等） | 409 |
| 500 | 服务内部错误 | 500 |
| 1001 | SSH 连接失败（网络/超时） | 200* |
| 1002 | SSH 认证失败（密钥/密码不匹配） | 200* |
| 1003 | SSH host key 指纹变更，拒绝连接 | 200* |
| 1004 | 信息采集失败（连接成功但命令执行异常） | 200* |
| 3001 | 批量新增：IP 列表解析失败（message 含首个非法项） | 400 |
| 3002 | 批量新增：解析后数量超过上限（100 台/批） | 400 |

\* 1001-1099 段用于"连接测试"类接口的业务失败，HTTP 仍返回 200，
由 code 区分（前端按 code 渲染失败原因），避免与网关语义混淆。

## 3. 鉴权

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | /api/v1/auth/login | `{ "username": "", "password": "" }` → 校验 bcrypt，Set-Cookie 会话（HttpOnly，SameSite=Lax，有效期 12h） |
| POST | /api/v1/auth/logout | 清除会话 |
| GET  | /api/v1/auth/me | 当前登录用户信息 |

登录账号来源：config `auth.username` + `auth.password_hash`（bcrypt，
由 `infra-ops gen-password` 子命令生成），单管理员，一期无 RBAC。

## 4. 主机管理

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /api/v1/hosts | 列表。查询参数：`role`（nginx/harbor/k8s/other）、`status`（online/offline/unverified）、`keyword`（名称/IP 模糊）、分页 |
| POST | /api/v1/hosts | 新增。body 见下 |
| GET | /api/v1/hosts/{id} | 详情（含 info_json 解析后的资源快照） |
| PUT | /api/v1/hosts/{id} | 更新（name/ip/port/role/remark/credential_id） |
| DELETE | /api/v1/hosts/{id} | 删除（二期有运行中任务时将返回 409，一期直接删） |
| POST | /api/v1/hosts/{id}/test | 连接测试 + 信息采集（同步，超时 8s），结果见下 |
| POST | /api/v1/hosts/batch | 批量新增：共享凭据 + IP 范围解析 + 自动测试取名，body/响应见下 |

### POST /hosts 请求体

```json
{
  "name": "nginx-01",          // 必填，全局唯一
  "ip": "172.16.0.11",         // 必填
  "port": 22,                  // 默认 22
  "role": "nginx",             // nginx|harbor|k8s|other，默认 other
  "remark": "接入层主节点",
  "credential_id": 1           // 必填，须为已存在凭据
}
```

### POST /hosts/{id}/test 响应 data

```json
{
  "reachable": true,
  "latency_ms": 42,
  "info": {
    "hostname": "nginx-01",
    "os": "Alibaba Cloud Linux 3",
    "kernel": "5.10.134-16",
    "uptime_hours": 1104.5,
    "cpu_cores": 4,
    "load1": 0.42,
    "mem_total_mb": 7847,
    "mem_used_percent": 41,
    "disk": [ { "mount": "/", "size_gb": 80, "used_percent": 34 } ]
  }
}
```

测试成功后自动更新该主机 status/latency_ms/info_json/last_check_at。

### POST /hosts/batch 批量新增

场景：批量采购/扩容的机器共用同一凭据，只需录入 IP 段；主机名自动取
服务器 hostname，备注统一填写。

请求体：

```json
{
  "credential_id": 1,                    // 必填，本批共享的凭据
  "ips": "172.16.1.11-20\n172.16.2.5",   // 必填，支持单 IP/范围/混合，
                                          // 换行、逗号、分号、空格分隔
  "port": 22,                            // 默认 22
  "role": "k8s",                         // nginx|harbor|k8s|other，默认 other
  "remark": "k8s 节点扩容",               // 可选，统一应用到本批
  "auto_test": true                      // 默认 true：创建后连接测试+采集
}
```

IP 范围语法：

- 单 IP：`172.16.2.5`
- 简写范围：`172.16.1.11-20`（末段 11→20）
- 完整范围：`172.16.1.11-172.16.1.20`
- 解析规则：去空白、去重、逐项 IPv4 校验；任一非法项 → 整体拒绝并返回 3001
  （message 指明首个非法项）；解析后数量 > 100 → 3002。

处理流程（auto_test=true）：

1. 批量落库（status=unverified）；IP 已存在于库中的条目**跳过不覆盖**。
2. 并发连接测试（上限 5，复用单机 test 逻辑，单连接超时 8s）。
3. 命名：连通 → name = 采集到的 hostname，与库内或本批内冲突时自动追加
   `-2`/`-3` 后缀；不连通或 auto_test=false → name = IP（可后续改名）。
4. 测试结果定 online/offline，info 快照、latency、last_check_at 同单机 test。

响应 data：

```json
{
  "total": 12, "created": 10, "skipped": 2,
  "results": [
    { "ip": "172.16.1.11", "status": "created_online",  "host_id": 9,  "name": "k8s-node-05" },
    { "ip": "172.16.1.12", "status": "created_offline", "host_id": 10, "name": "172.16.1.12", "error_code": 1001 },
    { "ip": "172.16.0.11", "status": "skipped_exists",  "host_id": 1 }
  ]
}
```

注意：接口同步执行，100 台 × 超时最坏耗时数分钟；前端超时按 5 分钟设置，
提交按钮全程 loading 防重复提交。

## 5. 凭据管理

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /api/v1/credentials | 列表（仅返回 id/name/type/username/fingerprint/remark/时间，**永不返回 secret**） |
| POST | /api/v1/credentials | 新增，body 见下 |
| PUT | /api/v1/credentials/{id} | 更新；`secret` 为空表示不改密钥材料 |
| DELETE | /api/v1/credentials/{id} | 删除；被主机引用时返回 409 |

### POST /credentials 请求体

```json
{
  "name": "aliyun-root-key",        // 必填，唯一
  "type": "private_key",            // private_key | password
  "username": "root",               // 默认 root
  "secret": "-----BEGIN ... 或密码", // 必填，仅写不回读
  "remark": ""
}
```

type=private_key 时服务端解析公钥指纹存入 fingerprint 用于展示。

## 6. 总览与审计

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /api/v1/overview | `{ total, online, offline, unverified, by_role: {nginx:2, harbor:1, k8s:4, other:1}, recent_audits: [最近10条] }` |
| GET | /api/v1/audit-logs | 分页列表，查询参数 `action`（前缀匹配，如 host.）、分页 |
| GET | /api/v1/healthz | 存活探针，无鉴权 |
| GET | /api/v1/version | `{ version, build_time, go_version }`，无鉴权 |

## 7. 审计动作清单（阶段一）

`auth.login_ok`、`auth.login_fail`、`auth.logout`、`host.create`、`host.update`、
`host.delete`、`host.test`、`host.batch_create`、`credential.create`、
`credential.update`、`credential.delete`。
detail 记录关键字段快照（脱敏后），由中间件在写响应成功后统一落库。

## 14. 工具-Elasticsearch 控制台（任务 08）

设计依据：docs/ES控制台设计.md（v1.4）。全部挂既有 /api/v1/es/:id 分组；
检索/分析为只读语义；W1–W8 为登记的写操作（名称白名单 / 内置只读 4013 / 二次确认回显 / 落审计）。
既有 POST /es/:id/index 与 DELETE /es/:id/index/:index 原样保留（§14.9）。

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | /es/:id/index-pattern/probe | 探测 index pattern（不落库） |
| GET/POST | /es/:id/views | 视图列表 / 创建（创建即同步字段） |
| GET/PUT/DELETE | /es/:id/views/:vid | 视图详情（含字段表）/ 更新 / 删除 |
| POST | /es/:id/views/:vid/refresh | 手动刷新单视图（同步、立即落库） |
| POST | /es/:id/kql/validate | KQL 只校验不执行（恒 200，ok:false 带位置） |
| POST | /es/:id/search | 检索：view_id+kql+start+end，一次拉满 1000 条入内存，回 result_id |
| POST | /es/:id/search/page | 内存分页（不访问 ES；4011 失效 / 4005 越界） |
| POST | /es/:id/analyze/distinct | 去重计数（cardinality+terms，全量匹配集） |
| POST | /es/:id/analyze/chart | 图状分析（dim1/dim2/metric） |
| GET/PUT/DELETE | /es/:id/ilm/policies(/:name) | ILM 策略列表/详情/覆盖(W1)/删除(W2) |
| GET | /es/:id/ilm/explain?pattern= | 索引 ILM 状态（接受 pattern） |
| POST | /es/:id/ilm/retry | 重试卡住步骤（W3，仅具体索引名） |
| GET | /es/:id/data-streams | 数据流列表 |
| PUT | /es/:id/data-streams/:name/lifecycle | 设置 DLM 保留期（W4） |
| GET/PUT/DELETE | /es/:id/index-templates(/:name) | 索引模板（W5/W6） |
| POST | /es/:id/index-templates/_simulate | 模板模拟预览（只读） |
| GET/PUT/DELETE | /es/:id/component-templates(/:name) | 组件模板（W7/W8） |

### ES 控制台错误码 4001–4015

| code | 含义 | HTTP |
|------|------|------|
| 4001 | KQL 语法错误（message 含位置，data 可带 pos/len） | 400 |
| 4002 | KQL 语义错误（字段不存在 / 算子与字段类型不匹配） | 400 |
| 4003 | 视图不存在 / 索引不存在 | 400 |
| 4004 | 未探测到可用时间字段，或视图字段表为空需先刷新 | 400 |
| 4005 | 翻页越界：from+size 超出已缓存结果窗口（1000 条） | 400 |
| 4006 | 索引匹配未命中任何索引或数据流 | 400 |
| 4007 | 视图名称在同一 ES 连接内重复 | 409 |
| 4008 | 视图正在同步中，无法重复刷新 | 409 |
| 4009 | 时间字段在匹配集合部分成员中缺失或类型不一致 | 400 |
| 4010 | 时间参数非法（唯一接受体 YYYY-MM-DD HH:mm:ss 字符串，UTC+8） | 400 |
| 4011 | 结果集缓存失效（result_id 不匹配） | 409 |
| 4012 | 分析参数非法（不可聚合 / 类型不符 / 桶数超限） | 400 |
| 4013 | 目标为 ES 内置（managed）策略或模板，只读 | 403 |
| 4014 | 生命周期策略仍被索引引用，ES 拒绝删除（回显原始报错） | 409 |
| 4015 | 资源名称非法（空 / 含 * , ? / 以 _ 或 . 开头） | 400 |

时间语义：界面展示、界面输入、请求体三处均为字符串 YYYY-MM-DD HH:mm:ss（UTC+8），
后端统一转毫秒（api/tool/es/timeparse.go 唯一入口），区间恒为下半开 [start, end)。
