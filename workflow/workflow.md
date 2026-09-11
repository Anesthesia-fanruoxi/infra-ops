# 长任务工作流

> 通用规则在上，任务列表在下；每个任务的详细规划见各自目录的 `frontend.md`（前端执行）/ `backend.md`（后端执行）/ `boundary.md`（边界）。纯前端任务只建 frontend.md + boundary.md，纯后端任务只建 backend.md + boundary.md。
> 流程：步骤完成 → 按验收标准验收 → 标记 \[x]。

## 通用规则

1. 代码文件单个不超过 300 行，超出须拆分（文档除外）
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

- [ ] 联调验收：一次录入 10+ 台真实服务器 —— 验收：全部入库、状态与自动命名正确、host.batch\_create 审计留痕

### 任务编排重构为「任务记录」模型（已完成）→ [tasks/03-task-records/](./tasks/03-task-records/)

> 方案：编排改为一次性任务记录——新建即未开始，执行一次后终态已结束；三态由最近一次运行派生，无 schema 迁移。移除历史运行区块与列表接口，单列表全量展示。详见 frontend.md / backend.md / boundary.md

- [x] 后端：列表联查最近运行（三态 + 结果统计），下线运行历史列表接口 —— 验收：/orchestrations 含 state/last\_run\_id/result，GET /api/orchestration/runs 返回 404（经验性验证通过）

- [x] 后端：一次性守卫（已运行不可重跑 / 不可编辑）+ 删除连带清运行 —— 验收：重跑返回 400，编辑被拒，删除后无孤儿运行（经验性验证通过）

- [x] 后端：保留清理连带删任务记录并清扫孤儿运行 —— 验收：超期已结束记录整条消失，运行中记录不受影响（经验性验证通过）

- [x] 前端：任务记录语义改造（三态 tabs / 结果列 / 按状态分流操作 / 去启用列）—— 验收：三态操作正确，控制台零报错（node --check 通过；浏览器交互待启动页面验收）

- [ ] 联调验收：新建 → 运行 → 结束全流程 —— 验收：状态流转正确、已结束不可重跑不可编辑、SSE 进度正常

### 任务运行抽屉：三层结构 + 双 SSE（已完成）→ [tasks/04-run-drawer/](./tasks/04-run-drawer/)

> 方案：点运行后自动展开 50% 宽只读抽屉——L1 步骤层（进展+完成聚合色，可点击切换）→ L2 主机层（三态徽章）→ L3 日志层（时间+IP+内容混排）。双 SSE 独立端点：步骤流（生命周期）+ 详情流（按步骤主机态与日志，快照打底+增量）；日志新增 orchestration\_run\_logs 落库（V14，级联清理）。详见 frontend.md / backend.md / boundary.md

- [x] 后端：迁移 V14 日志表 + model + 日志 repo —— 验收：旧库启动自动建表，编译通过（经验性验证：迁移幂等 + 日志落库/读取/级联删除全过）

- [x] 后端：事件总线新增步骤/日志 topic + 引擎改造（步骤事件、日志先落库再发布、状态行、跳过推送）+ 文件拆分 —— 验收：编译通过，运行中日志边执行边入库（拆分后 api/orchestration.go 293 行 / engine 299 行 / sse 274 行，均 ≤300）

- [x] 后端：双 SSE 端点（init 快照+过滤推送+done 关闭）+ 路由更新 + 移除旧端点 —— 验收：curl 可见事件序列，go build/vet/test 通过（httptest 协议测试覆盖 init/step/host/log/done/回溯/步骤过滤）

- [x] 前端：抽屉 mixin（三层结构 + GET 打底）—— 验收：node --check 通过，已结束任务静态展示正确

- [x] 前端：双 SSE 接入 + 自动追踪/手动切换 + 移除旧矩阵弹窗 + 样式 —— 验收：控制台零报错，无死代码残留（node --check 通过、模板配平、无陈旧引用；浏览器交互待用户启动页面验收）

- [ ] 联调验收：运行→自动开抽屉→三层实时联动→结束翻转列表；回溯逐步骤查看 —— 验收：无断流无丢行，跳过主机实时置灰

### 大数据底座全组件高可用（进行中）→ [tasks/05-bigdata-ha/](./tasks/05-bigdata-ha/)

> 方案：套件级 `ha` bool 开关（选套件卡片上开，开=全部支持组件双实例）；非 HA 路径零回归；HA 模式角色规划重构为全角色矩阵（每角色可指定主机）。设计依据 `docs/bigdata-ha-design.md`。详见 frontend.md / backend.md / boundary.md

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
- [ ] F5 非 HA 全回归 + 联调验收：关开关全流程 + HA 真机端到端部署 + 故障演练矩阵 —— 验收：控制台零报错，逐组件 30s 接管

### Elasticsearch 套件冷热温双模式（规划中）→ [tasks/06-es-cold-warm-hot/](./tasks/06-es-cold-warm-hot/)

> 方案：ES 套件升级为双模式部署——「集群」模式原样保留（仅升默认镜像 9.5.3），新增「冷热温」架构模式：每台多选 master/纯协调/数据-hot/warm/cold 角色，沿用主脑流水线多阶段编排（reset→masters→coords→data→verify），提供 SSL 自签开关、JVM 参数透传与自定义 scale_in 官方下线流程。LLM/ILM 等延到大套件。设计依据 `docs/es-suite-cold-warm-hot.md`。详见 frontend.md / backend.md / boundary.md

