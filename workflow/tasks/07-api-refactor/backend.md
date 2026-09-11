# 任务 07 · API 目录重构 — backend.md（后端执行）

> 设计依据：`docs/api目录重构.md`（评审稿，含目标目录结构 / 分包依赖 / 大文件拆分映射）。
> 原则：仅做**目录与包边界重组**，不改业务逻辑；全程可编译、单测不回归；单文件 ≤300 行。
> `api/` 包当前仅被 `router/router.go` 引用，无其他消费者，分包可控。
> 执行顺序即步骤编号；每步完成后按验收标准验收并在 workflow.md 标记 [x]。

## B1 基线

- [ ] `git status` 确认工作区干净；记录 `go build ./...` / `go vet ./...` / `go test ./...` 基线输出
- [ ] 记录 `api/` 所有文件行数快照，作为拆分前对照（大文件清单见设计文档 §1.2）

验收：基线命令全部通过且输出可复现；无未提交改动。

## B2 删除无用目录 api/data

- [ ] `git rm -r api/data`（连同遗留空库 `infra-ops.db`/`-shm`/`-wal`）
- [ ] 全局 `rg "api/data"` 确认零残留（已核实：全工程无引用，真实库在根 `data/infra-ops.db`）

验收：`api/data` 目录消失；无任何引用残留；`go build` 通过。

## B3 抽取共享层 api/shared

- [ ] 新建 `api/shared/` 包：`env.go`（`applyHostVars`）+ `slice.go`（`containsString`/`containsInt64`/`mergeStringList`/`filterEmpty`）
- [ ] 更新调用处为 `shared.Xxx(...)`（`api/deploy_task.go`、`api/orchestration_engine.go`、`api/stack_engine.go`、`api/stack_instance.go`、`api/stack_plan.go`、`api/stack_verify.go`）
- [ ] 本阶段仍保持 `api/` 单包扁平，仅新增 shared 子包，先跑通两张包共存

验收：`go build`/`go test` 全绿；git diff 无业务逻辑改动，仅引用前缀变化。

## B4 拆分子包（auth / credential / overview / sse / host）

- [ ] `api/auth/auth.go`（包名 auth）、`api/credential/credential.go`、`api/overview/overview.go`（misc.go 迁入）+`api/overview/services.go`（service.go 迁入）、`api/sse/sse.go`
- [ ] `api/host/host.go` + `api/host/host_batch.go` + 随迁 `api/host/host_batch_test.go`
- [ ] 更新 `router/router.go` 的 import 与构造器调用前缀（`api.NewAuthHandler`→`auth.NewAuthHandler` 等）
- [ ] 同步处理泛型/包内私有符号：构造器与方法导出，包内枚举类型据新包边界调整可见性

验收：每包独立 commit；`go build`/`go vet`/`go test` 全绿；`router.go` 编译通过。

## B5 拆分子包（tool：es / registry / sftp）

- [ ] `api/tool/es/es.go`、`api/tool/registry/registry.go`、`api/tool/sftp/sftp.go`（构造器导出 + 包名 tool/es 等）
- [ ] 更新 `router/router.go` 引用；随迁无测试文件（三工具当前无单测，靠 go build 验证）

验收：`go build`/`go vet` 全绿；三接入地址生成语义不变。

## B6 拆分子包（deploy / orchestration）

- [ ] `api/deploy/`：deploy_template.go + deploy_schedule.go + deploy_task.go（含 streamTee/limitedBuffer/applyHostVars 等，迁 `api/shared` 后的引用对齐）
- [ ] `api/orchestration/`：orchestration.go + orchestration_engine.go + orchestration_sse.go（依赖 deploy 包，单向导入）
- [ ] 随迁 `api/deploy/deploy_name_test.go`、`api/deploy/deploy_p0_test.go`；`configs_test.go` 归到被测逻辑所在包
- [ ] 更新 `router/router.go`；明确 orchestration→deploy 单向依赖，避免环

验收：`go build`/`go vet`/`go test` 全绿；orchestration 运行链路不受影响。

