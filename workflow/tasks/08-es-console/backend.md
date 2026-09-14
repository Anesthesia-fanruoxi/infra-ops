# 任务 08 · Elasticsearch 控制台：数据视图 · KQL 检索 — backend.md（后端执行）

> 设计依据：`docs/ES控制台设计.md`（v1.4，评审中）。§3 读写边界 / §4 参考实现红线 / §9 时间语义为强制约束。
> 原则：新增功能以读为主，唯一写能力是生命周期与模板管理（W1–W8 白名单 + 四条硬约束）；
> 编译产物不外露；时间接受体唯一（字符串 `YYYY-MM-DD HH:mm:ss` 按 UTC+8，后端转毫秒）。
> 单文件 ≤300 行。执行顺序即步骤编号；每步完成后按验收标准验收并在 workflow.md 标记 [x]。
> 前置说明：后端 `api/tool/es/` 已在 07-B5 拆好（es.go 293 / es_client_methods.go 278 / es_client.go 162，均 ≤300），
> 既有 `es_client.go` 连接层只读复用；新增 ES 调用在各新文件内自建请求与 `esErr` 归一化，不改动既有文件。

## B1 存储层（迁移 V26）

- [x] `store/migrations.go`：新增 `migrateV26` —— `es_views` 表
  （`conn_id` 外键 `ON DELETE CASCADE`、`name`、`index_pattern`、`time_field`、`fields_json`、`stats_json`、
  `sync_status` CHECK IN ('idle','syncing','failed')、`sync_error`、`synced_at`、必备 id/created_at/updated_at TEXT localtime）+
  `CREATE UNIQUE INDEX idx_es_views_conn_name ON es_views(conn_id, name)`；注册到迁移链
- [x] `model/es.go`：追加 `ESView` / `ESViewField`（Path/Types/Searchable/Aggregatable/TypeConflict/Partial/SubFields/Analyzer/Format）
- [x] `store/repo/es_view.go`：`es_views` 存取（不含业务判断）；
  含 `SaveSyncResults` 单事务批量写入 —— 成功视图整份替换 fields_json/stats_json/synced_at，
  失败视图仅落 sync_status='failed'+sync_error，快照原样保留（§6.3/§6.4）

验收：旧库启动自动建表、迁移幂等；`go build`/`go vet` 通过；conn_id+name 唯一约束生效。

## B2 探测与跨索引字段合并

- [x] `api/tool/es/mapping.go`：`_resolve/index/{pattern}`（indices / aliases / data_streams 三类计数 + docs_count 求和）；
  `_field_caps`（固定 `expand_wildcards=open`、`allow_no_indices=false`、`ignore_unavailable=false`）；`_mapping` 透传
- [x] §5.5 合并：同名同类型能力位取交（保守）；多类型保留全部并 `type_conflict:true`；
  部分成员存在标 `partial:true`；字段总数 >2000 截断（按字段名排序稳定）+ `truncated:true`
- [x] `mapping_test.go`：合并四情形 + 截断排序稳定性单测

验收：单测全绿；`go build`/`go vet` 通过。

## B3 时间解析（唯一入口）

- [x] `api/tool/es/timeparse.go`：`YYYY-MM-DD HH:mm:ss`（按 UTC+8 解释）→ 毫秒整数；
  输出解析对（起始 `gte`、结束 `lt`，下半开区间）
- [x] 非法输入一律 4010 不静默纠正：缺秒（`17:00`）、`T` 分隔、`now-1h` 相对表达式、纯毫秒数字、
  缺失/空串、`start >= end`
- [x] `timeparse_test.go`：§9 解析表全量用例 + 边界（跨日、闰秒外常规）

验收：单测全绿；时间转换全项目仅此一处，检索/分析/直方图均经由它。

## B4 视图 CRUD 与探测 handler

