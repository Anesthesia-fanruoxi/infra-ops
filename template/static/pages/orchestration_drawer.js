/* 运行抽屉 mixin：三层结构（L1 步骤条 / L2 主机层 / L3 日志层）+ 双 SSE（步骤流 + 详情流）。
 * 纯只读监控：运行中 = 步骤流 + 详情流两条连接；回溯/已结束 = 仅详情流快照。 */
window.OrchDrawerMixin = {
  data() {
    return {
      runDrawerVisible: false,
      drawerLoading: false,
      runMeta: null,      // 头部 run 元信息 {name,id,status,ok_hosts,fail_hosts,total_hosts,created_at}
      runStatus: '',      // running / success / partial / failed
      baselineRows: [],   // GET /orchestration/runs/:id 全量明细行（打底分组）
      drawerSteps: [],    // L1: [{seq,name,state,aggregate}]
      drawerHosts: [],    // L2: [{host_id,host_ip,host_name,status}]
      drawerLogs: [],     // L3: [{id,ts,ip,text}]
      logFilterIp: '',    // L3 主机过滤（空=全部）
      selectedSeq: 0,
      followMode: false,
      sseSteps: null,
      sseDetail: null,
      logAutoScroll: true
    }
  },
  computed: {
    runBadgeLabel() {
      return { running: '运行中', success: '成功', partial: '部分成功', failed: '失败' }[this.runStatus] || this.runStatus
    },
    filteredLogs() {
      if (!this.logFilterIp) return this.drawerLogs
      return this.drawerLogs.filter(l => l.ip === this.logFilterIp)
    }
  },
  methods: {
    /* ===== 打开 / 关闭 ===== */
    async openRunDrawer(runId) {
      if (!runId) return
      this.runDrawerVisible = true
      this.drawerLoading = true
      this.disconnectRunSse()
      try {
        const r = await api.get('/orchestration/runs/' + runId)
        if (r.code !== 0) { this.runDrawerVisible = false; return }
        const run = r.data.run
        const rows = r.data.steps || []
        this.runMeta = run
        this.runStatus = run.status
        this.baselineRows = rows
        this.drawerSteps = this.buildDrawerSteps(rows)
        const seqs = this.drawerSteps.map(s => s.seq)
        if (run.status === 'running') {
          this.selectedSeq = this.firstActiveSeq(rows) || seqs[0] || 0
          this.followMode = true
        } else {
          this.selectedSeq = seqs[0] || 0
          this.followMode = false
        }
        this.applyStepLocal()
        // 双 SSE：运行中 步骤流+详情流；已结束 仅详情流（回溯快照）
        if (run.status === 'running') this.connectSteps(runId)
        this.connectDetail(runId, this.selectedSeq)
      } catch (e) {} finally { this.drawerLoading = false }
    },
    closeRunDrawer() {
      this.disconnectRunSse()
      this.runDrawerVisible = false
      this.runMeta = null; this.runStatus = ''; this.baselineRows = []
      this.drawerSteps = []; this.drawerHosts = []; this.drawerLogs = []
      this.logFilterIp = ''
      this.selectedSeq = 0; this.followMode = false
    },
    /* ===== 打底分组 ===== */
    buildDrawerSteps(rows) {
      const seqs = [...new Set(rows.map(r => r.seq))].sort((a, b) => a - b)
      return seqs.map(seq => {
        const cells = rows.filter(r => r.seq === seq)
        const agg = this.aggCells(cells)
        return { seq, name: cells[0]?.template_name || '', state: (agg === 'running' || agg === 'pending') ? 'running' : 'finished', aggregate: agg }
      })
    },
    aggCells(cells) {
      let running = 0, pending = 0, skipped = 0, ok = 0, fail = 0
      cells.forEach(c => {
        if (c.status === 'running') running++
        else if (c.status === 'pending') pending++
        else if (c.status === 'skipped') skipped++
        else if (c.status === 'success') ok++
        else if (c.status === 'failed') fail++
      })
      if (running > 0) return 'running'
      if (pending > 0) return 'pending'
      if (skipped > 0 && ok === 0 && fail === 0) return 'skipped'
      if (fail === 0) return 'success'
      if (ok === 0) return 'failed'
      return 'partial'
    },
    firstActiveSeq(rows) {
      const running = rows.find(r => r.status === 'running')
      return running ? running.seq : 0
    },
    /* 用 baseline 局部重建 L2（详情流 init 会再覆盖为权威快照） */
    applyStepLocal() {
      const seen = {}
      this.drawerHosts = this.baselineRows
        .filter(r => r.seq === this.selectedSeq && !seen[r.host_id] && (seen[r.host_id] = true))
        .map(r => ({ host_id: r.host_id, host_ip: r.host_ip, host_name: r.host_name, status: r.status }))
      this.drawerLogs = []
      this.logFilterIp = ''
    },
    /* ===== 步骤选择 / 自动追踪 ===== */
    selectStep(seq) {
      // 先算追踪再判重：点击当前执行中（或下一个待执行）步骤即恢复追踪，即使它已被选中
      const liveSeq = this.drawerSteps.find(s => s.state === 'running')?.seq
      this.followMode = liveSeq != null && seq === liveSeq
      if (this.selectedSeq === seq) return
      this.selectedSeq = seq
      this.applyStepLocal()
      if (this.runMeta) this.connectDetail(this.runMeta.id, seq)
    },
    /* ===== 双 SSE ===== */
    connectSteps(runId) {
      this.disconnectSteps()
      const source = new EventSource('/api/sse/orchestration/steps?run_id=' + runId, { withCredentials: true })
      this.sseSteps = source
      source.addEventListener('init', e => { try { this.onStepsInit(JSON.parse(e.data)) } catch (err) {} })
      source.addEventListener('step', e => { try { this.onStepsEvent(JSON.parse(e.data)) } catch (err) {} })
      source.addEventListener('done', e => {
        this.disconnectSteps()
        try { const d = JSON.parse(e.data); if (d.run_status) { this.runStatus = d.run_status } } catch (err) {}
        this.loadList() // 结束后刷新列表，状态翻转为已结束
        this.refreshRunMeta(runId)
      })
      source.onerror = () => {}
    },
    connectDetail(runId, step) {
      this.disconnectDetail()
      if (!step) return
      const source = new EventSource('/api/sse/orchestration/detail?run_id=' + runId + '&step=' + step, { withCredentials: true })
      this.sseDetail = source
      source.addEventListener('init', e => { try { this.onDetailInit(JSON.parse(e.data)) } catch (err) {} })
      source.addEventListener('host', e => { try { this.onDetailHost(JSON.parse(e.data)) } catch (err) {} })
      source.addEventListener('log', e => { try { this.onDetailLog(JSON.parse(e.data)) } catch (err) {} })
      source.addEventListener('done', () => { this.disconnectDetail() })
      source.onerror = () => {}
    },
    disconnectSteps() { if (this.sseSteps) { this.sseSteps.close(); this.sseSteps = null } },
    disconnectDetail() { if (this.sseDetail) { this.sseDetail.close(); this.sseDetail = null } },
    disconnectRunSse() { this.disconnectSteps(); this.disconnectDetail() },
    /* ===== 事件处理 ===== */
    onStepsInit(d) {
      if (d.steps) this.drawerSteps = d.steps
      if (d.run_status) this.runStatus = d.run_status
    },
    onStepsEvent(ev) {
      const s = this.drawerSteps.find(x => x.seq === ev.seq)
      if (!s) return
      if (ev.state === 'started') {
        s.state = 'running'; s.aggregate = 'running'
        if (this.followMode && this.selectedSeq !== ev.seq) {
          this.selectedSeq = ev.seq
          this.applyStepLocal()
          if (this.runMeta) this.connectDetail(this.runMeta.id, ev.seq)
        }
      } else if (ev.state === 'finished') {
        s.state = 'finished'
        s.aggregate = ev.aggregate || 'success'
      }
    },
    onDetailInit(d) {
      this.drawerHosts = d.hosts || []
      this.drawerLogs = d.logs || []
      this.$nextTick(() => this.scrollLogsBottom())
    },
    onDetailHost(d) {
      const h = this.drawerHosts.find(x => x.host_id === d.host_id)
      if (h) h.status = d.status
      else this.drawerHosts.push({ host_id: d.host_id, host_ip: '', host_name: '', status: d.status })
    },
    onDetailLog(d) {
      this.drawerLogs.push({ id: d.id, ts: d.ts, ip: d.ip, text: d.text })
      this.$nextTick(() => this.scrollLogsBottom())
    },
    async refreshRunMeta(runId) {
      try {
        const r = await api.get('/orchestration/runs/' + runId)
        if (r.code === 0) { this.runMeta = r.data.run; this.runStatus = r.data.run.status }
      } catch (e) {}
    },
    /* ===== 日志滚动 ===== */
    onLogScroll(e) {
      const el = e.target
      this.logAutoScroll = el.scrollHeight - el.scrollTop - el.clientHeight < 30
    },
    scrollLogsBottom() {
      if (!this.logAutoScroll) return
      const el = this.$refs.orchLogBox
      if (el) el.scrollTop = el.scrollHeight
    },
    /* ===== 渲染辅助 ===== */
    runStatusCls(s) { return { running: 'running', success: 'online', partial: 'warning', failed: 'offline' }[s] || 'unverified' },
    stepCls(s) {
      const map = { running: 'orch-step-running', success: 'orch-step-ok', partial: 'orch-step-partial', failed: 'orch-step-fail', skipped: 'orch-step-skipped', pending: 'orch-step-pending' }
      return (map[s.aggregate] || 'orch-step-pending') + (s.state === 'running' ? ' is-running' : '')
    },
    stepMark(s) {
      if (s.aggregate === 'success') return '✓'
      if (s.aggregate === 'failed') return '✕'
      if (s.aggregate === 'partial') return '!'
      return ''
    },
    stepIsActive(s) { return s.seq === this.selectedSeq },
    hostStatusCls(s) { return { running: 'running', success: 'online', failed: 'offline', skipped: 'unverified', pending: 'unverified' }[s] || 'unverified' },
    hostStatusLabel(s) { return { running: '运行中', success: '成功', failed: '失败', skipped: '跳过', pending: '等待' }[s] || s },
    logTime(ts) { return ts ? (ts.length >= 19 ? ts.slice(11, 19) : ts) : '' }
  }
}
