window.OverviewPage = {
  props: ['page', 'user', 'versionData'],
  template: `
<div class="overview-page">
  <section class="overview-hero">
    <div class="overview-hero-grid"></div>
    <div class="overview-hero-glow"></div>
    <div class="overview-hero-content">
      <div class="overview-eyebrow"><span class="eyebrow-dot"></span> INFRASTRUCTURE CONTROL CENTER</div>
      <h1>实时运行中心</h1>
      <p>后端通过 SSE 按板块推送最新数据，主机状态、资源指标与操作记录一目了然。</p>
      <div class="overview-hero-meta">
        <span class="sse-status" :class="sseState"><span class="dot"></span>{{sseStatusText}}</span>
        <span class="overview-hero-meta-text">当前在线 {{stats.online}} / {{stats.total}} 台主机</span>
      </div>
    </div>
    <div class="overview-hero-summary">
      <div class="hero-summary-label">系统在线率</div>
      <div class="hero-summary-value">{{stats.onlineRate}}<small>%</small></div>
      <div class="hero-summary-track"><span :style="{width: stats.onlineRate + '%'}"></span></div>
      <div class="hero-summary-foot"><span>实时监控中</span><span>{{stats.unverified}} 台待验证</span></div>
    </div>
  </section>

  <section class="overview-section overview-summary-section">
    <div class="section-header overview-section-header">
      <div><span class="section-icon">▦</span><span class="title">主机汇总</span><span class="section-hint">主机状态与纳管资源统计</span></div>
      <span class="section-live-label"><span class="section-live-dot"></span>实时更新</span>
    </div>
    <div class="stat-cards">
      <div class="stat-card stat-card-total">
        <div class="stat-icon">⌂</div>
        <div class="stat-content"><div class="stat-head"><span class="stat-label">主机总数</span><span class="stat-trend">纳管规模</span></div><div class="stat-value">{{stats.total}}</div><div class="stat-foot"><span class="stat-positive">在线 {{stats.online}}</span><span>离线 {{stats.offline}}</span></div></div>
      </div>
      <div class="stat-card stat-card-online">
        <div class="stat-icon">↗</div>
        <div class="stat-content"><div class="stat-head"><span class="stat-label">在线率</span><span class="stat-trend">健康度</span></div><div class="stat-value">{{stats.onlineRate}}<small>%</small></div><div class="stat-progress"><span :style="{width: stats.onlineRate + '%'}"></span></div><div class="stat-foot"><span>共 {{stats.total}} 台纳管主机</span></div></div>
      </div>
      <div class="stat-card stat-card-credential">
        <div class="stat-icon">◈</div>
        <div class="stat-content"><div class="stat-head"><span class="stat-label">纳管凭据</span><span class="stat-trend">访问安全</span></div><div class="stat-value">{{stats.credTotal}}</div><div class="stat-foot"><span>SSH 密钥与密码</span></div></div>
      </div>
      <div class="stat-card stat-card-unverified">
        <div class="stat-icon">!</div>
        <div class="stat-content"><div class="stat-head"><span class="stat-label">未验证主机</span><span class="stat-trend">待处理</span></div><div class="stat-value">{{stats.unverified}}</div><div class="stat-foot"><span>待首次连接确认</span></div></div>
      </div>
    </div>
  </section>

  <div class="overview-panels">
    <section class="page-card overview-section overview-host-card">
      <div class="card-header overview-card-header">
        <div class="overview-card-title"><span class="panel-icon panel-icon-host">⌂</span><div><div class="title">主机速览 <span class="count-badge">{{hosts.length}} 台</span></div><span class="section-hint">最近纳管主机实时状态</span></div></div>
        <el-button size="small" text type="primary" @click="$emit('navigate','hosts')">查看全部 <span class="button-arrow">→</span></el-button>
      </div>
      <el-table class="overview-host-table" :data="hosts" size="default" :header-cell-style="{background:'#F8FAFC',color:'#94A3B8',fontSize:'12px'}">
        <el-table-column prop="name" label="主机" min-width="180">
          <template #default="{row}"><div class="host-cell"><span class="host-avatar">{{hostInitial(row.name)}}</span><div><div class="host-name">{{row.name}}</div><div class="mono host-ip">{{row.ip}}</div></div></div></template>
        </el-table-column>
        <el-table-column label="标签" width="100">
          <template #default="{row}"><span class="tag-badge" :class="tagClass(row.tag)">{{row.tag || '其他'}}</span></template>
        </el-table-column>
        <el-table-column label="状态" width="112">
          <template #default="{row}"><span class="status-badge" :class="row.status"><span class="dot"></span>{{statusText(row.status)}}</span></template>
        </el-table-column>
        <el-table-column label="内存使用" width="154">
          <template #default="{row}"><div class="metric"><div class="metric-bar"><div class="fill" :class="memClass(row)" :style="{width: memPct(row)+'%'}"></div></div><span class="metric-value">{{memPct(row)}}%</span></div></template>
        </el-table-column>
      </el-table>
      <div v-if="!hosts.length" class="empty-state overview-empty"><span class="empty-icon">⌂</span><strong>暂无主机数据</strong><span>主机接入后，实时状态会展示在这里</span></div>
    </section>

    <section class="page-card overview-section overview-audit-card">
      <div class="card-header overview-card-header">
        <div class="overview-card-title"><span class="panel-icon panel-icon-audit">✓</span><div><div class="title">操作日志 <span class="count-badge">{{recentAudits.length}} 条</span></div><span class="section-hint">最近 10 条操作记录</span></div></div>
        <el-button size="small" text type="primary" @click="$emit('navigate','audit')">查看全部 <span class="button-arrow">→</span></el-button>
      </div>
      <div v-if="recentAudits.length" class="audit-timeline">
        <div v-for="a in recentAudits" :key="a.id" class="audit-item">
          <div class="audit-marker"><span></span></div>
          <div class="audit-content"><div class="audit-topline"><span class="action-badge" :class="actionClass(a.action)">{{a.action}}</span><span class="mono audit-time">{{formatTime(a.created_at)}}</span></div><div class="audit-detail">{{auditDetail(a)}}</div></div>
        </div>
      </div>
      <div v-else class="empty-state overview-empty"><span class="empty-icon">✓</span><strong>暂无操作记录</strong><span>系统产生操作后会实时出现在这里</span></div>
    </section>
  </div>
</div>`,
  emits: ['navigate'],
  data() {
    return {
      stats: { total: 0, online: 0, offline: 0, onlineRate: 0, credTotal: 0, unverified: 0 },
      hosts: [],
      recentAudits: [],
      eventSource: null,
      sseState: 'connecting'
    }
  },
  computed: {
    sseStatusText() {
      if (this.sseState === 'connected') return '实时连接正常'
      if (this.sseState === 'reconnecting') return '正在重连'
      return '连接已断开'
    }
  },
  mounted() { this.connectSSE() },
  beforeUnmount() { if (this.eventSource) this.eventSource.close() },
  methods: {
    parseEvent(event) {
      try { return JSON.parse(event.data || '{}') } catch (e) { return null }
    },
    applySummary(event) {
      const d = this.parseEvent(event)
      if (!d) return
      this.stats.total = Number(d.total) || 0
      this.stats.online = Number(d.online) || 0
      this.stats.offline = Number(d.offline) || 0
      this.stats.unverified = Number(d.unverified) || 0
      this.stats.onlineRate = Number.isFinite(Number(d.online_rate)) ? Number(d.online_rate) : (this.stats.total ? Math.round(this.stats.online / this.stats.total * 100) : 0)
      this.stats.credTotal = Number(d.credential_total) || 0
    },
    applyHostOverview(event) {
      const d = this.parseEvent(event)
      if (d && Array.isArray(d.list)) this.hosts = d.list
    },
    applyOperationLogs(event) {
      const d = this.parseEvent(event)
      if (d && Array.isArray(d.list)) this.recentAudits = d.list
    },
    hostInitial(name) { return String(name || '?').trim().charAt(0).toUpperCase() },
    tagClass() { return 'other' },
    statusText(status) {
      if (status === 'online') return '在线'
      if (status === 'offline') return '离线'
      return '未验证'
    },
    actionClass(action) {
      const value = (action || '').toLowerCase()
      if (value.startsWith('auth')) return 'auth'
      if (value.startsWith('host')) return 'host'
      if (value.startsWith('credential')) return 'credential'
      return 'other'
    },
    auditDetail(a) { return a.detail || a.target_type || '系统操作' },
    formatTime(t) { if (!t) return '-'; const d = new Date(t); return String(d.getMonth()+1).padStart(2,'0')+'-'+String(d.getDate()).padStart(2,'0')+' '+String(d.getHours()).padStart(2,'0')+':'+String(d.getMinutes()).padStart(2,'0') },
    parseInfo(row) { if (!row?.info_json || row.info_json === '{}') return null; try { return typeof row.info_json === 'string' ? JSON.parse(row.info_json) : row.info_json } catch (e) { return null } },
    memPct(row) { const i = this.parseInfo(row); return i?.mem_used_percent ? Math.round(i.mem_used_percent) : 0 },
    memClass(row) {
      const percent = this.memPct(row)
      if (percent < 60) return 'low'
      if (percent < 80) return 'mid'
      return 'high'
    },
    connectSSE() {
      if (this.eventSource) this.eventSource.close()
      this.sseState = 'connecting'
      const source = new EventSource('/api/sse/overview', { withCredentials: true })
      this.eventSource = source
      source.addEventListener('connected', () => { this.sseState = 'connected' })
      source.addEventListener('host.summary', (event) => this.applySummary(event))
      source.addEventListener('host.overview', (event) => this.applyHostOverview(event))
      source.addEventListener('operation.logs', (event) => this.applyOperationLogs(event))
      source.onerror = () => { this.sseState = source.readyState === EventSource.CONNECTING ? 'reconnecting' : 'disconnected' }
      source.onopen = () => { this.sseState = 'connected' }
    }
  }
}
