# Elasticsearch 控制台：数据视图 · 索引浏览 · KQL 检索设计

> 文档类型：技术方案 · 版本：v1.4（评审中） · 日期：2026-09-11 · 范围：工具 → Elasticsearch
> 与 `docs/ES套件冷热温架构设计.md` 的分工：那份负责「**把 ES 集群部署起来**」，本文档负责「**连上去之后怎么看数据**」。
> v1.1 修订（相对 v1.0）：引入**数据视图（Data View）**作为检索的一等公民（§5）；字段表改为**本地持久化 + 异步同步**（§6）。
> v1.2 修订（相对 v1.1）：① 读写边界更正为「**新增功能只读，既有索引新建 / 删除保留**」——v1.1 的下架决定**作废**（§3）；② 同步机制改为「**并行拉取 → 内存缓存 → 单事务批量入库，只更新成功的视图**」（§6.3）；③ 显式确认同步失败**不覆盖**上一份成功快照（§6.4）。
> v1.3 修订（相对 v1.2）：① **移除 DSL 预览** —— 编译产物不再回前端，`kql/compile` 收敛为 `kql/validate`（只回校验结果），DSL 仅按 debug 级别进服务端日志（§14.4、§15.2）；② **时间范围语义定死**（其中「请求体恒为毫秒」的部分已于 v1.4 更正，见下）。
> v1.4 修订（相对 v1.3）：① **时间接受体反转** —— 界面展示、界面输入、请求体**三处统一为字符串** `YYYY-MM-DD HH:mm:ss`（按 UTC+8 解释），由**后端**统一转毫秒，前端不做任何时间转换；查 ES 时下发的**时间字段名取自视图配置**，不硬编码 `@timestamp`（§9、§14.4）；② **结果集改为内存缓存 + 内存分页** —— 一次检索拉满 **1000 条**存进程内存，新查询直接覆盖，翻页只读内存、不再访问 ES（新增 §11）；③ **新增即时分析** —— 去重计数 + 图状分析，附属于检索、不做独立菜单，走 ES 聚合而非本地 1000 条（新增 §12）；④ **新增生命周期管理与索引模板** —— 本期**首次**引入写操作，W1–W8 逐条登记于 §3.3，并配名称白名单 / 内置资源只读 / 二次确认 / 审计四条硬约束（新增 §13、§14.7、§14.8）；⑤ 明确**视图高级选项与多用户隔离不做**（个人使用）。

把现有 ES 工具从**原样透传 JSON DSL、按单个索引手查**，升级为**数据视图驱动的 KQL 检索控制台**：

- 新增**数据视图**：对齐 Kibana 的三要素（自定义名称 / 索引匹配逻辑 / 时间字段），视图与其字段表**持久化在本地**，字段由后台异步同步
- 新增**字段类型感知的 KQL 编译器**：`KQL → ES DSL` 在服务端完成，编译产物**不外露**
- 前端从裸 textarea 换成 Kibana Discover 式的三栏视图（时间直方图 / 字段侧栏 / 文档表）
- **时间接受体唯一**：界面展示、界面输入、请求体三处都是 `YYYY-MM-DD HH:mm:ss` 字符串（按 UTC+8 解释），**由后端统一转毫秒**，区间恒为下半开 `[start, end)`（见 §9）
- **结果集一次拉满 1000 条存进程内存**，新查询直接覆盖，翻页只读内存、不再查 ES（见 §11）
- 新增**即时分析**：去重计数 + 图状分析，附属于检索、不做独立菜单（见 §12）
- 新增**生命周期与索引模板管理**（本期唯一的写能力，逐条登记于 §3.3）；既有的索引新建 / 删除能力原样保留

## 1. 背景与现状

现有链路（`template/static/pages/es.js` → `api/tool/es/es.go` → `api/tool/es/es_client.go`）只有一条通路：

```
手选单个索引 + JSON textarea → JSON.parse → 首字节校验是 "{" → 组装 {from, size, query} → POST /{index}/_search
```

后端 `search()` 实际只做两件事：校验 `query` 首字节为 `{`，然后透传请求体。由此暴露出四个结构性问题：

| # | 问题 | 具体表现 |
| --- | --- | --- |
| 1 | **无视图概念** | 每次检索都要从索引下拉里手选一个**具体**索引。而日志类索引通常是按天滚动的索引集（`logs-2026.09.10`、`logs-2026.09.11`…），想跨天查询就只能一个一个选 —— 完全无法表达「这一批索引」这个检索对象 |
| 2 | **无字段认知** | 用户不知道索引里有哪些字段、各是什么类型，只能盲写 DSL；字段名写错要等 ES 报错才知道 |
| 3 | **无语法层** | 前端 `JSON.parse` 失败只提示「不是合法 JSON」，不定位行列；复杂嵌套手写成本极高 |
| 4 | **搜索体残缺** | 只组装 `{from, size, query}`。没有 `sort`（结果无法按时间排序）、没有 `track_total_hits`（分页 total 是 10000 的估算值而非精确值）、没有 `_source` 裁剪、没有 `highlight`、没有聚合（画不出时间分布） |

问题 1 与问题 2 是根因：**没有稳定的检索对象，也没有字段类型信息，就无法决定「查什么」和「用哪个算子」**。`field:value` 在 `text` 字段上应该分词匹配、在 `keyword` 字段上应该精确匹配、在 `long` 字段上应该走 `term`/`range` —— 缺了字段类型这一切都无从谈起。而字段类型在跨索引场景下必须**合并**（同一字段在不同索引里类型可能不同），合并结果又不可能每次检索都实时计算，因而必须**本地化 + 异步同步**。

## 2. 目标与非目标

### 目标（本期）

1. **数据视图管理**：对齐 Kibana 的三要素创建视图 —— 自定义名称 / 索引匹配逻辑 / 时间字段；支持列表、编辑、删除、单视图刷新
2. **字段本地化**：视图的合并字段表持久化到本地库；进程启动同步一次，运行中每 4 小时轮询一次，并可在视图界面手动刷新单个视图
3. **索引浏览**：集群索引列表 + 单索引原始 mapping 查看（排障用，区别于视图的合并字段表）
4. **KQL 检索**：以视图为检索对象；语法错误定位到字符位置；字段存在性与算子类型在编译期校验
5. **Discover 视图**：时间直方图 + 时间范围选择 + 字段侧栏 + 列选择 + 文档表 + 高亮 + 单条展开
6. **纯 KQL**：检索只通过 KQL 表达，移除 JSON textarea 入口
7. **时间语义固定且接受体唯一**：界面展示、界面输入、请求体三处都是 `YYYY-MM-DD HH:mm:ss` 字符串（按 UTC+8 解释），由**后端**统一转毫秒，过滤恒为下半开区间 `[起始, 结束)`；**前端不做时间转换**
8. **结果集内存缓存与内存分页**：一次检索拉满 1000 条存入进程内存，新查询直接覆盖，翻页只在内存切片、不再访问 ES
9. **即时分析**：对当前查询做去重计数与图状分析（柱 / 折线 / 饼），属于检索的附加能力，不做独立菜单
10. **生命周期与索引模板管理**：ILM 策略 / 索引模板 / 组件模板的查看与编辑 + 模板模拟预览（本期唯一的写能力）

### 非目标（明确不做）

- **除生命周期与模板管理（§13）之外的任何 ES 写操作** —— 新增功能的写能力 W1–W8 逐条登记在 §3.3，未登记者一律不得实现
- **DSL 预览 / 任何形式的编译产物外露** —— 使用者不需要知道 KQL 被转成了什么；编译产物只进服务端日志（debug 级），不进响应、不做界面展示
- **请求体接受毫秒时间戳或相对时间表达式**（`now-1h`、`1789117200000`、`start_ms`）—— 接受体唯一为 `YYYY-MM-DD HH:mm:ss` 字符串，其余一律 4010
- runtime field、脚本字段、`painless` 表达式
- 保存搜索、查询历史、结果导出
- **独立可视化菜单**（Lens / Dashboard 类）、保存的图表、多图拼装面板、图表导出、告警 —— 只保留基于当前查询结果的一次性分析（§12）
- **ES 侧深分页**（PIT + `search_after`、`from + size > 10000`）—— 结果窗口固定 1000 条并在**内存**分页（§11），从不向 ES 传 offset
- `nested` / `object` 字段的嵌套查询包装 —— 编译期报错提示不支持
- **视图高级选项与多用户隔离** —— 个人使用：不做「允许隐藏/系统索引」开关、不做 `allow_no_indices` 开关、不做自定义视图 ID、不做用户级视图可见性与权限控制；固定用 §5.3 的安全默认值
- **生命周期 / 模板的高级能力**：ILM 全局启停（`POST /_ilm/start|stop`）、ILM → data stream 迁移、手工触发 `rollover`、`_scripts` 模板、模板与策略的导入导出、删除被引用策略时的自动解绑（§13.2）
- 分析结果的持久化、缓存与导出（每次打开抽屉实时查一次，§12.4）

## 3. 读写边界（强制约束）

**本期新增功能绝大多数为只读；唯一的写能力是 §13 的生命周期与索引模板管理，全部写端点在 §3.3 逐条登记。**

### 3.1 新增功能的读取类调用（全清单）

逐一列举，新增功能合计只有下列读取类调用：

| 用途 | ES 调用 | 性质 |
| --- | --- | --- |
| 连通性 / 概览 / 健康 | `GET /`、`GET /_cluster/health` | 读 |
| 节点 | `GET /_nodes`、`GET /_nodes/stats` | 读 |
| 索引列表 | `GET /_cat/indices` | 读 |
| 索引匹配探测 | `GET /_resolve/index/{pattern}` | 读 |
| 字段能力表 | `GET /{pattern}/_field_caps` | 读 |
| 原始映射 | `GET /{index}/_mapping` | 读 |
| 检索 | `POST /{pattern}/_search` | 读（用 POST 仅因携带请求体） |
| 分析（聚合） | `POST /{pattern}/_search`（`size:0` + `aggs`） | 读 |
| 生命周期策略列表 / 详情 | `GET /_ilm/policy`、`GET /_ilm/policy/{name}` | 读 |
| 索引生命周期状态 | `GET /{pattern}/_ilm/explain` | 读 |
| 数据流列表 | `GET /_data_stream` | 读 |
| 索引模板列表 / 详情 | `GET /_index_template`、`GET /_index_template/{name}` | 读 |
| 组件模板列表 / 详情 | `GET /_component_template`、`GET /_component_template/{name}` | 读 |
| 模板模拟 | `POST /_index_template/_simulate` | 读（只解析与合并，不落配置） |

> `POST /_search` 与 `POST /_index_template/_simulate` 都是只读语义 —— ES 用 POST 承载请求体与复杂入参，不产生副作用。以「HTTP 方法不是 GET」判定为写操作是常见误读，此处显式记录以免评审时被当作违规。

除 §3.3 登记之外，新增接口**不得引入上表以外的 ES 调用**，尤其不得引入 `PUT` / `DELETE`。

### 3.2 保留的既有写操作（明确豁免）

工具里原有两处真实的 ES 写操作，**本期原样保留**：

| 既有端点 | 行为 | 本期处理 |
| --- | --- | --- |
| `POST /api/v1/es/:id/index` | `PUT /{index}` 新建索引（`es_client.go` 的 `createIndex`） | **保留**：路由注册、前端入口、方法实现均不动 |
| `DELETE /api/v1/es/:id/index/:index` | `DELETE /{index}` 删除索引（`es_client.go` 的 `deleteIndex`） | **保留**：同上 |

