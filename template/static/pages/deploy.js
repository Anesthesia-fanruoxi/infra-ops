window.DeployPage = {
  props: ['page', 'user', 'versionData'],
  template: `
<div class="deploy-page">
  <section class="deploy-hero">
    <div class="deploy-hero-grid"></div>
    <div class="deploy-hero-glow"></div>
    <div class="deploy-hero-content">
      <div class="deploy-hero-row">
        <div>
          <span class="deploy-eyebrow">DEPLOY CENTER</span>
          <h1>基础建设</h1>
          <p>执行记录回溯 · 三步创建批量部署</p>
        </div>
        <el-button type="primary" size="large" class="deploy-new-btn" @click="openWizard">
          <el-icon style="margin-right:6px"><Plus /></el-icon>新建任务
        </el-button>
      </div>
    </div>
  </section>

  <!-- 新建任务：三步引导弹框 -->
  <el-dialog v-model="wizardVisible" title="新建部署任务" width="900px" :close-on-click-modal="false" @closed="resetWizard">
    <el-steps :active="step - 1" finish-status="success" align-center style="margin-bottom:20px">
      <el-step title="选择模板" />
      <el-step title="选择主机" />
      <el-step title="自定义变量" />
    </el-steps>

    <!-- Step 1: 选模板 -->
    <div v-show="step===1">
      <el-select v-model="selectedTemplateId" placeholder="请选择部署模板" style="width:100%" @change="onTemplateChange" :loading="tplLoading" loading-text="模板加载中…" @visible-change="onTplDropdown" :teleported="false">
        <el-option v-for="t in templates" :key="t.id" :label="t.name" :value="t.id">
          <span>{{t.name}}</span><span style="float:right;color:var(--text-faint);font-size:12px">{{(t.variables||[]).length}} 个变量</span>
        </el-option>
      </el-select>
      <div v-if="tplLoading && !templates.length" class="deploy-placeholder">模板列表加载中…</div>
      <div v-if="selectedTemplate" class="deploy-tpl-desc">
        <p v-if="selectedTemplate.description">{{selectedTemplate.description}}</p>
        <span v-if="(selectedTemplate.variables||[]).length" class="deploy-var-count">包含 {{selectedTemplate.variables.length}} 个变量，可在第三步逐台填写</span>
      </div>
    </div>

    <!-- Step 2: 勾选主机 -->
    <div v-show="step===2">
      <div class="deploy-host-toolbar">
        <el-input v-model="hostFilter" placeholder="搜索主机名 / IP / 标签" clearable size="small" style="width:220px" />
        <el-select v-model="hostSort.field" size="small" style="width:100px" :teleported="false"><el-option label="按主机名" value="name" /><el-option label="按 IP" value="ip" /></el-select>
        <el-button size="small" @click="toggleHostSortOrder">{{hostSort.order==='asc' ? '↑ 升序' : '↓ 降序'}}</el-button>
        <el-button size="small" @click="toggleSelectAll">{{ selectedHosts.length === filteredHosts.length ? '取消全选' : '全选' }}</el-button>
        <span class="deploy-sel-count">已选 {{selectedHosts.length}} 台</span>
      </div>
      <div class="deploy-host-list deploy-host-list--dialog" v-loading="hostsLoading">
        <label v-for="h in filteredHosts" :key="h.id" class="deploy-host-item" :class="{'deploy-host-item--sel': selectedHostIds.has(h.id)}">
          <el-checkbox :model-value="selectedHostIds.has(h.id)" @change="toggleHost(h.id)" />
          <span class="deploy-host-name">{{h.name}}</span>
          <span class="mono deploy-host-ip">{{h.ip}}</span>
          <span class="tag-badge other" style="font-size:10px">{{h.tag || 'other'}}</span>
          <span class="status-badge" :class="h.status"><span class="dot"></span>{{h.status==='online'?'在线':h.status==='offline'?'离线':'未验证'}}</span>
        </label>
        <div v-if="!filteredHosts.length && !hostsLoading" class="deploy-placeholder">无匹配主机</div>
      </div>
    </div>

    <!-- Step 3: 逐台变量 + 自定义配置 -->
    <div v-show="step===3">
      <!-- 任务级自定义配置：留空则使用脚本默认配置；提供内容则整体覆盖对应配置文件 -->
      <div v-if="selectedConfigs.length" class="deploy-cfgs-task">
        <div class="deploy-host-vars-head">
          <span class="deploy-host-vars-title">自定义配置 <span class="deploy-var-hint">任务级默认 · 留空则使用脚本默认配置；粘贴完整内容将整体覆盖对应配置文件</span></span>
        </div>
        <div class="deploy-cfgs-fields">
          <div class="deploy-cfgs-field" v-for="c in selectedConfigs" :key="c.key">
            <label>{{c.label}}</label>
            <el-input type="textarea" :rows="4" class="mono deploy-cfgs-input" :model-value="taskConfigs[c.key] || ''" @input="val => onTaskConfig(c.key, val)" :placeholder="c.hint" />
          </div>
        </div>
      </div>

      <template v-if="hasTplVars">
        <div class="deploy-host-vars-head" style="margin-bottom:10px">
          <span class="deploy-host-vars-title">每台主机单独设置 <span class="deploy-var-hint">留空则使用模板默认值</span></span>
          <div>
            <el-button size="small" @click="resetAllHostParams">全部重置为默认</el-button>
            <el-button size="small" :disabled="selectedHosts.length < 2" @click="copyFirstToAll">复制首台到其他</el-button>
          </div>
        </div>
        <div class="deploy-host-vars-list">
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
            <div v-if="selectedConfigs.length" class="deploy-host-cfg-fields">
              <div class="deploy-cfgs-field deploy-cfgs-field--host" v-for="c in selectedConfigs" :key="c.key">
                <label>自定义 · {{c.label}}（覆盖任务级）</label>
                <el-input type="textarea" :rows="3" class="mono deploy-cfgs-input" :model-value="configValue(h.id, c.key)" @input="val => onHostConfig(h.id, c.key, val)" :placeholder="(taskConfigs[c.key] ? '继承任务级配置' : c.hint)" />
              </div>
            </div>
          </div>
        </div>
      </template>
      <div v-else class="deploy-placeholder deploy-placeholder--ok">该模板无需变量，已选 {{selectedHosts.length}} 台主机将执行相同脚本，直接开始部署即可</div>
    </div>

    <template #footer>
      <div class="deploy-wizard-footer">
        <span v-if="step===3 && hasTplVars && missingHosts.length" class="deploy-action-hint-inline">还有 {{missingHosts.length}} 台主机的必填变量未填写</span>
        <div>
          <el-button @click="wizardVisible=false">取消</el-button>
          <el-button v-if="step>1" @click="step--">上一步</el-button>
          <el-button v-if="step===1" type="primary" :disabled="!selectedTemplateId" @click="goStep(2)">下一步</el-button>
          <el-button v-if="step===2" type="primary" :disabled="!selectedHosts.length" @click="goStep(3)">下一步</el-button>
          <el-button v-if="step===3" type="primary" :loading="deploying" :disabled="!varsReady" @click="confirmDeploy">开始部署</el-button>
        </div>
      </div>
    </template>
  </el-dialog>

  <!-- 执行记录列表 -->
  <div class="page-card deploy-history">
    <div class="card-header">
      <div class="deploy-history-head">
        <span class="title">执行记录</span>
        <el-tag v-if="runningCount" type="warning" size="small" class="deploy-running-tag">
          <span class="dot running"></span>{{runningCount}} 个任务运行中
        </el-tag>
      </div>
      <el-button size="small" text @click="loadTasks"><el-icon style="margin-right:4px"><Refresh /></el-icon>刷新</el-button>
    </div>
    <el-table :data="tasks" style="width:100%" v-loading="tasksLoading" class="deploy-task-table">
      <el-table-column label="记录 ID" width="90">
        <template #default="{row}"><span class="mono">#{{row.id}}</span></template>
      </el-table-column>
      <el-table-column label="模板" min-width="160" prop="template_name" />
      <el-table-column label="状态" width="110">
        <template #default="{row}"><el-tag :type="taskTagType(row.status)" size="small">{{taskStatusLabel(row.status)}}</el-tag></template>
      </el-table-column>
      <el-table-column label="成功" width="76" align="center">
        <template #default="{row}"><span class="ok deploy-cnt">{{row.success_cnt}}</span><span class="mono faint">/{{row.total}}</span></template>
      </el-table-column>
      <el-table-column label="失败" width="70" align="center">
        <template #default="{row}"><span class="fail deploy-cnt">{{row.fail_cnt}}</span></template>
      </el-table-column>
      <el-table-column label="开始时间" width="170">
        <template #default="{row}"><span class="mono" style="font-size:12px;color:var(--text-faint)">{{formatTime(row.created_at)}}</span></template>
      </el-table-column>
      <el-table-column label="" width="100" fixed="right">
        <template #default="{row}"><el-button size="small" text type="primary" @click="openDrawer(row)">查看记录</el-button></template>
      </el-table-column>
    </el-table>
    <div v-if="!tasksLoading && !tasks.length" class="empty-state"><p>暂无执行记录</p></div>
  </div>

  <!-- 执行记录详情抽屉：主机状态 + 实时日志 -->
  <el-drawer v-model="drawerVisible" :title="'执行记录 #' + (recordMeta?.id || '')" size="44%" class="deploy-drawer" @closed="closeDrawer">
    <div v-if="recordMeta" class="deploy-drawer-meta">
      <span class="deploy-drawer-meta-tpl">{{recordMeta.template_name || '模板未记录'}}</span>
      <el-tag :type="taskTagType(recordMeta.status)" size="small">{{taskStatusLabel(recordMeta.status)}}</el-tag>
      <span v-if="recordMeta.created_at" class="mono faint deploy-drawer-meta-time">{{formatTime(recordMeta.created_at)}}</span>
    </div>
    <div v-if="recordHosts.length" class="deploy-drawer-hostlist">
      <div class="deploy-drawer-host" v-for="h in recordHosts" :key="h.id">
        <span class="status-badge" :class="hostStatusCls(h.status)"><span class="dot"></span>{{hostStatusText(h.status)}}</span>
        <span class="deploy-host-name">{{h.host_name}}</span>
        <span class="mono deploy-host-ip">{{h.host_ip}}</span>
      </div>
    </div>

    <div class="drawer-sec deploy-log-sec">
      <div class="drawer-sec-title">运行日志</div>
      <div class="orch-log-head">
        <span class="faint deploy-log-count">{{recordLogs.length}} 行</span>
        <div class="deploy-log-filter">
          <el-input v-model="logFilter" placeholder="过滤：主机 IP 或内容关键词" clearable size="small" style="width:220px" />
        </div>
      </div>
      <div class="deploy-log-box" ref="deployLogBox" @scroll="onLogScroll">
        <div v-if="!filteredLogs.length" class="deploy-log-empty">暂无日志</div>
        <div v-for="l in filteredLogs" :key="l.id" class="deploy-log-row">
          <span class="deploy-log-time">{{logTime(l.ts)}}</span>
          <span class="deploy-log-ip">{{l.ip}}</span>
          <span class="deploy-log-text">{{l.text}}</span>
        </div>
      </div>
    </div>
  </el-drawer>
</div>`,

  data() {
    return {
      step: 1, templates: [], tplLoading: false, tplLoaded: false, selectedTemplateId: null, selectedTemplate: null,
      hosts: [], hostsLoading: false, hostsLoaded: false, selectedHostIds: new Set(), hostFilter: '', hostSort: { field: 'name', order: 'asc' },
      hostParams: {}, // 逐主机变量覆盖 host_id -> {name: value}；留空字段=继承模板默认
      taskConfigs: {}, // 任务级自定义配置 config_key -> 内容（留空=用脚本默认）
      hostConfigs: {}, // 主机级自定义配置覆盖 host_id -> {key: content}
      deploying: false,
      wizardVisible: false,
      tasks: [], tasksLoading: false,
      // 执行记录抽屉
      drawerVisible: false, recordMeta: null, recordHosts: [], recordLogs: [], logFilter: '',
      sseLog: null, setupSse: null, logAutoScroll: true
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
    // 模板声明的可覆盖配置文件
    selectedConfigs() { return (this.selectedTemplate && this.selectedTemplate.configs) || [] },
    // 存在必填变量未填的主机列表
    missingHosts() {
      if (!this.hasTplVars) return []
      return this.selectedHosts.filter(h => this.hostMissingRequired(h).length > 0)
    },
    varsReady() {
      if (!this.selectedHosts.length || !this.selectedTemplate) return false
      return this.missingHosts.length === 0
    },
    runningCount() { return this.tasks.filter(t => t.status === 'running').length },
    filteredLogs() {
      if (!this.logFilter) return this.recordLogs
      const kw = this.logFilter.toLowerCase()
      return this.recordLogs.filter(l => (l.ip||'').toLowerCase().includes(kw) || (l.text||'').toLowerCase().includes(kw))
    }
  },
  mounted() { this.loadTasks(); this.connectSetup() },
  beforeUnmount() { this.closeDrawer(); this.closeSetup() },
  watch: {
    selectedTemplateId(id) {
      this.step = id ? 2 : 1
      this.selectedTemplate = this.templates.find(t => t.id === id) || null
      this.hostParams = {} // 切换模板后逐台变量全部失效
      this.taskConfigs = {}; this.hostConfigs = {} // 自定义配置同理
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
    /* ===== 自定义配置 ===== */
    onTaskConfig(key, val) {
      const n = { ...this.taskConfigs }
      if (val == null || val === '') delete n[key]
      else n[key] = val
      this.taskConfigs = n
    },
    configValue(hostId, key) { return (this.hostConfigs[hostId] && this.hostConfigs[hostId][key]) || '' },
    onHostConfig(hostId, key, val) {
      const cur = this.hostConfigs[hostId] ? { ...this.hostConfigs[hostId] } : {}
      if (val == null || val === '') delete cur[key]
      else cur[key] = val
      this.hostConfigs = { ...this.hostConfigs, [hostId]: cur }
    },
    buildTaskConfigs() { return { ...this.taskConfigs } },
    buildHostConfigs() {
      const o = {}
      this.selectedHosts.forEach(h => { if (this.hostConfigs[h.id] && Object.keys(this.hostConfigs[h.id]).length) o[h.id] = { ...this.hostConfigs[h.id] } })
      return o
    },
    /* ===== 新建任务向导 ===== */
    openWizard() {
      this.loadTemplates()
      this.wizardVisible = true
    },
    goStep(n) {
      if (n >= 2) this.loadHosts() // 主机列表懒加载（已加载则跳过）
      this.step = n
    },
    resetWizard() {
      this.step = 1
      this.selectedTemplateId = null
      this.selectedTemplate = null
      this.hostParams = {}
      this.taskConfigs = {}; this.hostConfigs = {}
      this.selectedHostIds = new Set()
      this.hostFilter = ''
    },
    /* ===== 部署（新建任务 → 新执行记录） ===== */
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
          host_params: this.buildHostParams(),
          configs: this.buildTaskConfigs(),
          host_configs: this.buildHostConfigs()
        })
        if (r.code === 0) {
          ElMessage.success('部署任务已创建')
          this.wizardVisible = false
          this.loadTasks()
          this.openDrawer({ id: r.data.task_id, status: 'running' }) // 立即打开该执行记录，实时查看日志
        }
      } catch (e) { ElMessage.error(e.response?.data?.message || '部署失败') } finally { this.deploying = false }
    },
    /* ===== setup sse：常驻执行记录状态流（运行中→终态），驱动列表刷新 ===== */
    connectSetup() {
      this.closeSetup()
      const source = new EventSource('/api/sse/deploy/setup', { withCredentials: true })
      this.setupSse = source
      source.addEventListener('init', (e) => {
        try {
          const d = JSON.parse(e.data)
          if (!d.idle && (d.running || []).length) { this.markRunning(d.running); this.refreshTaskRow(d) }
        } catch (err) { /* */ }
      })
      source.addEventListener('track', (e) => {
        try { const d = JSON.parse(e.data); this.patchTaskRow(d) } catch (err) { /* */ }
      })
      source.addEventListener('done', (e) => {
        try { const d = JSON.parse(e.data); this.patchTaskRow(d) } catch (err) { /* */ }
        this.loadTasks() // 任务结束，刷新列表翻转终态
      })
      source.onerror = () => { /* EventSource auto-reconnect，重连走默认即可 */ }
    },
    // init 里带来了运行中快照：将其并入列表（补缺即可，不整体刷新以免闪动）
    markRunning(running) {
      const have = new Set(this.tasks.map(t => t.id))
      const need = running.filter(t => !have.has(t.task_id))
      if (need.length) this.loadTasks()
    },
    refreshTaskRow(d) {
      if (!d.running || !this.tasks.length) return
      d.running.forEach(r => {
        const row = this.tasks.find(t => t.id === r.task_id)
        if (row) { row.status = 'running'; row.success_cnt = r.success_cnt; row.fail_cnt = r.fail_cnt; row.total = r.total }
      })
    },
    // track/done：更新列表里对应记录行的计数与状态（不重拉）
    patchTaskRow(d) {
      const row = this.tasks.find(t => t.id === d.task_id)
      if (!row) return
      if (d.success_cnt != null) row.success_cnt = d.success_cnt
      if (d.fail_cnt != null) row.fail_cnt = d.fail_cnt
      if (d.total != null) row.total = d.total
      if (d.status && d.status !== 'running') row.status = d.status
    },
    closeSetup() { if (this.setupSse) { this.setupSse.close(); this.setupSse = null } },
    /* ===== 执行记录抽屉 ===== */
    async openDrawer(row) {
      this.drawerVisible = true
      this.recordMeta = { id: row.id, status: row.status || 'running' }
      this.recordHosts = []
      this.logFilter = ''
      this.disconnectLog()
      try {
        const r = await api.get('/deploy/tasks/' + row.id)
        if (r.code === 0) {
          this.recordMeta = { id: r.data.id, template_name: r.data.template_name, status: r.data.status, created_at: r.data.created_at }
          this.recordHosts = (r.data.hosts || []).map(h => ({ ...h }))
        }
      } catch (e) { /* */ }
      this.connectLog(row.id)
    },
    closeDrawer() {
      this.disconnectLog()
      this.drawerVisible = false
      this.recordMeta = null; this.recordHosts = []; this.recordLogs = []; this.logFilter = ''
    },
    connectLog(taskId) {
      this.disconnectLog()
      const source = new EventSource('/api/sse/deploy/log?task_id=' + taskId, { withCredentials: true })
      this.sseLog = source
      source.addEventListener('init', (e) => {
        try {
          const d = JSON.parse(e.data)
          this.recordLogs = (d.logs || []).map(l => ({ id: l.id, ts: l.ts, ip: l.ip, text: l.text }))
          if (d.task_status && d.task_status !== 'running') this.disconnectLog()
          this.$nextTick(() => this.scrollLogsBottom())
        } catch (err) { /* */ }
      })
      source.addEventListener('log', (e) => {
        try {
          const l = JSON.parse(e.data)
          this.recordLogs.push({ id: l.id, ts: l.ts, ip: l.ip, text: l.text })
          this.$nextTick(() => this.scrollLogsBottom())
        } catch (err) { /* */ }
      })
      source.addEventListener('done', (e) => {
        try { const d = JSON.parse(e.data); if (d.task_status) this.recordMeta.status = d.task_status } catch (err) { /* */ }
        this.disconnectLog(); this.loadTasks()
      })
      source.onerror = () => { /* */ }
    },
    disconnectLog() { if (this.sseLog) { this.sseLog.close(); this.sseLog = null } },
    /* ===== 日志滚动 ===== */
    onLogScroll(e) {
      const el = e.target
      this.logAutoScroll = el.scrollHeight - el.scrollTop - el.clientHeight < 30
    },
    scrollLogsBottom() {
      if (!this.logAutoScroll) return
      const el = this.$refs.deployLogBox
      if (el) el.scrollTop = el.scrollHeight
    },
    /* ===== 渲染辅助 ===== */
    hostStatusCls(s) { if (s === 'success') return 'online'; if (s === 'failed') return 'offline'; if (s === 'running') return 'running'; return 'unverified' },
    hostStatusText(s) { return { pending: '等待中', running: '执行中', success: '成功', failed: '失败' }[s] || s },
    logTime(ts) { return ts ? (ts.length >= 19 ? ts.slice(11, 19) : ts) : '' },
    taskTagType(s) { if (s === 'success') return 'success'; if (s === 'failed' || s === 'partial') return 'danger'; if (s === 'running') return 'warning'; return 'info' },
    taskStatusLabel(s) { return { running: '执行中', success: '已完成', partial: '部分成功', failed: '失败' }[s] || s },
    formatTime(t) {
      if (!t) return '-'
      const d = new Date(t.replace(' ', 'T') + (t.includes('Z') ? '' : 'Z'))
      return d.getFullYear() + '-' + String(d.getMonth()+1).padStart(2,'0') + '-' + String(d.getDate()).padStart(2,'0') + ' ' + String(d.getHours()).padStart(2,'0') + ':' + String(d.getMinutes()).padStart(2,'0')
    }
  }
}