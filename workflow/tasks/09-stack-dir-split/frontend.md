# 任务 09 · 套件目录化拆分 — frontend.md（前端执行）

> 设计依据：`docs/套件目录化拆分设计.md` §4.2（前端目录树）/ §5.4（加载与样式隔离）/ §6（逐套件清单）。
> 已拍板：② 前端**静态引入**（方案 A，`index.html` 分组 `<script>` / `<link>`，不引入运行时按需注入）。
> 原则：**不改渲染结果，只改文件归属**；每步完成 `node --check` 全绿 + 向导 UI 无肉眼变化。
> 阶段映射：F1=P5，F2–F6=P6，F7=P7（前端部分）。参照范式：ES 控制台 9 个 `es_*.js`（已自洽，**明确不动**）。

## F1（P5）抽 common：`stacks.js` 骨架化 ✅ 2026-09-14

- [x] 新建 `template/static/stacks/common/`：`wizard.js`（向导骨架 step1/2/3、主机表、提交流程）、`host-table.js`（勾选/排序/全选）、`role-plan.js`（角色计划 chips 双维配色）、`instance-card.js`（实例卡片 + 操作菜单）、`verify-dialog.js`（校验对话框骨架，数据由套件 hooks 提供）、`assets.js`（前端资源登记）、`common.css`
- [x] `template/static/pages/stacks.js` 降为**挂载入口与路由**（22 行 ≤120）
- [x] 通用样式从 `style.css` 抽到 `common.css`，类名统一 `.stk-*`（wizard / host-table / role-plan / instance-card）
- [x] `index.html` 引入 `common/` 分组（此时 `stacks/<key>/` 尚未建立，仅通用骨架）

验收：✅ `node --check` 全绿；`stacks.js` 22 行 ≤120；向导 UI **无肉眼变化**（模板字符串逐字节等价，见 F7 冒烟断言）。

> **落地偏差**：通用骨架拆得比条目更细 —— 除 `wizard/host-table/role-plan/instance-card/verify-dialog/assets/common.css`
> 外，另分出 `labels.js`（标签与文案表）、`parts.js`（小组件）、`run-log.js`（运行日志）、
> `tpl-*.js` 6 个（hero / instance / preflight / run / verify / wizard 模板）与 `verify.css`
> （探活对话框骨架样式，使 `common.css` 从 562 行降到 409 行）。

## F2（P6-1）套件注册表与静态引入骨架 ✅ 2026-09-14

- [x] 新建 `template/static/stacks/registry.js`：`window.StackDrivers`（由 `window.StackForms` 升级），每套件三槽 `{ hooks, components, hints }`
- [x] `index.html` 按套件分组注释重组（通用骨架 → 9 套件 → 挂载壳）
- [x] 启动冒烟断言：registry 内 9 个套件 key 齐全，缺一即 `console.error`（`window.StacksRegistryReady()`）

验收：✅ `node --check` 全绿；页面无白屏；控制台零报错；9 个 key 全注册（`script/_fe_smoke.js` 断言齐备）。

> **落地偏差**：`index.html` 由 43 行增至 113 行 —— 方案 A「静态引入 + 分组注释」的必然结果
> （每套件 2 行 script/link + 分组注释）。这是**设计预期**，已重录基线留档。

## F3（P6-2）elasticsearch 套件前端迁移 ✅ 2026-09-14

- [x] 新建 `template/static/stacks/elasticsearch/{index.js,roles.js,cluster.js,verify.js,style.css}`
- [x] 迁入 `stacks.js` 的 `wizardHint` 冷热温分支、`esVerifyHostRole` / `verifyHostRows` ES 分支、`esRoleLabel` / `isEsCwh` / `hostRolesTags` / `instTierTags` / `esMemberRoles`
- [x] 迁入 `stacks-forms.js` 的 `StackFormElasticsearchRoles` + `haConflictList` helper
- [x] 样式前缀化：stacks 侧 `.es-*` → `.stk-es-*`（**ES 控制台的 9 个 `es_*.js` 用的 `.es-*` 不动**）

验收：✅ `node --check` 全绿；ES「集群」与「冷热温」双模式向导全流程无肉眼变化；抽屉/校验展示一致（模板逐字节等价）。

## F4（P6-3）bigdata 套件前端迁移 ✅ 2026-09-14

- [x] 新建 `template/static/stacks/bigdata/{index.js,select.js,op.js,roles.js,verify.js,style.css}`
- [x] 迁入 `stacks.js` 的 `isBigdataVerify` / `COMP_LABELS` / `verifyComponents` / `verifyComp*`
- [x] 迁入 `stacks-forms.js` 的 `StackFormBigdataSelect` / `StackFormBigdataOp` / `StackFormBigdataRoles`
- [x] 样式前缀化 `.stk-bd-*`；HA 全角色矩阵与组件依赖联动保持原行为

验收：✅ `node --check` 全绿；bigdata 向导（含 HA 开关 → 角色矩阵）无肉眼变化。

