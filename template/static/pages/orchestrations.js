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
          <p>多个部署单元按顺序串行执行 · 支持长任务 · 失败自动截断</p>
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
      <el-table-column label="名称" min-width="180">
        <template #default="{row}"><span style="font-weight:600">{{row.name}}</span>
          <div v-if="row.description" style="font-size:11px;color:var(--text-faint)">{{row.description}}</div></template>
      </el-table-column>
      <el-table-column label="步骤数" width="90">
        <template #default="{row}"><el-tag size="small" type="info">{{row.step_count}} 步</el-tag></template>
      </el-table-column>
      <el-table-column label="启用" width="70">
        <template #default="{row}"><span class="status-badge" :class="row.enabled?'online':'offline'"><span class="dot"></span>{{row.enabled?'是':'否'}}</span></template>
      </el-table-column>
      <el-table-column label="更新时间" width="170">
        <template #default="{row}"><span class="mono" style="font-size:12px;color:var(--text-faint)">{{formatTime(row.updated_at)}}</span></template>
      </el-table-column>
      <el-table-column label="操作" width="180" fixed="right">
        <template #default="{row}">
          <el-button size="small" type="primary" @click="startRun(row)">运行</el-button>
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
    <el-table :data="runs" style="width:100%" v-loading="runsLoading" @row-click="openRunDetailById">
      <el-table-column label="运行 ID" width="90"><template #default="{row}"><span class="mono">#{{row.id}}</span></template></el-table-column>
      <el-table-column label="编排" min-width="140" prop="name" />
      <el-table-column label="状态" width="100">
        <template #default="{row}"><el-tag :type="statusTagType(row.status)" size="small">{{statusLabel(row.status)}}</el-tag></template>
      </el-table-column>
      <el-table-column label="主机进度" width="130">
        <template #default="{row}"><span class="mono orch-prog"><span class="ok">{{row.ok_hosts}}</span> 成功 · <span class="fail">{{row.fail_hosts}}</span> 失败 · 共 {{row.total_hosts}}</span></template>
      </el-table-column>
      <el-table-column label="开始时间" width="170">
        <template #default="{row}"><span class="mono" style="font-size:12px;color:var(--text-faint)">{{formatTime(row.created_at)}}</span></template>
      </el-table-column>
    </el-table>
    <div v-if="!runsLoading && !runs.length" class="empty-state"><p>暂无运行记录</p></div>
  </div>

  <!-- 新建/编辑编排 -->
  <el-dialog v-model="editorVisible" :title="(form.id ? '编辑编排' : '新建编排')" width="920px" top="4vh"
    :close-on-click-modal="false" @open="onEditorOpen">
    <el-form label-position="top">
      <div class="form-grid">
        <el-form-item label="编排名称" required><el-input v-model="form.name" placeholder="如：新机初始化" /></el-form-item>
        <el-form-item label="说明"><el-input v-model="form.description" placeholder="可选" /></el-form-item>
      </div>
    </el-form>

    <div class="orch-steps-head">
      <span class="orch-steps-title">步骤（按顺序串行执行，每个步骤 = 选模板 → 勾主机 → 逐台变量）</span>
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
      <div class="orch-unit" v-for="(s,i) in form.steps" :key="s.uid">
        <div class="orch-step-main">
          <span class="orch-step-seq">{{i+1}}</span>
          <span class="orch-step-name">{{s.template_name}}</span>
          <span class="deploy-sel-count">已选 {{(s.hostIds||[]).length}} 台</span>
          <span class="orch-step-tools">
            <el-checkbox v-model="s.continue_on_error" size="small" title="该步骤失败时，失败主机仍继续参与后续步骤">失败继续</el-checkbox>
            <el-button size="small" text :disabled="i===0" @click="moveStep(i,-1)">↑</el-button>
            <el-button size="small" text :disabled="i===form.steps.length-1" @click="moveStep(i,1)">↓</el-button>
            <el-button size="small" text type="danger" @click="form.steps.splice(i,1)">移除</el-button>
          </span>
        </div>

        <!-- 该步骤的主机 -->
        <div class="orch-unit-hosts">
          <el-input v-model="s.hostFilter" placeholder="搜索主机名 / IP" clearable size="small" style="width:200px" />
          <el-button size="small" @click="toggleAllUnitHosts(s)">{{ unitAllSelected(s) ? '取消全选' : '全选' }}</el-button>
          <span v-if="s.retry_count>0" class="deploy-var-hint">失败重试 {{s.retry_count}} 次 / 间隔 {{s.retry_interval_sec}}s</span>
        </div>
        <div class="deploy-host-list deploy-host-list--unit" v-loading="hostLoading">
          <label v-for="h in unitHosts(s)" :key="h.id" class="deploy-host-item" :class="{'deploy-host-item--sel': (s.hostIds||[]).includes(h.id)}">
            <el-checkbox :model-value="(s.hostIds||[]).includes(h.id)" @change="toggleUnitHost(s,h.id)" />
            <span class="deploy-host-name">{{h.name}}</span>
            <span class="mono deploy-host-ip">{{h.ip}}</span>
            <span class="status-badge" :class="h.status"><span class="dot"></span>{{h.status==='online'?'在线':h.status==='offline'?'离线':'未验证'}}</span>
          </label>
          <div v-if="!unitHosts(s).length" class="deploy-placeholder">无匹配主机，请勾选本步骤要执行的主机</div>
        </div>

        <!-- 步骤默认参数 + 逐台变量 -->
        <template v-if="tplVars(s.template_id).length">
          <div class="orch-unit-vars-title">变量 <span class="deploy-var-hint">先设步骤默认值；个别主机不同再逐台覆盖</span></div>
          <div class="orch-step-params">
            <div class="orch-param" v-for="v in tplVars(s.template_id)" :key="'d'+v.name">
              <label>默认 · {{v.label || v.name}}<b v-if="v.required">*</b></label>
              <el-input size="small" v-model="s.params[v.name]" :placeholder="v.default || '模板默认'" />
            </div>
          </div>
          <div v-if="(s.hostIds||[]).length" class="orch-unit-vars-title" style="margin-top:10px">
            逐台覆盖
            <el-button size="small" text type="primary" @click="copyFirstHostVars(s)">复制首台到其他</el-button>
            <el-button size="small" text @click="clearHostVars(s)">清空覆盖</el-button>
          </div>
          <div class="orch-hostvars-rows">
            <div class="orch-hostvar-row" v-for="hid in (s.hostIds||[])" :key="'hv'+hid">
              <div class="orch-hostvar-head">
                <span class="deploy-host-name">{{hostName(hid)}}</span>
                <span class="mono deploy-host-ip">{{hostIP(hid)}}</span>
                <span v-if="hostVarCount(s,hid)" class="deploy-var-hint">已覆盖 {{hostVarCount(s,hid)}} 项</span>
                <el-button size="small" text type="primary" @click="clearHostVar(s,hid)">重置</el-button>
              </div>
              <div class="orch-step-params">
                <div class="orch-param" v-for="v in tplVars(s.template_id)" :key="'hv'+hid+v.name">
                  <label>{{v.label || v.name}}<b v-if="v.required">*</b></label>
                  <el-input size="small" :model-value="hostVarValue(s,hid,v)" @input="val => setHostVar(s,hid,v,val)" :placeholder="'继承: ' + stepParam(s, v)" />
                </div>
              </div>
            </div>
          </div>
        </template>
        <div v-else class="deploy-placeholder deploy-placeholder--ok" style="margin-top:8px">该模板无需变量</div>
      </div>
      <div v-if="!form.steps.length" class="deploy-placeholder">从上方选择模板并「添加步骤」</div>
    </div>
    <template #footer>
      <el-button @click="editorVisible=false">取消</el-button>
      <el-button type="primary" :disabled="!formValid" @click="saveOrch">保存</el-button>
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
      <el-table-column v-for="sq in stepSeqs" :key="sq" :label="seqLabel(sq)" min-width="120">
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
      <pre>{{ logText(logCell) }}</pre>
    </div>
  </el-dialog>
