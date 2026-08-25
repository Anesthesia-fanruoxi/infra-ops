const { createApp, ref, computed, watch } = Vue
const { ElMessage, ElMessageBox } = ElementPlus

// axios 封装
const api = axios.create({ baseURL: '/api', withCredentials: true, timeout: 15000 })
api.interceptors.response.use(
  res => res.data,
  err => {
    const code = err.response?.status
    const bizCode = err.response?.data?.code
    if (code === 401) { location.hash = '#/login'; return Promise.reject(err) }
    if (code === 403 && bizCode === 4031) {
      window.dispatchEvent(new CustomEvent('force-change-password'))
      return Promise.reject(err)
    }
    const msg = err.response?.data?.message || err.message || '请求失败'
    ElMessage.error(msg)
    return Promise.reject(err)
  }
)
window.api = api

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
  schedules: '<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>'
}

const routes = {
  overview: { title: '主机概览', sub: '运行状态一览' },
  hosts: { title: '主机管理', sub: '纳管服务器凭据与连接' },
  credentials: { title: '凭据管理', sub: 'SSH 私钥与密码加密托管' },
  audit: { title: '操作日志', sub: '关键操作审计追踪' },
  templates: { title: '部署模板', sub: '管理部署脚本与变量' },
  deploy: { title: '基础建设', sub: '批量部署与实时监控' },
  orchestrations: { title: '任务编排', sub: '多模板顺序编排执行' },
  schedules: { title: '定时任务', sub: '周期性自动化执行' }
}

const app = createApp({
  template: `
    <page-login v-if="activePage==='login'" :version="version" />
    <app-layout v-else :current-page="activePage" :tabs="openTabs" :user="user" :version="version"
      @nav="navigate" @logout="logout" @close-tab="closeTab" @refresh-tab="refreshTab">
      <div v-for="t in openTabs" :key="t.name + '#' + (pageVers[t.name]||0)" v-show="t.name===activePage" class="page-fade">
        <component :is="'page-' + t.name" :page="t.name" :user="user" :version-data="versionData" @navigate="navigate" />
      </div>
    </app-layout>
    <change-password-dialog v-model="pwdDialog" :forced="pwdForced" :old-password="pwdOld" @done="onPwdDone" />
  `,
  setup() {
    const activePage = ref('overview')
    const openTabs = ref([{ name: 'overview' }])
    const pageVers = ref({}) // 每个标签页的重挂载计数：+1 即强制该页重新加载
    const user = ref(null)
    const version = ref('')
    const versionData = ref(null)
    const pwdDialog = ref(false)
    const pwdForced = ref(false)
    const pwdOld = ref('')

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

    const openChangePassword = (oldPassword) => {
      pwdForced.value = true
      pwdOld.value = oldPassword || ''
      pwdDialog.value = true
    }
    const onPwdDone = () => {
      pwdDialog.value = false
      pwdForced.value = false
      pwdOld.value = ''
      // 改密后作废当前会话，回登录页用新密码重新登录
      api.post('/auth/logout').finally(() => {
        user.value = null
        ElMessage.success('密码修改成功，请使用新密码重新登录')
        activePage.value = 'login'
        location.hash = '#/login'
      })
    }

    const loadUser = async () => {
      try {
        const res = await api.get('/auth/me')
        if (res.code === 0) {
          user.value = res.data
          if (res.data?.must_change_password && activePage.value !== 'login') {
            openChangePassword()
          }
        }
      } catch (e) { location.hash = '#/login' }
    }
    const loadVersion = async () => {
      try { const res = await api.get('/version'); if (res.code === 0) { version.value = res.data.version; versionData.value = res.data } } catch (e) {}
    }
    const handleRoute = () => {
      const hash = location.hash.slice(2) || 'overview'
      if (!user.value && hash !== 'login') { activePage.value = 'login'; location.hash = '#/login'; return }
      if (routes[hash]) { ensureTab(hash); activePage.value = hash }
      else if (hash === 'login' && user.value) { location.hash = '#/' + activePage.value }
      else if (hash === 'login') activePage.value = 'login'
    }
    const navigate = (page) => {
      if (page === 'login') { location.hash = '#/login'; return }
      ensureTab(page)
      location.hash = '#/' + page
    }
    const logout = () => {
      ElMessageBox.confirm('确认退出登录？', '提示', { type: 'warning' }).then(() => {
        api.post('/auth/logout').finally(() => { user.value = null; location.hash = '#/login' })
      }).catch(() => {})
    }

    watch(() => user.value, (v) => { if (v) loadVersion() })

    window.addEventListener('force-change-password', openChangePassword)

    return { activePage, openTabs, pageVers, ensureTab, closeTab, refreshTab, user, version, versionData, pwdDialog, pwdForced, pwdOld, openChangePassword, onPwdDone, handleRoute, navigate, logout, loadUser, loadVersion, ICONS }
  }
})

app.use(ElementPlus)
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

// 布局
app.component('app-layout', {
  props: ['currentPage', 'tabs', 'user', 'version'],
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
      <div class="nav-item" :class="{active:currentPage==='schedules'}" @click="$emit('nav','schedules')"><span class="nav-icon">` + ICONS.schedules + `</span>定时任务</div>
      <div class="nav-group">安全审计</div>
      <div class="nav-item" :class="{active:currentPage==='credentials'}" @click="$emit('nav','credentials')"><span class="nav-icon">` + ICONS.credentials + `</span>凭据管理</div>
      <div class="nav-item" :class="{active:currentPage==='audit'}" @click="$emit('nav','audit')"><span class="nav-icon">` + ICONS.audit + `</span>操作日志</div>
    </div>
    <div class="sidebar-footer">v{{version || '—'}}</div>
  </div>
  <div class="main-area">
    <div class="header">
      <div style="display:flex;align-items:baseline"><span class="header-title">{{pageTitle}}</span><span class="header-sub">{{pageSub}}</span></div>
      <div class="header-actions">
        <div class="header-user"><div class="avatar">{{userInitial}}</div><span>{{user?.username}}</span></div>
        <el-button size="small" text @click="$emit('logout')">退出</el-button>
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
  emits: ['nav', 'logout', 'close-tab', 'refresh-tab'],
  computed: {
    pageTitle() { return routes[this.currentPage]?.title || '' },
    pageSub() { return routes[this.currentPage]?.sub || '' },
    userInitial() { return this.user?.username?.charAt(0)?.toUpperCase() || 'A' }
  },
  methods: {
    tabTitle(name) { return routes[name]?.title || name } // routes 是闭包变量，模板内不可直接访问
  }
})

// 页面
app.component('page-login', window.LoginPage)
app.component('page-overview', window.OverviewPage)
app.component('page-hosts', window.HostsPage)
app.component('page-credentials', window.CredentialsPage)
app.component('page-audit', window.AuditPage)
app.component('page-templates', window.TemplatesPage)
app.component('page-deploy', window.DeployPage)
app.component('page-orchestrations', window.OrchestrationsPage)
app.component('page-schedules', window.SchedulesPage)
app.component('change-password-dialog', window.ChangePasswordDialog)

const root = app.mount('#app')

window.addEventListener('hashchange', root.handleRoute)
// 刷新时先恢复会话再路由，避免 token 有效却被踢回登录页
root.loadUser().finally(() => {
  root.handleRoute()
  if (root.user) root.loadVersion()
})