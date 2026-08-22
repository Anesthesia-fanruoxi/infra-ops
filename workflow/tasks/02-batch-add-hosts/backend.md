# 任务 02 · 批量新增主机 — backend.md（后端执行）

> 接口契约以 `docs/04-api.md` POST /hosts/batch 为准（已登记，含错误码
> 3001/3002 与审计动作 host.batch_create）。分层遵守 rules.md §2：
> 业务逻辑在 api 层，SSH 走 common/sshx，存取走 store。

## 1. 前置依赖

- 主机 CRUD、凭据解密、SSH test/采集能力已就绪（单机新增与
  POST /hosts/{id}/test 的既有实现），本任务**复用**不重写。

## 2. IP 范围解析器（api/host_batch.go + 单测）

纯函数 `parseIPList(raw string) ([]string, error)`：

1. 分隔：换行 / 逗号 / 分号 / 空格，逐项 trim，丢弃空项。
2. 单项语法：
   - `a.b.c.d`：IPv4 校验（每段 0-255）。
   - `a.b.c.d-e`：简写范围，e 为末段终值，须 ≥ 起始末段且 ≤254。
   - `a.b.c.d-w.x.y.z`：完整范围，前三段必须一致，终值 ≥ 起值。
3. 展开后按出现顺序去重；任一非法项 → 返回 error（指明首个非法项，
   handler 映射 3001）；展开总数 > 100 → error（映射 3002）。
4. 单测必须覆盖：三类语法、混合输入、四种分隔符、重复去重、
   非法格式（字母/越界/缺段）、范围倒挂、超上限。

## 3. 批量创建流程（api 层 handler）

```
参数绑定校验（credential_id 存在性、role 枚举、port 1-65535）
→ parseIPList（失败整体拒绝，不落任何库）
→ 查库内已有 IP 集合 → 命中标记 skipped_exists
→ 逐台创建（status=unverified，name 先占位 IP）
→ auto_test 时并发测试（goroutine + channel 信号量，上限 5）
→ 汇总 results 返回
```

1. 创建与单机 POST /hosts 共用 store 写入路径，保证审计/字段一致。
2. 单台测试失败**不阻塞**整批：错误码记入该条 results，整体 HTTP 200。
3. 测试复用单机 test 的 sshx 调用与 host_keys TOFU 策略，禁止另起通道。

## 4. 自动命名策略

1. 连通：name = 采集 info.hostname（trim 后）；hostname 为空 → 回退 IP。
2. 冲突检测范围 = 库内已有名称 ∪ 本批已分配名称；冲突时追加 `-2`、`-3`
   递增直至唯一（如 k8s-node、k8s-node-2）。
3. 不连通或 auto_test=false：name = IP。
4. 名称冲突处理必须在 UPDATE 落库前完成，避免触发 UNIQUE 约束报错。

## 5. 审计与日志

1. 审计动作 `host.batch_create`，detail 记 total/created/skipped 与凭据 id
   （脱敏），由审计中间件统一落库（rules.md §4.5）。
2. 每台测试失败按 sshx 错误类型打 INFO/WARN，日志禁止出现凭据内容
   （rules.md §3.2）。

## 6. 验收自检

- [ ] `go build` 与 `go vet` 通过；解析器单测全绿
- [ ] 重复 IP、非法 IP、超上限、范围倒挂四类入参行为符合 04-api 定义
- [ ] 并发上限 5 可验证（如注入慢 mock 观察同时在飞数量）
- [ ] 单台 SSH 失败不影响其余条目入库与结果汇总
- [ ] 审计表出现 host.batch_create 记录且无敏感明文

## 7. 进度记录

- 2026-08-21：任务立项，backend/frontend/boundary 三件套编写完成，待确认后执行。