> **为什么保留**：这两条是工具既有的索引管理能力，后续运维场景仍要用（按模板预建索引、清理废弃索引），且与视图 / KQL / 检索链路完全独立 —— 它们不参与 §14 的任何新增接口，也不依赖视图的字段表。
>
> 版本说明：v1.1 曾按下架处理，v1.2 按评审意见**恢复**。

### 3.3 本期新增的写操作（逐条登记）

**这是本设计第一次为新增功能引入 ES 写能力。** 每条都必须在表内登记，未登记不得实现：

| # | 端点 | ES 调用 | 用途 |
| --- | --- | --- | --- |
| W1 | `PUT /es/:id/ilm/policies/:name` | `PUT /_ilm/policy/{name}` | 新建 / 覆盖生命周期策略 |
| W2 | `DELETE /es/:id/ilm/policies/:name` | `DELETE /_ilm/policy/{name}` | 删除生命周期策略 |
| W3 | `POST /es/:id/ilm/retry` | `POST /{index}/_ilm/retry` | 重试卡住的 ILM 步骤 |
| W4 | `PUT /es/:id/data-streams/:name/lifecycle` | `PUT /_data_stream/{name}/_lifecycle` | 设置数据流保留期（DLM） |
| W5 | `PUT /es/:id/index-templates/:name` | `PUT /_index_template/{name}` | 新建 / 覆盖索引模板 |
| W6 | `DELETE /es/:id/index-templates/:name` | `DELETE /_index_template/{name}` | 删除索引模板 |
| W7 | `PUT /es/:id/component-templates/:name` | `PUT /_component_template/{name}` | 新建 / 覆盖组件模板 |
| W8 | `DELETE /es/:id/component-templates/:name` | `DELETE /_component_template/{name}` | 删除组件模板 |

四条硬约束（**W1–W8 全部适用**）：

1. **路径参数白名单**：名称不得为空、不得含 `*` `,` `?`、不得以 `_` 或 `.` 开头（避免误伤 `_default_` 与系统模板）→ 4015
2. **内置资源只读**：`_meta.managed = true` 的策略 / 模板，或名称以 `.` 开头者，一律拒绝修改与删除 → 4013（403）
3. **二次确认 + 完整请求回显**：前端确认框里必须显示出**实际将发出的 ES 请求**（如 `PUT /_ilm/policy/logs-30d`）以及即将覆盖内容的摘要
4. **写操作一律落审计**：沿用既有 `repo.AuditRepo.Create(&model.AuditLog{...})`（`common/middleware/audit.go` + `model/audit.go`），记录 `Action`（如 `es.ilm.put`）/ `TargetType`（如 `es_ilm_policy`）/ `TargetID`（资源名）/ `RemoteIP`

> **与 v1.1–v1.3 的口径差异**：此前本节写的是「新增功能一律只读」。该表述自 v1.4 起**作废** —— 生命周期与模板管理天然是写操作，且已明确要求做上。约束改为「**写能力必须逐条登记，并满足上述四条硬约束**」；其余新增功能仍全为只读。

## 4. 参考实现评审结论（强制约束）

已有另一项目的 KQL→EQL 编译器（下称「参考实现」）提供了可复用的骨架：token 类型分为 `field / value / group / logic / operator`、group 递归下降、`field:*` 映射为 `exists`、`!=` 映射为 `must_not` 包一层 `bool`、顶层 OR 映射为 `should + minimum_should_match:1`。这些语义方向都是对的，本设计沿用。

但参考实现有 6 处缺陷，逐条作为**本设计的强制约束**写入：

| # | 缺陷 | 触发条件 | 后果 | 本设计对策 |
| --- | --- | --- | --- | --- |
| 1 | **引号语义退化** | 剥引号与不剥引号两条分支产出的 DSL 完全相同 | 裸词被强制按相邻短语匹配，`message:failed to connect` 查不到中间隔词的文档；引号写了等于没写 | 裸值 → `match`（`best_fields`）；引号包裹 → `match_phrase`。两分支必须产出**不同** DSL，由单测锁定 |
| 2 | **未识别算子产出 `must:[null]`** | 输入含未实现算子（如 `q>500`） | `{"bool":{"must":[null]}}` → ES 直接 400 解析失败 | 编译期对未知算子返回带位置的错误码 4001；生成器禁止把 nil 子句投入 bool |
| 3 | **单字符引号 panic** | 值恰好为 `"` 或 `'` | `value[1:len(value)-1]` 越界 → slice bounds out of range，接口 500 | 剥壳前先校验长度 ≥ 2，否则报 4001 |
| 4 | **`fields:["*"]` 无 `lenient`** | 索引含 integer / date / ip 等非 text 字段 | `multi_match` 展开报 `failed to create query`；且通配字段使每个子句都展开全索引字段，性能极差 | `fields` 用字段表得到的**真实候选字段列表**替代 `["*"]`；同时保留 `lenient:true` 兜底 |
| 5 | **转义为多次串行 `ReplaceAll`** | 输入含 `\*` `\(` `\)` `\ `；或串行替换导致二次解转义 | 未覆盖的转义被漏处理；`\\"` 被解成单个 `"`，把定界符与外层字符串混淆 | 单遍 scanner 解转义；遇到未知转义序列报 4001 并列出合法转义集 |
| 6 | **主 bool 的 `should` 缺 `minimum_should_match`** | 任何顶层 OR 均触发提前 return，该分支当前**不可达** | 属死代码，但一旦放宽前置判断（如支持混合布尔），`should` 在 must+should 并存时只影响打分、失去过滤作用 → 查询静默放大为「返回全量」 | 生成器级规则：**任何 `bool` 出现 `should` 必带 `minimum_should_match`**，由单测强制；不允许依赖调用方自觉 |

> 缺陷 1、2、6 属于「不报错但结果错」这一类，最危险 —— 搜索界面给出静默放宽的结果，使用者无从察觉。本设计把这三点列为单测必须覆盖的红线用例。

## 5. 数据视图模型（对齐 Kibana）

### 5.1 三要素

数据视图是**检索的一等公民**：KQL 检索、时间直方图、字段侧栏全部挂在视图上，而不挂在具体索引上。

| 要素 | 本设计字段名 | 说明 | 校验 |
| --- | --- | --- | --- |
| 自定义名称 | `name` | 视图的显示名 **与唯一标识** | 必填；≤64 字符；同一 ES 连接内不可重名（4007） |
| 索引匹配逻辑 | `index_pattern` | 一个 ES 索引表达式字符串 | 必填；语法见 §5.3；必须至少命中 1 个索引或数据流（4006） |
| 时间字段 | `time_field` | 作为时间轴与默认排序的 `date` 类型字段 | 必填；必须在 pattern 匹配集合的**全部成员**中都是 `date`（4009） |

与 Kibana 的对应关系：`name` = Data view name；`index_pattern` = Index pattern（Kibana 8 起 API 侧字段名为 `title`）；`time_field` = Time field。

**与 Kibana 的一处刻意收紧**：Kibana 允许 data view 不设时间字段（做非时序检索），本设计**强制必填** —— 本工具的每一次检索都以时间轴为前提（默认按时间倒序、时间直方图、时间范围选择器），没有时间字段的视图无法提供一致的交互契约。

### 5.2 创建流程（三步向导）

1. **名称** —— 用户输入，前端即时校验同连接内重名
2. **索引匹配** —— 用户输入 pattern；输入防抖 500ms 调 `probe` 预览，展示：
   - 命中数量与名称（索引 / 别名 / 数据流分别计数，名称超 20 项截断展示）
   - 命中文档总数（`docs.count` 求和，供用户确认没选错范围）
   - 命中集合为空 → 红色提示并禁止下一步（4006）
   - pattern 为 `*` 或命中数超过阈值（默认 50）→ 黄色警告「范围过宽，字段表可能被截断」，要求二次确认
3. **时间字段** —— 从探测结果的 `date` 类型字段中单选，`@timestamp` 置顶；若该字段在部分成员中缺失（4009），列出缺失成员并给出两个出路：换时间字段，或收窄 pattern

创建提交时**同步执行一次字段拉取并落库**（§6.4）—— 否则用户创建完视图却看不到任何字段。

### 5.3 索引匹配语义

| 写法 | 含义 |
| --- | --- |
| `logs-*` | 前缀通配 |
| `logs-2026.09.10,logs-2026.09.11` | 逗号分隔多索引 |
| `logs-*,-logs-debug` | 通配 + 排除 |
| `*` | 全部（前端二次确认后放行） |

固定使用下列安全默认值（不做成视图高级选项，见 §2 非目标）：

- `expand_wildcards=open` —— 不匹配已关闭索引，避免长时间挂起
- `allow_no_indices=false` —— 匹配不到就报错（4006），而不是静默返回空结果集。**这一条是「静默错误」的主要来源，必须显式关闭**
- `ignore_unavailable=false` —— 避免一部分索引不可用时结果悄悄变少

> **数据流（data stream）**：现代 ES 上 `logs-*` 命中的通常是 data stream 而非具体索引。因此探测接口要同时返回 `indices` / `aliases` / `data_streams` 三类并在展示层合并计数；**检索时直接把 pattern 交给 `_search`**（ES 原生支持通配与数据流），不展开成索引列表 —— 展开会在索引滚动时立刻失效。

### 5.4 时间字段探测

探测优先级：

1. 匹配集合中的 `@timestamp`（存在即用，也是 data stream 的强制要求）
2. `date` 类型字段中名称命中 `time` / `date` / `created` / `updated` 者
3. 仍无 → 返回 4004，前端提示手动指定

一致性校验（`pattern` 命中多个成员时）：逐成员检查 `time_field` 存在且类型为 `date`；不一致则创建接口返回 4009 并列出缺失成员。这对应 Kibana 的「You can't use a wildcard」提示 —— Kibana 的处理是要求改用非通配 pattern，本设计给出「换字段 / 收窄 pattern」两条出路，更实用。

### 5.5 字段合并策略（跨索引）

`_field_caps` 支持直接对 pattern 调用，且同名字段出现多种类型时响应里会**同时**给出（如 `{"message": {"text": {...}, "keyword": {...}}}`），据此合并：

| 情形 | 合并结果 |
| --- | --- |
| 同名**同类型** | 合并为一；能力位 `searchable` / `aggregatable` 取**全部成员为 true 才为 true**（保守，避免个别成员不可搜索导致整条查询失败） |
| 同名**多类型**（如 text + keyword） | 保留全部类型并标记 `type_conflict: true`；算子分派取**最宽的公共算子**（`match` + `lenient:true`，它在 text 与 keyword 上都能工作）；字段侧栏用警告色标注 |
| 仅**部分成员**存在该字段 | 保留并标记 `partial: true`；查询时该字段在缺失成员上自然不匹配，属预期行为 |
| 字段总数超上限（默认 2000） | 截断保留前 2000 个（按字段名排序保证稳定），响应带 `truncated: true`，前端提示收窄 pattern |

字段表数据结构：

```go
type Field struct {
    Path         string   // env / log.level / message.keyword
    Types        []string // ["text","keyword"]，多类型即冲突
    Searchable   bool     // 全部成员均可搜索才为 true
    Aggregatable bool
    TypeConflict bool
    Partial      bool
    SubFields    []string // text 的 .keyword 等
    Analyzer     string
    Format       string   // date 的 format
}

type Schema struct {
    ViewID    int64
    Pattern   string
    TimeField string
    Fields    []Field
    ByPath    map[string]*Field
    Truncated bool
}
```

## 6. 字段本地存储与异步同步

