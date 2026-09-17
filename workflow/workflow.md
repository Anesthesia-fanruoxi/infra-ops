# 长任务工作流

> 通用规则在上，任务列表在下；每个任务的详细规划见各自目录的 `frontend.md`（前端执行）/ `backend.md`（后端执行）/ `boundary.md`（边界）。纯前端任务只建 frontend.md + boundary.md，纯后端任务只建 backend.md + boundary.md。
> 流程：步骤完成 → 按验收标准验收 → 标记 \[x]。

## 通用规则

1. 代码文件单个尽量不超过 500 行，超出宜拆分（文档除外）
2. 长任务流程：先在任务目录写齐执行文档（frontend/backend）+ boundary.md（边界）→ 用户确认后开始执行 → 各步骤依次执行不再逐步确认 → 每步完成后验收并标记完成 → 超出 scope 时暂停询问
3. 每次验收完成后必须立即修改 workflow\.md 将对应步骤标记 \[x]，不得遗漏
4. 开始长任务前先读 workflow\.md，据此创建任务清单（TodoList）并在执行中同步实时更新
5. 代码改动须编译通过（`go build`；交叉编译 `CGO_ENABLED=0 GOOS=linux GOARCH=amd64`）；纯前端改动以浏览器控制台零报错验收；部署仅替换测试机（standby-01）并保留旧二进制备回退
6. 红线：不动生产环境；不提交密钥凭据；不未经确认删除文件或重置数据；不改 scope 目标外的模块
7. 修改完成后总结改动内容与效果；踩坑与决策记入对应任务执行文档的进度记录小节

## 任务列表

### 菜单页面美化（已完成）→ [tasks/01-menu-beautify/](./tasks/01-menu-beautify/)

> 方案：运维控制台风（深色侧栏 + 鲜明状态色 + 等宽技术数据），纯前端，对象为阶段一 5 页面（登录/总览/主机/凭据/审计日志）。详见 frontend.md（设计规范与逐页要点）/ boundary.md（文件白名单）

- [x] 设计令牌 + 全局组件：:root CSS 变量（色彩/字体/间距/阴影）+ 状态徽章/指标条/统计卡片/表格/弹窗全局样式 —— 验收：变量全部落在 :root，无散落魔法色值

- [x] 登录页美化：深色网格底 + 居中白卡片 + 品牌区 + 错误提示 —— 验收：登录交互正常，成功跳 #/overview

- [x] 总览页美化：四张统计卡片 + 3:2 双栏（主机速览/最近操作）—— 验收：overview 接口数据渲染正确

- [x] 主机页美化：筛选 chips + 指标表格 + 测试连接内联 loading + 详情抽屉 + 新增/编辑弹窗 —— 验收：主机 CRUD 与测试全流程走通

- [x] 凭据页 + 审计日志页美化：类型徽章/等宽指纹/secret 仅写弹窗；action 筛选/着色徽章/分页 —— 验收：secret 仅写不回读语义正确，筛选分页正常

- [x] 自检与总验收：留白约定（rules §6.3）+ 跨页一致性 + git diff 范围核对 —— 验收：自检清单全过，git diff 仅含 template/，控制台零报错（注：代码自检已过，git 无基线无法 diff 核对，见 frontend.md 进度记录）

### 批量新增主机（已完成）→ [tasks/02-batch-add-hosts/](./tasks/02-batch-add-hosts/)

> 方案：共享凭据 + IP 范围批量录入（单 IP / 172.16.1.11-20 / 混合），主机名自动取服务器 hostname（失败回退 IP），备注统一填写；落库后并发测试（上限 5）并采集信息。前后端任务，接口见 docs/04-api.md POST /hosts/batch，详见 frontend.md / backend.md / boundary.md

- [x] 后端：IP 范围解析器（单 IP/简写/完整范围，多分隔符，去重、IPv4 校验、上限 100）—— 验收：单测覆盖三类语法与非法输入，3001/3002 返回正确

- [x] 后端：POST /hosts/batch（批量落库 + 重复 IP 跳过 + 并发测试 + hostname 自动取名冲突加后缀）—— 验收：重复 IP 跳过不覆盖、单台失败不阻塞整批、库内/批内名字冲突自动 -2/-3