</div>`,

  data() {
    return {
      list: [], loading: false,
      templates: [], hostOptions: [], hostLoading: false,
      editorVisible: false,
      form: { id: 0, name: '', description: '', steps: [] },
      pickTemplateId: null, uidSeed: 1,
      runs: [], runsLoading: false,
      detailVisible: false, activeRun: null, runSteps: [], matrixRows: [], stepSeqs: [],
      logCell: null, logRow: null, sse: null
    }
  },
  computed: {
    formValid() {
      if (!this.form.name || !this.form.steps.length) return false
      return this.form.steps.every(s => s.template_id && (s.hostIds || []).length > 0)
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
    async onEditorOpen() {
      if (!this.templates.length) {
        try { const r = await api.get('/deploy/templates'); if (r.code === 0) this.templates = r.data || [] } catch(e) {}
      }
      if (!this.hostOptions.length) {
        this.hostLoading = true
        try { const r = await api.get('/hosts', { params: { page:1, page_size:500 } }); if (r.code === 0) this.hostOptions = r.data?.list || [] } catch(e) {} finally { this.hostLoading = false }
      }
    },
    tplVars(tid) {
      const t = this.templates.find(x => x.id === tid)
      return t?.variables || []
    },
    hostName(hid) { return this.hostOptions.find(h => h.id === hid)?.name || ('#' + hid) },
    hostIP(hid) { return this.hostOptions.find(h => h.id === hid)?.ip || '' },
    /* ===== 编辑 ===== */
    openEditor(row) {
      if (row) {
        api.get('/orchestrations/' + row.id).then(r => {
          if (r.code !== 0) return
          const o = r.data.orchestration
          this.form = {
            id: o.id, name: o.name, description: o.description,
            steps: (r.data.steps || []).map(s => ({
              uid: this.uidSeed++, template_id: s.template_id, template_name: s.template_name,
              params: s.params || {}, hostIds: s.host_ids || [], hostVars: s.host_vars || {},
              continue_on_error: !!s.continue_on_error,
              retry_count: s.retry_count || 0, retry_interval_sec: s.retry_interval_sec || 30,
              hostFilter: ''
            }))
          }
          this.editorVisible = true
        })
      } else {
        this.form = { id: 0, name: '', description: '', steps: [] }
        this.editorVisible = true
      }
    },
    addStep() {
      const t = this.templates.find(x => x.id === this.pickTemplateId)
      if (!t) return
      this.form.steps.push({
        uid: this.uidSeed++, template_id: t.id, template_name: t.name,
        params: {}, hostIds: [], hostVars: {},
        continue_on_error: false, retry_count: 0, retry_interval_sec: 30, hostFilter: ''
      })
      this.pickTemplateId = null
    },
    moveStep(i, dir) {
      const arr = this.form.steps, j = i + dir
      if (j < 0 || j >= arr.length) return
      ;[arr[i], arr[j]] = [arr[j], arr[i]]
    },
    unitHosts(s) {
      const kw = (s.hostFilter || '').toLowerCase()
      let list = this.hostOptions
      if (kw) list = list.filter(h => (h.name||'').toLowerCase().includes(kw) || (h.ip||'').includes(kw))
      return list
    },
    unitAllSelected(s) {
      const list = this.unitHosts(s)
      return list.length > 0 && list.every(h => (s.hostIds||[]).includes(h.id))
    },
    toggleUnitHost(s, id) {
      const i = (s.hostIds||[]).indexOf(id)
      i >= 0 ? s.hostIds.splice(i, 1) : s.hostIds.push(id)
    },
    toggleAllUnitHosts(s) {
      const list = this.unitHosts(s)
      if (this.unitAllSelected(s)) s.hostIds = (s.hostIds||[]).filter(id => !list.some(h => h.id === id))
      else list.forEach(h => { if (!(s.hostIds||[]).includes(h.id)) s.hostIds.push(h.id) })
    },
    stepParam(s, v) {
      if (s.params && s.params[v.name] !== undefined && s.params[v.name] !== '') return s.params[v.name]
      return v.default || '空'
    },
    hostVarValue(s, hid, v) {
      const hv = (s.hostVars || {})[String(hid)] || {}
      return hv[v.name] !== undefined ? hv[v.name] : ''
    },
    setHostVar(s, hid, v, val) {
      const key = String(hid)
      if (!s.hostVars) s.hostVars = {}
      if (!s.hostVars[key]) s.hostVars[key] = {}
      if (val === '' || val == null) delete s.hostVars[key][v.name]
      else s.hostVars[key][v.name] = val
    },
    hostVarCount(s, hid) {
      return Object.keys((s.hostVars || {})[String(hid)] || {}).length
    },
    clearHostVar(s, hid) { if (s.hostVars) delete s.hostVars[String(hid)] },
    clearHostVars(s) { s.hostVars = {} },
    copyFirstHostVars(s) {
      const ids = s.hostIds || []
      if (ids.length < 2) return
      const src = (s.hostVars || {})[String(ids[0])]
      if (!src || !Object.keys(src).length) { ElMessage.warning('首台主机没有覆盖项'); return }
      ids.slice(1).forEach(hid => { s.hostVars[String(hid)] = { ...src } })
    },
    async saveOrch() {
      const payload = {
        name: this.form.name, description: this.form.description, enabled: true,
        steps: this.form.steps.map(s => ({
          template_id: s.template_id,
          params: s.params || {},
          host_ids: s.hostIds || [],
          host_vars: s.hostVars || {},
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
    startRun(row) {
      ElMessageBox.confirm(
        '按顺序执行编排「' + row.name + '」共 ' + row.step_count + ' 个步骤？长任务可在历史运行中随时查看进度。',
        '确认执行', { type: 'warning', confirmButtonText: '开始执行', cancelButtonText: '取消' })
      .then(async () => {
        try {
          const r = await api.post('/orchestrations/' + row.id + '/run', {})
          if (r.code === 0) { ElMessage.success('编排已开始执行'); this.loadRuns(); this.openRunDetailById({ id: this.latestRunIdGuess() }) }
        } catch(e) {}
      }).catch(() => {})
    },
    latestRunIdGuess() { return this.runs.length ? this.runs[0].id : 0 },
    /* ===== 运行详情 ===== */
    openRunDetailById(row) {
      const id = typeof row === 'object' ? row.id : row
      if (!id) { this.loadRuns(); return }
      setTimeout(async () => {
        try {
          const r = await api.get('/orchestration/runs/' + id)
          if (r.code !== 0) return
          this.applyDetail(r.data)
          this.detailVisible = true
          if (this.activeRun?.status === 'running') this.connectSse(id)
          else this.closeSse()
        } catch(e) {}
      }, 400) // 等待运行记录落库
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
    logText(c) { return (c.error ? c.error + '\n' : '') + (c.output || '') || '无输出' },
    connectSse(runId) {
      this.closeSse()
      const source = new EventSource('/api/sse/orchestration?run_id=' + runId, { withCredentials: true })
      this.sse = source
      source.addEventListener('progress', (e) => {
        try { this.onProgress(JSON.parse(e.data)) } catch(err) {}
      })
      source.addEventListener('done', () => {
        this.closeSse()
        this.openRunDetailById(runId)
        this.loadRuns()
      })
      source.onerror = () => {}
    },
    closeSse() { if (this.sse) { this.sse.close(); this.sse = null } },
    onProgress(d) {
      if (!d.host_id) return
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
    statusTagType(s) { return { success:'success', running:'warning', partial:'danger', failed:'danger' }[s] || 'info' },
    statusLabel(s) { return { running:'执行中', success:'成功', partial:'部分成功', failed:'失败' }[s] || s },
    formatTime(t) {
      if (!t) return '-'
      const d = new Date(t.replace(' ', 'T') + (t.includes('Z') ? '' : 'Z'))
      return d.getFullYear() + '-' + String(d.getMonth()+1).padStart(2,'0') + '-' + String(d.getDate()).padStart(2,'0') + ' ' + String(d.getHours()).padStart(2,'0') + ':' + String(d.getMinutes()).padStart(2,'0')
    }
  }
}