### 6.1 表结构（迁移 V26）

当前最新迁移为 **V25**（部署模板 tags 列），本设计新增 **V26**。遵循 `boundaries/rules.md` §5：仅新增表、必备 `id`/`created_at`/`updated_at`（TEXT localtime）、JSON 快照列命名 `*_json`。

```sql
CREATE TABLE IF NOT EXISTS es_views (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    conn_id       INTEGER NOT NULL REFERENCES es_conns(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    index_pattern TEXT NOT NULL,
    time_field    TEXT NOT NULL DEFAULT '',
    fields_json   TEXT NOT NULL DEFAULT '[]',
    stats_json    TEXT NOT NULL DEFAULT '{}',
    sync_status   TEXT NOT NULL DEFAULT 'idle'
                  CHECK (sync_status IN ('idle','syncing','failed')),
    sync_error    TEXT NOT NULL DEFAULT '',
    synced_at     TEXT,
    created_at    TEXT NOT NULL DEFAULT (datetime('now','localtime')),
    updated_at    TEXT NOT NULL DEFAULT (datetime('now','localtime'))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_es_views_conn_name ON es_views(conn_id, name);
```

- `fields_json`：合并后的字段表快照（`[]Field`）
- `stats_json`：匹配概览（`indices` / `aliases` / `data_streams` 的名称与计数、`docs_count`、`truncated`）
- `conn_id` 外键 `ON DELETE CASCADE`：连接删除时视图一并清理（无孤儿视图）
- 视图表**不含任何凭据材料**；同步时临时解密连接密码，用完即弃，不落库（rules §3.1）

> **为什么不把字段拆成独立的 `es_view_fields` 表**：字段是「随时可由 ES 重算的派生数据」，不是需要逐行更新的业务实体。拆表会带来 N 行写入、逐行 diff 与事务成本，却换不到任何查询收益 —— 检索路径永远整表读取（编译 KQL 需要全量字段名与类型）。用单个 `*_json` 快照**整体替换**，反而天然实现了需求里的「重新获取单一视图的所有字段变更」。仅当后续出现「按字段检索视图」的需求时才值得拆表。

### 6.2 同步时机（三条路径）

| 触发 | 时机 | 范围 |
| --- | --- | --- |
| **启动同步** | 进程启动后延迟 10s 执行一次（让 HTTP 服务先就绪，避免启动被 ES 阻塞） | 全部视图，**并行拉取 + 单事务批量入库** |
| **周期同步** | 此后每 **4 小时** 一轮 | 全部视图，**并行拉取 + 单事务批量入库** |
| **手动刷新** | 视图界面「刷新视图」按钮 | **单个视图**，同步执行并**立即落库**（用户在前端等结果，见 §6.3） |

实现沿用 `main.go` 中 `runRetentionLoop` 的既有写法（`run()` 先执行一次 + `time.NewTicker(24*time.Hour)` 循环），新增 `runESViewSyncLoop(...)`，在 `main.go` 里以 `go runESViewSyncLoop(...)` 启动。

> **放置位置的约束**：同步需要解密连接密码，而 `rules.md` §2.6 规定「仅 api 层（及 main 注入的 probe）调用加解密」。因此 worker 实现放在 **`api/tool/es/view_sync.go`**（`crypto.Service` 由 main 注入），**不能**放进 `common/` 或 `store/`。这与 `common/probe` 的既有分层一致。

### 6.3 并发模型：并行拉取 → 内存缓存 → 单事务批量入库

同步分三步，职责严格分离：

| 阶段 | 行为 | 是否碰库 |
| --- | --- | --- |
| ① **并行拉取** | 每个视图一个 goroutine，信号量限制并发（**默认 4**，常量 `esSyncConcurrency`）；结果只写内存切片 | 否 |
| ② **结果缓存** | 全部 goroutine 结束后汇总为 `[]viewSyncResult`（成功 / 失败各带自己的载荷） | 否 |
| ③ **批量入库** | **单事务**批量 UPDATE：成功视图写新快照，失败视图只写状态 | 是，**整轮仅一次** |

改为并行的理由：拉取阶段全是 ES **读**操作（`_resolve/index` + `_field_caps`），读并发天然安全且无副作用；此前串行的唯一理由是规避 SQLite 写锁，而一旦把写入收敛成单事务，这个理由就消失了。启动轮并行后，字段就绪时间从「N × 单视图耗时」降到约「1 × 单视图耗时」。

「只更新正常的」在 SQL 层的落法（`store/repo/es_view.go` 的 `SaveSyncResults`，沿用 `store/repo/orchestration.go` 等既有的 `store.DB.Begin()` 事务写法）：

```go
// 单事务：成功视图整份替换快照；失败视图仅落状态，fields_json / stats_json / synced_at 原样保留
for _, r := range results {
    if r.Err == nil {
        tx.Exec(`UPDATE es_views
                    SET fields_json=?, stats_json=?, sync_status='idle', sync_error='',
                        synced_at=datetime('now','localtime'), updated_at=datetime('now','localtime')
                  WHERE id=?`, r.FieldsJSON, r.StatsJSON, r.ViewID)
    } else {
        tx.Exec(`UPDATE es_views
                    SET sync_status='failed', sync_error=?,
                        updated_at=datetime('now','localtime')
                  WHERE id=?`, r.ErrMsg, r.ViewID)
    }
}
```

约束与边界：

- **单视图互斥**：进程内 `sync.Map`（`map[int64]struct{}`）占位，**覆盖「拉取 + 入库」全过程**。手动刷新撞上本轮正在同步的视图 → 4008（409）。项目是单进程部署，无需引入分布式锁
- **并发安全已核实**：`crypto.Service` 只持有不可变的 `cipher.AEAD`，`Decrypt` 内仅调 `Open`、无共享可变状态，**可并发解密**连接密码（`common/crypto/crypto.go`）；`escClient` 每次请求自建 body，无共享缓冲
- **并发上限 4**：`_field_caps` 对宽 pattern 是重操作（要展开全部字段能力），且需为在线检索留出连接余量，故不与项目里「批量探活并发 5」拉齐而取更保守的 4
- **单视图超时 30s**：复用 `escClient` 的 `http.Client{Timeout: 30s}`。超时按该视图失败处理，不阻塞本轮其它视图
- **整轮不设总超时**，但整轮起止与耗时记入日志；下一轮需等本轮结束才发起（ticker 内串行发起，轮次之间不重叠）
- **连接级失败不扩散**：某连接的凭据失效 / 不可达 → 该连接下全部视图标记失败，其它连接照常
- **手动刷新是独立路径**：单视图、同步执行、结果**立即落库**（用户在前端等结果，不适合延迟批量）。它与后台轮次共用同一互斥标记，因此不会与后台轮次并发写同一行
- **`synced_at` 一律更新**：即使字段内容与上次完全一致也更新，否则前端「字段同步于 X」会一直停在旧时间。视图数量涨到数百后可再加「内容未变则跳过 UPDATE」的优化，当前不必

> 为什么不做「每个视图拉完就立刻写」：那会退化成 N 次独立事务。SQLite 是单写者（`store/db.go` 已开 WAL + `busy_timeout=5000`），并发写只会相互排队，并在长轮次里显著抬高 `database is locked` 概率；而单事务批量写入让整轮只取一次写锁，同时保证库内看到的是**某一轮的完整快照**，不会出现「一半视图新字段、一半旧字段」的中间态。

### 6.4 失败与降级

| 情形 | 处理 |
| --- | --- |
| 创建视图时首次同步失败 | 接口报错并按 §5.2 引导重试，**不落库**（一个看不到字段的视图是脏数据） |
| 周期 / 手动同步失败（单个视图） | **只更新该视图的状态列**：`sync_status='failed'` + `sync_error` 记归一化后的 ES 报错；`fields_json` / `stats_json` / `synced_at` **全部保持原值不被覆盖**；前端展示「字段数据同步于 X · 最近一次失败：Y」 |
| 同一轮里部分视图失败 | **只入库成功的那些**（「只更新正常的」）—— 失败视图留在上一份快照上，一轮内可能出现「部分视图是本轮新数据、部分是上轮数据」，但每个视图自身始终是完整快照 |
| 批量入库整轮失败（DB 错误 / 事务回滚） | 本轮结果整体丢弃并记日志，库内全部视图保持上一份成功快照；下一轮自愈。**不做逐视图补偿写** |
| 批量入库前进程退出 | 本轮拉取结果丢失（内存缓存未落库），库内不变；下一轮自愈。属可接受降级 —— 旧快照仍可正常检索 |
| 连接不可达 / 凭据失效 | 该连接下所有视图标记失败并记日志，**不影响其它连接**的同步 |
| 连接被删除 | 视图随外键级联删除 |
| 视图正在同步时进程退出 | `sync_status` 残留 `syncing`；启动时把全部 `syncing` 重置为 `idle` 再开始同步（自愈） |

## 7. 架构分层

```
KQL 文本
   ↓  kql.go       词法 + 语法分析 → []Token（含位置信息）
Token 序列
   ↓  dsl.go       Token + Schema → ES DSL 搜索体
ES DSL (JSON)
   ↓  es_client.go  既有 HTTP 层，只负责发请求与解析响应
Elasticsearch（本链路只读，见 §3）
   ↑  mapping.go    GET /_resolve/index + /_field_caps + /_mapping → Schema
   ↑  view_sync.go  并行拉取 Schema → 单事务批量落进 es_views.fields_json（只写成功的视图）
   本地库 es_views ← 检索时 Schema 在这里读，不再实时访问 ES
   ↓  result_cache.go  一次拉满 1000 条结果存进程内存，翻页只在内存切片（零 ES 往返）
   ↑  analyze.go       复用同一份 query，size:0 + aggs 走全量匹配集（只读）
```

两个关键结论：

1. **`dsl.go` 的入参必须是 `Schema`**，这是相对参考实现最重要的结构性改动。字段表带来了三件事：字段存在性校验（写错字段名 → 4002，而不是等 ES 报错）、按类型分派算子（§9）、把 `multi_match.fields:["*"]` 换成真实候选字段列表（修复参考实现缺陷 4）。
2. **检索路径零额外 ES 往返**。因为 `Schema` 已在本地库，编译 KQL 不需要实时拉 mapping —— 一次检索只有一次 `_search` 请求。这也是把字段本地化（而非每次实时查）在性能上的直接收益。

### 文件规划（受 `rules.md` §1 单文件 ≤300 行约束）