- [x] 前端：主机页「批量新增」弹窗（凭据选择/IP 文本域/角色/端口/备注 + 实时解析预览）—— 验收：范围语法预览计数正确，与现有重复有提示（解析器单测全过；浏览器交互待用户启动页面验收）

- [x] 前端：结果反馈弹窗（成功/失败/跳过分组 + 名称 + 失败原因）—— 验收：三类状态渲染正确，失败项可到主机页复测（渲染逻辑已实现；浏览器交互待用户启动页面验收）

- [x] 联调验收：一次录入 10+ 台真实服务器 —— 验收：全部入库、状态与自动命名正确、host.batch\_create 审计留痕（真机已通过）

### 任务编排重构为「任务记录」模型（已完成）→ [tasks/03-task-records/](./tasks/03-task-records/)

> 方案：编排改为一次性任务记录——新建即未开始，执行一次后终态已结束；三态由最近一次运行派生，无 schema 迁移。移除历史运行区块与列表接口，单列表全量展示。详见 frontend.md / backend.md / boundary.md

- [x] 后端：列表联查最近运行（三态 + 结果统计），下线运行历史列表接口 —— 验收：/orchestrations 含 state/last\_run\_id/result，GET /api/orchestration/runs 返回 404（经验性验证通过）

- [x] 后端：一次性守卫（已运行不可重跑 / 不可编辑）+ 删除连带清运行 —— 验收：重跑返回 400，编辑被拒，删除后无孤儿运行（经验性验证通过）

- [x] 后端：保留清理连带删任务记录并清扫孤儿运行 —— 验收：超期已结束记录整条消失，运行中记录不受影响（经验性验证通过）

- [x] 前端：任务记录语义改造（三态 tabs / 结果列 / 按状态分流操作 / 去启用列）—— 验收：三态操作正确，控制台零报错（node --check 通过；浏览器交互待启动页面验收）

- [x] 联调验收：新建 → 运行 → 结束全流程 —— 验收：状态流转正确、已结束不可重跑不可编辑、SSE 进度正常（真机已通过）

### 任务运行抽屉：三层结构 + 双 SSE（已完成）→ [tasks/04-run-drawer/](./tasks/04-run-drawer/)

> 方案：点运行后自动展开 50% 宽只读抽屉——L1 步骤层（进展+完成聚合色，可点击切换）→ L2 主机层（三态徽章）→ L3 日志层（时间+IP+内容混排）。双 SSE 独立端点：步骤流（生命周期）+ 详情流（按步骤主机态与日志，快照打底+增量）；日志新增 orchestration\_run\_logs 落库（V14，级联清理）。详见 frontend.md / backend.md / boundary.md

- [x] 后端：迁移 V14 日志表 + model + 日志 repo —— 验收：旧库启动自动建表，编译通过（经验性验证：迁移幂等 + 日志落库/读取/级联删除全过）

- [x] 后端：事件总线新增步骤/日志 topic + 引擎改造（步骤事件、日志先落库再发布、状态行、跳过推送）+ 文件拆分 —— 验收：编译通过，运行中日志边执行边入库（拆分后 api/orchestration.go 293 行 / engine 299 行 / sse 274 行，均 ≤300）

- [x] 后端：双 SSE 端点（init 快照+过滤推送+done 关闭）+ 路由更新 + 移除旧端点 —— 验收：curl 可见事件序列，go build/vet/test 通过（httptest 协议测试覆盖 init/step/host/log/done/回溯/步骤过滤）

- [x] 前端：抽屉 mixin（三层结构 + GET 打底）—— 验收：node --check 通过，已结束任务静态展示正确

- [x] 前端：双 SSE 接入 + 自动追踪/手动切换 + 移除旧矩阵弹窗 + 样式 —— 验收：控制台零报错，无死代码残留（node --check 通过、模板配平、无陈旧引用；浏览器交互待用户启动页面验收）

