// MySQL 工具页面：连接列表（表格 / 卡片）+ 新增编辑抽屉 + 工作台容器。
window.MySQLPage = {
  props: ['page', 'versionData'],
  components: { 'mysql-workbench': window.MySQLWorkbench, 'mysql-logs': window.MySQLLogs },
  template: `
<div>
  <!-- 视图一：连接列表 -->
  <template v-if="!current">
    <div class="tool-page tool-page--mysql">
      <section class="tool-intro">
        <div class="tool-intro-copy">
          <span class="tool-eyebrow">MYSQL CLIENT</span>
          <h1>MySQL</h1>
          <p>库表浏览 · SQL 查询 · 结果导出 · 支持 SSH 跳板机 · 密码加密托管</p>
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
            <el-button text @click="gotoSettings"><el-icon style="margin-right:4px"><Setting /></el-icon>AI 设置</el-button>
            <el-button text @click="openLogs('sql')"><el-icon style="margin-right:4px"><Tickets /></el-icon>使用记录</el-button>
            <el-button type="primary" @click="openForm(null)">新增连接</el-button>
          </div>
        </div>

        <div v-if="viewMode==='table'">
          <el-table class="hosts-table" :data="conns" style="width:100%" v-loading="loadingConn">
            <el-table-column label="连接" min-width="230">
              <template #default="{row}">
                <div><span style="font-weight:600">{{row.name}}</span>
                  <span v-if="row.ssh_host_name" class="action-badge info" style="margin-left:7px;font-size:10.5px">隧道</span></div>
                <div class="mono" style="font-size:12px;color:#9CA3AF">{{row.username}}@{{row.host}}:{{row.port}}<span v-if="row.default_schema">/{{row.default_schema}}</span></div>
              </template>
            </el-table-column>
            <el-table-column label="状态" width="98">
              <template #default="{row}"><span class="tool-status" :class="stClass(row._st)"><span class="st-dot"></span>{{row._testing ? '探测中' : stText(row._st)}}</span></template>
            </el-table-column>
            <el-table-column label="默认模式" width="100">
              <template #default="{row}"><span class="action-badge" :class="row.read_only ? 'credential' : 'other'" title="打开工作台时的初始模式，进去后可切换">{{row.read_only ? '只读' : '可写'}}</span></template>
            </el-table-column>
            <el-table-column label="跳板机" width="130">
              <template #default="{row}"><span style="color:#6B7280">{{row.ssh_host_name || '直连'}}</span></template>
            </el-table-column>
            <el-table-column label="备注" min-width="120">
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
          <article v-for="row in conns" :key="row.id" class="tool-card tool-card--mysql">
            <header class="tool-card-head">
              <div class="tool-card-icon">MY</div>
              <div class="tool-card-idbox">
                <h3 class="tool-card-name" :title="row.name">{{row.name}}</h3>
                <div class="tool-card-endpoint mono" :title="row.host">{{row.username}}@{{row.host}}:{{row.port}}</div>
                <span class="tool-status" :class="stClass(row._st)"><span class="st-dot"></span>{{row._testing ? '探测中' : stText(row._st)}}</span>
              </div>
              <el-button text class="host-card-more" @click.stop="openForm(row)" title="编辑">···</el-button>
            </header>
            <div class="tool-card-meta">
              <span class="action-badge" :class="row.read_only ? 'credential' : 'other'" title="打开工作台时的初始模式，进去后可切换">默认{{row.read_only ? '只读' : '可写'}}</span>
              <span v-if="row.ssh_host_name" class="action-badge info">经 {{row.ssh_host_name}}</span>
              <span v-if="row.default_schema" class="tool-card-user mono">{{row.default_schema}}</span>
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

    <el-dialog v-model="dialog" :title="form.id?'编辑连接':'新增连接'" width="600px" append-to-body>
      <el-form :model="form" label-position="top">
        <el-form-item label="名称" required><el-input v-model="form.name" placeholder="例如：订单库-生产" /></el-form-item>
        <el-form-item label="主机地址" required><el-input v-model="form.host" placeholder="直连填 IP；走跳板机时填跳板机可达的内网地址" /></el-form-item>
        <div class="mysql-two-col">
          <el-form-item label="端口" required><el-input-number v-model="form.port" :min="1" :max="65535" style="width:100%" /></el-form-item>
          <el-form-item label="用户名" required><el-input v-model="form.username" placeholder="如 root" /></el-form-item>
        </div>
        <el-form-item label="密码">
          <el-input v-model="form.password" type="password" show-password :placeholder="form.has_password?'留空则不修改':''" />
        </el-form-item>
        <div class="mysql-two-col">
          <el-form-item label="默认库"><el-input v-model="form.default_schema" placeholder="可空，打开后可在左侧切换" /></el-form-item>
          <el-form-item label="字符集">
            <el-select v-model="form.charset" style="width:100%">
              <el-option label="utf8mb4" value="utf8mb4" />
              <el-option label="utf8" value="utf8" />
              <el-option label="gbk" value="gbk" />
              <el-option label="latin1" value="latin1" />
            </el-select>
          </el-form-item>
        </div>
        <el-form-item label="SSH 跳板机">
          <el-select v-model="form.ssh_host_id" clearable placeholder="不选 = 直连数据库" style="width:100%">
            <el-option v-for="h in hosts" :key="h.id" :label="h.name + '（' + h.ip + '）'" :value="h.id" />
          </el-select>
          <div class="mysql-form-hint">复用「主机管理」里的主机与凭据建立隧道，数据库地址填跳板机可达的内网地址。</div>
        </el-form-item>
        <el-form-item label="默认模式">
          <el-switch v-model="form.read_only" active-text="只读" inactive-text="可写" />
          <div class="mysql-form-hint">打开工作台时的初始模式，连上之后可以随时切换。只读模式下非查询语句需逐条确认放行；可写模式下 DML 与 DDL 执行前同样会二次确认。</div>
        </el-form-item>
        <el-form-item label="备注"><el-input v-model="form.remark" type="textarea" :rows="2" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="dialog=false">取消</el-button><el-button type="primary" :loading="saving" @click="save">保存</el-button></template>
    </el-dialog>
  </template>

  <!-- 视图二：工作台 -->
  <mysql-workbench v-else :conn="current" @exit="current=null"
    @open-ai-config="gotoSettings" @open-logs="openLogs('ai', current)" />

  <!-- 使用记录：SQL 执行记录（人在工具里执行的语句）与 AI 调用记录（含 token 用量），两张表各看各的。
       列表页与工作台的 AI 面板共用这一个弹窗，后者默认落在 AI 页签；从工作台打开时
       叠加当前连接的过滤（conn-id / conn-name），列表页入口看全量。
       版式固定：宽 65%、高 70%，上下各留 15%，记录再多也不出屏，内容区内部滚动。 -->
  <el-dialog v-model="logsDialog" title="使用记录" width="65%" append-to-body class="mysql-logs-dialog">
    <mysql-logs v-if="logsDialog" :default-tab="logsTab"
      :conn-id="logsConn ? logsConn.id : 0" :conn-name="logsConn ? logsConn.name : ''" />
  </el-dialog>
</div>`,
  data() {
    return {
      conns: [], loadingConn: false, pg: 1, total: 0, pinging: false,
      viewMode: localStorage.getItem('mysql-view-mode') || 'table',
      dialog: false, form: {}, saving: false,
      logsDialog: false, logsTab: 'sql', logsConn: null,
      current: null, hosts: []
    }
  },
  computed: {
    onlineCount() { return this.conns.filter(c => c._st === 1).length }
  },
  watch: { viewMode(v) { localStorage.setItem('mysql-view-mode', v) } },
  mounted() { this.loadConns(); this.loadHosts() },
  methods: {
    async loadConns() {
      this.loadingConn = true
      try {
        const r = await api.get('/mysql', { params: { page: this.pg, page_size: 20 } })
        if (r.code === 0) { this.conns = r.data?.list || []; this.total = r.data?.total || 0; this.pingAll() }
      } catch (e) { /* 拦截器已提示 */ } finally { this.loadingConn = false }
    },
    // 跳板机候选来自「主机管理」，列表不做分页
    async loadHosts() {
      try {
        const r = await api.get('/hosts', { params: { page: 1, page_size: 200 } })
        if (r.code === 0) this.hosts = r.data?.list || []
      } catch (e) { /* 静默：无主机时下拉为空 */ }
    },
    async pingAll() {
      this.pinging = true
      this.conns.forEach(row => { row._st = undefined })
      await Promise.allSettled(this.conns.map(async (row) => {
        try { const r = await api.get('/mysql/' + row.id + '/ping'); row._st = r.code === 0 ? 1 : 0 } catch (e) { row._st = 0 }
      }))
      this.pinging = false
    },
    stText(s) { return s === 1 ? '在线' : s === 0 ? '不可用' : '未验证' },
    stClass(s) { return s === 1 ? 'ok' : s === 0 ? 'fail' : 'idle' },
    openForm(row) {
      this.form = row
        ? { id: row.id, name: row.name, host: row.host, port: row.port || 3306, username: row.username || '', password: '', default_schema: row.default_schema || '', charset: row.charset || 'utf8mb4', read_only: !!row.read_only, ssh_host_id: row.ssh_host_id || null, remark: row.remark, has_password: row.has_password }
        : { name: '', host: '', port: 3306, username: 'root', password: '', default_schema: '', charset: 'utf8mb4', read_only: true, ssh_host_id: null, remark: '' }
      this.dialog = true
    },
    async save() {
      if (!this.form.name) { ElMessage.warning('请填写名称'); return }
      if (!this.form.host) { ElMessage.warning('请填写主机地址'); return }
      this.saving = true
      try {
        const d = { ...this.form, ssh_host_id: this.form.ssh_host_id || 0 }
        if (!d.password) delete d.password
        if (this.form.id) await api.put('/mysql/' + this.form.id, d)
        else await api.post('/mysql', d)
        this.dialog = false
        this.loadConns()
      } catch (e) { /* 拦截器已提示 */ } finally { this.saving = false }
    },
    delConn(row) {
      this.$confirm('确认删除该 MySQL 连接？不影响数据库本身。', '提示', { type: 'warning' })
        .then(async () => { try { await api.delete('/mysql/' + row.id); this.loadConns() } catch (e) { /* */ } })
        .catch(() => {})
    },
    async testConn(row) {
      row._testing = true
      try {
        const r = await api.get('/mysql/' + row.id + '/ping')
        if (r.code === 0) this.$message.success('连接正常（' + (r.data?.version || '') + '），延迟 ' + (r.data?.latency_ms ?? '') + ' ms')
        else ElMessage.error(r.message)
      } catch (e) { /* 拦截器已提示 */ } finally { row._testing = false }
    },
    enter(row) { this.current = row },
    // ---- AI 接入配置 ----
    // AI 配置是平台级的，统一在「设置」页维护；这里只负责跳过去。
    // 配置变更通过全局事件通知（AI 面板内部监听），不走 props 逐层透传——
    // 触发源在设置页，根本不在 MySQL 的组件树里。
    gotoSettings() { location.hash = '#/settings' },
    // ---- 使用记录 ----
    // tab 决定打开时落在哪个页签：列表页看执行记录（sql），AI 面板看调用记录（ai）；
    // conn 传入时叠加连接过滤（工作台场景），列表页入口不传看全量
    openLogs(tab, conn) {
      this.logsTab = tab === 'ai' ? 'ai' : 'sql'
      this.logsConn = conn || null
      this.logsDialog = true
    }
  }
}