| 层 | 文件 | 内容 |
| --- | --- | --- |
| 后端 | `store/migrations.go` | 新增 `migrateV26`（`es_views` 表） |
| 后端 | `model/es.go` | 增加 `ESView` / `ESViewField` 模型 |
| 后端 | `store/repo/es_view.go` | `es_views` 存取（不含业务判断）；含 `SaveSyncResults` 单事务批量写入 |
| 后端 | `api/tool/es/mapping.go` | `_resolve/index` 探测 + `_field_caps` + `_mapping`，跨索引合并（§5.5） |
| 后端 | `api/tool/es/view.go` | 视图 CRUD handler + pattern 探测 handler |
| 后端 | `api/tool/es/view_sync.go` | 后台同步循环（启动 1 次 + 每 4h）：并行拉取 + 内存缓存 + 单事务批量入库；含手动刷新与单视图互斥 |
| 后端 | `api/tool/es/kql.go` | 词法分析 + 递归下降语法分析，输出带位置的 Token 序列 |
| 后端 | `api/tool/es/dsl.go` | Schema 感知的 DSL 组装（query + sort + aggs + highlight + _source） |
| 后端 | `api/tool/es/handler_search.go` | 检索 + KQL 校验 + 内存分页三个 handler（无 DSL 回显） |
| 后端 | `api/tool/es/timeparse.go` | **唯一的时间转换入口**：`YYYY-MM-DD HH:mm:ss`（UTC+8）↔ 毫秒整数（§9） |
| 后端 | `api/tool/es/result_cache.go` | 结果集内存缓存：1000 条窗口 + `result_id` + 容量/TTL/LRU 淘汰 + 分页切片（§11） |
| 后端 | `api/tool/es/analyze.go` | 两种分析的聚合组装与结果整形（去重计数 / 图状）+ 参数校验（§12） |
| 后端 | `api/tool/es/lifecycle.go` | ILM 策略 CRUD、索引 ILM 状态、DLM 保留期（含 W1–W4 + 审计）（§13） |
| 后端 | `api/tool/es/template.go` | 索引模板 / 组件模板 CRUD + `_simulate`（含 W5–W8 + 审计）（§13） |
| 单测 | `api/tool/es/kql_test.go` / `dsl_test.go` / `mapping_test.go` / `timeparse_test.go` / `result_cache_test.go` | 语法、优先级、类型分派、跨索引合并、§4 六条红线用例、时间字符串解析、缓存覆盖与失效 |
| 前端 | `template/static/pages/es.js` | 瘦身：连接管理 + 概览/节点 tab |
| 前端 | `template/static/pages/es_index.js` | 索引 tab（集群索引列表 + 单索引 mapping 抽屉） |
| 前端 | `template/static/pages/es_views.js` | 数据视图 tab（列表 + 三步向导 + 刷新/编辑/删除） |
| 前端 | `template/static/pages/es_fields.js` | Discover 左栏字段侧栏（含同步状态、冲突/部分标记） |
| 前端 | `template/static/pages/es_kql.js` | KQL 输入条（错误定位 / 字段补全 / 语法帮助） |
| 前端 | `template/static/pages/es_discover.js` | Discover 主视图（直方图 + 内存分页文档表 + 单条展开） |
| 前端 | `template/static/pages/es_analyze.js` | 分析抽屉（去重计数 / 图状分析）+ 纯 SVG 图表（§12.3） |
| 前端 | `template/static/pages/es_lifecycle.js` | 生命周期 tab（ILM 策略 + 索引状态 + DLM 保留期） |
| 前端 | `template/static/pages/es_templates.js` | 索引模板 tab（索引模板 + 组件模板 + 模拟预览） |

> **前置依赖（阻塞项）**：`api/tool/es/es_client.go` 现为 451 行、`template/static/pages/es.js` 现为 411 行，均已超 300 行上限。`workflow/tasks/07-api-refactor` 的 B5 / B8 正是为这两个文件规划的拆分，但目前 07 只完成到 B3，**B5/B8 尚未执行**。本任务必须在 07 的拆分落地后开工，或把该拆分并入本任务第一步 —— 否则新增文件会与超限文件挤在同一层，只会加剧违规。
>
> **v1.4 追加**：本设计让前端 tab 从 3 个（集群概览 / 索引 / 文档查询）变成 5 个（+ 数据视图 / 生命周期 / 索引模板），并新增内存分页与分析抽屉 —— `es.js` 只会更长。这一步**不能再延后**。

## 8. KQL 语法规范

### 8.1 词法单元

| 类型 | 说明 | 示例 |
| --- | --- | --- |
| `field` | 字段路径，字符集 `[A-Za-z0-9_.@-]`，紧邻算子时识别为字段 | `env` / `log.level` / `@timestamp` |
| `value` | 未加引号的字面量 | `prod` |
| `quoted` | 引号包裹（`"` 或 `'`），**内容不参与逻辑词识别** | `"AND or NOT"` |
| `op` | 比较算子 `: = != > >= < <=` | `>=` |
| `logic` | `and` / `or` / `not`（大小写不敏感），及 `&&` / `\|\|` / `!` | `AND` |
| `lparen` / `rparen` | 分组括号 | `(` `)` |
| `star` | 独立星号（匹配所有）或在值内作通配标记 | `*` / `nginx*` |

每个 Token 携带 `Pos`（起始字节偏移）与 `Raw`（原始片段），错误信息据此定位。

### 8.2 语法与优先级

```
query    := orExpr
orExpr   := andExpr ( "or" andExpr )*
andExpr  := unary ( ( "and" )? unary )*        // 空格 = 隐式 AND
unary    := "not" unary | primary
primary  := "(" query ")" | term
term     := field op value | value | "*"
```

优先级 **NOT > AND > OR**，同级左结合，括号可覆盖。Kibana KQL 中相邻子句（空格分隔）默认 AND，本设计保持一致。

DSL 生成策略：**不做扁平化拆分**（参考实现把顶层 OR 切分成多个 `should` 子句，遇到 `field:a and field:b or field:c` 这类混合结构时容易漏子句）。改为在 AST 上按需生成嵌套 `bool`：

- `AND` → `bool.must`
- `OR` → `bool.should` + `minimum_should_match: 1`（强制，见 §4 缺陷 6）
- `NOT` → `bool.must_not`
- 三种混用时生成**同一层** `bool` 的 `must` / `should` / `must_not`，而不是嵌套多层

### 8.3 转义

保留字符集：`* ? : = ! < > ( ) " ' , \` 与空格。单遍扫描处理：

| 输入 | 结果 |
| --- | --- |
| `\*` | 字面星号，**不**触发通配语义 |
| `\"` `\'` | 引号字面量，不充当定界符 |
| `\\` | 单个反斜杠 |
| `\ ` | 空格字面量 |
| `\x`（x 非保留字符） | 报 4001，提示合法转义集 |

### 8.4 与 Kibana KQL 的差异（明示取舍）

| 特性 | Kibana KQL | 本设计 | 原因 |
| --- | --- | --- | --- |
| 多值简写 `field:(a or b)` | 支持 | **不支持** | 一期用 `(field:a or field:b)` 替代，降低语法复杂度 |
| 区间简写 `field:(>=100 and <200)` | 支持 | **不支持** | 用 `field:>=100 and field:<200` 替代 |
| `nested` 字段 | 自动包 `nested` query | **报 4002** | 需索引侧 nested path 语义，一期不猜 |
| 脚本 / runtime field | 通过 Lind 支持 | **不支持** | 超出需求 |
| 语法大小写 | `and/or/not` 小写 | 大小写均接受 | 降低输入摩擦 |

## 9. 字段类型感知

字段表来源与合并规则见 §5.5；以下为 `dsl.go` 依字段类型分派算子的完整规则，**这是修复参考实现缺陷 1 与 4 的核心**：

| ES 类型 | `field:value` | `field:"a b"` | `field:>=N` | `field:abc*` | `field:*` |
| --- | --- | --- | --- | --- | --- |
| `text` | `match`（best_fields，单字段） | `match_phrase` | 4002 不支持 | `query_string` + `analyze_wildcard:true` | `exists` |
| `keyword` | `term`（精确） | `term` | 4002 不支持 | `wildcard` | `exists` |
| `long` / `integer` / `short` / `byte` / `float` / `double` / `scaled_float` / `half_float` | `term`（数值强转，转换失败报 4001） | `term` | `range` `gte`/`gt`/`lte`/`lt` | 4001 | `exists` |
| `date` | `range`（字面量编译期解析为**毫秒整数**，支持 `now-1d` 相对时间） | 4002 不支持 | `range`（边界为毫秒整数） | 4001 | `exists` |
| `boolean` | `term`（`true`/`false`） | 4002 不支持 | 4002 不支持 | 4002 不支持 | `exists` |
| `ip` | `term` | `term` | `range`（支持 CIDR） | 4002 不支持 | `exists` |
| `object` / `nested` | 引用叶子字段路径 | — | — | — | `exists` |
| **`type_conflict`（多类型）** | `match` + `lenient:true`（text/keyword 上都能工作的最宽公共算子） | 同左 | 4002 | 4002 | `exists` |
| **`partial`（部分成员缺失）** | 按已有类型正常分派（缺失成员上自然不匹配） | | | | |
| **字段不存在** | 4002，message 附带最接近的字段名建议 | | | | |
| **字段表未同步（空）** | 4004 语义错误：提示先刷新视图字段 | | | | |

裸值（不带字段名）统一走：

```json
{ "multi_match": { "query": "...", "type": "best_fields", "lenient": true, "fields": ["<字段表中全部 searchable 字段>"] } }
```

引号包裹的裸值改用 `type: "phrase"` —— 与缺陷 1 的对策一致。字段列表来自字段表，**不再使用 `["*"]`**。

### 时间范围与 filter context

时间范围不进 KQL 文本，由前端时间选择器生成，注入**最外层 `bool.filter`**（filter context 不参与打分，且有 query cache 优势）。

**三条硬规则**：

1. **接受体唯一：字符串 `YYYY-MM-DD HH:mm:ss`**。界面展示、界面输入、请求体三处**都是同一种字符串**，前端不做任何时间换算。快捷时间（近 15m / 1h / 24h / 7d）只是界面上的「填充预设」—— 点击后把算好的字符串填进选择器，**发给后端的仍是同一个字符串**；接口不认 `now-1h` 这类相对表达式
2. **时区固定 UTC+8**：字符串不带时区后缀，一律按 `+08:00` 解释（与项目 `datetime('now','localtime')` 的本地时间口径一致）。前端即便运行在其它时区，也按 `+08:00` 换算后再格式化 —— 即「界面显示的就是北京时间」
3. **区间恒为下半开 `[start, end)`**：解析为毫秒后，起始用 `gte`、结束用 `lt`。刻意取 `lt` 而非 `lte` —— 否则相邻两个区间（17:00–18:00 与 18:00–19:00）会把 `18:00:00.000` 这一瞬间的文档**重复计入两次**

后端解析规则（`timeparse.go`，全项目**唯一**的时间转换入口）：

| 输入 | 结果 |
| --- | --- |
| `"2026-09-11 17:00:00"` | `1789117200000` |
| `"2026-09-11 17:00"` | 400（必须含秒，避免歧义，不做「补零」这种猜法） |
| `"2026-09-11T17:00:00"` | 400（只接受空格分隔的单一格式） |
| `"now-1h"` / `"1789117200000"` | 400（不接受相对表达式，也不接受毫秒数字） |
| 缺失 / 空串 / `start >= end` | 400（4010） |

```json
{
  "bool": {
    "filter": [ { "range": { "@timestamp": { "gte": 1789117200000, "lt": 1789120800000 } } } ],
    "must":   [ /* KQL 编译结果 */ ]
  }
}
```

**时间字段名不硬编码 —— 按视图 / 索引的字段表下发**：

- 每个视图自带 `time_field`（§5.1，创建时校验其在该 pattern 的全部成员中都是 `date`），检索时 `range` 与 `sort` 一律用**该视图的 `time_field`**。不同索引集的命名并不统一（`@timestamp` / `time` / `log_time` / `event_time`），硬编码 `@timestamp` 会在非标准索引上直接查不到数据
- 单索引浏览（§14.3）走实时 `_mapping`：从该索引的字段里解析出可用时间字段，再据此生成查询
- 视图字段表为空（未同步成功）→ 4004，提示先刷新视图字段
- 分析（§12）与检索**共用同一份 query 段与同一个时间字段**，保证「图上的总数」= 「列表的 `total`」

其余要点：