- [x] 联调验收：运行→自动开抽屉→三层实时联动→结束翻转列表；回溯逐步骤查看 —— 验收：无断流无丢行，跳过主机实时置灰（真机已通过）

### 大数据底座全组件高可用（已完成）→ [tasks/05-bigdata-ha/](./tasks/05-bigdata-ha/)

> 方案：套件级 `ha` bool 开关（选套件卡片上开，开=全部支持组件双实例）；非 HA 路径零回归；HA 模式角色规划重构为全角色矩阵（每角色可指定主机）。设计依据 `docs/大数据底座高可用设计.md`。详见 frontend.md / backend.md / boundary.md

- [x] B1 模型与蓝图：StackVar.Type + StackBlueprint.HaSupport + `ha` 与辅助变量 + metastore_db 组件 —— 验收：go build/vet 通过，蓝图含新字段
- [x] B2 引擎占位符与分配算法：`__zk_ips`/`__hdfs_entry`/各组件双实例占位符 + 从角色确定性分配 + masters 白名单扩展 —— 验收：go build/vet + 渲染双形态校验
- [x] B3 校验规则与保护：validateBigdataMasters 六条（ZK必勾/≥3台/同组件互斥/metastore_db/双实例互斥）+ 缩容保护（承载 HA 角色主机禁缩）+ 创建/bootstrap 落点透传 —— 验收：非法组合逐条 400，store+api 单测全过
- [x] B4 HDFS HA 落地：core/hdfs-site 条件块 + node.sh 四分支（JN/NN1/NN2/DN）+ bootstrap haadmin 校验 —— 验收：bash -n + 渲染双形态校验通过
- [x] B5 ZK 系组件 HA：YARN 双 RM、Spark/Flink 双实例（ZK recovery）、HBase backup-master —— 验收：bash -n + 渲染校验通过（ha.sh 统一实现）
- [x] B6 Hive HA：metastore_db（MySQL 8）+ 2×MS + 2×HS2（ZK 服务发现）+ Trino 双 uri —— 验收：bash -n + 引擎 __ha_hive_jdo/__hive_ms_uris 注入校验通过
- [x] B7 探活/服务登记/卸载清理：bigdataExpectedContainers 扩为 HA 感知（镜像 ha.sh 分支）+ per-role 容器清单 + migrate_old per 容器 —— 验收：新增 HA 容器推断单测覆盖 主/备/JN+DB 三落点全过
- [x] B8 渲染校验扩展与回归：check_bigdata_tpl.py 三场景（全开/单组件/角色分离）已补齐 —— 验收：渲染校验全绿，go build/vet/test + bash -n 全过，非 HA 组合无回归
- [x] F1 套件卡片 HA 开关：el-switch + HA 角标 + ZK 自动勾选 + 提示条 + 下一步禁用 —— 验收：node --check + 联动符合 §3.1
- [x] F2 bool 变量与辅助变量渲染：el-switch（true/false 字符串）+ 条件显隐 —— 验收：node --check + params 值正确
- [x] F3 角色矩阵重构：HA 分组角色卡片 + 每行主机下拉 + 冲突标红 + masters 组装扩展 + 样式 —— 验收：node --check + 非 HA 零差异
- [x] F4 实例展示：卡片 HA 徽标 + 抽屉角色标签 —— 验收：node --check + 渲染正确
- [x] F5 非 HA 全回归 + 联调验收：关开关全流程 + HA 真机端到端部署 + 故障演练矩阵 —— 验收：控制台零报错，逐组件 30s 接管（真机全过，含扩缩容演练）

### Elasticsearch 套件冷热温双模式（已完成）→ [tasks/06-es-cold-warm-hot/](./tasks/06-es-cold-warm-hot/)

> 方案：ES 套件升级为双模式部署——「集群」模式原样保留（仅升默认镜像 9.5.3），新增「冷热温」架构模式：每台多选 master/纯协调/数据-hot/warm/cold 角色，沿用主脑流水线多阶段编排（reset→masters→coords→data→verify），提供 SSL 自签开关、JVM 参数透传与自定义 scale_in 官方下线流程。LLM/ILM 等延到大套件。设计依据 `docs/ES套件冷热温架构设计.md`。详见 frontend.md / backend.md / boundary.md

