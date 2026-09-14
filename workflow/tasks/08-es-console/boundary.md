# 任务 08 · Elasticsearch 控制台：数据视图 · KQL 检索 — boundary.md（边界）

> 本文件限定执行本任务时**允许修改的文件范围与操作边界**。
> 清单之外的任何文件一律只读；确需越界时，停止并上报，不得自行修改。

## 1. 允许修改的文件（白名单）

```
store/migrations.go                    # 仅追加 migrateV26（es_views 表）与迁移链注册
model/es.go                            # 仅追加 ESView / ESViewField
store/repo/es_view.go                  # 新增：视图存取 + SaveSyncResults 单事务批量
api/tool/es/**                         # 新增：mapping/view/view_sync/kql/dsl/handler_search/timeparse/
                                       # result_cache/analyze/lifecycle/template + *_test.go + 读写拆分文件
main.go                                # 仅追加 go runESViewSyncLoop(...) 启动与 crypto.Service 注入
router/**（按既有路由落点）            # 仅注册 §14 新端点
workflow/docs/04-api.md                # 登记 §14 端点与错误码 4001–4015
template/static/pages/es.js            # 瘦身为连接管理 + 概览/节点 tab
template/static/pages/es_index.js      # 新增：索引 tab（含既有新建/删除索引入口迁移）
template/static/pages/es_views.js      # 新增
template/static/pages/es_fields.js     # 新增
template/static/pages/es_kql.js        # 新增
template/static/pages/es_discover.js   # 新增
template/static/pages/es_analyze.js    # 新增
template/static/pages/es_lifecycle.js  # 新增
template/static/pages/es_templates.js  # 新增
template/static/style.css              # 仅追加 ES 段落样式（走 :root 令牌）
template/static/index.html             # 仅按序引入新增 script
docs/ES控制台设计.md                  # 实施中同步修订设计文档
workflow/tasks/08-es-console/**        # 本任务执行文档进度记录
workflow/workflow.md                   # 仅勾选本任务步骤 [x]
```

## 2. 禁止修改的文件（黑名单 · 只读）

```
api/tool/es/es.go、es_client.go、es_client_methods.go   # 既有连接/客户端层只读复用；
                                                        # 新增 ES 调用在白名单新文件内自建请求与 esErr 归一化
api/（tool/es 外的其他文件）           # 07 剩余 B8/B9 热区（deploy/host/sse/stack 子包）不碰
store/builtin_stacks.go、store/builtin_templates.go、store/builtin/**   # 套件/模板体系不涉
store/stacks/**                        # 套件脚本与配置不动
common/**、config/**
template/static/（白名单外的页面与资源）# stacks/hosts/deploy 等其他页面不动
workflow/tasks/（08 外的其他任务目录）
workflow/boundaries/**、workflow/docs/（04-api.md 外）
```

## 3. 接口与数据边界

1. 新端点全部挂既有 `/api/v1/es/:id` 分组；不新增独立路由组；前端 hash 路由不新增（页面内部 tab 展开）。
2. ES 读调用白名单 = 设计文档 §3.1 清单；**写调用白名单 = §3.3 W1–W8 八条**，未登记者一律不得实现
   （含 ILM 全局启停、rollover、migrate_to_data_stream、`_scripts` 模板等 §13.2 明确不做项）。
   既有 `POST /es/:id/index` 与 `DELETE /es/:id/index/:index` 原样保留，不下架不改造。
3. schema 变更仅 V26 一张 `es_views` 表；视图表不含任何凭据材料，同步时临时解密连接密码用完即弃不落库。
4. 时间接受体唯一：`start`/`end` 为 `YYYY-MM-DD HH:mm:ss` 字符串按 UTC+8 解释，
   后端 `timeparse.go` 是全项目唯一时间转换入口；前端不做任何时间换算；其余格式一律 4010。
5. W1–W8 四条硬约束后端兜底：名称白名单 4015 / 内置资源只读 4013 / 删除被引用策略 4014 回显原始报错不自动解绑 /
   写操作一律落审计（`repo.AuditRepo.Create`）。
6. 旧 `/search` 的 `{index, query}` 请求体**直接替换**为 `{view_id, kql, ...}`，不做兼容过渡；
   请求体带 `query` 或 `index` 时返回 400 并提示改用 `view_id` + `kql`。

## 4. 技术边界

1. 单文件 ≤300 行：新增后端文件与 `es.js` 拆分后均须达标；`lifecycle.go`/`template.go` 逼近上限时读写分文件。
2. 编译产物不外露：DSL 仅按 debug 级别打服务端日志（带 request_id），任何响应体不含 dsl 字段。
3. 零新依赖：分析图表纯 SVG 自绘（vendor 只有 Vue/Element Plus/axios），**禁止引入 ECharts 等图表库**。
4. 高亮 XSS：`v-html` 前转义 `<` `>` `&`；`pre_tags` 白名单只留 `<mark>`；禁止直接渲染 ES 返回片段。
5. `go:embed`：前端静态资源改动后必须 `go build` 重编译才生效，验证时注意。
6. 并发会话约束：07-api-refactor 尚余 B8/B9 改造 api/ 其他子包，与本任务热区（api/tool/es、es*.js）无交集，
   但开工前先 `git status` 确认工作区状态，发现冲突立即暂停上报。
7. 遵守 rules.md：占位符 SQL、日志脱敏（密码/Token 不落日志）、写操作审计、留白约定 §6.3。

## 5. 验收边界

1. 静态：`go build`、`go vet`、`go test ./...`（含新增 `*_test.go`）、`node --check`（全部改动 JS）全绿。
2. `git diff --name-only` 全部落在第 1 节白名单内。
3. 红线单测为硬性验收项：§4 六缺陷反向用例缺一即退回。
4. W1–W8 每条写操作真机验证：白名单外 4015、内置 4013、二次确认回显、审计落库四项齐全。
5. 真机联调仅在测试环境（standby-01 + 测试 ES 数据流），禁止对生产集群执行任何 W 类写操作（红线）；
   全链路清单见 frontend.md F9。
