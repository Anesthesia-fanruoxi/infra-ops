# api 目录重构方案

> 目标目录：`api/`（Go HTTP 接口层）
> 状态：计划阶段（待评审后分阶段执行）
> 日期：2026-09-11

---

## 一、现状问题

### 1.1 目录扁平、文件过多
`api/` 下平铺了 32 个 `.go` 文件，按菜单功能（认证 / 凭据 / 主机 / 部署 / 编排 / 套件 / 三工具 / 总览 / SSE）交错堆叠，缺乏目录边界，新增与排查成本高。

### 1.2 大文件集中了绝大部分逻辑
| 文件 | 行数 | 说明 |
|---|---|---|
| stack_engine.go | 1706 | 套件流水线 + 大数据 HA 逻辑 |
| stack_verify.go | 1170 | 套件探活 + 输出解析 + Redis 解析 |
| stack_instance.go | 975 | 实例操作 + 主机构建 + 校验 |
| stack_plan.go | 968 | 角色计划 |
| deploy_task.go | 817 | 部署执行引擎 |
| es.go | 691 | ES 连接 handler + client |
| registry.go | 660 | 镜像仓库 handler + client |
| stack.go | 571 | 套件入口 + 拓扑 |
| sftp.go | 456 | SFTP handler + client |
| host.go / sse.go / host_batch.go | 346 / 340 / 330 | 中等偏大 |

### 1.3 `api/data` 是无用残留目录（已确认）
- 全工程代码（`config/config.go:93`、`main.go:31`、`script/*.go`）数据库路径一律为根目录 `data/infra-ops.db`。
- 全工程搜索不到任何对 `api/data` 的引用。
- `api/data/infra-ops.db` 为 2026-08-26 遗留的空库（4096 字节，WAL 为空），无业务数据。
- **结论：可直接删除 `api/data/` 目录。**

> 说明：用户提到写入 `doc/`，但工程内实际目录名为 `docs/`（存放设计文档），本方案按 `docs/` 落盘。如需改为 `doc/` 请告知。

---

## 二、重构目标

1. **删除无用目录**：移除 `api/data/`。
2. **按菜单功能分包**：将 `api/` 拆分为直接对应用户菜单语义的子包（每个子目录一个 Go package）。
3. **拆分大文件**：将 >500 行的文件拆成职责单一的小文件。
4. **共享辅助函数收敛**：跨模块复用的工具函数集中到独立包，避免分包后重复定义或循环依赖。
5. **保持行为不变**：路由、构造器签名、对外 API 路径不变；全程可编译、单测全绿。

---

## 三、目标目录结构