> **落地偏差**：多落一个 `verify.js`（`verify.js` 承接 `isBigdataVerify` / `verifyComponents` 系列，
> `roles.js` 只放 HA 角色矩阵），目的是让「探活呈现」与「角色矩阵」两个变更源分开。

## F5（P6-4）redis 套件前端迁移 ✅ 2026-09-14

- [x] 新建 `template/static/stacks/redis/{index.js,topo.js,hooks.js,style.css}`
- [x] 迁入 `stacks-forms.js` 的 `StackFormRedisTopo`
- [x] 样式前缀化 `.stk-redis-*`；主从/哨兵/集群三模式的拓扑选择保持原行为

验收：✅ `node --check` 全绿；redis 三模式向导无肉眼变化。

> **落地偏差**：多落一个 `hooks.js`（向导 hint 与实例卡片附加信息），与 `topo.js`（拓扑选择控件）分文件。

## F6（P6-5）其余 6 套件前端迁移 ✅ 2026-09-14

- [x] `template/static/stacks/{elfk,kafka,rabbitmq,rocketmq,nacos,powerjob}/{index.js,style.css}`（同构：注册 `window.StackDrivers['<key>'] = { hooks, components, hints }`）
- [x] `formEntry` → `window.StackForms[key]` 机制升级为 `StackDrivers`，各套件 `hints`（如 ELFK 的 Logstash 落点提示）下沉到各自 `index.js`
- [x] 清理 `stacks-forms.js`（组件全部迁出后删除）

验收：✅ `node --check` 全绿；6 个套件向导无肉眼变化；`stacks-forms.js` **已删除**，无残留套件专属代码。

## F7（P7）前端收尾与真机走查 ▶ 静态项完成 / 真机项待人工

- [x] `style.css` 瘦身核对：无 `.stack-*` / stacks 侧 `.es-*` 套件专属规则残留（2333 → 1934 行）
- [x] 样式命名空间核对：通用 `.stk-*` / 套件 `.stk-<key简写>-*` 全部就位，无跨套件前缀混用
- [x] 全部新增/修改前端文件 `node --check` 通过；单文件 ≤500 行；`stacks.js` ≤120 行
- [ ] 真机走查（standby-01）：≥3 种套件向导**视觉形态确实不同且互不影响**（ES 角色分层、bigdata 组件矩阵、redis 拓扑选择），9 个套件向导各打开一次无报错
- [ ] 联调：ES / bigdata / redis 至少各跑通一次「选主机 → 选角色 → 部署 → 看结果」

验收：设计文档 §10「结构类 4、5」达标（结构项全绿）；**体验类 9（真机走查）未执行**，须人工在测试机发起。

> **行数目标的修订**：原定「`style.css` 降至 ~1200 行」已按设计文档 §10.4 改为**语义检查**
> —— boundary.md 只允许从 `style.css` 迁出 `.stack-*` 与 stacks 侧 `.es-*` 套件专属规则，
> 全部迁出后残量（1934 行）由其它页面样式决定，在「不越界」前提下不可再降。
> 硬门禁改为「套件专属选择器 0 处」，由 `script/check_stack_lines.py --targets` 校验。

## 进度记录

（执行中记录：组件注册时序问题、样式前缀冲突点、各期 commit 号、真机走查截图比对结论。）

- **组件注册时序（F1/F2 的核心风险）**：原 `stacks-forms.js` 依赖「先加载 forms、后加载页面」的隐式时序。
  拆分后改为**组件自注册**：每个套件独有组件文件在文件末尾 `P.register(key, {components: {...}})`，
  `elasticsearch/roles.js`、`bigdata/{select,op,roles}.js`、`redis/topo.js` 各自负责登记自己的局部组件，
  从而与 `<script>` 顺序解耦（`index.html` 只需保证「套件文件在挂载壳之前」）。
- **样式前缀冲突点**：stacks 侧旧类名 `.es-*` 与 ES 控制台的 `.es-*` 同名。处理方式：stacks 侧
  一律前缀化为 `.stk-es-*`，控制台不动；`style.css` 的语义检查用「精确名单」排除控制台的 `.es-*`，
  避免误报（见 `check_stack_lines.py` 的 `STYLE_STACK_RE`）。
- **模板字节等价**：`_fe_smoke.js` 在 Node 沙箱里按 `index.html` 顺序求值全部脚本（跳过 vendor 与 app.js），
  断言 `page.template` 与 `script/_expected_template.txt`（39422 字符）逐字节一致 —— 这是「前端目录化
  不改渲染结果」最硬的一条断言，比「肉眼无变化」更可靠。
- **真机走查未执行**：本机 8090 只做了服务启动与接口冒烟；standby-01 的 9 套件向导走查与
  ES/bigdata/redis 端到端部署**尚未在真机发起**（真机部署须人工确认后由页面发起，见 boundary §4.7）。
