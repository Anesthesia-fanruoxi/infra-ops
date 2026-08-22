# 07 — 前端设计（无构建链 / cdn-vue）

## 1. 技术方式

- Vue3 + Element Plus **全局构建版**，`<script>` 引入，无 npm/构建步骤。
- 依赖文件本地化到 `template/static/vendor/`，随 go:embed 打包（离线可用、不赌 CDN 稳定）。
  - `vue.global.prod.js`（3.4.x）
  - `element-plus`（index.full.min.js + index.css，2.x）
  - `element-plus-zh-cn`（中文 locale）
  - `axios`（1.x）
- 单页应用，hash 路由（`#/login`、`#/overview`…），刷新不依赖服务端路由回退。

## 2. 页面与路由

以下文件路径均相对 `template/static/`：

| 路由 | 文件 | 说明 |
|------|------|------|
| #/login | pages/login.js | 登录表单，成功后跳 overview |
| #/overview | pages/overview.js | 总览卡片（总数/在线/离线/按角色）+ 最近操作 |
| #/hosts | pages/hosts.js | 主机列表（角色/状态筛选、搜索、分页）+ 新增/编辑弹窗 + 批量新增 + 连接测试 + 详情抽屉（资源快照） |
| #/credentials | pages/credentials.js | 凭据列表 + 新增/编辑弹窗（secret 仅写不回读，编辑时留空=不修改） |
| #/audit | pages/audit.js | 操作日志列表（action 筛选、分页） |

## 3. 布局与风格（对齐已确认原型）

- 左侧深色固定侧边栏（Logo + 导航），顶部标题栏，右侧内容区。
- **留白约定（rules.md §6.3，用户明确偏好）**：表格行高 ≥52px、卡片间距 ≥20px、
  页面边距 ≥32px、不拥挤。
- 角色配色沿用原型：Nginx=绿、Harbor=紫、K8s=蓝、其他=灰。
- 状态色：online=绿点、offline=红点、unverified=灰点；负载条按阈值变色
  （<60 蓝、<80 橙、≥80 红）。

## 4. 统一请求封装（app.js）

- axios 实例：baseURL `/api/v1`，withCredentials；响应拦截器统一解包
  `{code,message,data}`：code≠0 → ElMessage.error(message)；401 → 跳 `#/login`。
- 提供 `api.get/post/put/del` 薄封装，页面内不直接裸用 axios。
- 连接测试按钮置 loading，失败按 code（1001/1002/1003/1004）渲染具体原因。

## 5. 交互要点

- 主机新增：先选已有凭据（下拉），无凭据引导先去凭据页创建。
- 连接测试：成功后前端局部刷新该行 status/latency/info，并 ElMessage 成功提示；
  详情抽屉展示 os/kernel/uptime/CPU/内存/磁盘（来自 info）。
- 凭据 secret 输入框：type=password 可切换；编辑态 placeholder 提示"留空则不修改"。
- 删除主机/凭据统一 ElMessageBox.confirm 二次确认；凭据被引用时后端返回 409，
  前端提示"该凭据正被 N 台主机使用"。
- 批量新增主机：主机页"新增主机"旁设"批量新增"按钮；弹窗 = 凭据下拉（本批共享）
  + IP 文本域（单 IP / 范围语法 172.16.1.11-20 / 混合录入，placeholder 给示例）
  + 角色/端口/备注；输入时前端实时解析预览（去重后 N 个、与现有重复 M 个将跳过，
  仅提示用，最终以服务端解析为准）；提交后按钮长 loading（超时 5 分钟），
  结果弹窗按 成功/失败/跳过 三组列出每台 IP、自动取得的名称与失败原因。

## 6. 交付边界

- 阶段一前端只覆盖上述 5 个页面；任务执行/实时日志 UI 属阶段二。
- 不做 i18n 框架（固定中文）；不做暗色主题（一期仅亮色）。