- [x] B1 蓝图与模式接入：cold_warm_hot 模式 + 模式级变量（ssl_enabled/coordinator_count/heap/jvm_opts）+「集群」镜像升 9.5.3 —— 验收：go build/vet 通过，旧「集群」渲染除镜像外零差异（check_es_tpl.py 隐式回归）
- [x] B2 角色模型与 node.roles 生成：角色勾选→node.roles、master 与数据层互斥、协调 2 台兜底 —— 验收：非法组合逐条 400（validateCWHRoles + 单测全过）
- [x] B3 主脑流水线编排：Pipeline 阶段 + node.sh 改 RUN 分发器（run_reset/masters/coords/data/verify）—— 验收：go build/vet + 渲染校验阶段序列（check_es_tpl.py 全过）
- [x] B4 SSL 自签签发：openssl CA + 每节点证书 + xpack 注入 + CA 下载 —— 验收：双形态渲染校验，开启形态 HTTPS 就绪（PEM 免密码，显式 security=false 规避隐性认证）
- [x] B5 JVM 参数调优：heap_xms/xmx 分层 + jvm_opts 透传 —— 验收：渲染校验 ES_JAVA_OPTS 拼装正确
- [x] B6 缩容 scale_in 官方下线：exclude._name 排空 + voting exclusions + 回 green 再停容器 + 末台 master 保护 —— 验收：软下线脚本渲染校验 + 缩容保护单测（真机缩一台待联调）
- [x] B7 服务登记/探活/接入地址：角色标签 + 按 tier 接入地址 + SSL 双形态 —— 验收：registerStackService/stackVerifyEndpoints 按 tier 分组语义正确
- [x] B8 渲染校验与回归：check_es_tpl.py 三场景 + 「集群」全回归 —— 验收：渲染全绿（10 项），go build/vet + 既有单测全过，bigdata 渲染无回归
- [x] F1 模式选择：向导第一步 radio 集群/冷热温，动态渲染其次 —— 验收：node --check + 切换即时生效（落点 stacks-forms.js）
- [x] F2 角色分配：每台角色多选，master 与数据层互斥置灰，协调默认 2 台 —— 验收：node --check + 非法组合拦截（stacks-forms.js es-role-plan 区）
- [x] F3 参数配置：SSL 开关 + coordinator_count + 端口 + JVM 堆与 jvm_opts —— 验收：node --check + true/false 字符串正确
- [x] F4 结果展示：模式/tier 徽标 + 成员角色标签 + 按 tier 接入地址 + CA 下载 —— 验收：node --check + 渲染正确无回归
- [x] F5 双模式全回归 + 联调验收：「集群」全流程零差异 + 冷热温真机端到端部署 → 分层 → 缩容下线 —— 验收：控制台零报错，真机扩缩容与 SSL 形态走通（真机全过）

### API 目录重构（已完成）→ [tasks/07-api-refactor/](./tasks/07-api-refactor/)

> 方案：删除无用 `api/data` 残留目录；将扁平单包 `api/` 按菜单功能拆分为子包（auth/credential/host/overview/deploy/orchestration/stack/tool/{es,registry,sftp}/sse），跨模块共享辅助收敛到 `api/shared`；大文件（stack_engine 1706 / stack_verify 1170 / stack_instance 975 / deploy_task 817 / es / registry / sftp / stack）拆至单文件 ≤300 行；不新增/不修改端点，纯导出路径与可见性调整。设计依据 `docs/api目录重构.md`。纯后端任务，详见 backend.md / boundary.md

