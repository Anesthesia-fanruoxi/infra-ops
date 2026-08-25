window.SchedulesPage = {
  props: ['page', 'user', 'versionData'],
  template: `
<div class="sched-page">
  <section class="sched-hero">
    <div class="sched-hero-grid"></div>
    <div class="sched-hero-glow"></div>
    <div class="sched-hero-content">
      <span class="sched-eyebrow">SCHEDULED TASKS</span>
      <h1>定时任务</h1>
      <p>周期性执行部署模板，自动化运维操作</p>
    </div>
    <div class="sched-hero-stats">
      <div class="sched-hero-stat"><span class="sched-hero-stat-val">{{list.length}}</span><span class="sched-hero-stat-lbl">任务总数</span></div>
      <div class="sched-hero-stat"><span class="sched-hero-stat-val">{{enabledCount}}</span><span class="sched-hero-stat-lbl">已启用</span></div>
    </div>
  </section>

  <div class="page-card sched-panel">
    <div class="card-header">
      <span class="title">任务列表</span>
      <el-button type="primary" @click="openDialog(null)">新增定时任务</el-button>
    </div>
    <el-table :data="list" style="width:100%" v-loading="loading" class="sched-table">
      <el-table-column label="任务名称" min-width="150">
        <template #default="{row}"><span style="font-weight:600">{{row.name}}</span></template>
      </el-table-column>
      <el-table-column label="模板" min-width="130" prop="template_name" />
      <el-table-column label="目标主机" min-width="160">
        <template #default="{row}"><span class="sched-hosts-text">{{(row.host_ids||[]).length}} 台主机</span></template>
      </el-table-column>
      <el-table-column label="Cron" width="140">
        <template #default="{row}"><span class="mono" style="font-size:12px">{{row.cron_expr}}</span></template>
      </el-table-column>
      <el-table-column label="状态" width="90" align="center">
        <template #default="{row}">
          <el-switch v-model="row.enabled" size="small" @change="toggleEnabled(row)" :loading="row._toggling" />
        </template>
      </el-table-column>
      <el-table-column label="上次执行" width="150">
        <template #default="{row}">
          <span v-if="row.last_run_at" class="mono" style="font-size:12px">{{formatTime(row.last_run_at)}}</span>
          <span v-else style="color:var(--text-faint);font-size:12px">-</span>
        </template>
      </el-table-column>
      <el-table-column label="下次执行" width="150">
        <template #default="{row}">
          <span v-if="row.enabled && row.next_run_at" class="mono" style="font-size:12px;color:var(--brand)">{{formatTime(row.next_run_at)}}</span>
          <span v-else style="color:var(--text-faint);font-size:12px">-</span>
        </template>
      </el-table-column>
      <el-table-column label="" width="180" fixed="right">
        <template #default="{row}"><div class="ops-cell">
          <el-button size="small" text @click="viewRuns(row)">执行记录</el-button>
          <el-button size="small" text @click="openDialog(row)">编辑</el-button>
          <el-button size="small" text type="danger" @click="delSchedule(row)">删除</el-button>
        </div></template>
      </el-table-column>
    </el-table>
    <div v-if="!loading && !list.length" class="empty-state sched-empty">
      <strong>暂无定时任务</strong><span>点击「新增定时任务」创建第一个周期任务</span>
    </div>
  </div>

  <!-- 新增 / 编辑弹窗 -->
  <el-dialog v-model="dlgVisible" :title="editing.id ? '编辑定时任务' : '新增定时任务'" width="640px" :close-on-click-modal="false" class="sched-dlg">
    <el-form :model="editing" label-position="top" ref="editFormRef" :rules="formRules">

      <!-- § 基本信息 -->
      <div class="sched-dlg-section">
        <div class="sched-dlg-section-head"><span class="sched-dlg-section-icon">1</span><span class="sched-dlg-section-title">基本信息</span></div>
        <div class="sched-dlg-section-body sched-dlg-two-col">
          <el-form-item label="任务名称" prop="name" required>
            <el-input v-model="editing.name" placeholder="如：每日 Nginx 重载" />
          </el-form-item>
          <el-form-item label="部署模板" prop="template_id" required>
            <el-select v-model="editing.template_id" placeholder="选择模板" style="width:100%" @change="onTplChange">
              <el-option v-for="t in templates" :key="t.id" :label="t.name" :value="t.id" />
            </el-select>
          </el-form-item>
        </div>
      </div>

      <!-- § 模板参数（条件） -->
      <div v-if="tplVars.length" class="sched-dlg-section">
        <div class="sched-dlg-section-head"><span class="sched-dlg-section-icon">2</span><span class="sched-dlg-section-title">模板参数</span></div>
        <div class="sched-dlg-section-body">
          <div class="sched-params-grid">
            <el-form-item v-for="v in tplVars" :key="v.name" :label="v.label || v.name" class="sched-param-item">
              <el-input v-model="editing.params[v.name]" :placeholder="v.default || '请输入'" size="small" />
            </el-form-item>
          </div>
        </div>
      </div>

      <!-- § 目标主机（可选） -->
      <div class="sched-dlg-section">
        <div class="sched-dlg-section-head"><span class="sched-dlg-section-icon" v-text="tplVars.length ? '3' : '2'"></span><span class="sched-dlg-section-title">目标主机</span><span class="sched-dlg-section-badge">可选</span></div>
        <div class="sched-dlg-section-body">
          <p class="sched-dlg-hint">不选择主机时任务仅保存配置，不会执行</p>
          <div class="sched-host-toolbar">
            <el-input v-model="hostFilter" placeholder="搜索主机名 / IP" clearable size="small" style="width:200px" prefix-icon="Search" />
            <el-select v-model="hostSort.field" size="small" style="width:100px"><el-option label="按主机名" value="name" /><el-option label="按 IP" value="ip" /></el-select>
            <el-button size="small" @click="toggleHostSortOrder">{{hostSort.order==='asc' ? '↑ 升序' : '↓ 降序'}}</el-button>
            <el-button size="small" @click="toggleSelectAll">{{ selectedHostIds.size === filteredHosts.length ? '取消全选' : '全选' }}</el-button>
          </div>
          <div class="sched-host-list" v-loading="hostsLoading">
            <label v-for="h in filteredHosts" :key="h.id" class="sched-host-item" :class="{'sched-host-item--sel': selectedHostIds.has(h.id)}">
              <el-checkbox :model-value="selectedHostIds.has(h.id)" @change="toggleHost(h.id)" />
              <span class="sched-host-name">{{h.name}}</span>
              <span class="mono sched-host-ip">{{h.ip}}</span>
              <span v-if="h.tag" class="tag-badge other" style="font-size:10px;margin-left:auto">{{h.tag}}</span>
            </label>
            <div v-if="!filteredHosts.length && !hostsLoading" class="sched-host-empty">无匹配主机</div>
          </div>
        </div>
      </div>

      <!-- § 执行计划 -->
      <div class="sched-dlg-section">
        <div class="sched-dlg-section-head"><span class="sched-dlg-section-icon" v-text="tplVars.length ? '4' : '3'"></span><span class="sched-dlg-section-title">执行计划</span></div>
        <div class="sched-dlg-section-body">
          <el-form-item prop="cron" required>
            <template #label><span class="sched-cron-label">Cron 表达式</span></template>
            <el-input v-model="editing.cron" placeholder="分 时 日 月 周 (如 0 2 * * *)" class="sched-cron-input" />
          </el-form-item>
          <div class="sched-cron-presets">
            <el-button v-for="p in cronPresets" :key="p.expr" size="small" :type="editing.cron===p.expr ? 'primary' : 'default'" :plain="editing.cron!==p.expr" @click="editing.cron=p.expr;editing.cron_desc=p.desc" class="sched-cron-preset-btn">{{p.label}}</el-button>
          </div>
          <transition name="el-fade-in">
            <div v-if="editing.cron_desc" class="sched-cron-hint">
              <svg viewBox="0 0 16 16" width="13" height="13" fill="currentColor"><path d="M8 15A7 7 0 1 1 8 1a7 7 0 0 1 0 14zm0 1A8 8 0 1 0 8 0a8 8 0 0 0 0 16z"/><path d="m8.93 6.588-2.29.287-.082.38.45.083c.294.07.352.176.288.469l-.738 3.468c-.194.897.105 1.319.808 1.319.545 0 1.178-.252 1.465-.598l.088-.416c-.2.176-.492.246-.686.246-.275 0-.375-.193-.304-.533L8.93 6.588zM9 4.5a1 1 0 1 1-2 0 1 1 0 0 1 2 0z"/></svg>
              <span>{{editing.cron_desc}}</span>
            </div>
          </transition>
        </div>
      </div>

    </el-form>
    <template #footer>
      <el-button @click="dlgVisible=false">取消</el-button>
      <el-button type="primary" :loading="saving" @click="saveSchedule">保存</el-button>
    </template>
  </el-dialog>

  <!-- 执行记录抽屉 -->
  <el-drawer v-model="drawerVisible" :title="'执行记录 — ' + (drawerTask?.name || '')" size="560px">
    <div v-loading="runsLoading" class="sched-runs">
      <div v-if="!runs.length && !runsLoading" class="empty-state" style="padding:40px 0"><p>暂无执行记录</p></div>
      <div v-for="r in runs" :key="r.id" class="sched-run-item">
        <div class="sched-run-header">
          <el-tag :type="runTagType(r.status)" size="small">{{runStatusLabel(r.status)}}</el-tag>
          <span class="mono" style="font-size:12px;color:var(--text-faint)">{{formatTime(r.created_at)}}</span>
        </div>
        <div class="sched-run-meta">
          <span v-if="r.finished_at">完成于 <span class="mono">{{formatTime(r.finished_at)}}</span></span>
          <span v-if="!r.total" style="color:var(--text-sub)">未配置目标主机，本次触发未执行任何操作</span>
          <span v-else>成功 <strong style="color:var(--ok)">{{r.success_cnt}}</strong> / 失败 <strong style="color:var(--danger)">{{r.fail_cnt}}</strong> / 总计 {{r.total}}</span>
        </div>
      </div>
    </div>
  </el-drawer>
</div>`,

  data() {
    return {
      list: [], loading: false, saving: false, dlgVisible: false,
      editing: { name: '', template_id: null, host_ids: [], cron: '', cron_desc: '', params: {} },
      templates: [], hosts: [], hostsLoading: false, hostFilter: '', hostSort: { field: 'name', order: 'asc' }, selectedHostIds: new Set(),
      drawerVisible: false, drawerTask: null, runs: [], runsLoading: false,
      cronPresets: [
        { label: '每小时', expr: '0 * * * *', desc: '每小时整点' },
        { label: '每天凌晨', expr: '0 2 * * *', desc: '每天凌晨 2:00' },
        { label: '每天早8点', expr: '0 8 * * *', desc: '每天上午 8:00' },
        { label: '每周一', expr: '0 2 * * 1', desc: '每周一凌晨 2:00' },
        { label: '每月1号', expr: '0 2 1 * *', desc: '每月 1 号凌晨 2:00' }
      ],
      formRules: {
        name: [{ required: true, message: '请输入任务名称', trigger: 'blur' }],
        template_id: [{ required: true, message: '请选择模板', trigger: 'change' }],
        cron: [{ required: true, message: '请输入 Cron 表达式', trigger: 'blur' }]
      }
    }
  },
  computed: {
    enabledCount() { return this.list.filter(s => s.enabled).length },
    tplVars() {
      const t = this.templates.find(t => t.id === this.editing.template_id)
      return t?.variables || []
    },
    filteredHosts() {
      let list = this.hosts
      if (this.hostFilter) {
        const kw = this.hostFilter.toLowerCase()
        list = list.filter(h => (h.name||'').toLowerCase().includes(kw) || (h.ip||'').toLowerCase().includes(kw))
      }
      const desc = this.hostSort.order === 'desc'
      const cmp = this.hostSort.field === 'ip' ? cmpHostIP : cmpHostName
      return [...list].sort((a, b) => desc ? cmp(b, a) : cmp(a, b))
    }
  },
  mounted() { this.load(); this.loadTemplates(); this.loadHosts() },
  methods: {
    async load() {
      this.loading = true
      try { const r = await api.get('/deploy/schedules'); if (r.code === 0) this.list = (r.data || []).map(s => ({...s, _toggling: false})) } catch (e) { /* */ } finally { this.loading = false }
    },
    async loadTemplates() {
      try { const r = await api.get('/deploy/templates'); if (r.code === 0) this.templates = r.data || [] } catch (e) { /* */ }
    },
    async loadHosts() {
      this.hostsLoading = true
      try { const r = await api.get('/hosts', { params: { page: 1, page_size: 200 } }); if (r.code === 0) this.hosts = r.data?.list || [] } catch (e) { /* */ } finally { this.hostsLoading = false }
    },
    formatTime(t) {
      if (!t) return '-'
      const d = new Date(t.replace(' ', 'T') + (t.includes('Z') ? '' : 'Z'))
      return d.getFullYear() + '-' + String(d.getMonth()+1).padStart(2,'0') + '-' + String(d.getDate()).padStart(2,'0') + ' ' + String(d.getHours()).padStart(2,'0') + ':' + String(d.getMinutes()).padStart(2,'0')
    },
    openDialog(row) {
      if (row) {
        this.editing = { ...row, cron: row.cron_expr || '', cron_desc: '', host_ids: [...(row.host_ids || [])], params: { ...(row.params || {}) } }
        this.selectedHostIds = new Set(row.host_ids || [])
      } else {
        this.editing = { name: '', template_id: null, host_ids: [], cron: '0 2 * * *', cron_desc: '每天凌晨 2:00', params: {} }
        this.selectedHostIds = new Set()
      }
      this.hostFilter = ''
      this.dlgVisible = true
    },
    onTplChange() { this.editing.params = {} },
    toggleHostSortOrder() { this.hostSort = { ...this.hostSort, order: this.hostSort.order === 'asc' ? 'desc' : 'asc' } },
    toggleHost(id) {
      const s = new Set(this.selectedHostIds)
      s.has(id) ? s.delete(id) : s.add(id)
      this.selectedHostIds = s
      this.editing.host_ids = [...s]
    },
    toggleSelectAll() {
      if (this.selectedHostIds.size === this.filteredHosts.length) {
        this.selectedHostIds = new Set(); this.editing.host_ids = []
      } else {
        this.selectedHostIds = new Set(this.filteredHosts.map(h => h.id)); this.editing.host_ids = [...this.selectedHostIds]
      }
    },
    async saveSchedule() {
      if (!this.editing.name?.trim()) { ElMessage.warning('请输入任务名称'); return }
      if (!this.editing.template_id) { ElMessage.warning('请选择模板'); return }
      if (!this.editing.cron?.trim()) { ElMessage.warning('请输入 Cron 表达式'); return }
      this.saving = true
      try {
        const body = { name: this.editing.name, template_id: this.editing.template_id, host_ids: [...this.selectedHostIds], params: this.editing.params || {}, cron_expr: this.editing.cron }
        if (this.editing.id) { await api.put('/deploy/schedules/' + this.editing.id, body) }
        else { await api.post('/deploy/schedules', body) }
        ElMessage.success('保存成功'); this.dlgVisible = false; this.load()
      } catch (e) { ElMessage.error(e.response?.data?.message || '保存失败') } finally { this.saving = false }
    },
    delSchedule(row) {
      ElMessageBox.confirm('确认删除定时任务「' + row.name + '」？', '提示', { type: 'warning' }).then(async () => {
        try {
          const r = await api.delete('/deploy/schedules/' + row.id)
          if (r.code === 0) { ElMessage.success('已删除'); this.load() }
          else ElMessage.error(r.message || '删除失败')
        } catch (e) { ElMessage.error(e.response?.data?.message || '删除失败') }
      }).catch(() => {})
    },
    async toggleEnabled(row) {
      row._toggling = true
      try {
        await api.post('/deploy/schedules/' + row.id + '/toggle', { enabled: row.enabled })
        ElMessage.success(row.enabled ? '已启用' : '已停用')
      } catch (e) {
        row.enabled = !row.enabled
        ElMessage.error(e.response?.data?.message || '操作失败')
      } finally { row._toggling = false }
    },
    async viewRuns(row) {
      this.drawerTask = row
      this.runs = []
      this.drawerVisible = true
      this.runsLoading = true
      try {
        const r = await api.get('/deploy/schedules/' + row.id + '/runs')
        if (r.code === 0) this.runs = r.data?.list || []
      } catch (e) { /* */ } finally { this.runsLoading = false }
    },
    runTagType(s) { if (s === 'success') return 'success'; if (s === 'failed') return 'danger'; if (s === 'partial') return 'warning'; return 'info' },
    runStatusLabel(s) { return { running: '执行中', success: '成功', failed: '失败', partial: '部分成功' }[s] || s }
  }
}
