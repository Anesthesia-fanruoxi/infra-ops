# 任务 09 · 套件目录化拆分 — boundary.md（边界）

> 本文件限定执行本任务时**允许修改的文件范围与操作边界**。
> 清单之外的任何文件一律只读；确需越界时，停止并上报，不得自行修改。
> 设计依据：`docs/套件目录化拆分设计.md`（已定稿，议题 ①②③④ 全部拍板）。

## 1. 允许修改的文件（白名单）

```
store/stackkit/**                    # 【新建】契约层：Driver 接口 / 可选能力接口 /
                                     #   Roles 规划助手 / 注册表容器 / probe 通用模型
store/registry.go                    # 【新建】套件装配表（init 注册 + Find/List）
store/embed.go                       # 【新建】唯一 embed 声明点（StackFS / BuiltinFS）
store/stacks/**                      # 各套件目录：新增 .go（driver / plan / register /
                                     #   verify / vars）；scripts/ 与 configs/ 原样保留
store/builtin_stacks.go              # 降为薄壳（查找 / 渲染 / 转发，≤120 行）
store/builtin_stacks_test.go         # 契约冒烟 + embed 自检单测（新增或改造）
store/stacks_test.go                 # 随迁适配
store/stack_render_snapshot_test.go  # 【新建】P0 渲染字节基线 / P4 渲染 diff 校验
store/builtin_templates.go           # 仅 §5.3 第 4 点：embed 收紧 + 3 处套件脚本读取入口
api/stack/**                         # 删除全部套件分支，改为面向 stackkit.Driver；
                                     #   引擎文件按职责拆分；套件专属整文件迁出
api/stack/snapshot_test.go           # 【新建】P0 行为快照（角色计划/登记服务/校验端点）
router/router.go                     # 仅当套件查找入口签名变化时同步
template/static/stacks/**            # 【新建】前端套件目录（registry / common / <套件>）
template/static/pages/stacks.js      # 降为挂载壳（≤120 行）
template/static/pages/stacks-forms.js# 套件组件迁出至 stacks/<key>/，本体降为薄壳或删除
template/static/style.css            # 仅抽出/迁出 .stack-* 与 stacks 侧 .es-* 套件专属规则
template/index.html                  # 仅重组套件相关 script / link 分组
script/check_stack_lines.py          # 【新建】行数与套件分支数检查
docs/套件目录化拆分设计.md            # 实施中同步修订
workflow/docs/03-directory.md        # P7 更新目录说明
workflow/tasks/09-stack-dir-split/** # 本任务执行文档 + baseline/ 基线产物 + 进度记录
workflow/workflow.md                 # 仅勾选本任务步骤 [x]
```

## 2. 禁止修改的文件（黑名单 · 只读）

```
model/**                              # StackBlueprint / StackMode / StackPhase / RolePlan 结构保持原样
store/db.go、store/migrations.go      # 无数据库 schema 变更
store/builtin_templates.go 的 builtinTemplates 约 30 条定义
                                      # 议题④-B 已拍板：部署模板体系不纳入目录化
common/**、config/**、main.go          # 无公共能力 / 配置变更
api/ 下除 stack 外的子包               # auth / credential / host / overview / deploy /
                                      #   orchestration / tool / sse / shared 均不动
template/static/pages/ 下非 stacks* 文件
                                      # 含 es_*.js 9 个 ES 控制台文件（已是拆分范式，明确不动）
template/static/vendor/**             # 第三方库
workflow/tasks/（09 外的其他任务目录）
workflow/boundaries/**、workflow/docs/（03 之外）
go.mod / go.sum                       # 不新增第三方依赖
```

## 3. 接口与数据边界

1. **不新增、不修改任何 HTTP 端点**：路由路径、请求/响应语义、错误码全部保持不变。
2. **无数据库 schema 变更**：不触碰 `store/migrations.go`、`store/db.go`。
3. **脚本与配置零变更**：`store/stacks/<key>/scripts/*.sh`、`configs/*` 一律原样保留（含三级路径 `elasticsearch/scripts/cold-warm-hot/*`），仅新增同目录下的 `.go` 文件。
4. **部署模板体系结构不迁移**（议题④-B）：`builtin_templates.go` 只改 `go:embed` 声明与脚本读取入口，约 30 条模板定义原样保留。
5. **对外不暴露内部实现**（沿用既有约定）：不新增任何把内部编译产物/DSL 回显给前端的接口。
6. **行为逐字节等价**：本任务是**纯结构重构**，禁止在同一期内做功能增删或行为优化。

## 4. 技术边界

1. **依赖方向铁律（无环是可行性前提）**：

   ```
   model ← store/stackkit ← { store/stacks/<key>, store/registry.go } ← api/stack
   ```

   - `store/stacks/<key>` **禁止 import `store` 包**（否则与 `store/registry.go` 形成循环）。
   - 套件之间**禁止互相 import**（ELFK 复用 ES 的 yml 素材走资源路径，不走代码依赖）。
   - 可选能力（RolePlanner / ServiceRegistrar / VarInjector / CreateValidator / ScaleGuard / TopologyBuilder / DefaultsProvider / VerifyProbe）一律用**类型断言**调用，未实现即走通用默认。

2. **embed 必须收紧**：`//go:embed stacks/*/scripts stacks/*/configs`（目录模式递归，三级路径仍命中；无 `configs/` 的套件只是该模式项不匹配，不影响编译）。新增**启动自检 + 单测**：遍历 registry，对每个套件的每个 mode 调 `Phase("node")`，读不到即 fail。

3. **每期只搬不改、期级回滚**：兼容壳（`BuiltinStack` 包装 `Driver`）保留到本期验收通过后再删；禁止把行为优化混入搬迁期。

4. **编译门禁**：`go build ./...`、`go vet ./...`、`go test ./...`；交叉编译 `CGO_ENABLED=0 GOOS=linux GOARCH=amd64`；前端每个改动文件 `node --check`。

5. **规模约束**：单文件尽量 ≤500 行；`store/builtin_stacks.go` ≤120 行；`template/static/pages/stacks.js` ≤120 行。

6. **提交约定**：按步骤粒度提交，信息中文 `type(scope): 描述`；跨期不合并提交。

7. **验证方式**：开发期服务本机 8090 后台启动验证；真机验证仅限测试机（standby-01），保留旧二进制备回退；真机部署须人工确认后由页面发起。

## 5. 验收边界

1. **结构硬指标**：
   - `api/stack/` 中套件分支数为 0（`grep 'bp.Key == "|stack_key == "|StackKey == "' api/stack/ --include=*.go` 为空）。
   - 每个套件的执行 + 探活 + 角色规划逻辑**全部位于 `store/stacks/<key>/`**。
   - 前端每套件 js/css 位于 `template/static/stacks/<key>/`。
2. **行为硬指标**：
   - 9 套件 × 各 mode 的角色计划 / 登记服务清单 / 校验端点清单与 P0 快照逐字段一致。
   - 每套件每 mode 渲染出的 node / bootstrap / scale_out / scale_in 脚本与 P0 基线 **diff 为空**。
3. **门禁**：`go build ./... && go vet ./... && go test ./...` 全绿 + 交叉编译通过 + `node --check` 全部前端文件通过。
4. **范围**：`git diff --name-only` 全部落在第 1 节白名单内。
5. **真机走查**：≥3 种套件向导页面**视觉形态确实不同**且互不影响（ES 角色分层 / bigdata 组件矩阵 / redis 拓扑选择）；至少 ES、bigdata、redis 各跑通一次端到端部署 + 扩缩容。