- **不再下发 `time_zone`**：边界值已是绝对时间点，时区信息包含在毫秒值里；`time_zone` 只保留在 §10 的 `date_histogram` 上，作用是让桶边界对齐本地日历（否则「按天分桶」会按 UTC 切）
- **KQL 内的日期字面量另算**：KQL 是给人写的，保留人类可读形式（`field:>=2026-09-11`、`field:>=now-1d`），`kql.go` 在**编译期**按 UTC+8 解析为毫秒整数 —— 最终 DSL 里的 `range` 边界恒为整数毫秒，与界面时间范围同构；相对时间在请求发起时冻结为具体毫秒
- **非法区间**：`start` / `end` 缺失、空串、格式不符，或 `start >= end` → 4010，**不静默纠正**（不自动把区间反过来、不补秒、不换时区）

## 10. 搜索体组装

以视图 `nginx-access-*`（时间字段 `@timestamp`）、「2026-09-11 17:00:00 → 2026-09-11 18:00:00」（即请求体里的 `start` / `end` 字符串，后端解析为 `1789117200000` / `1789120800000`，差值 3 600 000 ms）、`env:prod AND (level:error OR status:>=500)`、显示 message 与 host 两列为例：

```json
{
  "query": {
    "bool": {
      "filter": [
        { "range": { "@timestamp": { "gte": 1789117200000, "lt": 1789120800000 } } }
      ],
      "must": [
        {
          "bool": {
            "must": [ { "term": { "env": "prod" } } ],
            "should": [
              { "match": { "level": "error" } },
              { "range": { "status": { "gte": 500 } } }
            ],
            "minimum_should_match": 1
          }
        }
      ]
    }
  },
  "from": 0,
  "size": 20,
  "track_total_hits": true,
  "sort": [ { "@timestamp": { "order": "desc" } }, { "_doc": { "order": "desc" } } ],
  "_source": [ "@timestamp", "message", "host" ],
  "highlight": {
    "fields": { "message": { "number_of_fragments": 2, "fragment_size": 150 } },
    "pre_tags": ["<mark>"], "post_tags": ["</mark>"]
  },
  "aggs": {
    "histogram": {
      "date_histogram": {
        "field": "@timestamp",
        "fixed_interval": "60s",
        "time_zone": "+08:00",
        "min_doc_count": 0,
        "extended_bounds": { "min": 1789117200000, "max": 1789120800000 }
      }
    }
  }
}
```

注意此处 `_search` 的路径是**索引 pattern**（`nginx-access-*`），不是单个索引。

组装要点：

| 项 | 规则 |
| --- | --- |
| 索引参数 | 直接使用视图的 `index_pattern`，由 ES 原生解析通配与数据流（不展开成索引列表） |
| 字段来源 | 排序字段 / `_source` / highlight / 分析维度，**全部取自该视图的字段表**；时间字段名取 `es_views.time_field`，**不硬编码 `@timestamp`**（§9） |
| `track_total_hits` | 恒为 `true`，让分页 `total` 是精确值而非 10000 上限估算 |
| `sort` | 用视图的 `time_field`：`[{time_field: desc}, {_doc: desc}]`；`_doc` 作稳定分页的 tiebreaker |
| `_source` | 用户勾选的列；默认为时间字段 + 前 5 个可聚合的 `keyword`/`text` 字段。为空则不下发该键（返回全量） |
| `highlight` | 仅对**类型集合含 `text`** 的字段下发，不写 `{"*": {}}` |
| 时间范围 | 请求体是字符串，**后端解析为毫秒**后恒为 `{"gte": start_ms, "lt": end_ms}`（下半开）；不下发 `time_zone`（边界已是绝对时间点）；非法 → 4010（见 §9） |
| `aggs.histogram` | `fixed_interval` 由前端按 `end_ms - start_ms` / 约 50 桶计算（秒 / 分 / 时 / 天取整）；`min_doc_count:0` + `extended_bounds{min: start_ms, max: end_ms}` 保证横轴连续（仅用于补齐横轴，不参与过滤）；`time_zone:"+08:00"` 让桶边界对齐本地日历 |
| `from` / `size` | **恒为 `from: 0`、`size: 1000`**（常量 `esResultWindow`）—— 一次拉满结果窗口，不做 ES 侧翻页（§11）；`total` 精确值随响应返回，`total > 1000` 时前端提示结果被截断 |

## 11. 结果集缓存与内存分页

### 11.1 规则

一次检索只查 ES **一次**：固定 `from: 0`、`size: 1000`，把整份结果窗口留在服务端进程内存里；此后翻页都在内存切片上完成。

| 动作 | 是否访问 ES |
| --- | --- |
| 首次查询（改 KQL / 改时间范围 / 改列 / 首次进入 Discover） | **是**，一次 `_search` |
| 翻页（上一页 / 下一页 / 跳页 / 改每页条数） | **否**，内存切片 |
| 排序（切换 `time_field` 升 / 降序） | **是**（排序在 ES 侧），随后覆盖缓存 |
| 展开单条文档 | **否**，`_source` 已随结果返回（列选择受限时见 §11.4） |
| 分析 | **是**，独立的聚合请求（§12），不属于本缓存 |
| 「刷新」按钮 | **是**，强制重查并覆盖缓存 |

`from` / `size` 恒为 `0 / 1000` —— **ES 侧深分页问题（`from + size > 10000`）在本设计里不存在**，因为从不向 ES 传 offset。代价是只能浏览前 1000 条，因此界面必须显式提示（§11.3）。

### 11.2 缓存结构与失效

| 项 | 设计 |
| --- | --- |
| 存储位置 | 进程内存（`api/tool/es/result_cache.go`），**不落 SQLite** —— 纯派生数据，落库只带来写锁与磁盘占用 |
| 缓存键 | `conn_id + view_id`（个人使用，单用户；若日后真出现多用户并发，需把用户标识并入键） |
| 缓存值 | `resultSet{ResultID, Total, TimeField, StartMS, EndMS, Columns, Hits, Histogram, CreatedAt, LastAccess}` |
| 覆盖语义 | **新查询直接覆盖同一键上的旧结果**（需求原文口径），不保留多份快照 |
| `result_id` | 每次查询生成（`conn-view-seq` 短串）并随响应下发；翻页请求必须回传，不匹配 → 4011（409）+ 前端提示「结果集已更新，请重新查询」。**这是防止「用旧页码读新结果」造成错位的关键** |
| 容量上限 | 全局 ≤ 20 个结果集、≤ 20000 条文档、≤ 64 MB（按序列化长度估算），取先到者；**超限按 LRU 淘汰** |
| 空闲 TTL | 30 分钟未访问即释放 |
| 并发 | 单把 `sync.RWMutex` 保护缓存表：读命中走 `RLock`，整份替换走 `Lock` |
| 进程重启 | 缓存为空，属预期；前端收到 4011 后重新查询即可 |

> 为什么不做「每翻一页查一次 ES」：日志类索引上一次查询动辄数百毫秒，而翻页是本控制台最高频的动作；一次拉满 1000 条换取零延迟翻页，收益明显。1000 条 × 平均 4 KB ≈ 4 MB/视图，20 个结果集上限对应约 80 MB 峰值，可接受。

### 11.3 分页语义与提示

- 每页条数：10 / 20 / 50 / 100（默认 20）。**前端首次查询就拿到全部 1000 条**，正常翻页在本地切片完成、完全不经过后端；§14.5 的 `/search/page` 只作为「页面刷新 / 重新进入视图」后前端内存态丢失的兜底路径
- 结果截断提示：`total > 1000` 时，结果区顶部常驻一条提示 ——「匹配 **12345** 条，仅可浏览按时间排序的前 **1000** 条；请收窄时间范围或补充条件」
- `window_truncated` 由后端给出（`total > window`），前端不自行计算 —— 避免两处口径不一致
- 翻页越界（`from + size > 已缓存条数`）→ 4005（400），响应带 `window` 与 `total` 便于提示

### 11.4 已知取舍：展开文档与列选择

- **列选择变化会触发重查并覆盖缓存**（`_source` 变了，必须回 ES 取）。这不是纯前端行为 —— 切换列就重查，按钮需短暂 loading，UI 上要给出「正在重新查询」的语义
- 展开单条时若需要的字段不在 `_source` 内：一期**不做**「按 id 回查单文档」（还要处理 `ids` 查询与分片路由，收益低于复杂度），改为提示「该字段不在列选择范围内，勾选后重查」
- 分页与排序的一致性：排序变化会重查，因此不存在「同一页前后排序不同」的错位；`_doc` 作 tiebreaker 只保证同一份结果内顺序稳定

## 12. 分析（去重计数 / 图状分析）

**定位**：分析是**检索结果之上的一次性动作**，不是独立菜单、不是持久 Dashboard、不保存任何配置。入口放在 Discover 结果区右上角的「分析」按钮，点开抽屉，作用对象是**当前查询**（同一 `view_id`、同一 KQL、同一时间范围）。

### 12.1 数据源：走 ES 聚合，不走内存里的 1000 条

这是本节最重要的一条判断。分析**不是**对 §11 缓存里那 1000 条文档做本地统计，而是把**同一份查询条件**发给 ES 做聚合（`size: 0` + `aggs`），统计口径是**全量匹配集**。

理由很直接：`total` 可能是 300 万，而结果窗口只有 1000 条。若在本地统计，去重计数会得到「最多 1000」这类毫无意义的数字，且数值随排序抖动而变 —— 属于典型的「不报错但结果错」。因此：

- 分析请求体 = `{query: 与检索完全相同的 bool, size: 0, aggs: {...}, track_total_hits: true}`
- filter 段与 must 段**复用** `dsl.go` 的同一份输出（§14.4），保证「图上的总数」与「列表的 `total`」必然一致
- UI 上显式标注「基于当前条件的全量匹配集（N 条），**不是当前页**」

### 12.2 两种分析

| 类型 | 目的 | 输出 |
| --- | --- | --- |
| **A 去重计数** | 该字段有多少个不同取值，以及取值分布 | 表格式：字段 / 唯一值数（约）/ Top 值条状分布 + 占比 |
| **B 图状分析** | 选维度 + 指标出图 | 柱 / 折线 / 饼 + 可切换的表格视图 |

两者共用一个抽屉（顶部两个 tab），都以「选字段 + 选参数」为输入，都只读。

**A 去重计数**

- 字段选择：1–5 个（上限 10），默认取当前 `columns` 中可聚合的字段
- 每个字段发两个聚合：`cardinality` → 唯一值个数；`terms`（`size: 50`、`order: {_count: desc}`）→ Top 值与文档数
- **唯一值个数是近似值**：`cardinality` 基于 HyperLogLog++，`precision_threshold` 上限 40000，超过后误差显著上升。UI 必须在数值前带「约」并在 tooltip 说明 —— 把它当精确值用是这类功能最常见的误读

```json
{
  "size": 0, "track_total_hits": true,
  "query": { },
  "aggs": {
    "f0": { "cardinality": { "field": "host", "precision_threshold": 40000 } },
    "f0_top": { "terms": { "field": "host", "size": 50, "order": { "_count": "desc" } } }
  }
}
```

**B 图状分析**

维度 + 指标模型（与 Kibana 的 aggregation-based chart 同构）：

| 角色 | 可选类型 | 适用字段类型 | 图形 |
| --- | --- | --- | --- |
| 维度（一级，必选） | `terms` | `keyword` / 数值 / `boolean` / `ip` | 柱状 / 饼 |
| | `date_histogram` | `date` | 折线 |
| | `histogram`（`interval` 可指定） | 数值 | 柱状 |
| 维度（二级，可选） | 同上一级 | 同上 | 堆叠柱 / 分组 |
| 指标（必选一个） | `count`（默认，无需字段） | — | — |
| | `sum` / `avg` / `min` / `max` | 数值 | — |
| | `cardinality` | 可聚合字段 | — |