- [x] `api/tool/es/view.go`：§14.1 六端点（列表/创建/详情/更新/删除/手动刷新）+ §14.2 probe 探测端点
- [x] 三要素校验：name 必填 ≤64 同连接唯一（4007/409）；pattern 必须命中 ≥1（4006，`allow_no_indices=false`）；
  time_field 必须在全部成员中为 `date`（4009，响应列出缺失成员，引导「换字段 / 收窄 pattern」两出路）
- [x] probe 返回：命中概览（三类分别计数，名称 >20 截断）+ docs_count + 候选时间字段
  （`@timestamp` 置顶，其次 time/date/created/updated 命中，无则 4004）+ 字段摘要；
  pattern=`*` 或命中 >50 附黄色警告标记
- [x] 创建提交时同步执行一次字段拉取并落库（失败报错不落库）；改 pattern 或 time_field 自动重同步；
  手动刷新同步执行、立即落库、返回新字段表

验收：`go build`/`go vet`；非法输入逐条 400/409 且文案明确；创建后立即可见字段表。

## B5 后台同步循环

- [x] `api/tool/es/view_sync.go`：`runESViewSyncLoop` —— 启动延迟 10s 跑一次 + `time.NewTicker(4h)` 轮询，
  `main.go` 以 goroutine 启动（`crypto.Service` 由 main 注入，解密约束限定 worker 放 api 层）
- [x] 单轮三步模型：并行拉取（每视图一个 goroutine，信号量 `esSyncConcurrency=4`，单视图 30s 超时）→
  内存汇总 `[]viewSyncResult` → 单事务 `SaveSyncResults`（只更新成功视图）
- [x] 单视图互斥：进程内 `sync.Map` 占位覆盖「拉取+入库」全程；手动刷新撞后台轮次 → 4008（409）
- [x] 降级：连接不可达/凭据失效 → 该连接全部视图标记失败，不影响其它连接；批量入库整轮失败 → 整体丢弃记日志；
  启动时把全部残留 `syncing` 重置为 `idle` 再开同步；轮次串行不重叠；整轮起止与耗时记日志

验收：`go build`/`go vet`；手动触发一轮验证「并行拉取 → 单事务入库 → 失败保旧快照」；
SQLite 无 `database is locked` 抬升。

## B6 KQL 词法与语法分析

- [x] `api/tool/es/kql.go`：词法单元 field/value/quoted/op/logic/lparen/rparen/star，每 Token 带 `Pos`/`Raw`
- [x] 递归下降：`NOT > AND > OR`，同级左结合，空格 = 隐式 AND，括号可覆盖（§8.2 文法）
- [x] 转义单遍 scanner：`\*` `\"` `\'` `\\` `\ ` 等合法集，未知转义序列 → 4001 并列出合法集；
  剥引号壳前校验长度 ≥2（单字符引号 → 4001，不越界 panic）
- [x] 语法错误 4001 一律携带 pos/len 与出错片段

验收：`kql_test.go` 优先级/括号/引号内容不参与逻辑词识别/转义/错误定位用例全绿。

## B7 DSL 生成（Schema 感知）

- [x] `api/tool/es/dsl.go`：入参为 `Schema`（来自本地字段表）；字段不存在 → 4002 附最接近字段名建议；
  字段表为空 → 4004 提示先刷新
- [x] §9 算子分派全表：text → match / match_phrase / query_string+analyze_wildcard；
  keyword → term / wildcard；数值 → term（强转失败 4001）/ range；date → range（KQL 内日期字面量与
  `now-1d` 编译期按 UTC+8 冻结为毫秒）；boolean / ip（含 CIDR range）；type_conflict → match+lenient:true；
  `field:*` → exists；object/nested 引用叶子路径，nested 包装 → 4002
- [x] 生成器规则：AND/OR/NOT 生成**同一层** bool 的 must/should+`minimum_should_match:1`（任何 should 必带）/
  must_not，禁止 nil 子句投入 bool；裸值 → multi_match（best_fields；引号 → phrase），
  `fields` 取字段表全部 searchable 字段（禁止 `["*"]`）+ `lenient:true`
