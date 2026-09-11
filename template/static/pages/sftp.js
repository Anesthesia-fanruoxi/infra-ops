window.SFTPPage = {
  props: ['page', 'user', 'versionData'],
  template: `
<div>
  <!-- 视图一：连接列表 -->
  <template v-if="!current">
    <div class="tool-page tool-page--sftp">
      <section class="tool-intro">
        <div class="tool-intro-copy">
          <span class="tool-eyebrow">SECURE FILE TRANSFER</span>
          <h1>SFTP</h1>
          <p>SSH 文件传输 · 密码 / 私钥加密存储 · 浏览上传下载</p>
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

      <!-- 表格视图 -->
      <div v-if="viewMode==='table'">
        <el-table class="hosts-table" :data="conns" style="width:100%" v-loading="loadingConn">
          <el-table-column label="连接" min-width="220">
            <template #default="{row}">
              <div><span style="font-weight:600">{{row.name}}</span>
                <span v-if="row.has_key" class="action-badge host" style="margin-left:7px;font-size:10.5px">私钥</span></div>
              <div class="mono" style="font-size:12px;color:#9CA3AF">{{row.username}}@{{row.host}}:{{row.port}}</div>
            </template>
          </el-table-column>
          <el-table-column label="状态" width="98">
            <template #default="{row}"><span class="tool-status" :class="stClass(row._st)"><span class="st-dot"></span>{{row._testing ? '探测中' : stText(row._st)}}</span></template>
          </el-table-column>
          <el-table-column label="认证" width="110">
            <template #default="{row}"><span class="action-badge" :class="row.has_password?'credential':'other'">{{row.has_password?'密码':(row.has_key?'私钥':'未配置')}}</span></template>
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

      <!-- 卡片视图 -->
      <div v-else class="hosts-card-grid tool-card-grid" v-loading="loadingConn">
        <article v-for="row in conns" :key="row.id" class="tool-card tool-card--sftp">
          <header class="tool-card-head">
            <div class="tool-card-icon">S</div>
            <div class="tool-card-idbox">
              <h3 class="tool-card-name" :title="row.name">{{row.name}}</h3>
              <div class="tool-card-endpoint mono" :title="row.host">{{row.username}}@{{row.host}}:{{row.port}}</div>
              <span class="tool-status" :class="stClass(row._st)"><span class="st-dot"></span>{{row._testing ? '探测中' : stText(row._st)}}</span>
            </div>
            <el-button text class="host-card-more" @click.stop="openForm(row)" title="编辑">···</el-button>
          </header>
          <div class="tool-card-meta">
            <span class="action-badge" :class="row.has_key?'credential':'other'">{{row.has_key?'私钥认证':(row.has_password?'密码认证':'未配置')}}</span>
            <span v-if="row.has_password" class="tool-card-user mono">密码已存</span>
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
        <el-form-item label="名称" required><el-input v-model="form.name" placeholder="例如：生产 Web01" /></el-form-item>
        <el-form-item label="主机地址" required><el-input v-model="form.host" placeholder="ip 或 hostname，如 172.16.2.11" /></el-form-item>
        <div class="sftp-two-col">
          <el-form-item label="端口" required><el-input-number v-model="form.port" :min="1" :max="65535" style="width:100%" /></el-form-item>
          <el-form-item label="用户名" required><el-input v-model="form.username" placeholder="ssh 用户名" /></el-form-item>
        </div>
        <el-form-item label="密码"><el-input v-model="form.password" type="password" show-password :placeholder="form.has_password?'留空则不修改':''" /></el-form-item>
        <el-form-item label="私钥（PEM）"><el-input v-model="form.key" type="textarea" :rows="4" :placeholder="form.has_key?'留空则保留原私钥':'如填入私钥则优先使用私钥认证'" /></el-form-item>
        <el-form-item label="备注"><el-input v-model="form.remark" type="textarea" :rows="2" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="dialog=false">取消</el-button><el-button type="primary" :loading="saving" @click="save">保存</el-button></template>
    </el-dialog>
  </template>

  <!-- 视图二：文件浏览器 -->
  <template v-else>
    <div class="page-card reg-workspace sftp-workspace">
      <div class="card-header reg-whd">
        <el-button text @click="exit" style="margin-right:2px"><el-icon><Back /></el-icon></el-button>
        <div class="reg-whd-title">
          <div class="reg-whd-name">
            <span class="title">{{current.name}}</span>
            <span class="action-badge host" style="margin-left:6px">SFTP</span>
          </div>
          <div class="mono reg-whd-url">{{current.username}}@{{current.host}}:{{current.port}}</div>
        </div>
        <div class="header-extra">
          <el-input v-model="filterKw" placeholder="过滤当前目录..." clearable prefix-icon="Search" style="width:210px;margin-right:10px" />
          <el-button text @click="loadBrowse"><el-icon style="margin-right:4px"><Refresh /></el-icon>刷新</el-button>
          <el-button @click="pickUpload" type="primary"><el-icon style="margin-right:4px"><Upload /></el-icon>上传</el-button>
          <el-button @click="openMkdir">新建目录</el-button>
        </div>
      </div>

      <div class="reg-statbar es-statbar sftp-pathbar">
        <el-breadcrumb separator="/">
          <el-breadcrumb-item v-for="seg in breadcrumb" :key="seg.path" class="sftp-crumb" @click.prevent="goTo(seg.path)">
            {{seg.label}}
          </el-breadcrumb-item>
        </el-breadcrumb>
      </div>

      <div class="sftp-list" v-loading="loadingBrowse">
        <el-table class="hosts-table" :data="visibleEntries" style="width:100%" size="small" @row-dblclick="rowDbl">
          <el-table-column label="名称" min-width="260">
            <template #default="{row}">
              <span class="sftp-name" :class="row.is_dir?'is-dir':''" @click="openEntry(row)">
                <span class="sftp-icon">{{row.is_dir ? '📁' : '📄'}}</span>
                <span>{{row.name}}</span><span class="sftp-mode mono">{{row.mode}}</span>
              </span>
            </template>
          </el-table-column>
          <el-table-column label="大小" width="130">
            <template #default="{row}"><span class="mono sftp-size">{{row.is_dir ? '—' : fmtSize(row.size)}}</span></template>
          </el-table-column>
          <el-table-column label="修改时间" width="170">
            <template #default="{row}"><span class="mono" style="font-size:12px;color:#6B7280">{{row.mtime}}</span></template>
          </el-table-column>
          <el-table-column label="" fixed="right" width="210">
            <template #default="{row}">
              <div class="ops-cell">
                <el-button v-if="!row.is_dir" size="small" text type="primary" @click="download(row)">下载</el-button>
                <el-button size="small" text @click="openRename(row)">重命名</el-button>
                <el-button size="small" text type="danger" @click="delEntry(row)">删除</el-button>
              </div>
            </template>
          </el-table-column>
        </el-table>
        <empty-state v-if="!loadingBrowse && !visibleEntries.length" text="当前目录为空" />
      </div>
    </div>

    <input ref="fileInput" type="file" style="display:none" @change="onFilePicked" />

    <el-dialog v-model="mkdirDialog" title="新建目录" width="440px" append-to-body>
      <el-input v-model="mkdirName" placeholder="目录名" @keyup.enter="doMkdir" />
      <template #footer><el-button @click="mkdirDialog=false">取消</el-button><el-button type="primary" :loading="mkdirIng" @click="doMkdir">创建</el-button></template>
    </el-dialog>

    <el-dialog v-model="renameDialog" title="重命名" width="440px" append-to-body>
      <el-input v-model="renameNew" placeholder="新名称" @keyup.enter="doRename" />
      <template #footer><el-button @click="renameDialog=false">取消</el-button><el-button type="primary" :loading="renameIng" @click="doRename">确定</el-button></template>
    </el-dialog>
  </template>
</div>`,
  data() {
    return {
      conns: [], loadingConn: false, pg: 1, total: 0, pinging: false,
      viewMode: localStorage.getItem('sftp-view-mode') || 'table',
      dialog: false, form: {}, saving: false,
      current: null,
      cwd: '/', entries: [], filterKw: '', loadingBrowse: false,
      mkdirDialog: false, mkdirName: '', mkdirIng: false,
      renameDialog: false, renameFrom: '', renameNew: '', renameIng: false
    }
  },
  computed: {
    onlineCount() { return this.conns.filter(c => c._st === 1).length },
    breadcrumb() {
      const parts = this.cwd === '/' ? [] : this.cwd.split('/').filter(Boolean)
      const segs = [{ path: '/', label: '/' }]
      let acc = ''
      for (const p of parts) { acc += '/' + p; segs.push({ path: acc, label: p }) }
      return segs
    },
    visibleEntries() {
      const k = this.filterKw.trim().toLowerCase()
      if (!k) return this.entries
      return this.entries.filter(e => e.name.toLowerCase().includes(k))
    }
  },
  watch: { viewMode(v) { localStorage.setItem('sftp-view-mode', v) } },
  mounted() { this.loadConns() },
  methods: {
    async loadConns() {
      this.loadingConn = true
      try { const r = await api.get('/sftp', { params: { page: this.pg, page_size: 20 } }); if (r.code === 0) { this.conns = r.data?.list || []; this.total = r.data?.total || 0; this.pingAll() } } catch (e) { /* */ } finally { this.loadingConn = false }
    },
    // 对每个连接做轻量探测，得到在线/不可用/未验证状态
    async pingAll() {
      this.pinging = true
      this.conns.forEach(row => { row._st = undefined })
      await Promise.allSettled(this.conns.map(async (row) => {
        try { const r = await api.get('/sftp/' + row.id + '/ping'); row._st = r.code === 0 ? 1 : 0 } catch (e) { row._st = 0 }
      }))
      this.pinging = false
    },
    stText(s) { return s === 1 ? '在线' : s === 0 ? '不可用' : '未验证' },
    stClass(s) { return s === 1 ? 'ok' : s === 0 ? 'fail' : 'idle' },
    openForm(row) {
      this.form = row ? { id: row.id, name: row.name, host: row.host, port: row.port || 22, username: row.username || '', password: '', key: '', remark: row.remark, has_password: row.has_password, has_key: row.has_key } : { name: '', host: '', port: 22, username: '', password: '', key: '', remark: '' }
      this.dialog = true
    },
    async save() {
      if (!this.form.host) { ElMessage.warning('请填写主机地址'); return }
      this.saving = true
      try {
        const d = { ...this.form }
        if (!d.password) delete d.password
        if (!d.key) delete d.key
        if (this.form.id) await api.put('/sftp/' + this.form.id, d)
        else await api.post('/sftp', d)
        this.dialog = false; this.loadConns()
      } catch (e) { /* */ } finally { this.saving = false }
    },
    delConn(row) {
      this.$confirm('确认删除该 SFTP 连接？不影响远程服务器。', '提示', { type: 'warning' }).then(async () => { try { await api.delete('/sftp/' + row.id); this.loadConns() } catch (e) { /* */ } }).catch(() => {})
    },
    async testConn(row) {
      row._testing = true
      try { const r = await api.get('/sftp/' + row.id + '/ping'); if (r.code === 0) this.$message.success('连接正常，延迟 ' + (r.data?.latency_ms ?? '') + ' ms') } catch (e) { ElMessage.error('连接失败') } finally { row._testing = false }
    },
    async enter(row) {
      this.current = row; this.cwd = '/'; this.entries = []; this.filterKw = ''
      this.loadBrowse()
    },
    exit() { this.current = null; this.entries = [] },
    async loadBrowse() {
      if (!this.current) return
      this.loadingBrowse = true
      try {
        const r = await api.get('/sftp/' + this.current.id + '/browse', { params: { path: this.cwd } })
        if (r.code === 0) { const d = r.data || {}; this.cwd = d.current || this.cwd; this.entries = d.entries || [] }
      } catch (e) { /* */ } finally { this.loadingBrowse = false }
    },
    goTo(path) { this.cwd = path; this.loadBrowse() },
    openEntry(row) { if (row.is_dir) { this.cwd = row.path; this.loadBrowse() } },
    rowDbl(row) { if (row.is_dir) { this.cwd = row.path; this.loadBrowse() } },
    pickUpload() { this.$refs.fileInput.value = ''; this.$refs.fileInput.click() },
    async onFilePicked(e) {
      const file = e.target.files && e.target.files[0]
      if (!file) return
      const fd = new FormData()
      fd.append('path', this.cwd)
      fd.append('file', file)
      try {
        const r = await api.post('/sftp/' + this.current.id + '/upload', fd)
        if (r.code === 0) { ElMessage.success('已上传 ' + file.name); this.loadBrowse() }
      } catch (err) { /* */ }
    },
    async download(row) {
      try {
        const blob = await api.get('/sftp/' + this.current.id + '/download', { params: { path: row.path }, responseType: 'blob' })
        if (!(blob instanceof Blob)) { ElMessage.error(blob?.message || '下载失败'); return }
        const url = window.URL.createObjectURL(blob)
        const a = document.createElement('a')
        a.href = url; a.download = row.name; document.body.appendChild(a); a.click(); a.remove()
        window.URL.revokeObjectURL(url)
      } catch (e) { /* */ }
    },
    openMkdir() { this.mkdirName = ''; this.mkdirDialog = true },
    async doMkdir() {
      if (!this.mkdirName.trim()) return
      this.mkdirIng = true
      try {
        const r = await api.post('/sftp/' + this.current.id + '/mkdir', { path: this.cwd + '/' + this.mkdirName.trim() })
        if (r.code === 0) { this.mkdirDialog = false; this.loadBrowse() }
      } catch (e) { /* */ } finally { this.mkdirIng = false }
    },
    openRename(row) { this.renameFrom = row; this.renameNew = row.name; this.renameDialog = true },
    async doRename() {
      if (!this.renameNew.trim()) return
      this.renameIng = true
      try {
        const r = await api.post('/sftp/' + this.current.id + '/rename', { from: this.renameFrom.path, to: this.renameNew.trim() })
        if (r.code === 0) { this.renameDialog = false; this.loadBrowse() }
      } catch (e) { /* */ } finally { this.renameIng = false }
    },
    delEntry(row) {
      this.$confirm('确认删除 ' + row.path + ' ？' + (row.is_dir ? '目录将被递归删除！' : ''), '删除', { type: 'warning' }).then(async () => {
        try { const r = await api.delete('/sftp/' + this.current.id + '/delete', { params: { path: row.path } }); if (r.code === 0) this.loadBrowse() } catch (e) { /* */ }
      }).catch(() => {})
    },
    fmtSize(n) {
      if (n == null) return '-'
      if (n < 1024) return n + ' B'
      if (n < 1048576) return (n / 1024).toFixed(1) + ' KB'
      if (n < 1073741824) return (n / 1048576).toFixed(1) + ' MB'
      return (n / 1073741824).toFixed(2) + ' GB'
    }
  }
}