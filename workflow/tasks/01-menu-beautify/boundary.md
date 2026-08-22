# 任务 01 · 菜单页面重新设计 — 边界文档

> 本文件是本任务的硬性边界。白名单之外的文件一律只读；需要越界时必须暂停并向用户说明原因，未经确认不得继续。

## 1. 允许修改的文件

```text
template/index.html
template/static/style.css
template/static/app.js
template/static/pages/login.js
template/static/pages/overview.js
template/static/pages/hosts.js
template/static/pages/credentials.js
template/static/pages/audit.js
```

原则：优先只修改 `template/static/style.css`。只有现有 DOM 结构无法完成设计时，才修改页面 JS 或 `index.html`，并保持 API、路由和数据字段不变。

## 2. 只读文件

```text
template/static/vendor/**
*.go
go.mod
go.sum
config.yaml.example
config/**
common/**
api/**
router/**
store/**
model/**
script/**
README.md
.gitignore
workflow/**
```

本任务执行过程中不得修改 vendor、后端、数据库、项目级文档或其他任务文档。

## 3. 技术边界

- 保持 Vue 3 全局构建版、Element Plus、axios 和现有 hash 路由。
- 不引入 npm、webpack、vite、Tailwind、动画库或新的 UI 框架。
- 不引入 CDN、远程字体、远程图片或任何外部网络资源。
- 不改变 API 地址、请求方法、请求参数、响应字段和鉴权逻辑。
- 不显示密码、私钥、secret 等敏感字段。
- 不重置、覆盖或删除用户已有改动。

## 4. 操作约束

- 开始修改前，必须先输出简短执行计划。
- 计划经用户确认后再开始大范围修改；执行过程中不需要逐步询问确认。
- 按“全局基础样式 → 登录 → 总览 → 主机 → 凭据 → 审计 → 响应式”顺序执行。
- 每完成一个阶段，更新 `frontend.md` 的进度记录并汇报变更。
- 发现超出范围、接口缺失或需求冲突时，暂停并询问用户。
- 不提交代码、不创建分支、不推送，除非用户明确要求。

## 5. 验收与验证约束

允许执行：

```powershell
go build ./...
```

如果 Go 缓存目录权限不足，可以将 `GOCACHE` 指向项目目录下的临时目录；该临时目录不应纳入交付内容。

禁止执行：

```text
go test ./...
go run ...
npm install
npm run dev
npm run build
air
docker compose up
任何服务启动命令
任何自动化测试命令
```

不要求 AI 启动浏览器、打开页面或执行浏览器自动化验证；页面视觉和功能由用户手工验收。

## 6. 交付检查清单

- [ ] 修改文件全部位于第 1 节白名单。
- [ ] vendor、Go、API、数据库和配置文件未改动。
- [ ] 无新增外部资源和依赖。
- [ ] 现有路由、API、鉴权和敏感数据规则保持不变。
- [ ] 仅执行 `go build ./...`，未执行测试或启动服务。
- [ ] 未提交、未创建分支、未推送。
- [ ] 已在 `frontend.md` 记录阶段进度和遗留问题。