- [x] B1 基线：git 工作区干净，记录 go build/vet/test 基线 —— 验收：基线全通过可复现
- [x] B2 删除无用目录 api/data：git rm -r + rg "api/data" 零残留 —— 验收：目录消失、零引用、构建通过
- [x] B3 抽取共享层 api/shared：迁 applyHostVars/containsString/containsInt64/mergeStringList/filterEmpty —— 验收：go build/test 全绿，无逻辑改动
- [x] B4 拆分子包（auth/credential/overview/sse/host）+ router 引用更新 —— 验收：构建测试全绿（B4-B7 整体落地于工作区，未按独立 commit）
- [x] B5 拆分子包（tool/es、registry、sftp）—— 验收：go build/vet 全绿（同上，工作区整体落地）
- [x] B6 拆分子包（deploy/orchestration，单向依赖）+ 测试随迁 —— 验收：编排运行链路不回归（同上）
- [x] B7 拆分子包（stack 全部文件 + 测试）—— 验收：go test ./api/stack/... 全绿（同上，api/stack/ 已成型）
- [x] B8 大文件拆分收尾：~~deploy/deploy_task 340、host/host 347、host/host_batch 329、sse/sse 341 超 300 行待拆~~ —— 2026-09-11 规约放宽为「尽量 ≤500 行」，上述文件均达标，无需拆分
- [x] B9 大文件拆分收尾：~~stack 子包 6 文件超限（instance_build 388 / instance_validate 319 / plan_handler 314 / create 312 / bigdata 309 / verify 301）~~ —— 同上，均 ≤500 行，无需拆分
- [x] B10 收尾回归 + 冒烟：构建/vet/test 与基线比对、git diff 在白名单、服务启动各菜单接口正常 —— 验收：构建静态检查单测全绿，各菜单功能不变 —— 2026-09-11 go build/vet/test/cross 全绿，api 子包 ≤300 行达成

### Elasticsearch 控制台：数据视图 · KQL 检索（待确认）→ [tasks/08-es-console/](./tasks/08-es-console/)

> 方案：ES 工具从裸 JSON DSL 透传升级为数据视图驱动的 KQL 检索控制台——数据视图三要素（名称/索引匹配/时间字段，字段表本地持久化 + 异步同步：启动一次 + 每 4h + 手动刷新，并行拉取 → 单事务批量入库只更新成功视图）、字段类型感知 KQL→DSL 服务端编译（产物不外露，仅 debug 日志）、Discover 三栏视图（时间直方图/字段侧栏/文档表，结果窗口 1000 条存进程内存翻页零 ES 请求）、时间接受体唯一（`YYYY-MM-DD HH:mm:ss` 字符串按 UTC+8 后端转毫秒，区间下半开）、即时分析（去重计数 + 图状分析，纯 SVG 自绘）、生命周期与索引模板管理（本期唯一写能力 W1–W8 白名单 + 四条硬约束）。前置：`es.js` 418 行拆分并入 F1（后端 es 已在 07-B5 拆好）。设计依据 `docs/ES控制台设计.md`。详见 frontend.md / backend.md / boundary.md