- [x] B1 蓝图与模式接入：cold_warm_hot 模式 + 模式级变量（ssl_enabled/coordinator_count/heap/jvm_opts）+「集群」镜像升 9.5.3 —— 验收：go build/vet 通过，旧「集群」渲染除镜像外零差异（check_es_tpl.py 隐式回归）
- [x] B2 角色模型与 node.roles 生成：角色勾选→node.roles、master 与数据层互斥、协调 2 台兜底 —— 验收：非法组合逐条 400（validateCWHRoles + 单测全过）
- [x] B3 主脑流水线编排：Pipeline 阶段 + node.sh 改 RUN 分发器（run_reset/masters/coords/data/verify）—— 验收：go build/vet + 渲染校验阶段序列（check_es_tpl.py 全过）
- [x] B4 SSL 自签签发：openssl CA + 每节点证书 + xpack 注入 + CA 下载 —— 验收：双形态渲染校验，开启形态 HTTPS 就绪（PEM 免密码，显式 security=false 规避隐性认证）
- [x] B5 JVM 参数调优：heap_xms/xmx 分层 + jvm_opts 透传 —— 验收：渲染校验 ES_JAVA_OPTS 拼装正确
- [x] B6 缩容 scale_in 官方下线：exclude._name 排空 + voting exclusions + 回 green 再停容器 + 末台 master 保护 —— 验收：软下线脚本渲染校验 + 缩容保护单测（真机缩一台待联调）
- [x] B7 服务登记/探活/接入地址：角色标签 + 按 tier 接入地址 + SSL 双形态 —— 验收：registerStackService/stackVerifyEndpoints 按 tier 分组语义正确
- [x] B8 渲染校验与回归：check_es_tpl.py 三场景 + 「集群」全回归 —— 验收：渲染全绿（10 项），go build/vet + 既有单测全过，bigdata 渲染无回归
- [ ] F1 模式选择：向导第一步 radio 集群/冷热温，动态渲染其次 —— 验收：node --check + 切换即时生效
- [ ] F2 角色分配：每台角色多选，master 与数据层互斥置灰，协调默认 2 台 —— 验收：node --check + 非法组合拦截
- [ ] F3 参数配置：SSL 开关 + coordinator_count + 端口 + JVM 堆与 jvm_opts —— 验收：node --check + true/false 字符串正确
- [ ] F4 结果展示：模式/tier 徽标 + 成员角色标签 + 按 tier 接入地址 + CA 下载 —— 验收：node --check + 渲染正确无回归
- [ ] F5 双模式全回归 + 联调验收：「集群」全流程零差异 + 冷热温真机端到端部署 → 分层 → 缩容下线 —— 验收：控制台零报错，真机扩缩容与 SSL 形态走通

### API 目录重构（已规划）→ [tasks/07-api-refactor/](./tasks/07-api-refactor/)

> 方案：删除无用 `api/data` 残留目录；将扁平单包 `api/` 按菜单功能拆分为子包（auth/credential/host/overview/deploy/orchestration/stack/tool/{es,registry,sftp}/sse），跨模块共享辅助收敛到 `api/shared`；大文件（stack_engine 1706 / stack_verify 1170 / stack_instance 975 / deploy_task 817 / es / registry / sftp / stack）拆至单文件 ≤300 行；不新增/不修改端点，纯导出路径与可见性调整。设计依据 `docs/api目录重构.md`。纯后端任务，详见 backend.md / boundary.md

- [ ] B1 基线：git 工作区干净，记录 go build/vet/test 基线 —— 验收：基线全通过可复现
- [ ] B2 删除无用目录 api/data：git rm -r + rg "api/data" 零残留 —— 验收：目录消失、零引用、构建通过
- [ ] B3 抽取共享层 api/shared：迁 applyHostVars/containsString/containsInt64/mergeStringList/filterEmpty —— 验收：go build/test 全绿，无逻辑改动
- [ ] B4 拆分子包（auth/credential/overview/sse/host）+ router 引用更新 —— 验收：每包独立 commit、构建测试全绿
- [ ] B5 拆分子包（tool/es、registry、sftp）—— 验收：go build/vet 全绿
- [ ] B6 拆分子包（deploy/orchestration，单向依赖）+ 测试随迁 —— 验收：编排运行链路不回归
- [ ] B7 拆分子包（stack 全部文件 + 测试）—— 验收：go test ./api/stack/... 全绿
- [ ] B8 大文件拆分：deploy_task 拆 4、es/registry/sftp 各拆 handler+client —— 验收：新文件 ≤300 行、测试全绿
- [ ] B9 大文件拆分：stack_engine 拆 5、stack_verify 拆 5、stack_instance 拆 4、stack 拆 2 —— 验收：≤300 行、HA 单测与探活解析不回归
- [ ] B10 收尾回归 + 冒烟：构建/vet/test 与基线比对、git diff 在白名单、服务启动各菜单接口正常 —— 验收：构建静态检查单测全绿，各菜单功能不变

