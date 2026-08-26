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
          <p>流水线式编排多个部署单元 · 顺序串行 · 支持长任务</p>
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
      <el-table-column label="流水线" min-width="260">
        <template #default="{row}">
          <div class="pipe-mini">
            <template v-for="(s,idx) in pipePreview(row.id)" :key="idx">
              <span class="pipe-mini-arrow" v-if="idx>0">→</span>
              <span class="pipe-mini-node">{{s}}</span>
            </template>
          </div>
        </template>
      </el-table-column>
      <el-table-column label="步骤数" width="80">
        <template #default="{row}"><span class="mono">{{row.step_count}}</span></template>
      </el-table-column>
      <el-table-column label="启用" width="70">
        <template #default="{row}"><span class="status-badge" :class="row.enabled?'online':'offline'"><span class="dot"></span>{{row.enabled?'是':'否'}}</span></template>
      </el-table-column>
      <el-table-column label="更新时间" width="160">
        <template #default="{row}"><span class="mono" style="font-size:12px;color:var(--text-faint)">{{formatTime(row.updated_at)}}</span></template>
      </el-table-column>
      <el-table-column label="操作" width="170" fixed="right">
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

  <!-- 编辑编排：大弹框 + 流水线 -->
  <el-dialog v-model="editorVisible" :title="(form.id ? '编辑编排' : '新建编排')" width="980px" top="6vh"
    :close-on-click-modal="false" @open="onEditorOpen">
    <el-form label-position="top">
      <div class="form-grid">
        <el-form-item label="编排名称" required><el-input v-model="form.name" placeholder="如：新机初始化" style="max-width:360px" /></el-form-item>
        <el-form-item label="说明"><el-input v-model="form.description" placeholder="可选" style="max-width:420px" /></el-form-item>
      </div>
    </el-form>

    <!-- 流水线 -->
    <div class="pipe-wrap">
      <div class="pipe-flow">
        <div v-for="(s,i) in form.steps" :key="s.uid" class="pipe-node" :class="{active: editIdx===i}" @click="openDrawer(i)">
          <span class="pipe-seq">{{i+1}}</span>
          <div class="pipe-info">
            <b>{{s.template_name}}</b>
            <small>{{(s.hostIds||[]).length}} 台主机<template v-if="hostVarTotal(s)"> · {{hostVarTotal(s)}} 台覆盖变量</template></small>
          </div>
          <span class="pipe-tools" @click.stop>
            <el-icon class="pipe-tool" title="上移" @click="moveAt(i,-1)"><Top /></el-icon>
            <el-icon class="pipe-tool" title="下移" @click="moveAt(i,1)"><Bottom /></el-icon>
            <el-icon class="pipe-tool pipe-tool-danger" title="移除" @click="removeStep(i)"><Close /></el-icon>
          </span>
        </div>
        <div class="pipe-add" @click="addClick">
          <el-icon><Plus /></el-icon><span>添加步骤</span>
        </div>
      </div>
      <div class="pipe-hint">点击节点在右侧抽屉中编辑该步骤的主机与变量；执行顺序从左到右</div>
    </div>

    <template #footer>
      <el-button @click="editorVisible=false">取消</el-button>
      <el-button type="primary" :disabled="!formValid" @click="saveOrch">保存</el-button>
    </template>
  </el-dialog>

  <!-- 步骤编辑抽屉 -->
  <el-drawer v-model="drawerVisible" :title="drawerTitle" size="620px" :close-on-click-modal="false">
    <!-- 新增模式：选模板 -->
    <div v-if="drawerMode==='add'" v-loading="templatesLoading">
      <div class="drawer-tip">选择一个模板作为新步骤：</div>
      <div class="tpl-pick-list">
        <div v-for="t in templates" :key="t.id" class="tpl-pick-item" @click="pickTemplate(t)">
          <div><b>{{t.name}}</b><div v-if="t.description" class="tpl-pick-desc">{{t.description}}</div></div>
          <el-tag size="small" type="info">{{(t.variables||[]).length}} 变量</el-tag>
        </div>
      </div>
    </div>

    <!-- 编辑模式 -->
    <template v-else-if="editStep">
      <div class="drawer-sec">
        <div class="drawer-sec-title">目标主机 <span class="deploy-sel-count">已选 {{(editStep.hostIds||[]).length}} 台</span></div>
        <div class="orch-unit-hosts">
          <el-input v-model="editStep.hostFilter" placeholder="搜索主机名 / IP" clearable size="small" style="width:200px" />
          <el-button size="small" @click="toggleAllUnitHosts(editStep)">{{ unitAllSelected(editStep) ? '取消全选' : '全选' }}</el-button>
        </div>
        <div class="deploy-host-list deploy-host-list--unit" v-loading="hostLoading">
          <label v-for="h in unitHosts(editStep)" :key="h.id" class="deploy-host-item"
            :class="{'deploy-host-item--sel': (editStep.hostIds||[]).includes(h.id)}">
            <el-checkbox :model-value="(editStep.hostIds||[]).includes(h.id)" @change="toggleUnitHost(editStep,h.id)" />
            <span class="deploy-host-name">{{h.name}}</span>
            <span class="mono deploy-host-ip">{{h.ip}}</span>
            <span class="status-badge" :class="h.status"><span class="dot"></span>{{h.status==='online'?'在线':h.status==='offline'?'离线':'未验证'}}</span>
          </label>
          <div v-if="!unitHosts(editStep).length" class="deploy-placeholder">无匹配主机，请勾选本步骤要执行的主机</div>
        </div>
      </div>

      <template v-if="tplVars(editStep.template_id).length">
        <div class="drawer-sec">
          <div class="drawer-sec-title">步骤默认参数</div>
          <div class="orch-step-params">
            <div class="orch-param" v-for="v in tplVars(editStep.template_id)" :key="'d'+v.name">
              <label>{{v.label || v.name}}<b v-if="v.required">*</b></label>
              <el-input size="small" v-model="editStep.params[v.name]" :placeholder="v.default || '模板默认'" />
            </div>
          </div>
        </div>
        <div class="drawer-sec" v-if="(editStep.hostIds||[]).length">
          <div class="drawer-sec-title">逐台覆盖变量
            <el-button size="small" text type="primary" @click="copyFirstHostVars(editStep)">复制首台到其他</el-button>
            <el-button size="small" text @click="clearHostVars(editStep)">清空覆盖</el-button>
          </div>
          <div class="orch-hostvars-rows">
            <div class="orch-hostvar-row" v-for="hid in (editStep.hostIds||[])" :key="'hv'+hid">
              <div class="orch-hostvar-head">
                <span class="deploy-host-name">{{hostName(hid)}}</span>
                <span class="mono deploy-host-ip">{{hostIP(hid)}}</span>
                <span v-if="hostVarCount(editStep,hid)" class="deploy-var-hint">已覆盖 {{hostVarCount(editStep,hid)}} 项</span>
                <el-button size="small" text type="primary" @click="clearHostVar(editStep,hid)">重置</el-button>
              </div>
              <div class="orch-step-params">
                <div class="orch-param" v-for="v in tplVars(editStep.template_id)" :key="'hv'+hid+v.name">
                  <label>{{v.label || v.name}}<b v-if="v.required">*</b></label>
                  <el-input size="small" :model-value="hostVarValue(editStep,hid,v)" @input="val => setHostVar(editStep,hid,v,val)"
                    :placeholder="'继承: ' + stepParam(editStep, v)" />
                </div>
              </div>
            </div>
          </div>
        </div>
      </template>
      <div v-else class="deploy-placeholder deploy-placeholder--ok">该模板无需变量</div>

      <div class="drawer-sec">
        <div class="drawer-sec-title">失败策略</div>
        <div class="orch-fail-row">
          <el-checkbox v-model="editStep.continue_on_error">本步骤失败的仍继续参与后续步骤</el-checkbox>
          <span class="orch-retry">重试
            <el-input-number v-model="editStep.retry_count" size="small" :min="0" :max="5" controls-position="right" style="width:90px" /> 次 ·
            间隔 <el-input-number v-model="editStep.retry_interval_sec" size="small" :min="5" :max="600" controls-position="right" style="width:110px" /> 秒
          </span>
        </div>
      </div>
    </template>
  </el-drawer>

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
      detailCache: {}, // orchId -> [templateName...] 列表页流水线预览
      templates: [], templatesLoading: false, hostOptions: [], hostLoading: false,
      editorVisible: false,
      form: { id: 0, name: '', description: '', steps: [] },
      uidSeed: 1,
      drawerVisible: false, drawerMode: 'edit', editIdx: -1,
      runs: [], runsLoading: false,
      detailVisible: false, activeRun: null, runSteps: [], matrixRows: [], stepSeqs: [],
      logCell: null, logRow: null, sse: null
    }
  },
  computed: {
    formValid() {
      if (!this.form.name || !this.form.steps.length) return false
      return this.form.steps.every(s => s.template_id && (s.hostIds || []).length > 0)
    },
    editStep() {
      if (this.drawerMode !== 'edit' || this.editIdx < 0) return null
      return this.form.steps[this.editIdx] || null
    },
    drawerTitle() {
      if (this.drawerMode === 'add') return '添加步骤 · 选择模板'
      const s = this.editStep
      return s ? ('步骤 ' + (this.editIdx + 1) + ' · ' + s.template_name) : ''
    }
  },
  mounted() { this.loadList(); this.loadRuns() },
  beforeUnmount() { this.closeSse() },
  methods: {
    async loadList() {
      this.loading = true
      try {
        const r = await api.get('/orchestrations')
        if (r.code === 0) {
          this.list = r.data || []
          this.list.forEach(o => this.fetchPipePreview(o))
        }
      } catch(e) {} finally { this.loading = false }
    },
    async fetchPipePreview(o) {
      try {
        const r = await api.get('/orchestrations/' + o.id)
        if (r.code === 0) this.detailCache[o.id] = (r.data.steps || []).map(s => s.template_name)
      } catch(e) {}
    },
    pipePreview(id) { return this.detailCache[id] || [] },
    async loadRuns() {
      this.runsLoading = true
      try { const r = await api.get('/orchestration/runs', { params: { page:1, page_size:20 } }); if (r.code === 0) this.runs = r.data?.list || [] } catch(e) {} finally { this.runsLoading = false }
    },
    async onEditorOpen() {
      if (!this.templates.length) {
        this.templatesLoading = true
        try { const r = await api.get('/deploy/templates'); if (r.code === 0) this.templates = r.data || [] } catch(e) {} finally { this.templatesLoading = false }
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
    /* ===== 编辑器 ===== */
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
          this.editIdx = -1
          this.editorVisible = true
        })
      } else {
        this.form = { id: 0, name: '', description: '', steps: [] }
        this.editIdx = -1
        this.editorVisible = true
      }
    },
    /* ===== 流水线节点 ===== */
    openDrawer(i) { this.editIdx = i; this.drawerMode = 'edit'; this.drawerVisible = true },
    addClick() { this.editIdx = -1; this.drawerMode = 'add'; this.drawerVisible = true },
    pickTemplate(t) {
      this.form.steps.push({
        uid: this.uidSeed++, template_id: t.id, template_name: t.name,
        params: {}, hostIds: [], hostVars: {},
        continue_on_error: false, retry_count: 0, retry_interval_sec: 30, hostFilter: ''
      })
      this.editIdx = this.form.steps.length - 1
      this.drawerMode = 'edit'
    },
    removeStep(i) { this.form.steps.splice(i, 1); if (this.editIdx === i) this.drawerVisible = false; else if (this.editIdx > i) this.editIdx-- },
    moveAt(i, dir) {
      const arr = this.form.steps, j = i + dir
      if (j < 0 || j >= arr.length) return
      ;[arr[i], arr[j]] = [arr[j], arr[i]]
      if (this.editIdx === i) this.editIdx = j
      else if (this.editIdx === j) this.editIdx = i
    },
    moveStep(i, dir) { this.moveAt(i, dir) }, // 兼容旧调用
    /* ===== 抽屉内：主机 ===== */
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
      if (!s.hostIds) s.hostIds = []
      const i = s.hostIds.indexOf(id)
      i >= 0 ? s.hostIds.splice(i, 1) : s.hostIds.push(id)
    },
    toggleAllUnitHosts(s) {
      const list = this.unitHosts(s)
      if (this.unitAllSelected(s)) s.hostIds = (s.hostIds||[]).filter(id => !list.some(h => h.id === id))
      else list.forEach(h => { if (!(s.hostIds||[]).includes(h.id)) s.hostIds.push(h.id) })
    },
    /* ===== 抽屉内：变量 ===== */
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
    hostVarCount(s, hid) { return Object.keys((s.hostVars || {})[String(hid)] || {}).length },
    hostVarTotal(s) { return Object.values(s.hostVars || {}).filter(m => Object.keys(m).length).length },
    clearHostVar(s, hid) { if (s.hostVars) delete s.hostVars[String(hid)] },
    clearHostVars(s) { s.hostVars = {} },
    copyFirstHostVars(s) {
      const ids = s.hostIds || []
      if (ids.length < 2) return
      const src = (s.hostVars || {})[String(ids[0])]
      if (!src || !Object.keys(src).length) { ElMessage.warning('首台主机没有覆盖项'); return }
      ids.slice(1).forEach(hid => { s.hostVars[String(hid)] = { ...src } })
    },
    /* ===== 保存 / 删除 ===== */
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
          const before = this.runs[0]?.id || 0
          const r = await api.post('/orchestrations/' + row.id + '/run', {})
          if (r.code === 0) {
            ElMessage.success('编排已开始执行')
            setTimeout(() => { this.loadRuns().then(() => {
              const fresh = this.runs.find(x => x.id > before && x.orchestration_id === row.id)
              if (fresh) this.openRunDetailById(fresh.id)
            }) }, 500)
          }
        } catch(e) {}
      }).catch(() => {})
    },
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
      }, 300)
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