```
api/
├── auth/                    # 认证（登录/登出/改密）
│   └── auth.go
├── credential/              # 凭据管理
│   └── credential.go
├── host/                    # 主机管理 + 批量导入
│   ├── host.go
│   └── host_batch.go
├── overview/                # 总览 + 服务清单
│   ├── overview.go          # 由 misc.go 迁入
│   └── services.go          # 由 service.go 迁入
├── deploy/                  # 部署中心（模板/任务/定时）
│   ├── deploy_template.go
│   ├── deploy_schedule.go
│   ├── deploy_task.go       # 执行引擎核心（收缩）
│   ├── deploy_exec.go       # ★拆：SSH 远程执行运行时
│   ├── deploy_sse.go        # ★拆：SSE 流
│   └── deploy_vars.go       # ★拆：参数/配置合并渲染
├── orchestration/           # 任务编排
│   ├── orchestration.go
│   ├── orchestration_engine.go
│   └── orchestration_sse.go
├── stack/                   # 套件部署（重点拆分）
│   ├── stack.go             # 入口 + 拓扑 + 参数（收缩）
│   ├── stack_topo.go        # ★拆：拓扑/副本/参数校验
│   ├── stack_engine.go      # 流水线核心（收缩）
│   ├── stack_log.go         # ★拆：日志/事件推送
│   ├── stack_register.go    # ★拆：套件&大数据服务注册
│   ├── stack_bigdata.go     # ★拆：大数据角色/HA XML
│   ├── stack_vars.go        # ★拆：变量/镜像 URL/时序变量
│   ├── stack_compose.go     # ★拆：compose down 脚本生成
│   ├── stack_instance.go    # 实例 CRUD + 操作（收缩）
│   ├── stack_instance_build.go    # ★拆：实例主机构建
│   ├── stack_instance_validate.go # ★拆：扩容/组件校验
│   ├── stack_plan.go        # 角色计划
│   ├── stack_sse.go
│   ├── stack_verify.go      # 探活入口 + 汇总（收缩）
│   ├── stack_verify_script.go    # ★拆：探活脚本/容器清单
│   ├── stack_verify_parse.go     # ★拆：输出解析/状态判定
│   ├── stack_verify_redis.go     # ★拆：Redis 信息解析与提示
│   ├── stack_verify_endpoints.go # ★拆：接入地址/探活端点
│   └── stack_verify_util.go      # ★拆：字符串/JSON 工具
├── tool/                    # 工具
│   ├── es/           ├── es.go + es_client.go        # ★拆：handler / client
│   ├── registry/     ├── registry.go + registry_client.go  # ★拆
│   └── sftp/         ├── sftp.go + sftp_client.go           # ★拆
├── sse/                    # 全局 SSE
│   └── sse.go
└── shared/                 # ★跨模块共享辅助（包名 shared 或 internal/shared）
    ├── env.go              # applyHostVars 等跨模块变量替换
    └── slice.go            # containsString/mergeStringList/filterEmpty 等
```

### 测试文件归属
| 测试文件 | 归属包 |
|---|---|
| `stack_*_test.go`（6 个） | `stack/` |
| `host_batch_test.go` | `host/` |
| `deploy_name_test.go`、`deploy_p0_test.go` | `deploy/` |
| `configs_test.go` | 随被测逻辑（命中 host/deploy 参数合并则归对应包，并同步调整目标包名） |

---

## 四、分包依赖关系（含共享层设计）

```
            ┌────────────── api/shared (无业务依赖)
            │
router ──►  auth / credential / host / overview
router ──►  tool/{es,registry,sftp}      ──► api/shared
router ──►  deploy                        ──► api/shared
router ──►  orchestration ──► deploy     ──► api/shared
router ──►  stack                        ──► api/shared
router ──►  sse
```

- **包间依赖只允许单向**：`orchestration → deploy → shared`、`stack → shared`；`deploy`/`orchestration`/`stack` 平行调用各自执行引擎，不互相依赖，避免环。
- **共享层收敛清单**（当前跨模块复用的函数，迁入 `api/shared`）：
  - `applyHostVars`（deploy / orchestration_engine / stack_engine 共用）→ `shared/env.go`
  - `containsString`、`containsInt64`、`mergeStringList`、`filterEmpty` → `shared/slice.go`
  - 以上为已确认跨模块项；分包时若发现更多跨包依赖（如 exec 运行时被 orchestration 复用），统一抽到 `shared` 或 `deploy` 具名导出后再引用。
- **`stack` 包内部** 的辅助函数（`bashQuote`、`parseJSONMap`、`mergeParamMaps` 等）只在 stack 内复用，保持包内，不强行外提。

---

## 五、大文件拆分细则（函数级映射）

### 5.1 `stack_engine.go`（1706 → 拆 5 份）
| 新文件 | 承载函数 |
|---|---|
| `stack_engine.go` | execute / failAll / skipRemaining / markHostsDone / finish / runPipeline / runPhase* / syncInstanceAfterRun / probeDocker / execScript / forEachHost |
| `stack_log.go` | appendLog / publishLogs / publishHost |
| `stack_register.go` | registerRedisService / registerStackService / registerBigdataServices / isMasterAt / stackBigdataMasters / bigdataMasterExtra |
| `stack_bigdata.go` | stackBigdataRole / bigdataHAVarTable / xmlProp / bigdataHAXMLBlocks / mergeBigdataMasterExtra / bigdataRole 类型相关若在 model 则不动 |
| `stack_vars.go` + `stack_compose.go` | privatizeImages / isImageParam / trimRegistryPrefix / applyStackVars / stackClusterExtra( Merged ) / selectPipelinePhases / phaseHasComponent / stackVarsJSON ┆ stackComposeDownScript |

