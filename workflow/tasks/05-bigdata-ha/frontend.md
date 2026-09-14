# 任务 05 · 大数据底座全组件高可用 — frontend.md（前端执行）

> 设计依据：`docs/大数据底座高可用设计.md` §三（前端修改逻辑）。
> 范围：`template/static/pages/stacks-forms.js`、`stacks.js`、`style.css`。
> 非HA 模式交互必须与现状零差异（回归红线）。

## F1 套件卡片 HA 开关（stacks-forms.js）

- [ ] bigdata 套件卡片（`HaSupport`）头部右侧渲染「高可用」`el-switch`；开启显示「HA」角标
- [ ] 开关联动：未勾选 zookeeper → 自动勾选 + toast；组件区提示条（双实例说明 + 最低 3 台）；主机数不足时「下一步」禁用
- [ ] 开关可回退，切换后组件勾选与提示实时刷新

验收：`node --check` 通过；浏览器联动行为与设计文档 §3.1 一致，控制台零报错。

## F2 bool 变量与辅助变量渲染（stacks-forms.js）

- [ ] `Type=bool` 变量渲染 `el-switch`（`true`/`false` 字符串落 params）
- [ ] `hdfs_nameservice`/`jn_*`/`hive_db_*` 仅 `ha=true` 时显示

验收：`node --check` 通过；params 提交值为 `"true"/"false"` 字符串。

## F3 角色矩阵重构（stacks.js + style.css）

- [ ] 非 HA：角色规划 UI 保持现状零改动
- [ ] HA：按已选组件分组渲染「组件角色卡片」，角色行见设计文档 §3.2 表（HDFS NN1/NN2/JN×3、YARN RM1/2、Spark M1/2、Flink JM1/2、HBase HM1/2、Hive MS×2/HS2×2/DB、ZK 1/2/3）
- [ ] 每角色行主机下拉：默认「自动分配」，主角色行保留「跟随主节点」；组件未勾选不渲染其角色卡
- [ ] 同组件角色落点互斥：冲突行即时标红，提交前拦截
- [ ] masters JSON 组装扩展（全部从角色 key），随 params 持久化
- [ ] style.css：分组卡片网格（组件卡 + 角色行 + 冲突标红），复用现有 `.stack-role-plan/.stack-role-grid` 基础样式扩展

验收：`node --check` 通过；矩阵渲染/冲突标红/提交参数正确；非 HA 模式与现状零差异。

## F4 实例展示（stacks.js）

- [ ] `ha=true` 实例卡片显示「HA」徽标（状态胶囊左侧）
- [ ] 抽屉成员区追加角色标签（NN1·Active / NN2·Standby / JN / RM2 等，数据来自服务登记）

验收：`node --check` 通过；标签渲染正确，无溢出换行问题。

## F5 非 HA 全回归

- [ ] 关闭开关走完 新建→部署→探活→抽屉→卸载 全流程

验收：控制台零报错；交互与现状一致，无多余元素。

## 进度记录

（执行中追加：踩坑与决策）