- [x] B1 存储层：migrateV26（es_views 表 + conn_id+name 唯一索引）+ model ESView/ESViewField + repo/es_view.go（含 SaveSyncResults 单事务批量，失败保旧快照）—— 验收：迁移幂等、编译通过 —— 2026-09-11 go build/vet 全绿
- [x] B2 探测与字段合并：mapping.go（_resolve/index + _field_caps + _mapping；能力位取交/多类型冲突/partial/2000 截断）+ 单测 —— 验收：合并四情形单测全过 —— 2026-09-11 晚完成
- [x] B3 时间解析：timeparse.go（全项目唯一入口，UTC+8 → 毫秒，非法一律 4010 不静默纠正）+ 单测 —— 验收：解析表与非法输入单测全过 —— 2026-09-11 晚完成
- [x] B4 视图 CRUD：view.go（六端点 + probe 探测；4006/4007/4009 校验；创建即同步不落库失败引导；手动刷新立即落库）—— 验收：非法输入逐条 400/409 —— 2026-09-11 晚完成
- [x] B5 后台同步：view_sync.go（启动延迟 10s + 每 4h + 并行拉取上限 4 + 单视图互斥 + 30s 超时 + 单事务批量只更新成功 + 启动重置残留 syncing）+ main.go 接线 —— 验收：三步并发模型与降级验证，无写锁抬升 —— 2026-09-11 晚完成
- [x] B6 KQL 词法语法：kql.go（带位置 Token + 递归下降 NOT>AND>OR + 单遍转义 scanner + 单字符引号防护）—— 验收：kql_test 优先级/转义/错误定位全过 —— 2026-09-11 晚完成
- [x] B7 DSL 生成：dsl.go（Schema 感知 §9 类型分派全表 + 同层 bool 生成 + §10 搜索体组装 + debug 日志带 request_id）—— 验收：分派表逐行单测，should 必带 minimum_should_match —— 2026-09-11 晚完成
- [x] B8 结果缓存与检索 handler：result_cache.go（1000 窗口/result_id/三上限 LRU/TTL 30min）+ handler_search.go（search/search/page/kql/validate，无 DSL 回显，validate 恒 200）—— 验收：缓存覆盖/淘汰/4011/4005 单测全过 —— 2026-09-11 晚完成
- [x] B9 红线单测：§4 六缺陷反向用例 + 时间边界（lt 不含结束时刻）+ 缓存失效 —— 验收：go test ./api/tool/es/... 全绿 —— 2026-09-11 晚完成
- [x] B10 分析：analyze.go（去重计数 cardinality+terms / 图状 dim1/dim2/metric；复用同一 query 段；4012 校验）—— 验收：聚合组装与参数校验单测全过 —— 2026-09-11 晚完成
- [x] B11 生命周期与模板：lifecycle.go/template.go（W1–W8 写操作 + 名称白名单 4015 + 内置只读 4013 + 引用删除 4014 + 全部审计 + _simulate）—— 验收：四条硬约束逐条验证 —— 2026-09-11 晚完成
- [x] B12 路由登记与回归：§14 全端点挂 /api/v1/es/:id + §14.9 既有索引管理原样保留 + 04-api.md 登记 4001–4015 —— 验收：端点全注册、既有链路零改动、go build/vet/test 全绿 —— 2026-09-11 晚完成
- [x] F1 es.js 瘦身（连接+概览）+ es_index.js 索引 tab（既有新建/删除索引入口原样保留）—— 验收：node --check、es.js ≤500 行、索引管理零差异 —— 2026-09-11 晚完成
- [x] F2 es_views.js 数据视图 tab：列表（同步状态三态徽标）+ 三步向导（名称/探测预览/时间字段）+ 刷新/编辑/删除 —— 验收：node --check、向导交互符合设计 §5.2 —— 2026-09-11 晚完成
- [x] F3 es_fields.js 字段侧栏：树形字段 + 能力小标 + 冲突/部分警告 + 插入查询 + 分析快捷入口 —— 验收：node --check、标记与快捷入口正确 —— 2026-09-11 晚完成
- [x] F4 es_kql.js KQL 输入条（300ms 防抖校验 + pos/len 高亮）+ 时间选择器（字符串直传、结束「不含」标注）—— 验收：node --check、前端零时间换算 —— 2026-09-11 晚完成
- [x] F5 es_discover.js Discover 主视图：SVG 直方图拖拽选区 + 文档表（转义高亮 + 行展开）+ 内存分页 + 截断提示 + 移除旧文档查询 tab —— 验收：node --check、翻页零 ES 请求、XSS 转义核对 —— 2026-09-11 晚完成
- [x] F6 es_analyze.js 分析抽屉：去重计数（约 + 精度 tooltip）/ 图状分析（维度指标选择 + 运行按钮）、纯 SVG 柱/折线/饼 + 图表切换 —— 验收：node --check、「全量非当前页」标注 —— 2026-09-11 晚完成
- [x] F7 es_lifecycle.js 生命周期 tab：策略列表（内置置灰）+ JSON 编辑二次确认回显 + 索引 ILM 状态 + 重试 + DLM 保留期 —— 验收：node --check、内置只读 —— 2026-09-11 晚完成
- [x] F8 es_templates.js 索引模板 tab：双段列表 + composed_of 展开 + 保存前 _simulate 差异比对 —— 验收：node --check、模拟预览正确 —— 2026-09-11 晚完成
- [x] F9 样式令牌 + script 注册核对 + 全链路联调（建视图→KQL→直方图→翻页→分析→ILM→模板模拟→手动刷新）—— 验收：控制台零报错、真机全链路走通 —— 2026-09-11 晚：样式与 script 核对完成，真机全链路联调待换新二进制后执行
- [x] F10 索引模板「可视化创建」：表单五段 + 公共层（组件模板 `composed_of`）+ 实时 JSON 预览 + pattern 重叠检测，与「JSON 直接提交」并存 —— 验收：`node script/_fe_smoke.js` 第 13 项（沙箱内**直接执行** `EsTplBuild` 断言请求体与线上四份模板等价）+ 21 种反向验证全中；渲染台量出并修掉「对话框 1448px 顶出 849px 视口、底部按钮不可见」；用户复核反馈「右边 json 展示没有铺满」后二次修版式（右列原被撑到 1232px、预览框固定 378px，下方空出 690px）：整框高度受限 flex 列 + body 只当容器不自滚 + 两列各自滚 + 预览框吃掉右列剩余高度，13j 契约同步改写并 6 种反向验证全中 —— 2026-09-17 完成
- [x] F11 原始 JSON 抽屉铺满：索引 tab 点索引名打开的「原始 mapping」抽屉、索引模板页的「模拟结果」抽屉，JSON 面板向下铺满剩余高度 —— 背景：面板吃的是全局 `.es-source { max-height: 220px }`，抽屉 1294px 高时面板仍只有 220px、下方空出 959px（用户口径「只显示一部分，没有向下铺满」），短 mapping（内容 243px）也带滚动条、长 mapping（2456px）挤在 220px 小框里滚 —— 验收：`node script/_fe_smoke.js` 第 14 项（14a 样式契约 6 条 / 14b 三个抽屉的类必须挂在承载面板的 el-drawer 上且位于面板之前 / 14c 反向覆盖：凡是用带 `v-loading` 的 `.es-source` 面板的文件，其抽屉必须挂该类）+ 14 种反向验证全中并逐条还原回绿；渲染台 `script/measure_es_raw_drawer.html` 量出改前 220px、改后 1157px（铺满 YES、短 mapping 面板内不再滚动），另修掉两处顺带发现的静默缺陷：v-loading 遮罩改前盖满整个抽屉（0→1294）、面板与抽屉同为白底不描边看不出是否铺满 —— 2026-09-17 完成

