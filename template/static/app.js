const { createApp, ref, computed } = Vue
const { ElMessage, ElMessageBox } = ElementPlus

// axios 封装（桌面单机模式，接口无鉴权）
const api = axios.create({ baseURL: '/api', withCredentials: true, timeout: 15000 })
api.interceptors.response.use(
  res => res.data,
  err => {
    // 用户主动中断的请求（如目录生成点「停止」走 AbortController）不是故障：
    // 静默交给调用方收尾，否则会弹一条「canceled」的红色错误通知
    if (err && (err.code === 'ERR_CANCELED' || err.name === 'CanceledError' || err.name === 'AbortError')) return Promise.reject(err)
    // 错误提示走统一出口（error_notice.js）：不自动消失、带方法与路径，
    // 桌面模式打不开 F12，3 秒 toast 等于没报。
    const msg = err.response?.data?.message || err.message || '请求失败'
    if (window.ErrorNotice) window.ErrorNotice.notifyApiError(err)
    else ElMessage.error(msg)
    return Promise.reject(err)
  }
)
window.api = api
// 未捕获异常 / 未处理 Promise 拒绝同样要跳出来，否则界面只表现为「点了没反应」
if (window.ErrorNotice) window.ErrorNotice.attachGlobalHooks()

// 主机排序比较器：主机名字典序（忽略大小写）；IP 按数值八位组比较，非法段排最后
window.cmpHostName = (a, b) => {
  const x = String(a?.name || '').toLowerCase(), y = String(b?.name || '').toLowerCase()
  if (x === y) return (a?.id || 0) - (b?.id || 0)
  return x < y ? -1 : 1
}
window.cmpHostIP = (a, b) => {
  const pa = String(a?.ip || '').trim().split('.')
  const pb = String(b?.ip || '').trim().split('.')
  const octet = s => (/^\d+$/.test(s) && +s < 256) ? +s : 999
  for (let i = 0; i < 4; i++) {
    const d = octet(pa[i] ?? '') - octet(pb[i] ?? '')
    if (d) return d
  }
  return (a?.id || 0) - (b?.id || 0)
}

// 图标库
const ICONS = {
  overview: '<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/></svg>',
  hosts: '<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="4" width="20" height="6" rx="2"/><rect x="2" y="14" width="20" height="6" rx="2"/><line x1="6" y1="7" x2="6.01" y2="7"/><line x1="6" y1="17" x2="6.01" y2="17"/></svg>',
  credentials: '<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>',
  audit: '<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><line x1="16" y1="13" x2="8" y2="13"/><line x1="16" y1="17" x2="8" y2="17"/></svg>',
  templates: '<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="8" y1="13" x2="16" y2="13"/><line x1="8" y1="17" x2="16" y2="17"/></svg>',
  deploy: '<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="16 16 12 12 8 16"/><line x1="12" y1="12" x2="12" y2="21"/><path d="M20.39 18.39A5 5 0 0 0 18 9h-1.26A8 8 0 1 0 3 16.3"/></svg>',
  stacks: '<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="12 2 2 7 12 12 22 7 12 2"/><polyline points="2 17 12 22 22 17"/><polyline points="2 12 12 17 22 12"/></svg>',
  registry: '<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2c2 1.5 3.5 3.5 4 6h-8c.5-2.5 2-4.5 4-6z"/><rect x="3" y="18" width="18" height="3" rx="1"/><path d="M5 9h14c.5 3-1 7-5 8.5h-4C6 16 4.5 12 5 9z"/></svg>',
  tools: '<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/></svg>',
  es: '<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="7"/><line x1="21" y1="21" x2="16.2" y2="16.2"/><line x1="8" y1="11" x2="14" y2="11" stroke-opacity=".7"/></svg>',
  sftp: '<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/><path d="M12 11v6"/><path d="m9 14 3 3 3-3"/></svg>',
  mysql: '<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><ellipse cx="12" cy="5" rx="8" ry="3"/><path d="M4 5v14c0 1.7 3.6 3 8 3s8-1.3 8-3V5"/><path d="M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3"/></svg>',
  redis: '<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/></svg>',
  metrics: '<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 12 7 12 10 5 14 19 17 12 21 12"/></svg>',
  settings: '<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>'
}