## B7 拆分子包（stack，量最大）

- [ ] `api/stack/` 迁入全部 6 个 stack 业务文件 + 6 个 stack 测试文件（stack.go / stack_engine.go / stack_instance.go / stack_plan.go / stack_sse.go / stack_verify.go 及 *_test.go）
- [ ] 包内辅助（bashQuote/parseJSONMap/mergeParamMaps 等）保持包内不外提；跨包引用统一走 `api/shared`
- [ ] 更新 `router/router.go`（NewStackHandler 等，构造器导出）

验收：`go build`/`go vet`/`go test ./api/stack/...` 全绿；套件创建/探活/扩缩容语义不变。

## B8 大文件拆分（deploy_task / 三工具 client）

- [ ] `api/deploy/deploy_task.go`（817 行）拆 4 份：`deploy_task.go`（引擎核心）/`deploy_exec.go`（远程执行运行时 streamTee/limitedBuffer/runRemoteScript/templateRequires/checkRequires）/`deploy_sse.go`（SSEProgress/SSESetup/SSELog）/`deploy_vars.go`（mergeParams/parseConfigs/mergeConfigs/applyConfigOverrides/renderConfigFile/lastOctet/dedupInt64）
- [ ] `es.go`→`es.go`（handler）+`es_client.go`（escClient 请求层）；`registry.go`→`registry.go`+`registry_client.go`；`sftp.go`→`sftp.go`+`sftp_client.go`

验收：各新文件 ≤300 行（document/已有文件除外）；`go test ./api/deploy/ ./api/tool/...` 全绿；行为零差异。

## B9 大文件拆分（stack 系列）

- [ ] `stack_engine.go`（1706）拆 5：stack_engine（流水线核心）/stack_log（appendLog/publishLogs/publishHost）/stack_register（registerRedisService/registerStackService/registerBigdataServices/isMasterAt/stackBigdataMasters/bigdataMasterExtra）/stack_bigdata（stackBigdataRole/bigdataHAVarTable/xmlProp/bigdataHAXMLBlocks/mergeBigdataMasterExtra）/stack_vars+stack_compose（privatizeImages/applyStackVars/selectPipelinePhases/stackVarsJSON/stackComposeDownScript）
- [ ] `stack_verify.go`（1170）拆 5：stack_verify（入口）/stack_verify_script/buildStackVerifyScript·verifyContainers·bigdataExpectedContainers/stack_verify_parse（parse*·parseCtrState·parseComposeStates）/stack_verify_redis（redis*·parseSentinelMaster）/stack_verify_endpoints（stackVerifyEndpoints·bigdataVerifyEndpoints）
- [ ] `stack_instance.go`（975）拆 4：stack_instance（CRUD+runInstanceOp）/stack_instance_build（build*Hosts·activeInstanceHosts·replayStackMemberPlan）/stack_instance_validate（validate*·bigdataProtectedHosts·stackHostRole）/stack_instance_util（parseJSONStringList·parseComponentsCSV·mergeComponentList等）
- [ ] `stack.go`（571）拆 2：stack/stack_topo（parseReplicas·validateStackTopology·validateRedisTopology·mergeStackParams·stackVarInMode；randomKafkaClusterID/randomErlangCookie 随归属）

验收：拆分后各文件 ≤300 行；`go test ./api/stack/...` 全绿；大数据 HA 三落点单测、探活解析等不回归。

## B10 收尾回归 + 冒烟

- [ ] `go build ./...` / `go vet ./...` / `go test ./...` 与 B1 基线逐项比对，无新增失败
- [ ] `git diff --name-only` 全部落在 boundary.md 白名单内
- [ ] 启动服务（8090）冒烟：登录 / 主机 / 部署 / 编排 / 套件 / 三工具 / 总览 / SSE 各菜单接口返回正常

验收：构建/静态检查/单测与基线一致或更优；服务正常启动，各菜单功能不变。

## 进度记录

（执行中记录：分（拆）包时暴露的隐藏跨包依赖、包名/可见性调整决策、回滚的 commit 号。）