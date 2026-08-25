window.DeployPage = {
  props: ['page', 'user', 'versionData'],
  template: `
<div class="deploy-page">
  <section class="deploy-hero">
    <div class="deploy-hero-grid"></div>
    <div class="deploy-hero-glow"></div>
    <div class="deploy-hero-content">
      <span class="deploy-eyebrow">DEPLOY CENTER</span>
      <h1>基础建设</h1>
      <p>选择模板、勾选主机、逐台定制变量、批量执行</p>
    </div>
  </section>

  <!-- 三步向导 -->
  <div class="page-card deploy-wizard">
    <!-- Step 1: 选模板 -->
    <div class="deploy-step" :class="{'deploy-step--active': step===1, 'deploy-step--done': step>1}">
      <div class="deploy-step-num"><span>1</span></div>
      <div class="deploy-step-body">
        <h3>选择模板</h3>
        <el-select v-model="selectedTemplateId" placeholder="请选择部署模板" style="width:100%" @change="onTemplateChange" :disabled="running" :loading="tplLoading" loading-text="模板加载中…" @visible-change="onTplDropdown">
          <el-option v-for="t in templates" :key="t.id" :label="t.name" :value="t.id">
            <span>{{t.name}}</span><span style="float:right;color:var(--text-faint);font-size:12px">{{(t.variables||[]).length}} 个变量</span>
          </el-option>
        </el-select>
        <div v-if="tplLoading && !templates.length" class="deploy-placeholder">模板列表加载中…</div>
        <div v-if="selectedTemplate" class="deploy-tpl-desc">
          <p v-if="selectedTemplate.description">{{selectedTemplate.description}}</p>
          <span v-if="selectedTemplate.variables && selectedTemplate.variables.length" class="deploy-var-count">需要填写 {{selectedTemplate.variables.length}} 个参数</span>
        </div>
      </div>
    </div>

    <!-- Step 2: 勾选主机 -->
    <div class="deploy-step" :class="{'deploy-step--active': step===2, 'deploy-step--done': step>2, 'deploy-step--disabled': step<2}">
      <div class="deploy-step-num"><span>2</span></div>
      <div class="deploy-step-body">
        <h3>选择主机 <span v-if="selectedHosts.length" class="deploy-sel-count">已选 {{selectedHosts.length}} 台</span></h3>
        <div v-if="step<2" class="deploy-placeholder">请先选择模板</div>
        <div v-else>
          <div class="deploy-host-toolbar">
            <el-input v-model="hostFilter" placeholder="搜索主机名 / IP / 标签" clearable size="small" style="width:220px" />
            <el-select v-model="hostSort.field" size="small" style="width:100px"><el-option label="按主机名" value="name" /><el-option label="按 IP" value="ip" /></el-select>
            <el-button size="small" @click="toggleHostSortOrder">{{hostSort.order==='asc' ? '↑ 升序' : '↓ 降序'}}</el-button>
            <el-button size="small" @click="toggleSelectAll">{{ selectedHosts.length === filteredHosts.length ? '取消全选' : '全选' }}</el-button>
          </div>
          <div class="deploy-host-list" v-loading="hostsLoading">
            <label v-for="h in filteredHosts" :key="h.id" class="deploy-host-item" :class="{'deploy-host-item--sel': selectedHostIds.has(h.id)}">
              <el-checkbox :model-value="selectedHostIds.has(h.id)" @change="toggleHost(h.id)" :disabled="running" />
              <span class="deploy-host-name">{{h.name}}</span>
              <span class="mono deploy-host-ip">{{h.ip}}</span>
              <span class="tag-badge other" style="font-size:10px">{{h.tag || 'other'}}</span>
              <span class="status-badge" :class="h.status"><span class="dot"></span>{{h.status==='online'?'在线':h.status==='offline'?'离线':'未验证'}}</span>
            </label>
            <div v-if="!filteredHosts.length && !hostsLoading" class="deploy-placeholder">无匹配主机</div>
          </div>
          <div v-if="selectedHosts.length" class="deploy-step-next">
            <el-button type="primary" @click="step = 3">下一步：逐台变量</el-button>
          </div>
        </div>
      </div>
    </div>

    <!-- Step 3: 逐台变量 -->
    <div class="deploy-step" :class="{'deploy-step--active': step===3, 'deploy-step--disabled': step<3}">
      <div class="deploy-step-num"><span>3</span></div>
      <div class="deploy-step-body">
        <h3>自定义变量 <span v-if="hasTplVars && selectedHosts.length" class="deploy-sel-count">{{selectedHosts.length}} 台主机</span></h3>
        <div v-if="step<3 || !selectedHosts.length" class="deploy-placeholder">请先完成前两步</div>
        <template v-else>
          <!-- 逐主机变量：默认继承模板默认值，可逐台覆盖 -->
          <div v-if="hasTplVars" class="deploy-host-vars">
            <div class="deploy-host-vars-head">
              <span class="deploy-host-vars-title">每台主机单独设置 <span class="deploy-var-hint">留空则使用模板默认值</span></span>
              <div>
                <el-button size="small" @click="resetAllHostParams">全部重置为默认</el-button>
                <el-button size="small" :disabled="selectedHosts.length < 2" @click="copyFirstToAll">复制首台到其他</el-button>
              </div>
            </div>
            <div class="deploy-host-var-row" v-for="h in selectedHosts" :key="h.id">
              <div class="deploy-host-var-rowhead">
                <span class="deploy-host-name">{{h.name}}</span>
                <span class="mono deploy-host-ip">{{h.ip}}</span>
                <span v-if="hostMissingRequired(h).length" class="deploy-var-missing">必填未填</span>
                <el-button size="small" text type="primary" @click="resetHostParams(h.id)">重置本机</el-button>
              </div>
              <div class="deploy-host-var-fields">
                <div class="deploy-host-var-field" v-for="v in selectedTemplate.variables" :key="v.name">
                  <label :title="v.name">{{v.label || v.name}}<b v-if="v.required">*</b></label>
                  <el-input size="small" :model-value="hostParamValue(h.id, v)" @input="val => onHostParamInput(h.id, v, val)" :placeholder="v.default || '继承默认'" />
                </div>
              </div>
            </div>
          </div>
          <div v-else class="deploy-placeholder deploy-placeholder--ok">该模板无需变量，已选 {{selectedHosts.length}} 台主机将执行相同脚本，直接开始部署即可</div>
        </template>
      </div>
    </div>

    <!-- 执行按钮 -->
    <div v-if="step>=3" class="deploy-action-bar">
      <el-button type="primary" size="large" :loading="deploying" :disabled="!varsReady" @click="confirmDeploy">
        开始部署
      </el-button>
      <div v-if="!varsReady" class="deploy-action-hint">还有 {{missingHosts.length}} 台主机的必填变量未填写</div>
    </div>
  </div>

  <!-- 实时进度区 -->
  <div v-if="taskDetail" class="page-card deploy-progress-card">
    <div class="card-header">
      <div>
        <span class="title">部署进度</span>
        <el-tag :type="taskStatusType" size="small" style="margin-left:10px">{{taskStatusText}}</el-tag>
      </div>
      <span class="mono" style="font-size:12px;color:var(--text-faint)">任务 #{{taskDetail.id}}</span>
    </div>
    <div class="deploy-progress-summary">
      <span>总计 <strong>{{taskDetail.total}}</strong></span>
      <span class="deploy-prog-ok">成功 <strong>{{taskDetail.success_cnt}}</strong></span>
      <span class="deploy-prog-fail">失败 <strong>{{taskDetail.fail_cnt}}</strong></span>
    </div>
    <div class="deploy-progress-bar-wrap" v-if="taskDetail.total">
      <div class="deploy-progress-bar">
        <div class="deploy-progress-fill deploy-progress-fill--ok" :style="{width: (taskDetail.success_cnt/taskDetail.total*100)+'%'}"></div>
        <div class="deploy-progress-fill deploy-progress-fill--fail" :style="{width: (taskDetail.fail_cnt/taskDetail.total*100)+'%'}"></div>
      </div>
    </div>
    <el-table :data="taskHosts" size="small" class="deploy-progress-table">
      <el-table-column label="主机" min-width="160">
        <template #default="{row}"><span style="font-weight:500">{{row.host_name}}</span><span class="mono" style="margin-left:6px;font-size:11px;color:var(--text-faint)">{{row.host_ip}}</span></template>
      </el-table-column>
      <el-table-column label="状态" width="100">
        <template #default="{row}"><span class="status-badge" :class="hostStatusCls(row.status)"><span class="dot"></span>{{hostStatusText(row.status)}}</span></template>
      </el-table-column>
      <el-table-column label="输出" min-width="200">
        <template #default="{row}">
          <el-button v-if="row.output || row.error" size="small" text type="primary" @click="row._showOutput = !row._showOutput">{{row._showOutput ? '收起日志' : (row.status === 'running' ? '实时日志' : '查看')}}</el-button>
          <div v-if="row._showOutput" class="deploy-output-pre"><pre>{{ outputText(row) }}</pre></div>
        </template>
      </el-table-column>
    </el-table>
  </div>

  <!-- 历史任务 -->
  <div class="page-card deploy-history">
    <div class="card-header"><span class="title">历史任务</span></div>
    <el-table :data="tasks" style="width:100%" v-loading="tasksLoading" class="deploy-task-table" @row-click="viewTaskDetail">
      <el-table-column label="任务 ID" width="90">
        <template #default="{row}"><span class="mono">#{{row.id}}</span></template>
      </el-table-column>
      <el-table-column label="模板" min-width="140" prop="template_name" />
      <el-table-column label="状态" width="110">
        <template #default="{row}"><el-tag :type="taskTagType(row.status)" size="small">{{taskStatusLabel(row.status)}}</el-tag></template>
      </el-table-column>
      <el-table-column label="进度" width="100">
        <template #default="{row}"><span class="mono">{{row.success_cnt}}/{{row.total}}</span></template>
      </el-table-column>
      <el-table-column label="开始时间" width="170">
        <template #default="{row}"><span class="mono" style="font-size:12px;color:var(--text-faint)">{{formatTime(row.created_at)}}</span></template>
      </el-table-column>
    </el-table>
    <div v-if="!tasksLoading && !tasks.length" class="empty-state"><p>暂无部署记录</p></div>
  </div>

  <!-- 任务详情弹窗 -->
  <el-dialog v-model="detailVisible" :title="'任务详情 #' + (detailTask?.id || '')" width="720px">
    <div v-if="detailTask" class="deploy-detail">
      <div class="deploy-detail-meta">
        <span>模板：{{detailTask.template_name}}</span>
        <el-tag :type="taskTagType(detailTask.status)" size="small">{{taskStatusLabel(detailTask.status)}}</el-tag>
        <span class="mono" style="font-size:12px">{{formatTime(detailTask.created_at)}}</span>
      </div>
      <el-table :data="detailHosts" size="small">
        <el-table-column label="主机" min-width="140">
          <template #default="{row}"><span>{{row.host_name}}</span><span class="mono" style="margin-left:6px;font-size:11px;color:var(--text-faint)">{{row.host_ip}}</span></template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{row}"><span class="status-badge" :class="hostStatusCls(row.status)"><span class="dot"></span>{{hostStatusText(row.status)}}</span></template>
        </el-table-column>
        <el-table-column label="输出" min-width="200">
          <template #default="{row}">
            <el-button v-if="row.output || row.error" size="small" text @click="row._showOutput = !row._showOutput">{{row._showOutput ? '收起' : '查看'}}</el-button>
            <div v-if="row._showOutput" class="deploy-output-pre"><pre>{{row.error || row.output || '无输出'}}</pre></div>
          </template>
        </el-table-column>
      </el-table>
    </div>
  </el-dialog>
</div>`,

  data() {
    return {
      step: 1, templates: [], tplLoading: false, tplLoaded: false, selectedTemplateId: null, selectedTemplate: null,
      hosts: [], hostsLoading: false, hostsLoaded: false, selectedHostIds: new Set(), hostFilter: '', hostSort: { field: 'name', order: 'asc' },
      hostParams: {}, // 逐主机变量覆盖 host_id -> {name: value}；留空字段=继承模板默认
      deploying: false, running: false,
      taskDetail: null, taskHosts: [], taskSse: null,
      tasks: [], tasksLoading: false,
      detailVisible: false, detailTask: null, detailHosts: []
    }
  },
  computed: {
    filteredHosts() {
      let list = this.hosts
      if (this.hostFilter) {
        const kw = this.hostFilter.toLowerCase()
        list = list.filter(h => (h.name||'').toLowerCase().includes(kw) || (h.ip||'').toLowerCase().includes(kw) || (h.tag||'').toLowerCase().includes(kw))
      }
      return this.sortHostList(list, this.hostSort)
    },
    selectedHosts() { return this.hosts.filter(h => this.selectedHostIds.has(h.id)) },
    hasTplVars() { return !!(this.selectedTemplate && this.selectedTemplate.variables && this.selectedTemplate.variables.length) },
    // 存在必填变量未填的主机列表
    missingHosts() {
      if (!this.hasTplVars) return []
      return this.selectedHosts.filter(h => this.hostMissingRequired(h).length > 0)
    },
    varsReady() {
      if (!this.selectedHosts.length || !this.selectedTemplate) return false
      return this.missingHosts.length === 0
    },
    taskStatusType() {
      if (!this.taskDetail) return 'info'
      const s = this.taskDetail.status
      if (s === 'success') return 'success'; if (s === 'failed' || s === 'partial') return 'danger'; return 'warning'
    },
    taskStatusText() {
      if (!this.taskDetail) return ''
      return { running: '执行中', success: '已完成', partial: '部分成功', failed: '失败' }[this.taskDetail.status] || this.taskDetail.status
    }
  },
  mounted() { this.loadTasks() },
  beforeUnmount() { this.closeSse() },
  watch: {
    selectedTemplateId(id) {
      this.step = id ? 2 : 1
      this.selectedTemplate = this.templates.find(t => t.id === id) || null
      this.hostParams = {} // 切换模板后逐台变量全部失效
      if (id) this.loadHosts() // 第二步需要主机列表
    },
    // 进入第三步时若主机列表尚未加载则补拉（直接点步骤条的场景）
    step(v) { if (v === 3 && !this.hostsLoaded && !this.hostsLoading) this.loadHosts() }
  },
  methods: {
    /* ===== 加载（懒加载：模板下拉首次打开 / 首次进入第三步时触发） ===== */
    async loadTemplates() {
      if (this.tplLoading || this.tplLoaded) return
      this.tplLoading = true
      try { const r = await api.get('/deploy/templates'); if (r.code === 0) { this.templates = r.data || []; this.tplLoaded = true } } catch (e) { /* */ } finally { this.tplLoading = false }
    },
    onTplDropdown(visible) { if (visible) this.loadTemplates() },
    async loadHosts() {
      if (this.hostsLoading || this.hostsLoaded) return
      this.hostsLoading = true
      try {
        const r = await api.get('/hosts', { params: { page: 1, page_size: 200, status: 'online' } })
        if (r.code === 0) { this.hosts = r.data?.list || []; this.hostsLoaded = true }
      } catch (e) { /* */ } finally { this.hostsLoading = false }
    },
    async loadTasks() {
      this.tasksLoading = true
      try { const r = await api.get('/deploy/tasks', { params: { page: 1, page_size: 20 } }); if (r.code === 0) this.tasks = r.data?.list || [] } catch (e) { /* */ } finally { this.tasksLoading = false }
    },
    onTemplateChange() { /* handled by watcher */ },
    toggleHostSortOrder() { this.hostSort = { ...this.hostSort, order: this.hostSort.order === 'asc' ? 'desc' : 'asc' } },
    sortHostList(list, sort) {
      const desc = sort.order === 'desc'
      const cmp = sort.field === 'ip' ? cmpHostIP : cmpHostName
      return [...list].sort((a, b) => desc ? cmp(b, a) : cmp(a, b))
    },
    toggleHost(id) { const s = new Set(this.selectedHostIds); s.has(id) ? s.delete(id) : s.add(id); this.selectedHostIds = s },
    toggleSelectAll() {
      if (this.selectedHosts.length === this.filteredHosts.length) { this.selectedHostIds = new Set() }
      else { this.selectedHostIds = new Set(this.filteredHosts.map(h => h.id)) }
    },
    /* ===== 逐主机变量 ===== */
    hostParamValue(hostId, v) {
      const hp = this.hostParams[hostId]
      if (hp && Object.prototype.hasOwnProperty.call(hp, v.name)) return hp[v.name]
      return v.default || ''
    },
    // 该主机缺失的必填变量名列表
    hostMissingRequired(h) {
      if (!this.hasTplVars) return []
      return (this.selectedTemplate.variables || [])
        .filter(v => v.required && String(this.hostParamValue(h.id, v)).trim() === '')
        .map(v => v.label || v.name)
    },
    onHostParamInput(hostId, v, val) {
      const cur = this.hostParams[hostId] ? { ...this.hostParams[hostId] } : {}
      if (val === '' || val == null) delete cur[v.name]
      else cur[v.name] = val
      this.hostParams = { ...this.hostParams, [hostId]: cur }
    },
    resetHostParams(hostId) {
      if (!this.hostParams[hostId]) return
      const next = { ...this.hostParams }; delete next[hostId]; this.hostParams = next
    },
    resetAllHostParams() { this.hostParams = {} },
    copyFirstToAll() {
      const first = this.selectedHosts[0]; if (!first) return
      const src = {}
      ;(this.selectedTemplate.variables || []).forEach(v => { src[v.name] = this.hostParamValue(first.id, v) })
      const next = { ...this.hostParams }
      this.selectedHosts.forEach(h => { if (h.id !== first.id) next[h.id] = { ...src } })
      this.hostParams = next
    },
    buildHostParams() {
      const o = {}
      this.selectedHosts.forEach(h => { if (this.hostParams[h.id]) o[h.id] = this.hostParams[h.id] })
      return o
    },
    /* ===== 部署 ===== */
    async confirmDeploy() {
      if (!this.selectedHosts.length) { ElMessage.warning('请至少选择一台主机'); return }
      try {
        await ElMessageBox.confirm(
          '即将对 ' + this.selectedHosts.length + ' 台主机执行模板「' + this.selectedTemplate.name + '」，确认？',
          '确认部署', { type: 'warning', confirmButtonText: '确认执行', cancelButtonText: '取消' }
        )
      } catch (e) { return }
      this.deploying = true
      try {
        const r = await api.post('/deploy/run', {
          template_id: this.selectedTemplateId,
          // 按页面当前显示顺序提交（排序后全选时序号跟随该顺序，供 {{__seq}} 批量命名使用）
          host_ids: this.filteredHosts.filter(h => this.selectedHostIds.has(h.id)).map(h => h.id),
          host_params: this.buildHostParams()
        })
        if (r.code === 0) {
          ElMessage.success('部署任务已创建')
          this.taskDetail = { id: r.data.task_id, status: 'running', total: this.selectedHosts.length, success_cnt: 0, fail_cnt: 0 }
          this.taskHosts = this.selectedHosts.map(h => ({ host_id: h.id, host_name: h.name, host_ip: h.ip, status: 'pending', output: '', error: '' }))
          this.connectTaskSse(r.data.task_id)
          this.running = true; this.step = 4
          this.loadTasks()
        }
      } catch (e) { ElMessage.error(e.response?.data?.message || '部署失败') } finally { this.deploying = false }
    },
    /* ===== SSE 进度 ===== */
    connectSse(taskId) { this.connectTaskSse(taskId) },
    connectTaskSse(taskId) {
      this.closeSse()
      const source = new EventSource('/api/sse/deploy?task_id=' + taskId, { withCredentials: true })
      this.taskSse = source
      source.addEventListener('progress', (e) => {
        try {
          const d = JSON.parse(e.data)
          this.taskDetail.total = d.total || this.taskDetail.total
          this.taskDetail.success_cnt = d.success_cnt ?? this.taskDetail.success_cnt
          this.taskDetail.fail_cnt = d.fail_cnt ?? this.taskDetail.fail_cnt
          this.taskDetail.status = d.task_status || 'running'
          const h = this.taskHosts.find(x => x.host_id === d.host_id)
          if (h) {
            if (d.status === 'output') {
              // 执行过程中的增量日志，实时追加
              h.output = (h.output || '') + (d.output || '')
            } else {
              h.status = d.status
              if (d.output) h.output = d.output  // 终态用全量输出覆盖，避免重复
              h.error = d.error || ''
            }
          }
        } catch (err) { /* */ }
      })
      source.addEventListener('done', (e) => {
        try {
          const d = JSON.parse(e.data)
          if (d && d.task_status) {
            this.taskDetail.total = d.total || this.taskDetail.total
            this.taskDetail.success_cnt = d.success_cnt
            this.taskDetail.fail_cnt = d.fail_cnt
            this.taskDetail.status = d.task_status
          }
        } catch (err) { /* */ }
        this.running = false; this.closeSse(); this.loadTasks()
      })
      source.onerror = () => { /* EventSource auto-reconnect */ }
    },
    closeSse() { if (this.taskSse) { this.taskSse.close(); this.taskSse = null } },
    /* ===== 任务详情 ===== */
    async viewTaskDetail(row) {
      try {
        const r = await api.get('/deploy/tasks/' + row.id)
        if (r.code === 0) {
          this.detailTask = r.data
          this.detailHosts = (r.data.hosts || []).map(h => ({...h, _showOutput: false}))
          this.detailVisible = true
          if (r.data.status === 'running') this.connectTaskSse(r.data.id)
        }
      } catch (e) { ElMessage.error('加载任务详情失败') }
    },
    hostStatusCls(s) { if (s === 'success') return 'online'; if (s === 'failed') return 'offline'; if (s === 'running') return 'running'; return 'unverified' },
    outputText(row) { const txt = (row.error ? row.error + '\n' : '') + (row.output || ''); return txt || '无输出' },
    hostStatusText(s) { return { pending: '等待中', running: '执行中', success: '成功', failed: '失败' }[s] || s },
    taskTagType(s) { if (s === 'success') return 'success'; if (s === 'failed' || s === 'partial') return 'danger'; if (s === 'running') return 'warning'; return 'info' },
    taskStatusLabel(s) { return { running: '执行中', success: '已完成', partial: '部分成功', failed: '失败' }[s] || s },
    formatTime(t) {
      if (!t) return '-'
      const d = new Date(t.replace(' ', 'T') + (t.includes('Z') ? '' : 'Z'))
      return d.getFullYear() + '-' + String(d.getMonth()+1).padStart(2,'0') + '-' + String(d.getDate()).padStart(2,'0') + ' ' + String(d.getHours()).padStart(2,'0') + ':' + String(d.getMinutes()).padStart(2,'0')
    }
  }
}