- 桶数：一级 ≤ 50、二级 ≤ 20（默认一级 20 / 二级 10）；`sum_other_doc_count > 0` 时前端补一段「其他」并标注
- **`terms` 桶计数不精确**（分片级 top-N 合并），响应里的 `doc_count_error_upper_bound` 必须展示在桶的 tooltip 上（`> 0` 时）
- `date_histogram` 复用 §10 的 `fixed_interval` 推算与 `time_zone: "+08:00"`，横轴与顶部直方图对齐
- 一期不支持：`composite`、`pipeline`（derivative / cumulative）、脚本、`significant_terms`、多值 `percentiles`
- 字段不可聚合 / 指标与字段类型不符 / 桶数超限 → 4012

```json
{
  "size": 0,
  "query": { },
  "aggs": {
    "b1": {
      "terms": { "field": "host", "size": 20, "order": { "_count": "desc" } },
      "aggs": { "m1": { "avg": { "field": "bytes" } } }
    }
  }
}
```

### 12.3 出图实现：前端纯 SVG 自绘

项目 vendor 目录只有 Vue / Element Plus / axios（全部本地化，离线部署），**没有图表库**。一期分析只需柱 / 折线 / 饼三种基础图，因此**不引入 ECharts（约 1 MB）**，改为在 `es_analyze.js` 内用 SVG 自绘：

- 颜色取 `style.css` 的 `:root` 设计令牌，跟随主题
- 柱状 / 折线：固定 4 条网格线；X 轴标签超过 12 个按步长抽稀；hover 出 tooltip
- 饼图：桶数 > 8 时合并为「其他」，附数值列表
- **图 / 表可切换，表格视图是必须的** —— 数值要靠表格核对，图只用来发现形状
- 若后续要做 Dashboard 类多图拼装，再单独评估引入图表库（届时是独立任务）

### 12.4 与「不做可视化」的边界

本期**不做**：独立可视化菜单、保存的图表、多图拼装面板、定时刷新、图表导出、告警。分析结果**不落库、不缓存** —— 每次打开抽屉实时查一次 ES（聚合本身便宜，但用户会反复调参，因此前端做 300ms 防抖 + 显式「运行」按钮）。

## 13. 生命周期管理与索引模板

两者都是**集群级配置的查看与编辑**，与视图 / 检索链路独立，且是本期**唯一的写能力**（登记见 §3.3）。前端各占一个 tab（§15.1）。

### 13.1 生命周期（ILM）

| 能力 | ES 调用 | 读写 |
| --- | --- | --- |
| 策略列表（名称 / 版本 / 修改时间 / 阶段摘要 / 内置标记） | `GET /_ilm/policy` | 读 |
| 策略详情（完整 JSON） | `GET /_ilm/policy/{name}` | 读 |
| 新建 / 覆盖策略 | `PUT /_ilm/policy/{name}` | **写（W1）** |
| 删除策略 | `DELETE /_ilm/policy/{name}` | **写（W2）** |
| 索引的 ILM 状态（关联策略 / 当前阶段与步骤 / `age` / 卡住原因） | `GET /{pattern}/_ilm/explain` | 读 |
| 重试卡住的步骤 | `POST /{index}/_ilm/retry` | **写（W3）** |

- **索引 ILM 状态接受 pattern**，所以**视图详情页可以直接给一个入口**（`GET /{view.index_pattern}/_ilm/explain`）——「这个视图命中的索引现在跑到生命周期的哪一步」是最常被问的问题
- 策略编辑用 **JSON 编辑器**（与 §15.2 的 KQL 输入条不同，这里不做表单建模）：ILM 的阶段 / 动作组合太多，表单化只会做窄。提交前前端做 `JSON.parse` + 结构粗校验，最终校验交给 ES
- **内置策略只读**：`ilm-history-ilm-policy`、`.fleet-*` 等由 ES 管理（`_meta.managed = true`）→ 列表置灰、后端拒绝（4013 / 403）
- **删除被索引引用的策略**：ES 会拒绝，**把 ES 原始报错完整回显**（含引用它的索引名），映射为 4014（409）。**不做「先解绑再删」的自动处理** —— 那属于替用户做危险决定

### 13.2 数据流生命周期（DLM）

目标版本 9.5.3 上，data stream 的保留策略由 DLM 管理（`PUT /_data_stream/{name}/_lifecycle`），与 ILM 是两套机制。本期只做最小集：

| 能力 | ES 调用 | 读写 |
| --- | --- | --- |
| 数据流列表与保留期 | `GET /_data_stream` | 读 |
| 设置保留期 | `PUT /_data_stream/{name}/_lifecycle` | **写（W4）** |

明确不做：ILM 索引迁移为 data stream（`POST /_ilm/migrate_to_data_stream`）、ILM 全局启停（`POST /_ilm/start|stop`）、手工 `rollover` 触发、frozen 层可搜索快照操作。

### 13.3 索引模板与组件模板

| 能力 | ES 调用 | 读写 |
| --- | --- | --- |
| 索引模板列表（名称 / `index_patterns` / `priority` / 版本 / `composed_of`） | `GET /_index_template` | 读 |
| 模板详情（完整 JSON） | `GET /_index_template/{name}` | 读 |
| 新建 / 覆盖模板 | `PUT /_index_template/{name}` | **写（W5）** |
| 删除模板 | `DELETE /_index_template/{name}` | **写（W6）** |
| 组件模板列表 / 详情 | `GET /_component_template`、`GET /_component_template/{name}` | 读 |
| 组件模板新建 / 覆盖 / 删除 | `PUT|DELETE /_component_template/{name}` | **写（W7 / W8）** |
| **模拟**：给定模板 + 一个假想索引名，预览合并后的 settings / mappings / aliases | `POST /_index_template/_simulate` | 读 |

- **模拟（simulate）是这块最有价值的能力，必须做**：模板写错了通常要等下一次索引滚动才暴露，而 `_simulate` 能在保存前告诉你「这个索引会被创建成什么样」。前端做成「保存前预览」+「详情页试算」两个入口
- 索引模板常由多个组件模板拼装，详情页把 `composed_of` 展开成有序列表，并标出每个组件贡献了哪些键 —— 逐层合并的最终结果以 `_simulate` 的输出为准
- **内置模板只读**（名称以 `.` 开头或 `_meta.managed = true`）→ 4013（403）

### 13.4 与「只读」原则的关系

v1.1–v1.3 写的「新增功能一律只读」自本期起作废：生命周期与模板管理是需求明确要求做上的能力，天然属于写操作。作为交换，写能力被压缩到一个很小的白名单（W1–W8），并附加四条硬约束（名称白名单 / 内置资源只读 / 二次确认与请求回显 / 全部落审计，见 §3.3）——**「可以写」不等于「随便写」**。

## 14. 接口设计

所有端点挂在既有 `/api/v1/es/:id` 分组下。

### 14.1 数据视图

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/es/:id/views` | 视图列表（含 `sync_status` / `synced_at` / 字段数 / 命中概览） |
| POST | `/es/:id/views` | 创建 `{name, index_pattern, time_field}`；**创建时同步拉一次字段并落库** |
| GET | `/es/:id/views/:vid` | 视图详情（含完整字段表 `fields`） |
| PUT | `/es/:id/views/:vid` | 改名 / 改 pattern / 改时间字段；改 pattern 或时间字段后自动重同步 |
| DELETE | `/es/:id/views/:vid` | 删除视图（不触碰 ES） |
| POST | `/es/:id/views/:vid/refresh` | **手动刷新单个视图**：同步执行，重新拉取该视图全部字段变更并返回新字段表 |

### 14.2 探测（创建向导用，不落库）

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/es/:id/index-pattern/probe` | 入参 `{index_pattern}` → 命中概览（indices / aliases / data_streams 与 `docs_count`）+ 候选时间字段 + 字段摘要；`expand_wildcards=open`、`allow_no_indices=false` |

### 14.3 索引浏览（排障用，不落库）

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/es/:id/indices/:index/mapping` | **单个索引的原始 mapping**，实时拉取。与视图的合并字段表用途不同：这里看的是某个索引的真实形态，不受合并策略影响 |

### 14.4 检索与校验

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/es/:id/kql/validate` | 只校验不执行：返回是否通过，失败时带位置与片段（`pos` / `len`）。**不返回任何编译产物** |
| POST | `/es/:id/search` | **语义变更**：请求体由 `{index, query}` 改为 `{view_id, kql, start, end, ...}`；**一次拉满 1000 条存入内存**并返回 `result_id` |

`POST /es/:id/kql/validate` 的存在理由只有一个 —— 让输入框能实时提示「第 N 个字符处的字段名不存在 / 算子与字段类型不匹配」。v1.3 起**移除了 DSL 预览**，因此该端点不叫 `compile` 而叫 `validate`：**响应体里没有任何 DSL 字段**。

```json
{ "ok": false, "error": { "code": 4002, "message": "字段 envv 不存在，是否想用 env？", "pos": 0, "len": 4 } }
```

> 与 `/search` 的错误码**同源但不同 HTTP 状态**：校验端点**恒返回 200**（`ok:false` 是输入过程中的正常状态），否则防抖请求会把浏览器控制台与接口错误率面板刷满；`/search` 遇到同样的错误才返回 HTTP 400 + 4001 / 4002。

`POST /es/:id/search` 请求：

```json
{
  "view_id": 3,
  "kql": "env:prod and (level:error or status:>=500)",
  "start": "2026-09-11 17:00:00",
  "end": "2026-09-11 18:00:00",
  "columns": ["@timestamp", "message", "host"],
  "histogram_interval": "60s"
}
```

- `start` / `end`：**必填**，字符串 `YYYY-MM-DD HH:mm:ss`，**按 UTC+8 解释**。**这是唯一的时间接受体** —— 前端不做任何时间转换，后端统一转毫秒（§9）
- 区间语义固定 `[start, end)`；缺失、空串、格式不符或 `start >= end` → 4010
- **不再有 `size` / `from`**：结果窗口由后端常量固定为 1000 条（§11），翻页走 §14.5
- 请求体里**不再出现索引名与时间字段** —— 二者都由 `view_id` 从本地 `es_views` 解析；查 ES 时下发的**时间字段名取自该视图的 `time_field`**，不硬编码 `@timestamp`（§9）

响应 `data`：

```json
{
  "took": 12, "timed_out": false,
  "result_id": "3-1789117200000-7",
  "total": 12345, "total_relation": "eq",
  "window": 1000, "window_truncated": true,
  "view_id": 3, "index_pattern": "nginx-access-*", "time_field": "@timestamp",
  "start": "2026-09-11 17:00:00", "start_ms": 1789117200000,
  "end": "2026-09-11 18:00:00", "end_ms": 1789120800000,
  "columns": ["@timestamp", "message", "host"],
  "hits": [
    { "index": "nginx-access-2026.09.11", "id": "abc", "score": 1.23,
      "source": { }, "highlight": { "message": ["<mark>error</mark> ..."] } }
  ],
  "histogram": [
    { "key": 1789117200000, "key_as_string": "2026-09-11T17:00:00.000+08:00", "doc_count": 42 }
  ]
}
```