const routes = {
  overview: { title: '主机概览', sub: '运行状态一览' },
  hosts: { title: '主机管理', sub: '纳管服务器凭据与连接' },
  credentials: { title: '凭据管理', sub: 'SSH 私钥与密码加密托管' },
  audit: { title: '审计日志', sub: '操作日志与 AI 调用台账' },
  templates: { title: '部署模板', sub: '管理部署脚本与变量' },
  deploy: { title: '基础建设', sub: '批量部署与实时监控' },
  orchestrations: { title: '任务编排', sub: '多模板顺序编排执行' },
  stacks: { title: '套件部署', sub: '集群实例 · 扩容 / 缩容 / 加装 / 卸载' },
  registry: { title: '镜像仓库', sub: '连接 Docker Registry · 镜像 / 标签管理' },
  es: { title: 'Elasticsearch', sub: '集群概览 · 索引管理 · 文档查询' },
  sftp: { title: 'SFTP', sub: '远程文件浏览 · 上传 · 下载' },
  mysql: { title: 'MySQL', sub: '库表浏览 · SQL 查询 · 结果导出' },
  redis: { title: 'Redis', sub: 'Key 浏览 · 结尾模糊匹配 · 值预览' },
  metrics: { title: '监控查询', sub: 'PromQL 查询 · AI 生成与解读 · 趋势绘图' },
  settings: { title: '设置', sub: 'AI 接入 · 系统信息 · 数据表' }
}
// 页面 id → 标题的映射对全局开放：统一 AI 调用记录组件（ai_records.js）按 menu
// 存页面 id，展示名必须与路由表同源，避免两处维护、改名后对不上
window.INFRA_ROUTES = routes

const app = createApp({
  template: `
    <app-layout :current-page="activePage" :tabs="openTabs" :version="version"
      @nav="navigate" @close-tab="closeTab" @refresh-tab="refreshTab">
      <div v-for="t in openTabs" :key="t.name + '#' + (pageVers[t.name]||0)" v-show="t.name===activePage" class="page-fade">
        <component :is="'page-' + t.name" :page="t.name" :version-data="versionData" @navigate="navigate" />
      </div>
    </app-layout>
  `,
  setup() {
    const activePage = ref('overview')
    const openTabs = ref([{ name: 'overview' }])
    const pageVers = ref({}) // 每个标签页的重挂载计数：+1 即强制该页重新加载
    const version = ref('')
    const versionData = ref(null)

    const ensureTab = (page) => {
      if (!openTabs.value.some(t => t.name === page)) openTabs.value.push({ name: page })
    }
    const closeTab = (name) => {
      const idx = openTabs.value.findIndex(t => t.name === name)
      if (idx < 0) return
      openTabs.value.splice(idx, 1)
      if (!openTabs.value.length) openTabs.value.push({ name: 'overview' })
      if (activePage.value === name) {
        const next = openTabs.value[Math.min(idx, openTabs.value.length - 1)]
        activePage.value = next.name
        location.hash = '#/' + next.name
      }
    }
    // 刷新 = 该页组件整体重建（数据重拉、SSE 自动重连）
    const refreshTab = (name) => {
      pageVers.value = { ...pageVers.value, [name]: (pageVers.value[name] || 0) + 1 }
    }

    const loadVersion = async () => {
      try { const res = await api.get('/version'); if (res.code === 0) { version.value = res.data.version; versionData.value = res.data } } catch (e) {}
    }
    const handleRoute = () => {
      const hash = location.hash.slice(2) || 'overview'
      if (routes[hash]) { ensureTab(hash); activePage.value = hash }
    }
    const navigate = (page) => {
      ensureTab(page)
      location.hash = '#/' + page
    }

    return { activePage, openTabs, pageVers, ensureTab, closeTab, refreshTab, version, versionData, handleRoute, navigate, loadVersion, ICONS }
  }
})

// 全局中文语言包（vendor/element-plus-zh-cn.min.js）：日期选择器/分页等组件文案走中文
app.use(ElementPlus, { locale: ElementPlusLocaleZhCn })
// 注册全部图标为全局组件（支持 prefix-icon="Lock" 等字符串引用）
for (const [name, comp] of Object.entries(ElementPlusIconsVue)) {
  app.component(name, comp)
}
app.config.globalProperties.$message = ElMessage
app.config.globalProperties.$confirm = (message, title, options) => ElMessageBox.confirm(message, title, options)

// 全局组件：空态
app.component('empty-state', {
  props: { text: { type: String, default: '暂无数据' } },
  template: '<div class="empty-state"><p>{{text}}</p></div>'
})