- [x] §10 搜索体组装：pattern 直传 `_search`；时间范围注入最外层 `bool.filter`（`gte start_ms` + `lt end_ms`，
  不下发 `time_zone`）；`track_total_hits:true`；`sort` 用视图 `time_field` desc + `_doc` tiebreaker；
  `_source` = 用户列（空则不下发）；highlight 仅 text 类型字段；`from:0, size:1000`；
  `aggs.histogram` date_histogram（fixed_interval 前端传入 + `time_zone:"+08:00"` + min_doc_count:0 + extended_bounds）
- [x] 编译产物仅按 **debug 级别**打服务端日志（KQL 原文 + DSL + 时间毫秒值，带 request_id）

验收：`dsl_test.go` 类型分派表逐行用例 + should/minimum_should_match 强制 + 时间 filter 形态校验。

## B8 结果集缓存与检索 handler

- [x] `api/tool/es/result_cache.go`：键 `conn_id+view_id`；新查询直接覆盖；`result_id` 随响应下发；
  全局三上限（≤20 结果集 / ≤20000 条 / ≤64MB，序列化长度估算）超限 LRU 淘汰；空闲 TTL 30min；
  单把 `sync.RWMutex`
- [x] `api/tool/es/handler_search.go` 三 handler（无任何 DSL 回显字段）：
  `POST /search`（`view_id+kql+start+end+columns+histogram_interval`，一次拉满 1000 条入库存；
  响应含 result_id/total/window/window_truncated/时间原样回显+ms 值；带 query/index 旧参数 → 400 提示改用 view_id+kql）；
  `POST /search/page`（内存切片，不访问 ES；result_id 不匹配 → 4011(409)，越界 → 4005(400) 带 window/total）；
  `POST /kql/validate`（恒 200，`ok:false` 时带 code/message/pos/len）

验收：`result_cache_test.go` 覆盖/淘汰/TTL/4011/4005 单测全绿；响应体无 dsl 字段。

## B9 红线单测（§4 六缺陷反向用例）

- [x] 缺陷 1：裸值与引号值产出 DSL 必须不同（match vs match_phrase），同一输入双断言锁定
- [x] 缺陷 2：未知算子（如 `q>500`）→ 4001，编译结果不含 nil 子句
- [x] 缺陷 3：值为单个 `"` 或 `'` → 4001，不 panic
- [x] 缺陷 4：multi_match.fields 来自字段表真实候选 + lenient:true
- [x] 缺陷 5：单遍解转义，`\\"` 不被二次解转义
- [x] 缺陷 6：任何 bool 出现 should 必带 minimum_should_match（遍历断言）
- [x] 时间边界：结束时刻文档不计入（gte/lt）；非法区间 4010 不静默纠正

验收：`go test ./api/tool/es/...` 全绿；三类「不报错但结果错」红线全部有反向用例。

## B10 分析（去重计数 / 图状）

- [x] `api/tool/es/analyze.go`：两个 handler（§14.6），复用 `dsl.go` 同一份 query 段
  （`size:0 + track_total_hits:true`，与检索同源，响应带 total）
- [x] 去重计数：字段 1–5（上限 10）个，每字段 `cardinality`（precision_threshold:40000）+
  `terms`（size:50, order _count:desc）Top 值分布
- [x] 图状：dim1 必选（terms / date_histogram / histogram），dim2 可选，metric 必选
  （count / sum / avg / min / max / cardinality）；桶数一级 ≤50 二级 ≤20；
  date_histogram 复用 §10 fixed_interval 推算与 `time_zone:"+08:00"`；`sum_other_doc_count` 与
  `doc_count_error_upper_bound` 透传前端
- [x] 参数校验：字段不可聚合 / 指标类型不符 / 桶数超限 → 4012；start/end 校验同 §14.4（4010）

验收：单测覆盖聚合组装、与检索同源 query、4012 逐条。

## B11 生命周期与索引模板（W1–W8）

- [x] `api/tool/es/lifecycle.go`：ILM 策略列表/详情/PUT(W1)/DELETE(W2)、`_ilm/explain`（接受 pattern）、
  `_ilm/retry`（W3，仅具体索引名）、数据流列表、`PUT /_data_stream/{name}/_lifecycle`（W4）
