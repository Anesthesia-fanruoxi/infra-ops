# 任务 08 · Elasticsearch 控制台：数据视图 · KQL 检索 — frontend.md（前端执行）

> 设计依据：`docs/ES控制台设计.md` §15（前端设计）、§11（内存分页）、§12（分析）。
> 范围：`template/static/pages/es*.js`（拆分 + 新增）、`style.css`、`index.html`。
> 强制约束：时间展示/输入/发送三处统一 `YYYY-MM-DD HH:mm:ss` 字符串，**前端不做任何时间换算**（后端按 UTC+8 解释）；
> 不提供任何 DSL 查看入口；高亮渲染前必须转义。
> 组件按 `stacks-forms.js` 既有做法注册为全局组件（`app.component(...)`），`index.html` 按序引入 script，不新增路由。

## F1 es.js 瘦身与索引 tab 拆分（前置拆分）

- [x] `es.js`（现 418 行 >300）瘦身为：连接管理 + 集群概览/节点 tab；新建 `es_index.js` 承接索引 tab
  （集群索引列表 + 单索引原始 mapping 抽屉 + 既有新建/删除索引入口**原样保留**，§3.2）
- [x] 旧「文档查询」tab（JSON textarea）暂留占位，F5 Discover 上线后整体移除（目标：纯 KQL，无 textarea 入口）
- [x] `index.html` 按序引入新 script

验收：`node --check` 通过；es.js ≤300 行；既有索引管理交互与现状零差异。

## F2 数据视图 tab（es_views.js）

- [x] 视图列表：名称 / pattern / 时间字段 / 字段数 / synced_at / 同步状态徽标（idle 灰、syncing 蓝、failed 红 +
  「最近一次失败：Y」）；行内操作：进入检索 / 刷新（loading，4008 提示「同步进行中」）/ 编辑 / 查看生命周期状态
  （explain，pattern 默认取该视图）/ 删除（`ElMessageBox.confirm`）
- [x] 三步向导（§5.2）：① 名称（即时同连接重名校验）；② 索引匹配（防抖 500ms 调 probe，
  命中数与 docs_count 预览、名称 >20 截断、空命中红色提示禁下一步、pattern=`*` 或命中 >50 黄色警告二次确认）；
  ③ 时间字段（date 候选单选、`@timestamp` 置顶、4009 列出缺失成员并给「换字段 / 收窄 pattern」两出路）
- [x] 创建成功直接进入 Discover；编辑提交（改 pattern/时间字段）后等待自动重同步刷新列表

验收：`node --check` 通过；向导三步交互与探测预览正确；同步状态徽标三态渲染。

## F3 字段侧栏（es_fields.js）

- [x] 树形展示字段路径；每项类型徽标 + 可搜索/可聚合两个能力小标；`type_conflict` / `partial` 警告色 + tooltip 说明
- [x] 可搜索字段点击「插入查询」往 KQL 框插入 `field:` 并光标置于取值处；
  可聚合字段额外提供「去重计数」「作为分析维度」快捷入口（打开分析抽屉并预选该字段）
- [x] 顶部「字段同步于 HH:mm」+ 刷新入口（调单视图 refresh）；`truncated` 时提示收窄 pattern

验收：`node --check` 通过；冲突/部分/截断标记与快捷入口正确。

## F4 KQL 输入条与时间选择器（es_kql.js）

- [x] KQL 输入条：输入防抖 300ms 调 `kql/validate`；出错在输入框下方显示带位置的错误并高亮出错片段（pos/len）；
  通过则静默；提供语法帮助；**不提供 DSL 查看入口**
- [x] 时间选择器：预设（近 15m/1h/24h/7d）仅按北京时间算好字符串填入选择器；
  自定义绝对区间 `YYYY-MM-DD HH:mm:ss`（起始 + 结束）；结束时刻旁标注「不含」（下半开）；
  前端零时间换算——发送与展示均为同一字符串；快捷项不改传递格式

验收：`node --check` 通过；错误定位高亮正确；时间串直传、无任何 Date 换算逻辑。

## F5 Discover 主视图（es_discover.js）

- [x] 三栏布局：左字段侧栏（F3）+ 中部时间直方图（纯 SVG，支持拖拽选子区间→回填时间选择器并重查）+
  底部文档表；顶部视图切换下拉 + KQL 条（F4）+ 分析按钮（F6 入口）
- [x] 文档表：表头随 `columns` 动态生成；高亮片段转义 `<` `>` `&` 后以 `<mark>` v-html 渲染
  （pre_tags 白名单只允许 `<mark>`）；点击行展开 `_source` 全量 JSON（等宽字体，复用 `fmtSource`）
- [x] 内存分页：首次查询已拿全 1000 条，翻页本地切片**不发请求**；分页条常驻「总 N 条 · 窗口 1000 · 当前 21–40」；
  `window_truncated` 为真时顶部常驻「匹配 N 条，仅可浏览前 1000 条，请收窄时间范围或补充条件」