// 全局组件：统一 AI 调用记录（审计页 AI 标签页与各工具的调用记录入口共用，
// 定义于 pages/ai_records.js，全局注册后工具页面模板可直接以 <ai-records> 引用）
app.component('ai-records', window.AIRecords)

// 布局
app.component('app-layout', {
  props: ['currentPage', 'tabs', 'version'],
  template: `
<div class="layout">
  <div class="sidebar">
    <div class="sidebar-logo"><span class="logo-mark">i</span><span>infra-ops</span></div>
    <div class="sidebar-nav">
      <div class="nav-group">运行管理</div>
      <div class="nav-item" :class="{active:currentPage==='overview'}" @click="$emit('nav','overview')"><span class="nav-icon">` + ICONS.overview + `</span>总览</div>
      <div class="nav-item" :class="{active:currentPage==='hosts'}" @click="$emit('nav','hosts')"><span class="nav-icon">` + ICONS.hosts + `</span>主机管理</div>
      <div class="nav-group">部署中心</div>
      <div class="nav-item" :class="{active:currentPage==='templates'}" @click="$emit('nav','templates')"><span class="nav-icon">` + ICONS.templates + `</span>部署模板</div>
      <div class="nav-item" :class="{active:currentPage==='deploy'}" @click="$emit('nav','deploy')"><span class="nav-icon">` + ICONS.deploy + `</span>基础建设</div>
      <div class="nav-item" :class="{active:currentPage==='orchestrations'}" @click="$emit('nav','orchestrations')"><span class="nav-icon">` + ICONS.deploy + `</span>任务编排</div>
      <div class="nav-item" :class="{active:currentPage==='stacks'}" @click="$emit('nav','stacks')"><span class="nav-icon">` + ICONS.stacks + `</span>套件部署</div>
      <div class="nav-group">工具</div>
      <div class="nav-item" :class="{active:currentPage==='registry'}" @click="$emit('nav','registry')"><span class="nav-icon">` + ICONS.registry + `</span>镜像仓库</div>
      <div class="nav-item" :class="{active:currentPage==='es'}" @click="$emit('nav','es')"><span class="nav-icon">` + ICONS.es + `</span>Elasticsearch</div>
      <div class="nav-item" :class="{active:currentPage==='sftp'}" @click="$emit('nav','sftp')"><span class="nav-icon">` + ICONS.sftp + `</span>SFTP</div>
      <div class="nav-item" :class="{active:currentPage==='mysql'}" @click="$emit('nav','mysql')"><span class="nav-icon">` + ICONS.mysql + `</span>MySQL</div>
      <div class="nav-item" :class="{active:currentPage==='redis'}" @click="$emit('nav','redis')"><span class="nav-icon">` + ICONS.redis + `</span>Redis</div>
      <div class="nav-item" :class="{active:currentPage==='metrics'}" @click="$emit('nav','metrics')"><span class="nav-icon">` + ICONS.metrics + `</span>监控查询</div>
      <div class="nav-group">安全审计</div>
      <div class="nav-item" :class="{active:currentPage==='credentials'}" @click="$emit('nav','credentials')"><span class="nav-icon">` + ICONS.credentials + `</span>凭据管理</div>
      <div class="nav-item" :class="{active:currentPage==='audit'}" @click="$emit('nav','audit')"><span class="nav-icon">` + ICONS.audit + `</span>审计日志</div>
      <div class="nav-group">系统</div>
      <div class="nav-item" :class="{active:currentPage==='settings'}" @click="$emit('nav','settings')"><span class="nav-icon">` + ICONS.settings + `</span>设置</div>
    </div>
    <div class="sidebar-footer">v{{version || '—'}}</div>
  </div>
  <div class="main-area">
    <div class="header" @dblclick="winDblClick">
      <div style="display:flex;align-items:baseline"><span class="header-title">{{pageTitle}}</span><span class="header-sub">{{pageSub}}</span></div>
      <!-- 无边框窗口的自绘控制（仅桌面模式；浏览器调试隐藏）。最大化/还原两枚图标
           的互斥显示由 CSS 按 html[data-win-maximised] 切换（wails-shim.js 维护该属性） -->
      <div class="win-ctls" v-if="winDesktop">
        <button class="win-ctl" title="最小化" @click="winMin"><svg viewBox="0 0 12 12" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.1"><path d="M2.5 6h7"/></svg></button>
        <button class="win-ctl" title="最大化 / 还原" @click="winMax">
          <svg class="win-ico-max" viewBox="0 0 12 12" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.1"><rect x="2.5" y="2.5" width="7" height="7" rx="1.2"/></svg>
          <svg class="win-ico-restore" viewBox="0 0 12 12" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.1"><path d="M4.4 2.1h3.7c1 0 1.8.8 1.8 1.8v3.7"/><rect x="2.1" y="4.4" width="5.5" height="5.5" rx="1.2"/></svg>
        </button>
        <button class="win-ctl win-ctl-close" title="关闭窗口" @click="winClose"><svg viewBox="0 0 12 12" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.1"><path d="M3 3l6 6M9 3l-6 6"/></svg></button>
      </div>
    </div>
    <div class="tab-bar">
      <div v-for="t in tabs" :key="t.name" class="tab-item" :class="{active:t.name===currentPage}" @click="$emit('nav',t.name)">
        <span class="tab-label">{{tabTitle(t.name)}}</span>
        <span class="tab-actions">
          <el-icon class="tab-btn" title="重新加载本页" @click.stop="$emit('refresh-tab',t.name)"><Refresh /></el-icon>
          <el-icon class="tab-btn" title="关闭标签页" @click.stop="$emit('close-tab',t.name)"><Close /></el-icon>
        </span>
      </div>
    </div>
    <div class="content"><slot></slot></div>
  </div>
</div>`,
  emits: ['nav', 'close-tab', 'refresh-tab'],
  computed: {
    pageTitle() { return routes[this.currentPage]?.title || '' },
    pageSub() { return routes[this.currentPage]?.sub || '' },
    // 仅桌面窗口（Wails）渲染自绘窗口控制按钮；浏览器调试不显示
    winDesktop() { return !!window.__INFRA_DESKTOP__ }
  },
  methods: {
    tabTitle(name) { return routes[name]?.title || name }, // routes 是闭包变量，模板内不可直接访问
    // 窗口控制桥在 wails-shim.js（window.__INFRA_WIN__，运行时未就绪时为 undefined）
    winMin() { if (window.__INFRA_WIN__) window.__INFRA_WIN__.minimise() },
    winMax() { if (window.__INFRA_WIN__) window.__INFRA_WIN__.toggleMaximise() },
    winClose() { if (window.__INFRA_WIN__) window.__INFRA_WIN__.close() },
    // 双击顶栏 = 最大化/还原（仿原生标题栏；点按控制按钮时不触发）
    winDblClick(e) { if (!this.winDesktop || (e.target.closest && e.target.closest('.win-ctls'))) return; this.winMax() }
  }
})