### 5.2 `stack_verify.go`（1170 → 拆 5 份）
| 新文件 | 承载函数 |
|---|---|
| `stack_verify.go` | VerifyInstance / verifyInstanceHost / assembleStackVerify / applyRedisClusterChecks / anyHostOK / bigdataRoleMasters / overrideBigdataMasters |
| `stack_verify_script.go` | buildStackVerifyScript / verifyContainers / bigdataExpectedContainers / bigdataContainerComponent |
| `stack_verify_parse.go` | parseStackVerifyOutput / parseBigdataCtrChecks / parseLiveContainers / imageShort / formatDockerStarted / parseCtrState(Ex) / parseComposeStates |
| `stack_verify_redis.go` | redisVerifyHints / parseRedisInfo / parseConnectedSlaves / parseSentinelMaster / redisVersion / redisRoleMatches / liveRoleLabel |
| `stack_verify_endpoints.go` | stackVerifyEndpoints / bigdataVerifyEndpoints |

### 5.3 `stack_instance.go`（975 → 拆 4 份）
| 新文件 | 承载函数 |
|---|---|
| `stack_instance.go` | ListInstances / GetInstance / PatchInstance / DeleteInstance / ScaleOut / ScaleIn / AddComponent / RemoveComponent / Uninstall / Reinstall / runInstanceOp / InstanceRuns |
| `stack_instance_build.go` | buildCreateHosts / buildScaleOutHosts / buildAddComponentHosts / buildUninstallHosts / buildScaleInHosts / buildReinstallHosts / activeInstanceHosts / instanceHostsAsRunHosts / replayStackMemberPlan / instanceMasterIP / instanceMasterHostID / masterIPOf |
| `stack_instance_validate.go` | validateScaleIn / validateBigdata* / bigdataProtectedHosts / stackHostRole |
| `stack_instance_util.go` | parseJSONStringList / encodeJSONStringList / parseComponentsCSV / mergeComponentList / intersectComponents / subtractComponents / mergeStringList |

### 5.4 `deploy_task.go`（817 → 拆 4 份）
| 新文件 | 承载函数 |
|---|---|
| `deploy_task.go` | StartScheduler / NewDeployHandler / Run / createAndRun / execute / Tasks / TaskDetail |
| `deploy_exec.go` | execOnHost / execHostWith / runRemoteScript / streamTee / limitedBuffer / templateRequires / checkRequires / extractSelfReportedName |
| `deploy_sse.go` | SSEProgress / SSESetup / SSELog |
| `deploy_vars.go` | mergeParams / parseConfigs / mergeConfigs / applyConfigOverrides / renderConfigFile / applyHostVars / lastOctet / dedupInt64 |

### 5.5 工具三件（各拆 handler / client）
- `es.go` → `es.go`（handler：List/Create/Update/Delete/resolve/Ping/Overview/Nodes/Indices/CreateIndex/DeleteIndex/Search + 请求规整）+ `es_client.go`（escClient：do/esErr/ping/overview/nodes/indices/createIndex/deleteIndex/search + 编码辅助）。
- `registry.go` → `registry.go`（handler）+ `registry_client.go`（registryClient：endpoint/do/tryRefreshToken/scopeForPath/catalogAll/tagsAll/manifest/deleteManifest）。
- `sftp.go` → `sftp.go`（handler：CRUD/Ping/dial）+ `sftp_client.go`（Browse/Mkdir/Upload/Download/Remove/Rename + removeRecursive/cleanRemotePath/dirParent）。

