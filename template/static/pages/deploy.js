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
      <p>选择模板、配置参数、批量执行部署任务</p>
    </div>
  </section>

  <!-- 三步向导 -->
  <div class="page-card deploy-wizard">
    <!-- Step 1: 选模板 -->
    <div class="deploy-step" :class="{'deploy-step--active': step===1, 'deploy-step--done': step>1}">
      <div class="deploy-step-num"><span>1</span></div>
      <div class="deploy-step-body">
        <h3>选择模板</h3>
        <el-select v-model="selectedTemplateId" placeholder="请选择部署模板" style="width:100%" @change="onTemplateChange" :disabled="running">
          <el-option v-for="t in templates" :key="t.id" :label="t.name" :value="t.id">
            <span>{{t.name}}</span><span style="float:right;color:var(--text-faint);font-size:12px">{{(t.variables||[]).length}} 个变量</span>
          </el-option>
        </el-select>
        <div v-if="selectedTemplate" class="deploy-tpl-desc">
          <p v-if="selectedTemplate.description">{{selectedTemplate.description}}</p>
          <span v-if="selectedTemplate.variables && selectedTemplate.variables.length" class="deploy-var-count">需要填写 {{selectedTemplate.variables.length}} 个参数</span>
        </div>
      </div>
    </div>

    <!-- Step 2: 填参数 -->
    <div class="deploy-step" :class="{'deploy-step--active': step===2, 'deploy-step--done': step>2, 'deploy-step--disabled': step<2}">
      <div class="deploy-step-num"><span>2</span></div>
      <div class="deploy-step-body">
        <h3>配置参数</h3>
        <div v-if="step<2" class="deploy-placeholder">请先选择模板</div>
        <div v-else-if="!selectedTemplate.variables || !selectedTemplate.variables.length" class="deploy-placeholder deploy-placeholder--ok">该模板无需参数</div>
        <div v-else class="deploy-params">
          <el-form label-position="top" size="default">
            <el-form-item v-for="(v,i) in selectedTemplate.variables" :key="v.name" :label="v.label || v.name" :required="v.required">
              <el-input v-model="params[v.name]" :placeholder="v.default || '请输入' + (v.label || v.name)" />
              <div v-if="v.default" class="deploy-param-hint">默认值：{{v.default}}</div>
            </el-form-item>
          </el-form>
        </div>
      </div>
    </div>

    <!-- Step 3: 选主机 + 执行 -->
    <div class="deploy-step" :class="{'deploy-step--active': step===3, 'deploy-step--disabled': step<3}">
      <div class="deploy-step-num"><span>3</span></div>
      <div class="deploy-step-body">
        <h3>选择主机 <span v-if="selectedHosts.length" class="deploy-sel-count">已选 {{selectedHosts.length}} 台</span></h3>
        <div v-if="step<3" class="deploy-placeholder">请先完成前两步</div>
        <div v-else>
          <div class="deploy-host-toolbar">
            <el-input v-model="hostFilter" placeholder="搜索主机名 / IP / 标签" clearable size="small" style="width:220px" />
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
        </div>
      </div>
    </div>

    <!-- 执行按钮 -->
    <div v-if="step>=3" class="deploy-action-bar">
      <el-button type="primary" size="large" :loading="deploying" :disabled="!selectedHosts.length" @click="confirmDeploy">
        开始部署
      </el-button>
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
          <el-button v-if="row.output || row.error" size="small" text @click="row._showOutput = !row._showOutput">{{row._showOutput ? '收起' : '查看'}}</el-button>
          <div v-if="row._showOutput" class="deploy-output-pre"><pre>{{row.error || row.output || '无输出'}}</pre></div>
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
      step: 1, templates: [], selectedTemplateId: null, selectedTemplate: null,
      params: {}, hosts: [], hostsLoading: false, selectedHostIds: new Set(), hostFilter: '',
      deploying: false, running: false,
      taskDetail: null, taskHosts: [], taskSse: null,
      tasks: [], tasksLoading: false,
      detailVisible: false, detailTask: null, detailHosts: []
    }
  },
  computed: {
    filteredHosts() {
      if (!this.hostFilter) return this.hosts
      const kw = this.hostFilter.toLowerCase()
      return this.hosts.filter(h => (h.name||'').toLowerCase().includes(kw) || (h.ip||'').toLowerCase().includes(kw) || (h.tag||'').toLowerCase().includes(kw))
    },
    selectedHosts() { return this.hosts.filter(h => this.selectedHostIds.has(h.id)) },
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
  mounted() { this.loadTemplates(); this.loadHosts(); this.loadTasks() },
  beforeUnmount() { this.closeSse() },
  watch: {
    selectedTemplateId(id) {
      this.step = id ? 2 : 1
      this.selectedTemplate = this.templates.find(t => t.id === id) || null
      this.params = {}
      if (this.selectedTemplate?.variables) {
        this.selectedTemplate.variables.forEach(v => { this.params[v.name] = v.default || '' })
      }
    }
  },
  methods: {
    /* ===== 加载 ===== */
    async loadTemplates() {
      try { const r = await api.get('/deploy/templates'); if (r.code === 0) this.templates = r.data || [] } catch (e) { /* */ }
    },
    async loadHosts() {
      this.hostsLoading = true
      try { const r = await api.get('/hosts', { params: { page: 1, page_size: 200, status: 'online' } }); if (r.code === 0) this.hosts = r.data?.list || [] } catch (e) { /* */ } finally { this.hostsLoading = false }
    },
    async loadTasks() {
      this.tasksLoading = true
      try { const r = await api.get('/deploy/tasks', { params: { page: 1, page_size: 20 } }); if (r.code === 0) this.tasks = r.data?.list || [] } catch (e) { /* */ } finally { this.tasksLoading = false }
    },
    onTemplateChange() { /* handled by watcher */ },
    toggleHost(id) { const s = new Set(this.selectedHostIds); s.has(id) ? s.delete(id) : s.add(id); this.selectedHostIds = s; this.step = 3 },
    toggleSelectAll() {
      if (this.selectedHosts.length === this.filteredHosts.length) { this.selectedHostIds = new Set() }
      else { this.selectedHostIds = new Set(this.filteredHosts.map(h => h.id)); this.step = 3 }
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
          host_ids: this.selectedHosts.map(h => h.id),
          params: this.params
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
          this.taskDetail.success_cnt = d.success_cnt
          this.taskDetail.fail_cnt = d.fail_cnt
          this.taskDetail.status = d.task_status || 'running'
          const h = this.taskHosts.find(x => x.host_id === d.host_id)
          if (h) { h.status = d.status; h.output = d.output || ''; h.error = d.error || '' }
          if (d.task_status === 'done' || d.task_status === 'success' || d.task_status === 'failed' || d.task_status === 'partial') {
            this.running = false; this.closeSse(); this.loadTasks()
          }
        } catch (err) { /* */ }
      })
      source.addEventListener('done', () => { this.running = false; this.closeSse(); this.loadTasks() })
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
    hostStatusCls(s) { if (s === 'success') return 'online'; if (s === 'failed') return 'offline'; return 'unverified' },
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
