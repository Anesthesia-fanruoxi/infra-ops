window.AuditPage = {
  props: ['page', 'versionData'],
  emits: ['navigate'],
  template: `
<div class="audit-page">
  <!-- 页头横幅 -->
  <section class="audit-hero">
    <div class="audit-hero-grid"></div>
    <div class="audit-hero-glow"></div>
    <div class="audit-hero-content">
      <div class="audit-eyebrow">AUDIT TRAIL</div>
      <h1>审计日志</h1>
      <p>全量台账：写操作与 AI 调用，何时做了什么、结果与用量</p>
    </div>
    <div class="audit-hero-stats">
      <div class="audit-hero-stat">
        <span class="audit-hero-stat-val">{{stats.today_count}}</span>
        <span class="audit-hero-stat-lbl">今日操作</span>
      </div>
      <div class="audit-hero-stat" :class="{'audit-hero-stat--warn': stats.fail_24h > 0}">
        <span class="audit-hero-stat-val">{{stats.fail_24h}}</span>
        <span class="audit-hero-stat-lbl">24h 失败</span>
      </div>
      <div class="audit-hero-stat">
        <span class="audit-hero-stat-val">{{stats.total_count}}</span>
        <span class="audit-hero-stat-lbl">累计记录</span>
      </div>
    </div>
  </section>

  <!-- 标签页：操作日志（SSE 时间线） / AI 调用日志（统一记录组件，看全部菜单） -->
  <div class="audit-tabs">
    <button type="button" class="audit-tab" :class="{active: tab==='ops'}" @click="switchTab('ops')">操作日志</button>
    <button type="button" class="audit-tab" :class="{active: tab==='ai'}" @click="switchTab('ai')">AI 调用日志</button>
  </div>

  <template v-if="tab==='ops'">
  <!-- 筛选栏 -->
  <div class="page-card audit-filter-card">
    <div class="audit-filters">
      <el-select v-model="filters.action" placeholder="业务模块" clearable @change="onFilterChange">
        <el-option label="全部模块" value="" />
        <el-option label="主机" value="hosts" />
        <el-option label="凭据" value="credentials" />
        <el-option label="部署" value="deploy" />
        <el-option label="任务编排" value="orchestrations" />
        <el-option label="套件部署" value="stacks" />
        <el-option label="镜像仓库" value="registry" />
        <el-option label="Elasticsearch" value="es" />
        <el-option label="SFTP" value="sftp" />
      </el-select>
      <el-select v-model="filters.status" placeholder="状态" clearable @change="onFilterChange">
        <el-option label="全部" value="" />
        <el-option label="成功" value="success" />
        <el-option label="失败" value="fail" />
      </el-select>
      <el-input v-model="filters.keyword" placeholder="搜索操作 / 返回消息" clearable style="width:200px" @keyup.enter="onFilterChange" @clear="onFilterChange" />
      <el-date-picker
        v-model="dateRange"
        type="datetimerange"
        range-separator="至"
        start-placeholder="开始时间"
        end-placeholder="结束时间"
        value-format="YYYY-MM-DD HH:mm:ss"
        :teleported="true"
        @change="onDateChange"
        style="width:340px"
      />
      <el-button @click="resetFilters">重置</el-button>
    </div>
  </div>

  <!-- 时间线主体 -->
  <div class="page-card audit-timeline-card">
    <div v-if="loading && !list.length" v-loading="true" style="min-height:200px"></div>

    <!-- 空态 -->
    <div v-else-if="!list.length" class="empty-state audit-empty">
      <svg viewBox="0 0 24 24" width="36" height="36" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/><line x1="16" y1="17" x2="8" y2="17"/></svg>
      <strong>暂无操作记录</strong>
      <span>系统产生操作后会出现在这里</span>
    </div>

    <!-- 时间线 -->
    <div v-else class="audit-timeline-wrap">
      <div class="audit-timeline">
        <div
          v-for="item in list"
          :key="item.id"
          class="audit-node"
          :class="{'audit-node--fail': isFail(item), 'audit-node--new': item._isNew}"
        >
          <div class="audit-node-dot" :class="isFail(item) ? 'dot--fail' : 'dot--ok'"></div>
          <div class="audit-node-body">
            <div class="audit-node-head">
              <span class="audit-node-action">
                <span class="action-badge" :class="methodClass(item.action)">{{methodOf(item.action)}}</span>
                <span class="audit-path" :title="item.action">{{pathOf(item.action)}}</span>
              </span>
              <span class="audit-node-time" :title="item.created_at">{{relativeTime(item.created_at)}}</span>
            </div>
            <div class="audit-node-result">
              <span class="audit-result-tag" :class="isFail(item) ? 'fail' : 'ok'">{{isFail(item) ? '失败' : '成功'}}</span>
              <span class="audit-result-msg" v-if="resultText(item)" :title="item.message">{{resultText(item)}}</span>
              <span class="audit-result-code" v-if="isFail(item)">code {{item.code}}</span>
            </div>
            <div class="audit-node-meta">
              <span v-if="item.target_type || item.target_id" class="audit-meta-target">
                <svg viewBox="0 0 16 16" width="12" height="12" fill="currentColor"><path d="M2 2a2 2 0 0 1 2-2h8a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V2zm2-1a1 1 0 0 0-1 1v12a1 1 0 0 0 1 1h8a1 1 0 0 0 1-1V2a1 1 0 0 0-1-1H4z"/><path d="M9.5 4a.5.5 0 0 1 .5.5v3a.5.5 0 0 1-.5.5h-3a.5.5 0 0 1-.5-.5v-3a.5.5 0 0 1 .5-.5h3z"/></svg>
                <template v-if="item.target_type">{{item.target_type}}</template>
                <template v-if="item.target_id"> / {{item.target_id}}</template>
              </span>
              <span class="audit-meta-http" v-if="item.http_status > 0">HTTP {{item.http_status}}</span>
            </div>
          </div>
        </div>
      </div>

      <!-- 加载更多 -->
      <div class="audit-load-more">
        <el-button
          v-if="hasMore"
          :loading="loadingMore"
          @click="loadMore"
          class="audit-more-btn"
        >加载更多</el-button>
        <span v-else class="audit-no-more">没有更多了</span>
      </div>
    </div>
  </div>

  <!-- 新日志浮动提示 -->
  <transition name="audit-toast-fade">
    <div v-if="showNewTip" class="audit-new-toast" @click="scrollToTop">
      <svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor"><path d="M8 4a.5.5 0 0 1 .5.5v3.793l1.854 1.853a.5.5 0 0 1-.708.708l-2-2a.5.5 0 0 1 0-.708l2-2a.5.5 0 0 1 .708.708L8.5 8.293V4.5A.5.5 0 0 1 8 4z"/><path d="M8 12a.5.5 0 0 1-.5-.5V8.207l-1.854 1.853a.5.5 0 0 1-.708-.708l2-2a.5.5 0 0 1 .708 0l2 2a.5.5 0 0 1-.708.708L8.5 8.207V11.5A.5.5 0 0 1 8 12z"/></svg>
      有新日志
    </div>
  </transition>
  </template>

  <!-- AI 调用日志：统一记录组件，不传 menu 看全部（含按菜单用量汇总） -->
  <div v-else class="page-card audit-ai-card">
    <ai-records show-menu />
  </div>
</div>`,

  data() {
    return {
      tab: 'ops',
      list: [],
      loading: false,
      loadingMore: false,
      total: 0,
      pg: 1,
      pageSize: 20,
      filters: { action: '', status: '', keyword: '' },
      dateRange: null,
      stats: { today_count: 0, fail_24h: 0, total_count: 0 },
      eventSource: null,
      showNewTip: false,
      newTipTimer: null,
      lastNewTipTime: 0
    }
  },

  computed: {
    hasMore() {
      return this.list.length < this.total
    }
  },

  mounted() {
    this.connectSSE(1, 'replace')
  },
  beforeUnmount() {
    this.closeSSE()
    clearTimeout(this.newTipTimer)
  },

  methods: {
    /* ===== SSE 连接管理 ===== */
    buildAuditUrl(page) {
      const params = new URLSearchParams()
      params.set('page', String(page))
      params.set('page_size', String(this.pageSize))
      if (this.filters.action) params.set('action', this.filters.action)
      if (this.filters.status) params.set('status', this.filters.status)
      if (this.filters.keyword) params.set('keyword', this.filters.keyword)
      if (this.dateRange && this.dateRange[0]) params.set('from', this.dateRange[0])
      if (this.dateRange && this.dateRange[1]) params.set('to', this.dateRange[1])
      return '/api/sse/audits?' + params.toString()
    },
    closeSSE() {
      if (this.eventSource) { this.eventSource.close(); this.eventSource = null }
    },
    connectSSE(page, mode) {
      this.closeSSE()
      if (mode === 'replace') {
        this.pg = 1; this.loading = true; this.list = []; this.total = 0
      } else {
        this.loadingMore = true
      }
      const url = this.buildAuditUrl(page)
      const source = new EventSource(url, { withCredentials: true })
      this.eventSource = source

      source.addEventListener('connected', () => {})

      source.addEventListener('stats', (e) => {
        try {
          const d = JSON.parse(e.data)
          this.stats.today_count = d.today_count || 0
          this.stats.fail_24h = d.fail_24h || 0
          this.stats.total_count = d.total_count || 0
        } catch (err) { /* 静默 */ }
      })

      source.addEventListener('logs', (e) => {
        try {
          const d = JSON.parse(e.data)
          const incoming = d.list || []
          if (mode === 'replace') {
            this.list = incoming
          } else {
            this.list = this.list.concat(incoming)
          }
          this.total = d.total || 0
          this.pg = d.page || page
          this.loading = false
          this.loadingMore = false
        } catch (err) { /* 静默 */ }
      })

      source.addEventListener('append', (e) => {
        try {
          const item = JSON.parse(e.data)
          item._isNew = true
          this.list.unshift(item)
          this.total++
          setTimeout(() => { item._isNew = false }, 1800)
          if (this.pg > 1) this.maybeShowNewTip()
        } catch (err) { /* 静默 */ }
      })

      source.onerror = () => { /* EventSource 自动重连 */ }
    },

    /* ===== 标签页切换 ===== */
    // SSE 长连接只在「操作日志」页签存活：切到 AI 页签即断开，切回来重新拉取最新
    switchTab(t) {
      if (this.tab === t) return
      this.tab = t
      this.showNewTip = false
      if (t === 'ops') {
        this.connectSSE(1, 'replace')
      } else {
        this.closeSSE()
      }
    },

    /* ===== 筛选 ===== */
    onFilterChange() { this.connectSSE(1, 'replace') },
    onDateChange() { this.connectSSE(1, 'replace') },
    resetFilters() {
      this.filters = { action: '', status: '', keyword: '' }
      this.dateRange = null
      this.connectSSE(1, 'replace')
    },

    /* ===== 加载更多 ===== */
    loadMore() {
      if (!this.hasMore || this.loadingMore) return
      this.connectSSE(this.pg + 1, 'append')
    },

    /* ===== 新日志提示 ===== */
    maybeShowNewTip() {
      const now = Date.now()
      if (now - this.lastNewTipTime < 30000) return
      this.lastNewTipTime = now; this.showNewTip = true
      clearTimeout(this.newTipTimer)
      this.newTipTimer = setTimeout(() => { this.showNewTip = false }, 4000)
    },
    scrollToTop() {
      this.showNewTip = false
      this.connectSSE(1, 'replace')
    },

    /* ===== 判定 & 分类 ===== */
    // 失败判定与后端一致：业务码非 0 或 HTTP 状态 >= 400
    isFail(item) { return (Number(item.code) || 0) !== 0 || (Number(item.http_status) || 0) >= 400 },
    splitAction(action) {
      const s = String(action || '')
      const i = s.indexOf(' ')
      return i < 0 ? { method: s, path: '' } : { method: s.slice(0, i), path: s.slice(i + 1) }
    },
    methodOf(action) { return this.splitAction(action).method },
    pathOf(action) { return this.splitAction(action).path },
    methodClass(action) {
      const m = this.methodOf(action).toLowerCase()
      if (m === 'post' || m === 'put' || m === 'patch' || m === 'delete') return m
      return 'other'
    },
    resultText(item) {
      const msg = item.message || ''
      if (this.isFail(item)) return msg || '未知错误'
      return msg === 'ok' ? '' : msg
    },

    /* ===== 相对时间 ===== */
    relativeTime(dateStr) {
      if (!dateStr) return '-'
      const now = Date.now()
      const d = new Date(dateStr.replace(' ', 'T'))
      const diff = Math.max(0, now - d.getTime())
      const sec = Math.floor(diff / 1000)
      if (sec < 60) return '刚刚'
      const min = Math.floor(sec / 60)
      if (min < 60) return min + ' 分钟前'
      const hr = Math.floor(min / 60)
      if (hr < 24) return hr + ' 小时前'
      const day = Math.floor(hr / 24)
      if (day < 7) return day + ' 天前'
      const M = String(d.getUTCMonth() + 1).padStart(2, '0')
      const DD = String(d.getUTCDate()).padStart(2, '0')
      const hh = String(d.getUTCHours()).padStart(2, '0')
      const mm = String(d.getUTCMinutes()).padStart(2, '0')
      return M + '-' + DD + ' ' + hh + ':' + mm
    }
  }
}