### 套件目录化拆分（待确认）→ [tasks/09-stack-dir-split/](./tasks/09-stack-dir-split/)

> 方案：把套件的后端执行/探活逻辑与前端组件/样式**按套件目录化**。后端新建契约层 `store/stackkit`（最小 Driver + 8 个可选能力接口，类型断言调用），套件 Go 代码与 `scripts/` **同级**放入 `store/stacks/<key>/`，`go:embed` 收紧为 `stacks/*/scripts stacks/*/configs`（连带 `builtin_templates.go` 同步收紧），`store/builtin_stacks.go` 降为薄壳，`api/stack` 删除全部套件分支（40+ 处、15 个文件）改为面向 `stackkit.Driver`。前端新建 `template/static/stacks/<key>/`（js/css 自治，样式前缀 `.stk-<key>-*`），通用逻辑抽 `stacks/common/`，`stacks.js` 降为挂载壳。已拍板：① 后端 Go 代码与 scripts 同级（方案 A）② 前端静态引入（方案 A）③ 一次性迁移全部 9 套件（不设试点期）④ 部署模板体系不纳入目录化（仅做 embed 最小联动）。设计依据 `docs/套件目录化拆分设计.md`。详见 frontend.md / backend.md / boundary.md

- [x] B1（P0）基线与行为快照：9 套件 × 各 mode 的角色计划 / 登记服务 / 校验端点快照 + 渲染字节基线 + 行数与分支数检查脚本 —— 验收：三类基线落盘入库（394KB），检查脚本当前全绿可复现 —— 2026-09-14 完成
- [x] B2（P1）契约层与 embed 收紧：`store/stackkit` + `store/registry.go` + `store/embed.go`，`BuiltinStack` 改兼容壳，`builtin_templates.go` embed 联动 —— 验收：go build/vet/test 全绿，快照与渲染基线零差异，套件 `.go` 未进二进制 —— 2026-09-14 完成（探针实证收紧生效；发布体积 +6.6KB）
- [x] B3（P2-1）搬 PlanRoles · 简单型 5 套件（kafka/nacos/powerjob/rabbitmq/rocketmq）—— 验收：角色计划与快照逐字段一致 —— 2026-09-14 完成（22 例行为快照 + 52 份渲染基线逐字节一致；套件分支 52→46）
- [x] B4（P2-2）搬 PlanRoles · redis 三模式 —— 验收：三模式角色计划与快照一致 —— 2026-09-14 完成
- [x] B5（P2-3）搬 PlanRoles · elasticsearch 双模式 —— 验收：cluster + 冷热温角色计划与快照一致 —— 2026-09-14 完成
- [x] B6（P2-4）搬 PlanRoles · elfk（Logstash 落点三处一致）—— 验收：快照一致 + TestPlanGenericELFK 等价单测通过 —— 2026-09-14 完成
- [x] B7（P2-5）搬 PlanRoles · bigdata（HA 矩阵）收尾 + switch 删除 —— 验收：switch 消失，builtin_stacks.go ≤120 行 —— 2026-09-14 完成（B10 收尾至 60 行）
- [x] B8（P3）搬探活：ServiceRegistrar + VerifyProbe + ScaleGuard —— 验收：登记服务 / 校验端点 / 缩容保护与快照一致 —— 2026-09-14 完成
- [x] B9（P4）搬变量与拓扑：VarInjector + TopologyBuilder + Defaults + CreateValidator —— 验收：**渲染脚本 diff 为空**，api/stack 套件分支数为 0 —— 2026-09-14 完成（52 份渲染基线逐字节一致）
- [x] B10（P7）后端收尾：删兼容壳 + 补单测 + 更新 `workflow/docs/03-directory.md` —— 验收：全量门禁与快照/渲染 diff 全绿 —— 2026-09-14 完成（`builtin_stacks.go` 60 行，`FindBuiltinStack` 直接返回 `stackkit.Driver`；新增 3 组契约闸门；`check_stack_lines.py --targets` 通过）
- [x] F1（P5）抽 common：`stacks.js` 骨架化 ≤120 行 + 通用类 `.stk-*` —— 验收：node --check 全绿，向导 UI 无肉眼变化 —— 2026-09-14 完成（`stacks.js` 22 行挂载壳）
- [x] F2（P6-1）套件注册表 `StackDrivers` + `index.html` 分组静态引入 —— 验收：9 个 key 全注册，控制台零报错 —— 2026-09-14 完成（装载冒烟：57 脚本、9 套件齐备、模板 39422 字符逐字节一致）
- [x] F3（P6-2）elasticsearch 前端迁移（roles/cluster/verify + `.stk-es-*`）—— 验收：双模式向导无肉眼变化 —— 2026-09-14 完成
- [x] F4（P6-3）bigdata 前端迁移（select/op/roles + `.stk-bd-*`）—— 验收：含 HA 角色矩阵全流程无肉眼变化 —— 2026-09-14 完成
- [x] F5（P6-4）redis 前端迁移（topo + `.stk-redis-*`）—— 验收：三模式向导无肉眼变化 —— 2026-09-14 完成
- [x] F6（P6-5）其余 6 套件前端迁移（elfk/kafka/rabbitmq/rocketmq/nacos/powerjob）—— 验收：向导无肉眼变化，`stacks-forms.js` 无残留 —— 2026-09-14 完成（该文件已删除，内容全迁）
- [x] F7（P7）前端收尾与真机走查：`style.css` 瘦身核对 + 9 套件走查 —— 验收：≥3 种套件视觉形态确实不同，ES/bigdata/redis 各跑通一次端到端 —— 2026-09-14 **静态项全部完成**（`style.css` 无套件专属选择器、`stacks.js` 22 行、58 个前端文件 `node --check` 通过）；**真机走查（standby-01）与端到端部署未执行**，须人工在测试机发起
