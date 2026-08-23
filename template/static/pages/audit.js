window.AuditPage = {
  props: ['page', 'user', 'versionData'],
  emits: ['navigate'],
  template: `
<div class="audit-page">
  <!-- 页头横幅 -->
  <section class="audit-hero">
    <div class="audit-hero-grid"></div>
    <div class="audit-hero-glow"></div>
    <div class="audit-hero-content">
      <div class="audit-eyebrow">AUDIT TRAIL</div>
      <h1>操作日志</h1>
      <p>系统关键操作全链路审计，实时追踪每一次变更</p>
    </div>
    <div class="audit-hero-stats">
      <div class="audit-hero-stat">
        <span class="audit-hero-stat-val">{{stats.today_count}}</span>
        <span class="audit-hero-stat-lbl">今日操作</span>
      </div>
      <div class="audit-hero-stat" :class="{'audit-hero-stat--warn': stats.fail_login_24h > 0}">
        <span class="audit-hero-stat-val">{{stats.fail_login_24h}}</span>
        <span class="audit-hero-stat-lbl">24h 失败登录</span>
      </div>
      <div class="audit-hero-stat">
        <span class="audit-hero-stat-val">{{stats.active_ips}}</span>
        <span class="audit-hero-stat-lbl">活跃 IP</span>
      </div>
    </div>
  </section>

  <!-- 筛选栏 -->
  <div class="page-card audit-filter-card">
    <div class="audit-filters">
      <el-select v-model="filters.action" placeholder="操作类型" clearable @change="onFilterChange">
        <el-option label="全部" value="" />
        <el-option label="认证" value="auth" />
        <el-option label="主机" value="host" />
        <el-option label="凭据" value="credential" />
      </el-select>
      <el-select v-model="filters.status" placeholder="状态" clearable @change="onFilterChange">
        <el-option label="全部" value="" />
        <el-option label="成功" value="success" />
        <el-option label="失败" value="fail" />
      </el-select>
      <el-input v-model="filters.keyword" placeholder="关键词（详情 / IP）" clearable style="width:200px" @keyup.enter="onFilterChange" @clear="onFilterChange" />
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
          :class="{'audit-node--fail': isFail(item.action), 'audit-node--new': item._isNew}"
        >
          <div class="audit-node-dot" :class="actionDotClass(item.action)"></div>
          <div class="audit-node-body">
            <div class="audit-node-head">
              <span class="action-badge" :class="actionBadgeClass(item.action)">{{item.action}}</span>
              <span class="audit-node-time" :title="item.created_at">{{relativeTime(item.created_at)}}</span>
            </div>
            <div class="audit-node-detail" v-if="item.detail">{{item.detail}}</div>
            <div class="audit-node-meta">
              <span v-if="item.remote_ip" class="audit-meta-ip" :title="item.remote_ip">
                <svg viewBox="0 0 16 16" width="12" height="12" fill="currentColor"><path d="M0 8a8 8 0 1 1 16 0A8 8 0 0 1 0 8zm8-5a.5.5 0 0 0-.5.5v5.243l1.72 1.72a.5.5 0 0 0 .708-.708L8.5 8.293V3.5A.5.5 0 0 0 8 3z"/></svg>
                {{item.remote_ip}}
              </span>
              <span v-if="item.target_type || item.target_id" class="audit-meta-target">
                <svg viewBox="0 0 16 16" width="12" height="12" fill="currentColor"><path d="M2 2a2 2 0 0 1 2-2h8a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V2zm2-1a1 1 0 0 0-1 1v12a1 1 0 0 0 1 1h8a1 1 0 0 0 1-1V2a1 1 0 0 0-1-1H4z"/><path d="M9.5 4a.5.5 0 0 1 .5.5v3a.5.5 0 0 1-.5.5h-3a.5.5 0 0 1-.5-.5v-3a.5.5 0 0 1 .5-.5h3z"/></svg>
                <template v-if="item.target_type">{{item.target_type}}</template>
                <template v-if="item.target_id"> / {{item.target_id}}</template>
              </span>
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
</div>`,

  data() {
    return {
      list: [],
      loading: false,
      loadingMore: false,
      total: 0,
      pg: 1,
      pageSize: 20,
      filters: { action: '', status: '', keyword: '' },
      dateRange: null,
      stats: { today_count: 0, fail_login_24h: 0, active_ips: 0 },
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
          this.stats.fail_login_24h = d.fail_login_24h || 0
          this.stats.active_ips = d.active_ips || 0
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
    isFail(action) { return (action || '').endsWith('_fail') },
    actionBadgeClass(action) {
      const a = (action || '').toLowerCase()
      if (a.startsWith('auth')) return 'auth'
      if (a.startsWith('host')) return 'host'
      if (a.startsWith('credential')) return 'credential'
      return 'other'
    },
    actionDotClass(action) {
      if (this.isFail(action)) return 'dot--fail'
      const a = (action || '').toLowerCase()
      if (a.startsWith('auth')) return 'dot--auth'
      if (a.startsWith('host')) return 'dot--host'
      if (a.startsWith('credential')) return 'dot--credential'
      return 'dot--other'
    },

    /* ===== 相对时间 ===== */
    relativeTime(dateStr) {
      if (!dateStr) return '-'
      const now = Date.now()
      const d = new Date(dateStr.replace(' ', 'T') + (dateStr.includes('Z') ? '' : 'Z'))
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