// 页面
app.component('page-overview', window.OverviewPage)
app.component('page-hosts', window.HostsPage)
app.component('page-credentials', window.CredentialsPage)
app.component('page-audit', window.AuditPage)
app.component('page-templates', window.TemplatesPage)
app.component('page-deploy', window.DeployPage)
app.component('page-orchestrations', window.OrchestrationsPage)
app.component('page-stacks', window.StacksPage)
// 套件表单子组件（差异化表单插槽，定义于 stacks-forms.js）
app.component('stack-form-bigdata-select', window.StackFormBigdataSelect)
app.component('stack-form-bigdata-op', window.StackFormBigdataOp)
app.component('stack-form-bigdata-roles', window.StackFormBigdataRoles)
app.component('stack-form-redis-topo', window.StackFormRedisTopo)
app.component('stack-form-elasticsearch-roles', window.StackFormElasticsearchRoles)
app.component('page-registry', window.RegistryPage)
app.component('page-es', window.ESPage)
app.component('page-sftp', window.SFTPPage)
// MySQL 工具：子组件（树 / 结果网格）先于页面注册，页面里以 components 引用
app.component('page-mysql', window.MySQLPage)
// Redis 工具：key 浏览器与值预览都是子组件，由 RedisPage 自行引用
app.component('page-redis', window.RedisPage)
// 监控查询：图表 / AI 面板由 MetricsPage 自行引用（metrics.js 内部已完成注册依赖）
app.component('page-metrics', window.MetricsPage)
// 设置：三张卡片同样是子组件，由 SettingsPage 自行引用
app.component('page-settings', window.SettingsPage)

const root = app.mount('#app')

window.addEventListener('hashchange', root.handleRoute)
root.handleRoute()
root.loadVersion()