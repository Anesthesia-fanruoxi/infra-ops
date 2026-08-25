window.OrchestrationsPage = {
  props: ['page', 'user', 'versionData'],
  template: `
<div class="orch-page">
  <section class="deploy-hero">
    <div class="deploy-hero-grid"></div>
    <div class="deploy-hero-glow"></div>
    <div class="deploy-hero-content">
      <div class="deploy-hero-row">
        <div>
          <span class="deploy-eyebrow">ORCHESTRATION</span>
          <h1>任务编排</h1>
          <p>多个模板按步骤顺序执行 · 步骤栅栏 · 失败自动截断后续</p>
        </div>
        <el-button type="primary" size="large" class="deploy-new-btn" @click="openEditor()">
          <el-icon style="margin-right:6px"><Plus /></el-icon>新建编排
        </el-button>
      </div>
    </div>
  </section>

  <!-- 编排列表 -->
  <div class="page-card">
    <div class="card-header">
      <span class="title">编排定义</span>
      <el-button size="small" text @click="loadList"><el-icon style="margin-right:4px"><Refresh /></el-icon>刷新</el-button>
    </div>
    <el-table :data="list" style="width:100%" v-loading="loading">
      <el-table-column label="名称" min-width="160">
        <template #default="{row}"><span style="font-weight:600">{{row.name}}</span>
          <div v-if="row.description" style="font-size:11px;color:var(--text-faint)">{{row.description}}</div></template>
      </el-table-column>
      <el-table-column label="模式" width="110">
        <template #default="{row}"><el-tag size="small" type="info">{{row.exec_mode==='by_step'?'步骤栅栏':'主机独立'}}</el-tag></template>
      </el-table-column>
      <el-table-column label="步骤数" width="80">
        <template #default="{row}"><span class="mono">{{row.step_count}}</span></template>
      </el-table-column>
      <el-table-column label="启用" width="70">
        <template #default="{row}"><span class="status-badge" :class="row.enabled?'online':'offline'"><span class="dot"></span>{{row.enabled?'是':'否'}}</span></template>
      </el-table-column>
      <el-table-column label="更新时间" width="170">
        <template #default="{row}"><span class="mono" style="font-size:12px;color:var(--text-faint)">{{formatTime(row.updated_at)}}</span></template>
      </el-table-column>
      <el-table-column label="操作" width="180" fixed="right">
        <template #default="{row}">
          <el-button size="small" type="primary" @click="openRun(row)">运行</el-button>
          <el-button size="small" @click="openEditor(row)">编辑</el-button>
          <el-button size="small" type="danger" text @click="removeOrch(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
    <div v-if="!loading && !list.length" class="empty-state"><p>还没有编排，点击右上角「新建编排」创建</p></div>
  </div>

  <!-- 历史运行 -->
  <div class="page-card">
    <div class="card-header">
      <span class="title">历史运行</span>
      <el-button size="small" text @click="loadRuns"><el-icon style="margin-right:4px"><Refresh /></el-icon>刷新</el-button>
    </div>
    <el-table :data="runs" style="width:100%" v-loading="runsLoading" @row-click="openRunDetail">
      <el-table-column label="运行 ID" width="90"><template #default="{row}"><span class="mono">#{{row.id}}</span></template></el-table-column>
      <el-table-column label="编排" min-width="140" prop="name" />
      <el-table-column label="状态" width="100">
        <template #default="{row}"><el-tag :type="statusTagType(row.status)" size="small">{{statusLabel(row.status)}}</el-tag></template>
      </el-table-column>
      <el-table-column label="主机进度" width="120">
        <template #default="{row}"><span class="mono orch-prog"><span class="ok">{{row.ok_hosts}}</span> / <span class="fail">{{row.fail_hosts}}</span> / {{row.total_hosts}}</span></template>
      </el-table-column>
      <el-table-column label="开始时间" width="170">
        <template #default="{row}"><span class="mono" style="font-size:12px;color:var(--text-faint)">{{formatTime(row.created_at)}}</span></template>
      </el-table-column>
    </el-table>
    <div v-if="!runsLoading && !runs.length" class="empty-state"><p>暂无运行记录</p></div>
  </div>

  <!-- 新建/编辑编排 -->
  <el-dialog v-model="editorVisible" :title="(form.id ? '编辑编排：' : '新建编排')" width="820px" :close-on-click-modal="false" @open="loadTemplates">
    <el-form label-position="top">
      <div class="form-grid">
        <el-form-item label="编排名称" required><el-input v-model="form.name" placeholder="如：新机初始化" /></el-form-item>
        <el-form-item label="说明"><el-input v-model="form.description" placeholder="可选" /></el-form-item>
      </div>
    </el-form>
    <div class="orch-steps-head">
      <span class="orch-steps-title">步骤链（按顺序执行）</span>
      <div class="orch-add-step">
        <el-select v-model="pickTemplateId" placeholder="选择模板" size="small" style="width:220px" :teleported="false">
          <el-option v-for="t in templates" :key="t.id" :label="t.name" :value="t.id">
            <span>{{t.name}}</span><span style="float:right;color:var(--text-faint);font-size:12px">{{(t.variables||[]).length}} 变量</span>
          </el-option>
        </el-select>
        <el-button size="small" type="primary" :disabled="!pickTemplateId" @click="addStep">添加步骤</el-button>
      </div>
    </div>
    <div class="orch-step-list">
      <div class="orch-step-row" v-for="(s,i) in form.steps" :key="i">
        <div class="orch-step-main">
          <span class="orch-step-seq">{{i+1}}</span>
          <span class="orch-step-name">{{s.template_name}}</span>
          <span class="orch-step-tools">
            <el-button size="small" text :disabled="i===0" @click="moveStep(i,-1)">↑</el-button>
            <el-button size="small" text :disabled="i===form.steps.length-1" @click="moveStep(i,1)">↓</el-button>
            <el-button size="small" text type="danger" @click="form.steps.splice(i,1)">移除</el-button>
          </span>
        </div>
        <div class="orch-step-params" v-if="tplVars(s.template_id).length">
          <div class="orch-param" v-for="v in tplVars(s.template_id)" :key="v.name">
            <label>{{v.label || v.name}}<b v-if="v.required">*</b></label>
            <el-input size="small" v-model="s.params[v.name]" :placeholder="'默认: ' + (v.default || '空')" />
          </div>
        </div>
        <div class="orch-step-opts">
          <el-checkbox v-model="s.continue_on_error" size="small">失败继续后续步骤</el-checkbox>
          <span class="orch-retry">重试 <el-input-number v-model="s.retry_count" size="small" :min="0" :max="5" controls-position="right" style="width:90px" /> 次 · 间隔 <el-input-number v-model="s.retry_interval_sec" size="small" :min="5" :max="600" controls-position="right" style="width:100px" /> 秒</span>
        </div>
      </div>
      <div v-if="!form.steps.length" class="deploy-placeholder">从上方选择模板并「添加步骤」</div>
    </div>
    <template #footer>
      <el-button @click="editorVisible=false">取消</el-button>
      <el-button type="primary" :disabled="!form.name || !form.steps.length" @click="saveOrch">保存</el-button>
    </template>
  </el-dialog>

  <!-- 运行：选主机 -->
  <el-dialog v-model="runVisible" :title="'运行编排：' + (activeOrch?.name || '')" width="640px" :close-on-click-modal="false">
    <div class="deploy-host-toolbar">
      <el-input v-model="runFilter" placeholder="搜索主机名 / IP" clearable size="small" style="width:200px" />
      <el-button size="small" @click="toggleAllRunHosts">{{ runSelected.length === filteredRunHosts.length && filteredRunHosts.length ? '取消全选' : '全选' }}</el-button>
      <span class="deploy-sel-count">已选 {{runSelected.length}} 台</span>
    </div>
    <div class="deploy-host-list deploy-host-list--dialog" v-loading="runHostsLoading">
      <label v-for="h in filteredRunHosts" :key="h.id" class="deploy-host-item" :class="{'deploy-host-item--sel': runSelected.includes(h.id)}">
        <el-checkbox :model-value="runSelected.includes(h.id)" @change="toggleRunHost(h.id)" />
        <span class="deploy-host-name">{{h.name}}</span>
        <span class="mono deploy-host-ip">{{h.ip}}</span>
        <span class="status-badge" :class="h.status"><span class="dot"></span>{{h.status==='online'?'在线':h.status==='offline'?'离线':'未验证'}}</span>
      </label>
    </div>
    <template #footer>
      <el-button @click="runVisible=false">取消</el-button>
      <el-button type="primary" :disabled="!runSelected.length" :loading="starting" @click="startRun">开始执行</el-button>
    </template>
  </el-dialog>

  <!-- 运行详情（矩阵） -->
  <el-dialog v-model="detailVisible" :title="'运行详情 #' + (activeRun?.id || '')" width="920px" top="6vh">
    <div v-if="activeRun" class="orch-run-meta">
      <span>{{activeRun.name}}</span>
      <el-tag :type="statusTagType(activeRun.status)" size="small">{{statusLabel(activeRun.status)}}</el-tag>
      <span class="mono orch-prog">成功 <b class="ok">{{activeRun.ok_hosts}}</b> · 失败 <b class="fail">{{activeRun.fail_hosts}}</b> · 共 {{activeRun.total_hosts}} 台</span>
      <span class="mono" style="font-size:12px;color:var(--text-faint)">{{formatTime(activeRun.created_at)}}</span>
    </div>
    <el-table :data="matrixRows" size="small" border>
      <el-table-column label="主机" min-width="150" fixed="left">
        <template #default="{row}"><span style="font-weight:500">{{row.host_name}}</span><br/><span class="mono" style="font-size:11px;color:var(--text-faint)">{{row.host_ip}}</span></template>
      </el-table-column>
      <el-table-column v-for="sq in stepSeqs" :key="sq" :label="seqLabel(sq)" min-width="130">
        <template #default="{row}">
          <span v-if="row.cells[sq]" class="orch-cell" :class="'st-' + row.cells[sq].status" @click="showLog(row, sq)">
            {{cellText(row.cells[sq])}}
          </span>
        </template>
      </el-table-column>
    </el-table>
    <div v-if="logCell" class="deploy-output-pre" style="margin-top:10px">
      <div class="orch-log-head">
        <b>{{logRow.host_name}}</b> · 步骤{{logCell.seq}} {{logCell.template_name}}
        <el-button size="small" text @click="logCell=null">收起</el-button>
      </div>
      <pre>{{ (logCell.error ? logCell.error + '\\n' : '') + (logCell.output || '') || '无输出' }}</pre>
    </div>
  </el-dialog>
</div>`,

  data() {
    return {
      list: [], loading: false,
      templates: [],
      editorVisible: false, form: { id: 0, name: '', description: '', steps: [] }, pickTemplateId: null,
      runVisible: false, activeOrch: null, runHosts: [], runHostsLoading: false, runSelected: [], runFilter: '', starting: false,
      runs: [], runsLoading: false,
      detailVisible: false, activeRun: null, runSteps: [], matrixRows: [], stepSeqs: [], logCell: null, logRow: null,
      sse: null
    }
  },
  computed: {
    filteredRunHosts() {
      if (!this.runFilter) return this.runHosts
      const kw = this.runFilter.toLowerCase()
      return this.runHosts.filter(h => (h.name||'').toLowerCase().includes(kw) || (h.ip||'').includes(kw))
    }
  },
  mounted() { this.loadList(); this.loadRuns() },
  beforeUnmount() { this.closeSse() },
  methods: {
    async loadList() {
      this.loading = true
      try { const r = await api.get('/orchestrations'); if (r.code === 0) this.list = r.data || [] } catch(e) {} finally { this.loading = false }
    },
    async loadRuns() {
      this.runsLoading = true
      try { const r = await api.get('/orchestration/runs', { params: { page:1, page_size:20 } }); if (r.code === 0) this.runs = r.data?.list || [] } catch(e) {} finally { this.runsLoading = false }
    },
    async loadTemplates() {
      if (this.templates.length) return
      try { const r = await api.get('/deploy/templates'); if (r.code === 0) this.templates = r.data || [] } catch(e) {}
    },
    tplVars(tid) {
      const t = this.templates.find(x => x.id === tid)
      return t?.variables || []
    },
    /* ===== 编辑 ===== */
    openEditor(row) {
      if (row) {
        api.get('/orchestrations/' + row.id).then(r => {
          if (r.code !== 0) return
          const o = r.data.orchestration, steps = r.data.steps || []
          this.form = { id: o.id, name: o.name, description: o.description, steps: steps.map(s => ({
            template_id: s.template_id, template_name: s.template_name,
            params: this.safeParse(s.params_json), continue_on_error: !!s.continue_on_error,
            retry_count: s.retry_count, retry_interval_sec: s.retry_interval_sec })) }
          this.editorVisible = true
        })
      } else {
        this.form = { id: 0, name: '', description: '', steps: [] }
        this.editorVisible = true
      }
    },
    safeParse(jsonStr) {
      try { return JSON.parse(jsonStr || '{}') || {} } catch(e) { return {} }
    },
    addStep() {
      const t = this.templates.find(x => x.id === this.pickTemplateId)
      if (!t) return
      this.form.steps.push({ template_id: t.id, template_name: t.name, params: {}, continue_on_error: false, retry_count: 0, retry_interval_sec: 30 })
      this.pickTemplateId = null
    },
    moveStep(i, dir) {
      const arr = this.form.steps, j = i + dir
      if (j < 0 || j >= arr.length) return
      ;[arr[i], arr[j]] = [arr[j], arr[i]]
    },
    async saveOrch() {
      const payload = {
        name: this.form.name, description: this.form.description, exec_mode: 'by_step', enabled: true,
        steps: this.form.steps.map((s, i) => ({
          seq: i + 1, template_id: s.template_id,
          params_json: JSON.stringify(s.params || {}),
          continue_on_error: !!s.continue_on_error,
          retry_count: +s.retry_count || 0,
          retry_interval_sec: +s.retry_interval_sec || 30 }))
      }
      try {
        const r = this.form.id ? await api.put('/orchestrations/' + this.form.id, payload) : await api.post('/orchestrations', payload)
        if (r.code === 0) { ElMessage.success('已保存'); this.editorVisible = false; this.loadList() }
      } catch(e) {}
    },
    removeOrch(row) {
      ElMessageBox.confirm('确认删除编排「' + row.name + '」？历史运行记录将保留。', '提示', { type: 'warning' })
        .then(async () => { const r = await api.delete('/orchestrations/' + row.id); if (r.code === 0) { ElMessage.success('已删除'); this.loadList() } })
        .catch(() => {})
    },
    /* ===== 运行 ===== */
    async openRun(row) {
      this.activeOrch = row; this.runSelected = []; this.runFilter = ''; this.runVisible = true
      this.runHostsLoading = true
      try {
        const r = await api.get('/hosts', { params: { page:1, page_size:200, status:'online' } })
        if (r.code === 0) this.runHosts = r.data?.list || []
      } catch(e) {} finally { this.runHostsLoading = false }
    },
    toggleRunHost(id) {
      const i = this.runSelected.indexOf(id)
      i >= 0 ? this.runSelected.splice(i, 1) : this.runSelected.push(id)
    },
    toggleAllRunHosts() {
      if (this.runSelected.length === this.filteredRunHosts.length && this.filteredRunHosts.length) this.runSelected = []
      else this.runSelected = this.filteredRunHosts.map(h => h.id)
    },
    async startRun() {
      this.starting = true
      try {
        // 按当前显示顺序提交，保证 {{__seq}} 序号与页面一致
        const ids = this.filteredRunHosts.filter(h => this.runSelected.includes(h.id)).map(h => h.id)
        const r = await api.post('/orchestrations/' + this.activeOrch.id + '/run', { host_ids: ids })
        if (r.code === 0) {
          this.runVisible = false
          ElMessage.success('编排已开始执行')
          this.loadRuns()
          this.openRunDetailById(r.data.run_id)
        }
      } catch(e) {} finally { this.starting = false }
    },
    /* ===== 运行详情 ===== */
    openRunDetail(row) { this.openRunDetailById(row.id) },
    async openRunDetailById(id) {
      try {
        const r = await api.get('/orchestration/runs/' + id)
        if (r.code !== 0) return
        this.applyDetail(r.data)
        this.detailVisible = true
        if (this.activeRun?.status === 'running') this.connectSse(id)
        else this.closeSse()
      } catch(e) {}
    },
    applyDetail(data) {
      this.activeRun = data.run
      this.runSteps = data.steps || []
      const map = {}, order = []
      this.runSteps.forEach(r => {
        if (!map[r.host_id]) { map[r.host_id] = { host_id:r.host_id, host_name:r.host_name, host_ip:r.host_ip, cells:{} }; order.push(r.host_id) }
        map[r.host_id].cells[r.seq] = r
      })
      this.matrixRows = order.map(h => map[h])
      this.stepSeqs = [...new Set(this.runSteps.map(r => r.seq))].sort((a,b)=>a-b)
    },
    seqLabel(sq) {
      const any = this.runSteps.find(r => r.seq === sq)
      return sq + '. ' + (any?.template_name || '')
    },
    showLog(row, sq) { this.logRow = row; this.logCell = row.cells[sq] },
    cellText(c) {
      return { pending:'等待', running:'执行中', success:'成功', failed:'失败', skipped:'跳过' }[c.status] || c.status
    },
    connectSse(runId) {
      this.closeSse()
      const source = new EventSource('/api/sse/orchestration?run_id=' + runId, { withCredentials: true })
      this.sse = source
      source.addEventListener('progress', (e) => {
        try { this.onProgress(JSON.parse(e.data)) } catch(err) {}
      })
      source.addEventListener('done', () => {
        this.closeSse()
        this.openRunDetailById(runId) // 终态全量刷新一次
        this.loadRuns()
      })
      source.onerror = () => {}
    },
    closeSse() { if (this.sse) { this.sse.close(); this.sse = null } },
    onProgress(d) {
      if (!d.host_id) return // finished 等汇总事件由 done 分支处理
      const row = this.matrixRows.find(r => r.host_id === d.host_id)
      if (d.status === 'skipped-refresh') { this.openRunDetailById(d.run_id); return }
      if (!row) return
      const cell = row.cells[d.seq]
      if (!cell) return
      if (d.status === 'output') { cell.output = (cell.output || '') + (d.output || '') }
      else {
        cell.status = d.status
        if (d.attempt) cell.attempt = d.attempt
        if (d.output) cell.output = d.output
        if (d.error) cell.error = d.error
      }
    },
    /* ===== 工具 ===== */
    statusTagType(s) { return { success:'success', running:'warning', partial:'danger', failed:'danger' }[s] || 'info' },
    statusLabel(s) { return { running:'执行中', success:'成功', partial:'部分成功', failed:'失败' }[s] || s },
    formatTime(t) {
      if (!t) return '-'
      const d = new Date(t.replace(' ', 'T') + (t.includes('Z') ? '' : 'Z'))
      return d.getFullYear() + '-' + String(d.getMonth()+1).padStart(2,'0') + '-' + String(d.getDate()).padStart(2,'0') + ' ' + String(d.getHours()).padStart(2,'0') + ':' + String(d.getMinutes()).padStart(2,'0')
    }
  }
}
