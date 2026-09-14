// ES 控制台主页面（F1 瘦身）：连接管理 + 集群概览/节点 tab + tab 壳。
// 索引 / 数据视图(Discover) / 生命周期 / 索引模板拆分到 es_index / es_views / es_lifecycle / es_templates。
window.ESPage = {
  props: ['page', 'user', 'versionData'],
  components: {
    'es-index-tab': window.EsIndexTab,
    'es-views-tab': window.EsViewsTab,
    'es-lifecycle-tab': window.EsLifecycleTab,
    'es-templates-tab': window.EsTemplatesTab
  },
  template: `
<div>
  <!-- 视图一：连接列表 -->
  <template v-if="!current">
    <div class="tool-page tool-page--es">
      <section class="tool-intro">
        <div class="tool-intro-copy">
          <span class="tool-eyebrow">ELASTICSEARCH</span>
          <h1>Elasticsearch</h1>
          <p>数据视图 · KQL 检索 · 生命周期 / 索引模板 · 密码加密托管</p>
        </div>
        <div class="tool-intro-stats">
          <div class="tool-mini-stat"><span>连接</span><strong>{{total}}</strong></div>
          <div class="tool-mini-stat"><span>在线</span><strong>{{onlineCount}}</strong></div>
        </div>
      </section>
      <div class="page-card tool-panel">
      <div class="card-header">
        <div class="tool-list-title">
          <span class="title">连接列表</span>
          <span class="tool-list-sub">表格 / 卡片切换 · 支持探测连通性</span>
        </div>
        <div class="header-extra">
          <el-radio-group v-model="viewMode" size="small" class="hosts-view-switch">
            <el-radio-button label="table">表格</el-radio-button>
            <el-radio-button label="card">卡片</el-radio-button>
          </el-radio-group>
          <el-button text :loading="pinging" @click="loadConns"><el-icon style="margin-right:4px"><Refresh /></el-icon>刷新</el-button>
          <el-button type="primary" @click="openForm(null)">新增连接</el-button>
        </div>
      </div>

      <div v-if="viewMode==='table'">
        <el-table class="hosts-table" :data="conns" style="width:100%" v-loading="loadingConn">
          <el-table-column label="连接" min-width="230">
            <template #default="{row}">
              <div><span style="font-weight:600">{{row.name}}</span>
                <span class="action-badge" :class="row.insecure?'other':'host'" style="margin-left:7px;font-size:10.5px">{{row.insecure?'HTTP:insecure':'HTTPS'}}</span></div>
              <div class="mono" style="font-size:12px;color:#9CA3AF">{{row.url}}</div>
            </template>
          </el-table-column>
          <el-table-column label="状态" width="98">
            <template #default="{row}"><span class="tool-status" :class="stClass(row._st)"><span class="st-dot"></span>{{row._testing ? '探测中' : stText(row._st)}}</span></template>
          </el-table-column>
          <el-table-column label="认证" width="110">
            <template #default="{row}"><span class="action-badge" :class="row.auth_type==='basic'?'credential':'other'">{{row.auth_type==='basic'?'账号认证':'匿名'}}</span></template>
          </el-table-column>
          <el-table-column label="备注" min-width="130">
            <template #default="{row}"><span :title="row.remark" style="color:#6B7280">{{row.remark || '-'}}</span></template>
          </el-table-column>
          <el-table-column label="更新时间" width="158">
            <template #default="{row}"><span class="mono" style="font-size:12.5px;color:#6B7280">{{row.updated_at||'-'}}</span></template>
          </el-table-column>
          <el-table-column label="" width="200" fixed="right">
            <template #default="{row}">
              <div class="ops-cell">
                <el-button size="small" text @click="openForm(row)">编辑</el-button>
                <el-button size="small" text :loading="row._testing" @click="testConn(row)">测试</el-button>
                <el-button size="small" text type="primary" @click="enter(row)">打开</el-button>
                <el-button size="small" text type="danger" @click="delConn(row)">删除</el-button>
              </div>
            </template>
          </el-table-column>
        </el-table>
        <div class="tool-empty-table" v-if="!loadingConn && !conns.length"><empty-state text="暂无连接，点击右上角「新增连接」开始" /></div>
      </div>

      <div v-else class="hosts-card-grid tool-card-grid" v-loading="loadingConn">
        <article v-for="row in conns" :key="row.id" class="tool-card tool-card--es">
          <header class="tool-card-head">
            <div class="tool-card-icon">ES</div>
            <div class="tool-card-idbox">
              <h3 class="tool-card-name" :title="row.name">{{row.name}}</h3>
              <div class="tool-card-endpoint mono" :title="row.url">{{row.url}}</div>
              <span class="tool-status" :class="stClass(row._st)"><span class="st-dot"></span>{{row._testing ? '探测中' : stText(row._st)}}</span>
            </div>
            <el-button text class="host-card-more" @click.stop="openForm(row)" title="编辑">···</el-button>
          </header>
          <div class="tool-card-meta">
            <span class="action-badge" :class="row.insecure?'other':'host'">{{row.insecure?'HTTP:insecure':'HTTPS'}}</span>
            <span class="action-badge" :class="row.auth_type==='basic'?'credential':'other'">{{row.auth_type==='basic'?'账号认证':'匿名'}}</span>
            <span v-if="row.auth_type==='basic'" class="tool-card-user mono">{{row.username}}</span>
          </div>
          <div class="tool-card-remark" v-if="row.remark" :title="row.remark">{{row.remark}}</div>
          <footer class="tool-card-actions">
            <button type="button" class="hca-btn" @click="testConn(row)" :disabled="row._testing">{{row._testing?'测试中…':'测试'}}</button>
            <button type="button" class="hca-btn" @click="openForm(row)">编辑</button>
            <button type="button" class="hca-btn hca-btn-primary" @click="enter(row)">打开</button>
            <button type="button" class="hca-btn hca-btn-danger" @click="delConn(row)">删除</button>
          </footer>
        </article>
        <empty-state v-if="!loadingConn && !conns.length" text="暂无连接，点击右上角「新增连接」开始" />
      </div>
      <div class="tool-pagination" v-if="total>20"><el-pagination v-model:current-page="pg" :page-size="20" :total="total" layout="total, prev, pager, next" @current-change="loadConns" /></div>
    </div>
    </div>
    <el-dialog v-model="dialog" :title="form.id?'编辑连接':'新增连接'" width="560px" append-to-body>
      <el-form :model="form" label-position="top">
        <el-form-item label="名称" required><el-input v-model="form.name" placeholder="例如：测试集群" /></el-form-item>
        <el-form-item label="集群地址" required>
          <el-input v-model="form.url" placeholder="host:9200 或 http(s)://host:port" />
        </el-form-item>
        <el-form-item label="连接方式">
          <el-radio-group v-model="form.insecure">
            <el-radio :value="false" border>HTTPS（信任证书）</el-radio>
            <el-radio :value="true" border>HTTP / 自签证书（insecure）</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="认证方式">
          <el-radio-group v-model="form.auth_type" @change="onAuthChange">
            <el-radio value="anonymous" border>无需认证</el-radio>
            <el-radio value="basic" border>账号密码（xpack）</el-radio>
          </el-radio-group>
        </el-form-item>
        <template v-if="form.auth_type==='basic'">
          <el-form-item label="用户名" required><el-input v-model="form.username" /></el-form-item>
          <el-form-item label="密码" required>
            <el-input v-model="form.password" type="password" show-password :placeholder="form.has_password?'留空则不修改':''" />
          </el-form-item>
        </template>
        <el-form-item label="备注"><el-input v-model="form.remark" type="textarea" :rows="2" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="dialog=false">取消</el-button><el-button type="primary" :loading="saving" @click="save">保存</el-button></template>
    </el-dialog>
  </template>

  <!-- 视图二：ES 控制台 -->
  <template v-else>
    <div class="page-card reg-workspace es-workspace">
      <div class="card-header reg-whd">
        <el-button text @click="exit" style="margin-right:2px"><el-icon><Back /></el-icon></el-button>
        <div class="reg-whd-title">
          <div class="reg-whd-name">
            <span class="title">{{current.name}}</span>
            <span class="action-badge" :class="current.insecure?'other':'host'">{{current.insecure?'HTTP:insecure':'HTTPS'}}</span>
            <el-tag v-if="overview" size="small" effect="dark" :type="healthType(overview.status)" style="margin-left:2px">{{statusLabel(overview.status)}}</el-tag>
            <el-tag v-if="overview" size="small" type="info" class="reg-es-ver">v{{overview.version}}</el-tag>
          </div>
          <div class="mono reg-whd-url">{{current.url}}</div>
        </div>
        <div class="header-extra">
          <el-button text type="primary" :loading="loadingOverview" @click="reloadAll"><el-icon style="margin-right:4px"><Refresh /></el-icon>刷新</el-button>
        </div>
      </div>

      <el-tabs v-model="activeTab" class="reg-es-tabs">
        <!-- 概览 -->
        <el-tab-pane label="集群概览" name="overview">
          <div v-loading="loadingOverview">
            <template v-if="overview">
              <div class="reg-statbar es-statbar">
                <div class="reg-stat"><span class="reg-stat-num mono" :style="'color:'+healthColor(overview.status)">{{overview.nodes}}</span><span class="reg-stat-label">节点 / 数据{{overview.data_nodes}}</span></div>
                <div class="reg-stat"><span class="reg-stat-num mono">{{overview.active_shards}}</span><span class="reg-stat-label">活跃分片（主{{overview.active_primary_shards}}）</span></div>
                <div class="reg-stat"><span class="reg-stat-num mono" :class="{muted:overview.unassigned_shards===0}">{{overview.unassigned_shards}}</span><span class="reg-stat-label">未分配分片</span></div>
                <div class="reg-stat"><span class="reg-stat-num mono">{{overview.initializing_shards}} / {{overview.relocating_shards}}</span><span class="reg-stat-label">恢复中 / 重定位</span></div>
              </div>
              <div class="es-overview-cards">
                <div class="es-ov-card"><span class="es-ov-label">集群状态</span><span class="es-ov-value"><span class="health-dot" :style="{background:healthColor(overview.status)}"></span><b style="margin-left:6px">{{statusLabel(overview.status)}}</b></span></div>
                <div class="es-ov-card"><span class="es-ov-label">集群名称</span><span class="es-ov-value mono">{{overview.cluster_name}}</span></div>
                <div class="es-ov-card"><span class="es-ov-label">版本</span><span class="es-ov-value mono">{{overview.version}}</span></div>
                <div class="es-ov-card"><span class="es-ov-label">节点构成</span><span class="es-ov-value"><b>{{overview.nodes}}</b> 节点 / <b>{{overview.data_nodes}}</b> 数据</span></div>
              </div>
            </template>
          </div>
          <div class="es-sub-head">节点（{{nodes.length}}）</div>
          <el-table class="hosts-table" :data="nodes" style="width:100%" size="small" v-loading="loadingNodes">
            <el-table-column label="节点" min-width="150">
              <template #default="{row}"><span style="font-weight:600">{{row.name}}</span></template>
            </el-table-column>
            <el-table-column label="版本" width="90">
              <template #default="{row}"><span class="mono">{{row.version}}</span></template>
            </el-table-column>
            <el-table-column label="角色" min-width="190">
              <template #default="{row}"><span class="es-role-list"><span v-for="r in row.roles" :key="r" class="action-badge info">{{roleShort(r)}}</span><span v-if="!row.roles||!row.roles.length" style="color:#9CA3AF">-</span></span></template>
            </el-table-column>
            <el-table-column label="CPU" width="130">
              <template #default="{row}"><div class="metric" v-if="row.cpu_percent>=0"><div class="metric-bar"><div class="fill" :style="{width:row.cpu_percent+'%',background:usageColor(row.cpu_percent)}"></div></div><span class="metric-value mono">{{row.cpu_percent}}%</span></div><span v-else style="color:#9CA3AF">-</span></template>
            </el-table-column>
            <el-table-column label="堆内存" width="130">
              <template #default="{row}"><div class="metric" v-if="row.heap_percent>=0"><div class="metric-bar"><div class="fill" :style="{width:row.heap_percent+'%',background:usageColor(row.heap_percent)}"></div></div><span class="metric-value mono">{{row.heap_percent}}%</span></div><span v-else style="color:#9CA3AF">-</span></template>
            </el-table-column>
            <el-table-column label="磁盘" width="140">
              <template #default="{row}"><span class="mono" style="font-size:12px">{{fmtBytes(row.disk_available)}} / {{fmtBytes(row.disk_total)}}</span></template>
            </el-table-column>
            <el-table-column label="文档数" width="100">
              <template #default="{row}"><span class="mono">{{row.docs_count>=0?row.docs_count:'-'}}</span></template>
            </el-table-column>
          </el-table>
        </el-tab-pane>

        <el-tab-pane label="索引" name="indices" lazy>
          <es-index-tab :conn="current" />
        </el-tab-pane>

        <el-tab-pane label="数据视图" name="views" lazy>
          <es-views-tab :conn="current" />
        </el-tab-pane>

        <el-tab-pane label="生命周期" name="lifecycle" lazy>
          <es-lifecycle-tab :conn="current" />
        </el-tab-pane>

        <el-tab-pane label="索引模板" name="templates" lazy>
          <es-templates-tab :conn="current" />
        </el-tab-pane>
      </el-tabs>
    </div>
  </template>
</div>`,
  data() {
    return {
      conns: [], loadingConn: false, pg: 1, total: 0, kw: '', pinging: false,
      viewMode: localStorage.getItem('es-view-mode') || 'table',
      dialog: false, form: {}, saving: false,
      current: null,
      activeTab: 'overview',
      overview: null, loadingOverview: false,
      nodes: [], loadingNodes: false
    }
  },
  computed: {
    onlineCount() { return this.conns.filter(c => c._st === 1).length }
  },
  watch: { viewMode(v) { localStorage.setItem('es-view-mode', v) } },
  mounted() { this.loadConns() },
  methods: {
    async loadConns() {
      this.loadingConn = true
      try { const r = await api.get('/es', { params: { page: this.pg, page_size: 20 } }); if (r.code === 0) { this.conns = r.data?.list || []; this.total = r.data?.total || 0; this.pingAll() } } catch (e) { /* */ } finally { this.loadingConn = false }
    },
    async pingAll() {
      this.pinging = true
      this.conns.forEach(row => { row._st = undefined })
      await Promise.allSettled(this.conns.map(async (row) => {
        try { const r = await api.get('/es/' + row.id + '/ping'); row._st = r.code === 0 ? 1 : 0 } catch (e) { row._st = 0 }
      }))
      this.pinging = false
    },
    stText(s) { return s === 1 ? '在线' : s === 0 ? '不可用' : '未验证' },
    stClass(s) { return s === 1 ? 'ok' : s === 0 ? 'fail' : 'idle' },
    openForm(row) {
      this.form = row ? { id: row.id, name: row.name, url: row.url, insecure: !!row.insecure, auth_type: row.auth_type || 'anonymous', username: row.username || '', password: '', remark: row.remark, has_password: row.has_password } : { name: '', url: '', insecure: false, auth_type: 'basic', username: 'elastic', password: '', remark: '' }
      this.dialog = true
    },
    onAuthChange() { this.form.username = ''; this.form.password = '' },
    async save() {
      this.saving = true
      try {
        const d = { ...this.form }
        if (d.auth_type !== 'basic') { delete d.username; delete d.password } else if (!d.password) delete d.password
        if (this.form.id) await api.put('/es/' + this.form.id, d)
        else await api.post('/es', d)
        this.dialog = false; this.loadConns()
      } catch (e) { /* */ } finally { this.saving = false }
    },
    delConn(row) {
      this.$confirm('确认删除该 ES 连接？删除连接不影响集群本身。', '提示', { type: 'warning' }).then(async () => { try { await api.delete('/es/' + row.id); this.loadConns() } catch (e) { /* */ } }).catch(() => {})
    },
    async testConn(row) {
      row._testing = true
      try { const r = await api.get('/es/' + row.id + '/ping'); if (r.code === 0) this.$message.success('连接正常，延迟 ' + (r.data?.latency_ms ?? '') + ' ms') } catch (e) { /* */ } finally { row._testing = false }
    },
    enter(row) {
      this.current = row
      this.activeTab = 'overview'
      this.overview = null; this.nodes = []
      this.loadOverview(); this.loadNodes()
    },
    exit() { this.current = null },
    reloadAll() { this.loadOverview(); this.loadNodes() },
    async loadOverview() {
      this.loadingOverview = true
      try { const r = await api.get('/es/' + this.current.id + '/overview'); if (r.code === 0) this.overview = r.data } catch (e) { /* */ } finally { this.loadingOverview = false }
    },
    async loadNodes() {
      this.loadingNodes = true
      try { const r = await api.get('/es/' + this.current.id + '/nodes'); if (r.code === 0) this.nodes = r.data?.list || [] } catch (e) { /* */ } finally { this.loadingNodes = false }
    },
    roleShort(r) {
      const map = { master: '主', data: '数据', ingest: '摄取', coordinating_only: '协调', ml: 'ML', voting_only: '投票', warm: 'warm', cold: 'cold', hot: 'hot', frozen: 'frozen' }
      return map[r] || r
    },
    statusLabel(s) { return s === 'green' ? '健康' : s === 'yellow' ? '警告' : s === 'red' ? '危险' : '—' },
    healthType(s) { return s === 'green' ? 'success' : s === 'yellow' ? 'warning' : 'danger' },
    healthColor(s) { return s === 'green' ? 'var(--ok)' : s === 'yellow' ? 'var(--warn)' : 'var(--danger)' },
    usageColor(p) { return p >= 85 ? 'var(--danger)' : p >= 70 ? 'var(--warn)' : 'var(--ok)' },
    fmtBytes(n) {
      if (n == null) return '-'
      if (n < 1024) return n + ' B'
      if (n < 1048576) return (n / 1024).toFixed(1) + ' KB'
      if (n < 1073741824) return (n / 1048576).toFixed(1) + ' MB'
      return (n / 1073741824).toFixed(2) + ' GB'
    }
  }
}
