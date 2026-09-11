# 任务 06 · Elasticsearch 套件冷热温双模式 — frontend.md（前端执行）

> 设计依据：`docs/es-suite-cold-warm-hot.md` §2/§3/§5/§8（模式选择、角色分配、参数、结果展示）。
> 范围：`template/static/pages/stacks-forms.js`、`stacks.js`、`style.css`。
> 「集群」模式交互必须与现状零差异（回归红线）。

## F1 模式选择（stacks-forms.js）

- [ ] 向导第一步 el-radio 呈现「集群」「冷热温」两模式，与 Redis 模式选择同一交互；选中后动态渲染下一步
- [ ] 冷热温模式主机数不足（<4）时「下一步」禁用并给提示

验收：`node --check` 通过；模式切换即时生效，控制台零报错。

## F2 角色分配（stacks-forms.js + style.css）

- [ ] 主机选择区每台呈现角色多选 checkbox：master / 纯协调 coordinating / 数据-hot / 数据-warm / 数据-cold
- [ ] master 与数据层互斥置灰；纯协调默认勾选 2 台；热节点每台唯一隐式保证
- [ ] 角色行冲突/不足即时标红并在提交前拦截；masters JSON 按角色 key 组装随 params 持久化
- [ ] 数据层多选（同机 hot+warm+cold 叠加）给出 I/O 冲突提示，默认建议仅单选数据层

验收：`node --check` 通过；角色组合非法被拦截、参数提交正确；非冷热温模式与现状零差异。

## F3 参数配置（stacks-forms.js）

- [ ] 冷热温模式展示 SSL 开关（`ssl_enabled`，bool 渲染 el-switch）、`coordinator_count`、port、`cluster_name`
- [ ] JVM 配置：每台 `heap_xms/heap_xmx` + `jvm_opts`（文本域，默认 `-XX:+UseG1GC`）

验收：`node --check` 通过；params 提交值 `"true"/"false"` 字符串正确拼装。

## F4 结果展示（stacks.js + style.css）

- [ ] 冷热温实例卡片展示模式徽标 + tier 徽章（hot/warm/cold）；抽屉成员区追加以角色标签（master / coordinating / data-hot 等）
- [ ] 接入地址按 tier 分组展示；SSL 开启时提供 CA 下载
- [ ] 集群健康状态展示

验收：`node --check` 通过；徽标/角色标签/接入地址渲染正确，无溢出换行；集群模式显示无回归。

## F5 双模式全回归

- [ ] 「集群」模式走通 新建→部署→探活→抽屉→卸载，与现状零差异（仅镜像变化）

验收：控制台零报错；无多余元素；回归无异常。

## 进度记录

（执行中追加：踩坑与决策）