- `hits` 一次性返回最多 1000 条（整份结果窗口），同时已存入服务端内存；前端本地切片翻页后**不再请求后端**
- `window_truncated: total > 1000` —— 前端据此显示「匹配 12345 条，仅可浏览前 1000 条」
- `start` / `end` 原样回显 + 解析后的 `start_ms` / `end_ms`：便于用户与排障核对「我传的字符串到底被解释成了哪个时间点」

> **响应不含编译产物**。v1.3 移除了 v1.1 / v1.2 的 `dsl` 回显：使用者不需要知道 KQL 被转成了什么。排障时把「KQL 原文 + 编译结果 + `start` / `end` 与其毫秒值」按 **debug 级别**打进服务端日志 —— 既保留可核对性，又不把内部实现变成对外契约。
>
> `histogram[].key` 为毫秒时间戳，由前端按北京时间格式化展示。

> 关于移除 `query` 与 `index` 参数：唯一消费方是 `template/static/pages/es.js`，与本任务同批次改造，仓库内无其他引用，`rules.md` §4 未要求对外兼容，故直接替换而不保留过渡字段。请求体仍带 `query` 或 `index` 时返回 400 并提示改用 `view_id` + `kql`。

### 14.5 结果集分页

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/es/:id/search/page` | 入参 `{result_id, from, size}` → 从**内存**结果集切片返回；**不访问 ES**；越界 → 4005，`result_id` 失效 → 4011 |

```json
{ "result_id": "3-1789117200000-7", "from": 20, "size": 20 }
```

> 前端首次查询已拿到全部 1000 条，正常翻页在本地完成；本端点服务于「页面刷新 / 重新进入视图」这类前端内存态丢失的场景，也是 §11.2 缓存语义的唯一远端入口。

### 14.6 分析

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/es/:id/analyze/distinct` | 去重计数：入参 `{view_id, kql, start, end, fields: [...]}` → 每字段的唯一值数（近似）+ Top 值分布 |
| POST | `/es/:id/analyze/chart` | 图状分析：入参 `{view_id, kql, start, end, dim1, dim2?, metric}` → 桶与指标序列 |

```json
{
  "view_id": 3,
  "kql": "env:prod",
  "start": "2026-09-11 17:00:00", "end": "2026-09-11 18:00:00",
  "dim1": { "type": "terms", "field": "host", "size": 20 },
  "metric": { "type": "avg", "field": "bytes" }
}
```

- 两者都**不需要 `result_id`** —— 分析走全量匹配集（§12.1），与内存结果集无关；`start` / `end` 的格式与校验同 §14.4（4010）
- 响应带 `total`（与检索同源的精确总数），供界面显示「基于全量 N 条，非当前页」
- 参数越界 / 字段类型不匹配 / 桶数超限 → 4012

### 14.7 生命周期管理

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/es/:id/ilm/policies` | 策略列表（含阶段摘要与 `managed` 标记） |
| GET | `/es/:id/ilm/policies/:name` | 策略详情（原始 JSON） |
| PUT | `/es/:id/ilm/policies/:name` | **写（W1）**新建 / 覆盖 |
| DELETE | `/es/:id/ilm/policies/:name` | **写（W2）**删除 |
| GET | `/es/:id/ilm/explain` | 索引 ILM 状态，入参 `pattern`（可直接传视图的 `index_pattern`） |
| POST | `/es/:id/ilm/retry` | **写（W3）**：入参 `{index}`，仅接受具体索引名 |
| GET | `/es/:id/data-streams` | 数据流列表与保留期 |
| PUT | `/es/:id/data-streams/:name/lifecycle` | **写（W4）**设置保留期 |

### 14.8 索引模板与组件模板

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/es/:id/index-templates` | 索引模板列表（名称 / `index_patterns` / `priority` / `composed_of`） |
| GET | `/es/:id/index-templates/:name` | 模板详情（原始 JSON） |
| PUT | `/es/:id/index-templates/:name` | **写（W5）**新建 / 覆盖 |
| DELETE | `/es/:id/index-templates/:name` | **写（W6）**删除 |
| GET | `/es/:id/component-templates`、`/es/:id/component-templates/:name` | 组件模板列表 / 详情 |
| PUT | `/es/:id/component-templates/:name` | **写（W7）** |
| DELETE | `/es/:id/component-templates/:name` | **写（W8）** |
| POST | `/es/:id/index-templates/_simulate` | 模拟：入参 `{template 或 name, index_name}` → 合并后的 settings / mappings / aliases |

> W1–W8 的安全约束（名称白名单、内置资源只读、二次确认、审计）见 §3.3，实现时逐条对照。

### 14.9 既有索引管理（保留，不属本期新增）

| 方法 | 路径 | 处理 |
| --- | --- | --- |
| POST | `/es/:id/index` | **保留**：路由注册与前端入口均维持现状 |
| DELETE | `/es/:id/index/:index` | **保留**：同上 |

> 依 §3.2。本节仅作登记，说明它们是**既有**写能力，本期不新增、不下架。

## 15. 前端设计

### 15.1 视图结构

侧边栏保持单一 `es` 入口（hash 路由是**扁平名字**、不支持路径参数，故不新增路由）。层级在页面内部展开：

```
es.js
├─ tab 集群概览      （不动）
├─ tab 索引          ← es_index.js（集群索引列表 + 单索引 mapping 抽屉 + 新建 / 删除索引，后两者为既有能力保留，见 §3.2）
├─ tab 数据视图       ← es_views.js（视图列表 + 三步向导 + 刷新 / 编辑 / 删除）
│    └─ 点视图名 → Discover 全宽视图
│         ├─ 左栏：字段侧栏（本地字段表 + 同步状态 + 冲突/部分标记）      ← es_fields.js
│         ├─ 顶部：视图切换下拉 + KQL 输入条 + 时间选择器                 ← es_kql.js
│         ├─ 中部：时间直方图（可拖拽选范围）
│         ├─ 右上：分析按钮 → 分析抽屉（去重计数 / 图状分析）             ← es_analyze.js
│         └─ 底部：文档表（内存分页）+ 单条展开                           ← es_discover.js
├─ tab 生命周期       ← es_lifecycle.js（ILM 策略列表/详情 + 索引状态 + DLM 保留期）
└─ tab 索引模板       ← es_templates.js（索引模板 + 组件模板 + 模拟预览）
```

组件按 `stacks-forms.js` 的既有做法注册为全局组件（`app.component('es-discover', ...)`），在 `index.html` 中按序引入 script，不使用侧栏路由。

> tab 由 3 个（集群概览 / 索引 / 文档查询）变为 5 个，`es.js` 现有 411 行必然继续膨胀 —— 这是 §7 前置依赖（拆 `es_client.go` + `es.js`）**必须先行落地**的直接原因。

### 15.2 交互要点

- **新建视图向导**：三步（名称 / 索引匹配 / 时间字段），与 §5.2 一一对应；第 2 步输入 pattern 时实时探测预览（命中数与文档总数），第 3 步列出候选时间字段并对不一致给出警告；创建成功后直接进入 Discover
- **视图列表**：每行展示名称 / pattern / 时间字段 / 字段数 / `synced_at` / 同步状态徽标（`idle` 灰、`syncing` 蓝 —— 仅手动刷新期间、`failed` 红）；行内操作：进入检索、刷新、编辑、**查看生命周期状态**（`GET /{pattern}/_ilm/explain`，§13.1）、删除（`ElMessageBox.confirm` 二次确认）
- **手动刷新**：按钮置 loading，命中 4008 时提示「同步进行中」；完成后就地更新字段数与 `synced_at`
- **字段侧栏**：树形展示字段路径；每项显示类型徽标与两个能力小标（可搜索 / 可聚合）；`type_conflict` 与 `partial` 用警告色 + tooltip 说明；可搜索字段可点击「插入查询」往 KQL 框插入 `field:` 并把光标置于取值处；**可聚合字段额外提供「去重计数」与「作为分析维度」两个快捷入口**（直接打开分析抽屉并预选该字段）；顶部显示「字段同步于 17:20」+ 刷新入口
- **KQL 输入条**：输入时防抖 300ms 调 `kql/validate`；出错时在输入框下方显示带位置的错误，并在文本中高亮出错片段（`pos` / `len`）；校验通过则静默。**不提供 DSL 查看入口** —— 编译产物不外露（§14.4）
- **时间选择器**：常用预设（近 15m / 1h / 24h / 7d）+ 自定义绝对区间。**展示与发送都是 `YYYY-MM-DD HH:mm:ss` 字符串**：预设按钮只做「按北京时间算好填入」，不改变传递格式；前端**不做任何时间换算**（后端按 UTC+8 解释，§9）。直方图上拖拽选子区间后回填选择器并重查。区间为下半开 —— 结束时刻本身不计入，界面在该时刻旁标注「不含」以免误读
- **结果区与分页**：首次查询一次拿回最多 1000 条，**翻页在本地切片完成**（不发请求）；分页条常驻显示「总 N 条 · 窗口 1000 · 当前 21–40」；`window_truncated` 为真时，结果区顶部常驻提示「匹配 N 条，仅可浏览前 1000 条，请收窄时间范围或补充条件」；切换 `columns` 会触发重查（§11.4），按钮给 loading 与「重新查询」文案
- **文档表**：表头随 `columns` 动态生成；高亮片段用 `<mark>` 包裹（`v-html` 渲染前**必须转义** `<`、`>`、`&`，`pre_tags` 只允许 `<mark>`）；点击行展开 `_source` 全量 JSON（等宽字体，复用现有 `fmtSource`）
- **分析抽屉**：结果区右上「分析」按钮打开，两个 tab（去重计数 / 图状分析）。顶部常驻说明「基于当前条件的**全量匹配集**，非当前页」；去重计数的唯一值数带「约」与精度 tooltip；图状分析左侧选维度 / 指标、右侧出图且可切表格（§12.3）
- **生命周期 tab**：左侧策略列表（内置策略置灰 + 「ES 管理」徽标），右侧 JSON 详情与编辑；保存前二次确认并回显 `PUT /_ilm/policy/{name}`；「索引状态」区输入 pattern（默认取最近用过的视图 pattern）查看 `_ilm/explain`，卡住的步骤提供「重试」按钮（W3）
- **索引模板 tab**：列表分「索引模板 / 组件模板」两段；详情页把 `composed_of` 展开成有序列表并标注各组件贡献的键；编辑抽屉底部固定「保存前模拟」按钮 —— 填入假想索引名调 `_simulate`，把合并结果与当前配置做差异比对后再保存
- **权限沿用**：所有请求经 `app.js` 的 axios 封装，401 跳登录

### 15.3 样式

复用现有设计令牌（`style.css` 的 `:root` 变量）与 `.es-*` 类族；Discover 三栏按项目留白约定（`rules.md` §6.3：卡片间距 ≥20px、表格行高 ≥52px）排版。新增样式集中在 `style.css` 的 ES 段落，不散落魔法色值。分析图表的颜色、网格线、tooltip 也一律取 `:root` 令牌（§12.3），随主题切换。

## 16. 错误码

新增 4001–4099 段用于 ES 工具，登记到 `workflow/docs/04-api.md` 错误码表：

