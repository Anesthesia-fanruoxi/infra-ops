# 任务 07 · API 目录重构 — boundary.md（边界）

> 本文件限定执行本任务时**允许修改的文件范围与操作边界**。
> 清单之外的任何文件一律只读；确需越界时，停止并上报，不得自行修改。

## 1. 允许修改的文件（白名单）

```
api/**                    # 全部业务接口文件：拆分子包、大文件拆分、
                          #   新增 api/shared；api/data 为删除项（见 §3）
router/router.go          # 仅更新分包后的 import 路径与构造器调用前缀
docs/api目录重构.md        # 实施中同步修订设计文档
workflow/tasks/07-api-refactor/**   # 本任务执行文档进度记录
workflow/workflow.md                  # 仅勾选本任务步骤 [x]
```

## 2. 禁止修改的文件（黑名单 · 只读）

```
model/**、store/**           # 无 model/schema/蓝图变更，仓库层零改动
common/**、config/**         # 无公共能力/配置变更
script/**                    # 调试脚本与渲染校验脚本不涉本任务
template/**                  # 纯后端重构，前端页面不动
api/data/**                  # 目标为整目录删除，不做增量修改
workflow/tasks/（07 外的其他任务目录）
workflow/boundaries/**、workflow/docs/**
go.mod / go.sum              # 不新增第三方依赖
```

## 3. 接口与数据边界

1. **删除项**：`api/data/` 整目录删除（其内为空库残留；真实 SQLite 库在根 `data/`）。
2. 不新增 / 不修改任何 HTTP 端点，路由路径与请求/响应语义保持不变。
3. 无数据库 schema 变更；不触碰 `store/migrations.go`、`store/db.go`。
4. 不改业务逻辑：本任务仅重排 `package` 边界、移动函数与更新导入路径。
5. 删文件前必须 `rg "api/data"` 确认零引用（基线已核实）。

## 4. 技术边界

1. **包边界约束**：Go 中每个子目录必须是独立 package，拆分子包后同包符号共享、
   跨包符号需导出。迁移时仅调整可见性与导入前缀，不改实现。
2. **共享层收敛**：跨模块辅助（`applyHostVars`/`containsString`/`containsInt64`/
   `mergeStringList`/`filterEmpty`）迁入 `api/shared`；分包时发现的隐藏跨包依赖
   一律下沉 shared 或具名导出后引用，保持 `orchestration→deploy→shared`、`stack→shared`
   单向依赖，杜绝循环导入。
3. **单文件 ≤300 行**（工作流通用规则 §1）；拆完重建工程。
4. 每阶段/每分包一个 git commit（便于 `git revert` 局部回滚）；开工前 `git status`
   确认无未合并外部改动，冲突立即暂停上报。
5. 编译门禁：`go build ./...`、`go vet ./...`、`go test ./...`；交叉编译
   `CGO_ENABLED=0 GOOS=linux GOARCH=amd64` 通过。
6. 遵守 rules.md：不提交密钥凭据、不改 scope 目标外模块。

## 5. 验收边界

1. 静态：`go build`、`go vet`、`go test` 与 B1 基线逐项一致或更优；交叉编译通过。
2. `git diff --name-only` 全部落在第 1 节白名单内。
3. `rg "api/data"` 无命中；`api/` 下按设计文档 §三 的目标结构分包完成。
4. 所有 >300 行的源文件已拆解，无遗漏（大文件清单见 docs/api目录重构.md §五）。
5. 冒烟：服务 8090 启动，登录/主机/部署/编排/套件/工具/总览/SSE 各菜单接口返回正常；
   部署仅替换测试机（standby-01）并保留旧二进制备回退。