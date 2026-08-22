# 任务 02 · 批量新增主机 — boundary.md（边界）

> 本文件限定执行本任务时**允许修改的文件范围与操作边界**。
> 清单之外的任何文件一律只读；确需越界时，停止并上报，不得自行修改。

## 1. 允许修改的文件（白名单）

```
api/host_batch.go               # 新增：IP 解析器 + 批量 handler（≤300 行）
api/host_batch_test.go          # 新增：解析器单测
api/host.go                     # 仅在需要复用测试/命名函数处小幅调整
store/host_repo.go              # 新增批量查询/创建所需 repo 方法
model/host.go                   # 仅在需要批量结果结构处追加
template/static/pages/hosts.js  # 批量新增弹窗 + 结果弹窗
template/static/style.css       # 仅追加新样式（走 CSS 变量）
```

## 2. 禁止修改的文件（黑名单 · 只读）

```
common/sshx/**、common/crypto/**   # 只复用，不改通道与加密逻辑
store/migrations.go                # 本任务无 schema 变更，禁止加迁移
config/**、router/**               # 路由若需注册新接口，仅 router.go 追加
                                   # 一行——确需时先上报确认
docs/**、workflow/**、boundaries/**
README.md / .gitignore / script/**
```

## 3. 接口与数据边界

1. 只实现 `docs/04-api.md` 已登记的 POST /hosts/batch 契约；
   字段、错误码（3001/3002）、审计动作不得自行增改。
2. 数据库仅使用 hosts 既有表结构（name UNIQUE、status 枚举），
   不新增表、不加列、不写迁移。
3. 凭据读取必须经既有解密链路，明文不出 api 层、不进日志。

## 4. 技术边界

1. 并发测试用 goroutine + 信号量实现，上限 5；禁止引入第三方任务库。
2. 前端不引入新依赖、不新增外网请求；解析预览为本地轻量实现，
   不得替代服务端校验。
3. 遵守 rules.md：单文件 ≤300 行、占位符 SQL、日志脱敏。

## 5. 验收边界

1. `go build`、`go vet`、`go test ./api/...` 全绿。
2. `git diff --name-only` 全部落在第 1 节白名单内。
3. 联调用真实测试机（standby-01 及允许的测试节点），禁止对生产
   K8s 节点做任何写操作（rules.md 红线）。