| code | 含义 | HTTP |
| --- | --- | --- |
| 4001 | KQL 语法错误（message 含位置与片段，data 带 `pos`/`len`） | 400 |
| 4002 | KQL 语义错误（字段不存在 / 算子与字段类型不匹配） | 400 |
| 4003 | 视图不存在 / 索引不存在 | 404 |
| 4004 | 未探测到可用时间字段，或视图字段表为空需先刷新 | 400 |
| 4005 | 翻页越界：`from + size` 超出已缓存的结果窗口（1000 条） | 400 |
| 4006 | 索引匹配未命中任何索引或数据流 | 400 |
| 4007 | 视图名称在同一 ES 连接内重复 | 409 |
| 4008 | 视图正在同步中，无法重复刷新 | 409 |
| 4009 | 时间字段在匹配集合的部分成员中缺失或类型不一致 | 400 |
| 4010 | 时间参数非法：`start` / `end` 缺失、空串、格式不符 `YYYY-MM-DD HH:mm:ss`，或 `start >= end` | 400 |
| 4011 | 结果集缓存已失效（`result_id` 不匹配，多为已被新查询覆盖） | 409 |
| 4012 | 分析参数非法：字段不可聚合 / 指标与字段类型不匹配 / 桶数超限 | 400 |
| 4013 | 目标是 ES 内置（managed）策略或模板，只读 | 403 |
| 4014 | 生命周期策略仍被索引引用，ES 拒绝删除 | 409 |
| 4015 | 资源名称非法：空、含 `*` `,` `?`，或以 `_` / `.` 开头 | 400 |

ES 侧原始错误（映射冲突、查询被拒等）仍走既有 `esErr()` 归一化后以 500 返回，不做码段细分。

## 17. 实施步骤与风险

### 实施步骤

1. **前置**：落地 `tasks/07-api-refactor` 的 B5 / B8 拆分（`es_client.go` 451 行、`es.js` 411 行 → 均 ≤300）。本期新增 5 个 tab（含内存分页与分析），**这一步不可跳过**；或将等价拆分并入本任务首步
2. **存储层**：`migrateV26` + `model/es.go` 增 `ESView`/`ESViewField` + `store/repo/es_view.go`
3. **探测与合并**：`mapping.go` 实现 `_resolve/index` + `_field_caps` + `_mapping`，含 §5.5 跨索引合并（能力位保守取交、多类型冲突、部分存在、2000 截断）+ 单测
4. **时间解析**：`timeparse.go` 实现 `YYYY-MM-DD HH:mm:ss`（UTC+8）→ 毫秒的**唯一**入口 + 单测（缺秒、`T` 分隔、相对表达式、毫秒数字一律 400）
5. **视图 CRUD**：`view.go`（含 pattern 探测 handler、创建即同步一次）
6. **异步同步**：`view_sync.go`（启动延迟 10s 跑一次 + 每 4h 轮询 + **并行拉取（上限 4）** + 单视图互斥 + 30s 单视图超时 + **单事务批量入库、只更新成功视图** + 失败保留旧快照 + 启动重置残留 `syncing`），在 `main.go` 以 goroutine 启动；批量写入落在 `store/repo/es_view.go` 的 `SaveSyncResults`
7. **词法 + 语法**：`kql.go` 输出带位置的 Token 序列，覆盖转义、引号、优先级、括号
8. **DSL 生成**：`dsl.go` 实现 §9 分派表与 §10 搜索体组装（时间范围恒为 `gte start_ms` + `lt end_ms`；排序 / `_source` / highlight / 分析维度的字段**一律取自视图字段表**，时间字段名取 `time_field`；编译产物按 debug 级别打日志）
9. **结果集缓存**：`result_cache.go`（1000 条窗口 + `result_id` + 容量/TTL/LRU + 4011/4005）+ `/search/page`
10. **红线单测**：§4 六条缺陷逐条写成反向用例（尤其 1 / 2 / 6 这三类「不报错但结果错」）+ 时间解析 + 缓存覆盖与失效用例
11. **分析**：`analyze.go` 两种分析（去重计数 / 图状）的聚合组装与结果整形 + 4012 校验
12. **生命周期与模板**：`lifecycle.go`、`template.go`（含 W1–W8 写操作、名称白名单、内置资源只读、审计写入）+ `_simulate`
13. **接口**：注册 §14 全部端点；§14.9 的既有索引管理端点**原样保留**；`04-api.md` 登记 4001–4015
14. **前端**：`es.js` 瘦身 → `es_index.js` → `es_views.js` → `es_fields.js` → `es_kql.js` → `es_discover.js`（含内存分页）→ `es_analyze.js`（含 SVG 图表）→ `es_lifecycle.js` → `es_templates.js`
15. **联调**：接一个真实数据流（如 `logs-*`），跑通「建视图 → 看字段 → 写 KQL → 看直方图 → 拖拽改时间范围 → 翻页（零 ES 请求）→ 展开文档 → 分析出图 → 看该视图命中索引的 ILM 状态 → 改模板并模拟预览 → 手动刷新字段」全链路

### 主要风险

| 风险 | 应对 |
| --- | --- |
| 编译结果「不报错但结果错」（引号语义、should 变可选、nil 子句） | 列为红线单测；§4 表格逐条对应反向用例；编译产物按 **debug 级别**打服务端日志（含 KQL 原文与生成的 DSL），排障时从日志核对 —— 移除了前端 DSL 预览后，日志是唯一核对入口，故**必须带 request_id 便于串联** |
| 时间区间闭 / 开搞错，导致边界文档重复计入或漏查 | 区间固定 `gte` + `lt`，并由单测锁定「结束时刻的文档不计入」；非法区间直接 4010，不静默纠正（§9） |
| 跨索引字段类型冲突导致查询在部分成员上失败 | 冲突字段统一降级 `match` + `lenient:true`；能力位保守取交；侧栏显式标注，不静默 |
| 后台同步打爆 ES | 并行度上限（默认 4）+ 单视图 30s 超时；`_field_caps` 对宽 pattern 是重操作，并发不再调高；轮次之间不重叠 |
| 多视图各自写库撞上 SQLite 写锁 | 拉取阶段**只写内存**，全部结束后**单事务批量写入** —— 整轮只取一次写锁（WAL + `busy_timeout=5000`） |
| 批量入库前进程退出，本轮拉取白做 | 内存缓存未落库即丢弃，库内仍是上一份成功快照，视图照常可检索；下一轮自愈。属可接受降级 |
| pattern 过宽（`*`）拉回上千字段 | 探测接口提前预警（命中 >50 时黄色警告）；字段表 2000 截断并带 `truncated` 标记 |
| 同步失败覆盖掉可用字段，界面变空白 | 失败**不覆盖** `fields_json`，保留上次成功快照并回显失败原因与时间 |
| 视图与实际索引状态漂移（索引滚动/删除） | 4h 周期同步 + 手动刷新；检索时 ES 原生按 pattern 解析，天然跟随最新索引（不依赖缓存展开） |
| 进程崩溃残留 `syncing` 状态导致视图永久卡住 | 启动时把全部 `syncing` 重置为 `idle` 再开同步（自愈） |
| 高亮片段 XSS | 渲染前转义 `<`、`>`、`&`；`pre_tags` 白名单只留 `<mark>`；禁止把 ES 返回的片段直接 `v-html` |
| 单文件行数再次超限 | 已按 §7 表格预拆 **14 个后端文件 + 9 个前端文件**；提交前逐文件核对 ≤300 行 |
| 结果窗口只有 1000 条，查不到更早的数据 | 结果区顶部常驻「匹配 N 条，仅可浏览前 1000 条」提示并引导收窄条件；如需浏览全量另立任务做 PIT + `search_after`（§11.3） |
| ES 版本差异（目标 9.5.3） | `_resolve/index` / `_field_caps` / `date_histogram.fixed_interval` / `track_total_hits` 均为 7.x+ 稳定 API，无版本分支；ILM / 索引模板 / DLM 亦为 8.x+ 稳定 API |
| 结果集缓存把内存吃掉 | 单视图上限 1000 条 + 全局「结果集数 / 总条数 / 总字节」三重上限 + 空闲 TTL + LRU 淘汰；`_source` 受 `columns` 限制（§11.2） |
| 翻页读到已被新查询覆盖的缓存 | 每次查询返回 `result_id`，翻页必须回传；不匹配 → 4011 并提示重新查询（§11.2） |
| 分析被误读为「当前页的统计」 | 分析走全量匹配集（`size:0` + `aggs`），UI 强制标注「非当前页」；与列表共用同一份 query 段（§12.1） |
| 去重计数被当作精确值 | `cardinality` 是近似（HyperLogLog++），UI 数值前带「约」并 tooltip 说明 `precision_threshold` 上限 40000（§12.2） |
| `terms` 桶计数有误差 | 展示 `doc_count_error_upper_bound`（> 0 时 tooltip 提示），并补「其他」段（§12.2） |
| 生命周期 / 模板写操作误改生产集群 | 名称白名单 + 内置资源只读（4013）+ 确认框回显完整 ES 请求 + 全部落审计（§3.3）；删除被引用策略时不做自动解绑 |
| 时间字符串被解析出偏差 | 只接受 `YYYY-MM-DD HH:mm:ss` 单一格式（缺秒即 400）、固定按 UTC+8 解释、响应回显 `start_ms` 供核对（§9） |
| 分析图表引入 1 MB 图表库 | 一期纯 SVG 自绘、零新依赖；颜色取 `:root` 令牌（§12.3） |
| `lifecycle.go` / `template.go` 逼近 300 行 | 读与写分文件（`*_read.go` / `*_write.go`）或把 handler 拆到 `handler_*.go`，提交前逐文件核对 |

## 18. 参考来源

1. Elasticsearch 官方文档 · Resolve index API：https://www.elastic.co/guide/en/elasticsearch/reference/current/indices-resolve-index-api.html
2. Elasticsearch 官方文档 · Field capabilities API：https://www.elastic.co/guide/en/elasticsearch/reference/current/search-field-caps.html
3. Elasticsearch 官方文档 · Mapping API：https://www.elastic.co/guide/en/elasticsearch/reference/current/indices-get-mapping.html
4. Elasticsearch 官方文档 · Date histogram aggregation：https://www.elastic.co/guide/en/elasticsearch/reference/current/search-aggregations-bucket-datehistogram-aggregation.html
5. Elasticsearch 官方文档 · Bool query（`minimum_should_match` 语义）：https://www.elastic.co/guide/en/elasticsearch/reference/current/query-dsl-bool-query.html
6. Elasticsearch 官方文档 · Multi-match query（`lenient`）：https://www.elastic.co/guide/en/elasticsearch/reference/current/query-dsl-multi-match-query.html
7. Kibana 官方文档 · Data views（名称 / index pattern / 时间字段）：https://www.elastic.co/guide/en/kibana/current/data-views.html
8. Kibana 官方文档 · KQL 语法：https://www.elastic.co/guide/en/kibana/current/kuery-query.html
9. Elasticsearch 官方文档 · ILM API（`_ilm/policy`、`_ilm/explain`、`_ilm/retry`）：https://www.elastic.co/guide/en/elasticsearch/reference/current/ilm-apis.html
10. Elasticsearch 官方文档 · Data stream lifecycle（DLM）：https://www.elastic.co/guide/en/elasticsearch/reference/current/data-stream-lifecycle.html
11. Elasticsearch 官方文档 · Index templates（含 `_simulate`）：https://www.elastic.co/guide/en/elasticsearch/reference/current/index-templates.html
12. Elasticsearch 官方文档 · Component templates：https://www.elastic.co/guide/en/elasticsearch/reference/current/indices-component-template.html
13. Elasticsearch 官方文档 · Cardinality aggregation（精度与 `precision_threshold`）：https://www.elastic.co/guide/en/elasticsearch/reference/current/search-aggregations-metrics-cardinality-aggregation.html
14. Elasticsearch 官方文档 · Terms aggregation（`doc_count_error_upper_bound`）：https://www.elastic.co/guide/en/elasticsearch/reference/current/search-aggregations-bucket-terms-aggregation.html