- [x] `api/tool/es/template.go`：索引模板/组件模板 列表/详情/PUT(W5,W7)/DELETE(W6,W8) +
  `POST /_index_template/_simulate`（读，保存前预览与详情页试算两用）
- [x] 四条硬约束逐条落地：名称白名单（非空、不含 `*` `,` `?`、不以 `_` `.` 开头 → 4015）；
  内置资源只读（`_meta.managed=true` 或名称 `.` 开头 → 4013/403）；
  删除被引用策略 ES 原始报错完整回显 → 4014（409），不做自动解绑；
  写操作一律 `repo.AuditRepo.Create`（Action 如 `es.ilm.put`、TargetType/TargetID/RemoteIP）
- [x] 行数逼近 300 时读写分文件（`*_read.go`/`*_write.go`）或拆 handler

验收：W1–W8 每条验证白名单拒绝/内置拒绝/审计落库；`_simulate` 输出合并结果正确。

## B12 路由登记与回归

- [x] 路由注册 §14 全部端点（挂既有 `/api/v1/es/:id` 分组）
- [x] §14.9 既有 `POST /es/:id/index`、`DELETE /es/:id/index/:index` 原样保留（路由注册、前端入口、实现均不动）
- [x] `workflow/docs/04-api.md`：登记 §14 端点与错误码 4001–4015
- [x] 回归：`go build`/`go vet`/`go test ./...` 全绿；既有 ES 工具链路（连接/概览/索引管理）零回归

验收：端点全注册、既有索引管理链路无 diff、构建测试全绿。

## 进度记录

### 2026-09-11 晚（B6–B12 本会话完成）

- B6 修复两个词法 bug 后全绿：① 保留字符 ':'/',' 落入 scanIdent 后 pos 不前进 → 无限追加空 token 直至 OOM（28GB 分配失败，即上个会话测试卡死的真凶）；': ' 现在作为算子识别、',' 报 4001。② parseTerm 不支持 field:>=N 链式比较 → ':' 后允许跟一个比较算子（eff 取比较符）。新增 TestKQLNoProgressRegress 死循环回归用例。
- es_codes.go 错误码语义对齐设计 §16：4001=语法、4002=语义（原 4001=FieldNotExist 系误标）。
- B7 dsl.go（494 行）：Schema 入参、§9 分派全表、同层 bool 收敛（修了一个真 bug：子句归属误用子节点 kind，AND 链被错误放进 should）、multi_match fields 取真实候选、日期字面量编译期冻结毫秒（注意 time.Duration 是纳秒基准，勿直接当毫秒加）。dsl_test.go 20+ 用例。
- B8 result_cache.go + handler_search.go：键 conn-view、result_id=conn-view-seq、三上限 LRU、TTL 30min；三 handler 无 DSL 回显（debug 日志带自生成 request_id）。注意 GetRawData 要先于 JSON 解析（ShouldBindJSON 会消费 body）。
- B10 analyze.go：cardinality+terms / dim1+dim2+metric，buildMetric 返回 dslErr 而非产出非法 DSL。
- B11 lifecycle.go + template.go：W1–W8 + _simulate；四条硬约束落地。**边界偏差记录**：Handler 结构体在 es.go（黑名单只读），为落审计字段做了最小越界（仅加 auditRepo 字段）；AuditLog.TargetID 是 int64，资源名记入 Detail。
- B12 路由全量注册（gin 静态 _simulate 与参数 :name 同段已用临时程序验证不冲突）；04-api.md 登记 §14 端点与 4001–4015。补充 §14.3 遗漏项：GET /es/:id/indices/:index/mapping（IndexMapping handler）。
- 旧 legacy Search handler（es.go，黑名单）保留为死代码，路由已不再指向；待解禁后清理。
- 回归：go build/vet/test ./... 全绿；二进制 17MB 已重建（-ldflags -s -w）。