- [x] 列选择切换触发重查并覆盖缓存：按钮 loading +「正在重新查询」语义；4011 时提示「结果集已更新，请重新查询」
- [x] 移除旧「文档查询」tab（F1 占位收尾），检索入口唯一指向 Discover

验收：`node --check` 通过；翻页网络面板零 ES 请求；XSS 转义核对；直方图拖拽回填正确。

## F6 分析抽屉（es_analyze.js）

- [x] 结果区右上「分析」按钮打开抽屉，两个 tab（去重计数 / 图状分析）；顶部常驻
  「基于当前条件的**全量匹配集**（N 条），非当前页」
- [x] 去重计数：字段多选（默认取当前 columns 可聚合字段）；唯一值数带「约」+ tooltip 说明
  `precision_threshold`（40000）；Top 值条状分布 + 占比
- [x] 图状分析：左侧维度（terms/date_histogram/histogram + 桶数）与指标（count/sum/avg/min/max/cardinality）选择，
  显式「运行」按钮 + 300ms 防抖；右侧出图且**图/表可切换**（表格必须）
- [x] 纯 SVG 自绘柱/折线/饼（零图表库依赖）：颜色取 `style.css` `:root` 令牌；固定 4 条网格线；
  X 轴标签 >12 抽稀；hover tooltip；饼图桶 >8 合并「其他」；`doc_count_error_upper_bound > 0` 上桶 tooltip

验收：`node --check` 通过；控制台零报错；三种图形与表格切换渲染正确。

## F7 生命周期 tab（es_lifecycle.js）

- [x] 策略列表（名称/版本/修改时间/阶段摘要/内置标记）；内置策略置灰 +「ES 管理」徽标
- [x] JSON 详情编辑（`JSON.parse` + 结构粗校验）；保存前二次确认并**完整回显**将发出的 ES 请求
  （如 `PUT /_ilm/policy/logs-30d`）与覆盖摘要
- [x] 「索引状态」区：输入 pattern（默认最近用过的视图 pattern）查看 explain（关联策略/当前阶段/age/卡住原因）；
  卡住步骤「重试」按钮（W3，确认后调用）
- [x] 数据流列表与保留期设置（W4，二次确认回显）

验收：`node --check` 通过；内置只读、二次确认回显正确。

## F8 索引模板 tab（es_templates.js）

- [x] 列表分「索引模板 / 组件模板」两段（名称/index_patterns/priority/composed_of/内置标记）
- [x] 详情：`composed_of` 展开为有序列表并标注各组件贡献的键；内置模板置灰只读
- [x] 编辑抽屉（JSON）底部固定「保存前模拟」：填入假想索引名调 `_simulate`，
  合并结果与当前配置差异比对展示后再保存；详情页另设「试算」入口

验收：`node --check` 通过；模拟预览与差异比对渲染正确。

## F9 样式与全链路联调

- [x] `style.css` 新增 ES 段落：复用 `:root` 设计令牌与 `.es-*` 类族；Discover 三栏按留白约定
  （rules §6.3：卡片间距 ≥20px、表格行高 ≥52px）；图表颜色/网格/tooltip 一律走令牌
- [x] `index.html` 最终 script 顺序核对；全局组件注册核对
- [x] 联调（接真实数据流，如 `logs-*`）全链路：建视图 → 看字段 → 写 KQL → 看直方图 → 拖拽改时间范围 →
  翻页（零 ES 请求）→ 展开文档 → 分析出图 → 看该视图命中索引的 ILM 状态 → 改模板并模拟预览 → 手动刷新字段

验收：控制台零报错；全链路真机走通。

## 进度记录

### 2026-09-11 晚（F1–F9 本会话完成）

- 组件注册方式：不动 app.js（黑名单），es_*.js 只挂 window 全局对象，es.js 用局部 components: 选项引用；index.html 在 es.js 前按依赖序引入 8 个新 script。
- es.js 瘦身 305 行（连接管理 + 概览/节点 + tab 壳）；旧「文档查询」JSON textarea 已整体移除（F5 收尾），检索入口唯一指向 Discover。
- 时间红线执行：展示/输入/发送均为 YYYY-MM-DD HH:mm:ss 字符串；快捷预设与直方图拖拽只做「北京时间算好填入」，不改传递格式；结束选择器旁标注「不含」。
- XSS 红线执行：renderHl 先转义 & < > 再拼 <mark>（split 白名单标记）。
- 补后端 GET /es/:id/indices/:index/mapping（IndexMapping）供索引 tab 的 mapping 抽屉。
- 未做真机联调（需用户在自己终端用 script/restart-server.ps1 换上新二进制后走 F9 全链路清单）。

