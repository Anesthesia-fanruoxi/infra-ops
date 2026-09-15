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
    // 组件视图（docs/角色物化设计.md）：按主机分组 —— 每台主机列出其承载的组件及各组件状态。
    // 组件状态来自运行步骤（stack_run_steps.Component → 状态聚合：failed > running > pending > success > skipped），
    // 组件在该主机上的角色名收进悬停提示（同一主机同一组件只出一枚芯片）。
    runStepCompStatus() {
      const rank = { failed: 5, running: 4, pending: 3, success: 2, skipped: 1 }
      const m = {}
      ;(this.recordSteps || []).forEach(s => {
        ;(s.component || '').split(',').forEach(c => {
          c = c.trim(); if (!c) return
          const r = rank[s.status] || 0
          if (!m[c] || r > m[c].r) m[c] = { r, status: s.status }
        })
      })
      return m
    },
    runHostComps() {
      const plan = this.recordPlan
      if (!plan || !Array.isArray(plan.hosts) || !plan.hosts.length) return []
      const st = this.runStepCompStatus
      const stackKey = this.recordMeta && this.recordMeta.stack_key
      const catalog = this.compsOf(stackKey)
      const hasCatalog = c => catalog.some(x => x.key === c)
      const hostByIp = {}
      ;(this.recordHosts || []).forEach(h => { hostByIp[h.host_ip] = h })
      return plan.hosts.map(ph => {
        const byComp = {}, order = []
        ;(ph.roles || []).forEach(r => {
          if (!byComp[r.comp]) { byComp[r.comp] = { comp: r.comp, roles: [], labels: [], manual: false }; order.push(r.comp) }
          byComp[r.comp].roles.push(r)
          byComp[r.comp].labels.push(r.label)
          if (r.source === 'manual') byComp[r.comp].manual = true
        })
        const comps = order.map(k => {
          const c = byComp[k]
          // 芯片名：组件目录里有的（大数据等）用组件名；目录里没有的（redis 等通用套件）
          // 单角色直接用角色名（Redis Master / Sentinel），更具体
          const label = hasCatalog(k) ? this.compLabel(stackKey, k) : (c.labels.length === 1 ? c.labels[0] : this.compLabel(stackKey, k))
          // 无步骤数据的历史运行：芯片只出组件名，不带状态
          const s = st[k] ? st[k].status : null
          return { comp: k, label, status: s, title: c.labels.join('、') + (c.manual ? ' · 含手动指定' : '') }
        })
        return { host_ip: ph.host_ip, host_name: ph.host_name, rh: hostByIp[ph.host_ip] || null, comps }
      }).sort((a, b) => ((a.rh || {}).seq || 0) - ((b.rh || {}).seq || 0))
    },
    runHasCompView() { return this.runHostComps.length > 0 },
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
          if (this.drawerVisible && this.recordMeta && d.run_id === this.recordMeta.id) {
            // 流水线步骤状态迁移（与主机进度互斥出现）
            if (d.step_key) {
              const s = (this.recordSteps || []).find(x => x.key === d.step_key)
              if (s) s.status = d.step_status
              return
            }
            if (d.host_id) {
              const h = this.recordHosts.find(x => x.host_id === d.host_id)
              if (h) {
                if (d.status) h.status = d.status
                if (d.prereq_status) h.prereq_status = d.prereq_status
                if (d.node_status) h.node_status = d.node_status
                if (d.bootstrap_status) h.bootstrap_status = d.bootstrap_status
              }
            }
            // 运行终态兜底：整体拉一次详情，保证主机聚合状态/步骤收敛与后端最终一致
            if (d.run_status && d.run_status !== 'running') {
              api.get('/stacks/runs/' + d.run_id).then(r => {
                if (r.code === 0 && this.drawerVisible && this.recordMeta && this.recordMeta.id === d.run_id) {
                  this.recordMeta = { ...this.recordMeta, ...r.data }
                  this.recordHosts = r.data.hosts || []
                  this.recordSteps = r.data.steps || this.recordSteps
                }
              }).catch(() => {})
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
      this.recordPlan = null
      this.recordMeta = { id: row.id, status: row.status, stack_key: row.stack_key, stack_name: row.stack_name, mode: row.mode, created_at: row.created_at }
      this.recordHosts = []; this.recordSteps = []; this.logFilter = ''; this.disconnectLog()
      try {
        const r = await api.get('/stacks/runs/' + row.id)
        if (r.code === 0) {
          this.recordMeta = { ...this.recordMeta, ...r.data }
          this.recordHosts = r.data.hosts || []
          this.recordSteps = r.data.steps || []
          if (this.recordMeta.instance_id) this.loadRunPlan(this.recordMeta.instance_id)
        }
      } catch (e) { /* */ }
      this.connectLog(row.id)
      this.startRunPoll(row.id)
    },
    // 运行中每 10s 对账一次详情（步骤/主机状态），SSE 丢事件时的兜底；终态自动停止。
    startRunPoll(runId) {
      this.stopRunPoll()
      this.runPoll = setInterval(async () => {
        if (!this.drawerVisible || !this.recordMeta || this.recordMeta.id !== runId) { this.stopRunPoll(); return }
        if (this.recordMeta.status && this.recordMeta.status !== 'running') { this.stopRunPoll(); return }
        try {
          const r = await api.get('/stacks/runs/' + runId)
          if (r.code === 0 && this.drawerVisible && this.recordMeta && this.recordMeta.id === runId) {
            this.recordMeta = { ...this.recordMeta, ...r.data }
            this.recordHosts = r.data.hosts || []
            this.recordSteps = r.data.steps || this.recordSteps
          }
        } catch (e) { /* */ }
      }, 10000)
    },
    stopRunPoll() { if (this.runPoll) { clearInterval(this.runPoll); this.runPoll = null } },
    // 组件视图数据源：实例的角色计划（create/reinstall 等操作在部署前已物化落库）
    async loadRunPlan(instanceId) {
      try {
        const r = await api.get('/stacks/instances/' + instanceId + '/plan')
        if (r.code === 0 && r.data && Array.isArray(r.data.hosts) && r.data.hosts.length) this.recordPlan = r.data
      } catch (e) { /* 拉不到计划时回落节点视图 */ }
    },
    closeDrawer() {
      this.disconnectLog(); this.stopRunPoll()
      this.drawerVisible = false
      this.recordMeta = null; this.recordHosts = []; this.recordLogs = []; this.recordPlan = null; this.recordSteps = []
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
          if (r.code === 0) {
            this.recordMeta = { ...this.recordMeta, ...r.data }
            this.recordHosts = r.data.hosts || []
            this.recordSteps = r.data.steps || this.recordSteps
          }
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
