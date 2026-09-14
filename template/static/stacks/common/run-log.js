// 套件部署页 · 通用骨架 · 运行日志抽屉与双 SSE（运行轨迹 + 日志流）
// 迁自 pages/stacks.js（逐字保留），套件专属分支改为按槽位分发。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  // 源文件模块级助手（逗号列表 → 数组）：骨架统一提供在 StacksParts.splitList
  const splitList = P.splitList
  P.mixins.push({
    computed: {
    showBootstrapPill() { return this.recordHosts.some(h => h.bootstrap_status && h.bootstrap_status !== 'skipped') },
    showPrereqPill() { return this.isContainerStack(this.recordMeta) },
    },
    methods: {
    onLogDrawerBeforeClose(done) {
      if (this.logDrawerLocked) {
        ElMessage.warning('加装进行中，请等待完成后再关闭')
        return
      }
      done()
    },
    connectSetup() {
      this.closeSetup()
      const source = new EventSource('/api/sse/stacks/setup', { withCredentials: true })
      this.setupSse = source
      source.addEventListener('init', (e) => {
        try { const d = JSON.parse(e.data); if (d.running && d.running.length) this.loadInstances() } catch (err) { /* */ }
      })
      source.addEventListener('track', (e) => {
        try {
          const d = JSON.parse(e.data)
          if (this.drawerVisible && this.recordMeta && d.run_id === this.recordMeta.id && d.host_id) {
            const h = this.recordHosts.find(x => x.host_id === d.host_id)
            if (h) {
              if (d.status) h.status = d.status
              if (d.prereq_status) h.prereq_status = d.prereq_status
              if (d.node_status) h.node_status = d.node_status
              if (d.bootstrap_status) h.bootstrap_status = d.bootstrap_status
            }
          }
        } catch (err) { /* */ }
      })
      source.addEventListener('done', () => { this.afterStackOp() })
      source.onerror = () => { /* */ }
    },
    closeSetup() { if (this.setupSse) { this.setupSse.close(); this.setupSse = null } },
    async openDrawer(row) {
      this.drawerVisible = true
      this.recordMeta = { id: row.id, status: row.status, stack_key: row.stack_key, stack_name: row.stack_name, mode: row.mode, created_at: row.created_at }
      this.recordHosts = []; this.logFilter = ''; this.disconnectLog()
      try {
        const r = await api.get('/stacks/runs/' + row.id)
        if (r.code === 0) {
          this.recordMeta = { ...this.recordMeta, ...r.data }
          this.recordHosts = r.data.hosts || []
        }
      } catch (e) { /* */ }
      this.connectLog(row.id)
    },
    closeDrawer() {
      this.disconnectLog()
      this.drawerVisible = false
      this.recordMeta = null; this.recordHosts = []; this.recordLogs = []
    },
    connectLog(runId) {
      this.disconnectLog()
      const source = new EventSource('/api/sse/stacks/log?run_id=' + runId, { withCredentials: true })
      this.sseLog = source
      source.addEventListener('init', (e) => {
        try {
          const d = JSON.parse(e.data)
          this.recordLogs = (d.logs || []).map(l => ({ id: l.id, ts: l.ts, ip: l.ip, phase: l.phase, text: l.text }))
          if (d.run_status && d.run_status !== 'running') this.disconnectLog()
          this.$nextTick(() => this.scrollLogsBottom())
        } catch (err) { /* */ }
      })
      source.addEventListener('log', (e) => {
        try {
          const l = JSON.parse(e.data)
          this.recordLogs.push({ id: l.id, ts: l.ts, ip: l.ip, phase: l.phase, text: l.text })
          this.$nextTick(() => this.scrollLogsBottom())
        } catch (err) { /* */ }
      })
      source.addEventListener('done', async (e) => {
        try { const d = JSON.parse(e.data); if (d.run_status) this.recordMeta.status = d.run_status } catch (err) { /* */ }
        try {
          const r = await api.get('/stacks/runs/' + runId)
          if (r.code === 0) { this.recordMeta = { ...this.recordMeta, ...r.data }; this.recordHosts = r.data.hosts || [] }
        } catch (err) { /* */ }
        this.disconnectLog(); this.afterStackOp()
      })
    },
    disconnectLog() { if (this.sseLog) { this.sseLog.close(); this.sseLog = null } },
    onLogScroll(e) {
      const el = e.target
      this.logAutoScroll = el.scrollHeight - el.scrollTop - el.clientHeight < 30
    },
    scrollLogsBottom() {
      if (!this.logAutoScroll) return
      const el = this.$refs.stackLogBox
      if (el) el.scrollTop = el.scrollHeight
    },
    },
  })
})()