### 5.6 `stack.go`（571 → 拆 2 份）
- `stack.go`：NewStackHandler / List / Preflight / Run / createAndRun / Runs / RunDetail。
- `stack_topo.go`：parseReplicas / validateStackTopology / masterMustBeSelected / validateRedisTopology / mergeStackParams / stackVarInMode / randomKafkaClusterID / randomErlangCookie。

### 5.7 保持不拆的文件
`host.go`(346)、`sse.go`(340)、`host_batch.go`(330)、`orchestration_engine.go`(290)、`orchestration_sse.go`(254)、`deploy_schedule.go`(249)、`deploy_template.go`(228) 等 ≤350 行文件，仅随包迁移目录，不强行拆分。

---

## 六、实施步骤（分阶段，风险隔离）

> 每阶段结束必须：`go build ./...` + `go vet ./...` + `go test ./...` 全绿方可进入下一阶段。

### 阶段 0：基线
- 记录当前 `go test`/`go build` 基线结果作为回归参照。
- 确认 git 工作区干净。

### 阶段 1：删除 `api/data/`
- `git rm -r api/data`（连同空库文件），提交。
> 提交前再次确认无引用（已核实：无任何代码引用）。

### 阶段 2：抽出 `api/shared`
- 新建 `api/shared/`，迁入 `applyHostVars`、`containsString`、`containsInt64`、`mergeStringList`、`filterEmpty`。
- 更新原调用处为 `shared.Xxx(...)`。
- 本阶段仍保持 `api/` 单包扁平，先跑通双包可编译。

### 阶段 3：按模块拆分子包（每个模块作为独立 commit，便于回滚）
顺序建议（由外到内、依赖少的先迁）：
1. `auth`、`credential`、`overview`、`sse`
2. `host`
3. `tool/{es,registry,sftp}`
4. `deploy`
5. `orchestration`（依赖 deploy）
6. `stack`（依赖 shared，工作量最大，单独一个 commit）
- 每个子包迁移含：建目录、改 `package` 声明与包内私有化判断、更新 `router/router.go` 的 import 与构造器调用前缀、随迁测试文件。
- `api/` 根目录迁移完成后应清空（仅保留子目录），或保留一个空的 `api/doc.go` 说明包划分。

### 阶段 4：大文件拆分（沿用阶段 3 的包内操作，同样逐个 commit）
- 按第五节映射逐文件拆分，遵循「只搬函数不改逻辑」，新增文件 `package` 与所在包子包一致。

### 阶段 5：收尾与回归
- `go build ./...`、`go vet ./...`、`go test ./...` 与阶段 0 基线比对。
- 手工冒烟：启动服务，抽查登录/主机/部署/套件/工具各菜单接口返回正常。

---

## 七、风险与对策

| 风险 | 对策 |
|---|---|
| 分包破坏编译（构造器签名/导入路径） | 阶段 3 每模块独立 commit；仅改包前缀，不改行为 |
| 跨模块隐藏共享依赖未被识别 | 阶段 2 已收敛已知 5 个函数；分包时以 `go build` 快速暴露缺失，全量收敛 |
| 循环导入（如 orchestration↔deploy、stack↔deploy） | 依赖规则单一方向（§四）；必要时将共用运行时下沉到 `deploy` 具名导出或 `shared` |
| 大文件拆分引入逻辑偏差 | 只搬函数不改逻辑；每个拆分 commit 后跑对应包单测（`go test ./api/stack/` 等） |
| 测试文件跟随分包后包名失效 | 随源码同步改 `package <pkg>` 与同包符号引用 |
| 回滚代价 | 阶段粒度小（单模块/单文件一提交），`git revert` 单个 commit 即可局部回退 |

---

## 八、验收标准
1. `api/data/` 目录已删除，无任何 `api/data` 引用残留。
2. `api/` 下按菜单分包完成，各文件归属与第三节结构一致。
3. 所有 >500 行文件已拆解，单文件行数 ≤ 350（stack 核心 `stack_engine.go` 拆分后 ≤ 350）。
4. 构建/静态检查/单测与基线一致或更优；服务可正常启动，各菜单接口功能不变。