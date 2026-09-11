window.RegistryPage = {
  props: ['page', 'user', 'versionData'],
  template: `
<div>
  <!-- 视图一：连接列表 -->
  <template v-if="!current">
    <div class="tool-page tool-page--registry">
      <section class="tool-intro">
        <div class="tool-intro-copy">
          <span class="tool-eyebrow">DOCKER REGISTRY</span>
          <h1>镜像仓库</h1>
          <p>连接 Docker Registry v2 · 密码加密存储 · 镜像 / 标签管理</p>
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
          <el-table-column label="备注" prop="remark" min-width="120">
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
        <article v-for="row in conns" :key="row.id" class="tool-card tool-card--registry">
          <header class="tool-card-head">
            <div class="tool-card-icon">R</div>
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
    <el-dialog v-model="dialog" :title="form.id?'编辑连接':'新增连接'" width="540px" append-to-body>
      <el-form :model="form" label-position="top">
        <el-form-item label="名称" required><el-input v-model="form.name" placeholder="例如：内网 Harbor" /></el-form-item>
        <el-form-item label="仓库地址" required>
          <el-input v-model="form.url" placeholder="host:5000 或 http(s)://host:port" />
        </el-form-item>
        <el-form-item label="连接方式">
          <el-radio-group v-model="form.insecure">
            <el-radio :value="false" border>HTTPS（信任证书）</el-radio>
            <el-radio :value="true" border>HTTP / 自签证书（insecure）</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="认证方式">
          <el-radio-group v-model="form.auth_type" @change="onAuthChange">
            <el-radio value="anonymous" border>匿名访问</el-radio>
            <el-radio value="basic" border>账号密码认证</el-radio>
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

  <!-- 视图二：镜像管理 -->
  <template v-else>
    <div class="page-card reg-workspace">
      <div class="card-header reg-whd">
        <el-button text @click="exit" style="margin-right:2px"><el-icon><Back /></el-icon></el-button>
        <div class="reg-whd-title">
          <div class="reg-whd-name">
            <span class="title">{{current.name}}</span>
            <span class="action-badge" :class="current.insecure?'other':'host'">{{current.insecure?'HTTP:insecure':'HTTPS'}}</span>
          </div>
          <div class="mono reg-whd-url">{{current.url}}</div>
        </div>
        <div class="header-extra">
          <el-input v-model="kw" placeholder="搜索仓库..." clearable prefix-icon="Search" style="width:230px;margin-right:10px" @input="filterImgs" />
          <el-button text type="primary" @click="refreshCatalog" :loading="loadingCatalog"><el-icon style="margin-right:4px"><Refresh /></el-icon>刷新</el-button>
        </div>
      </div>

      <div class="reg-statbar">
        <div class="reg-stat"><span class="reg-stat-num mono">{{allImgs.length}}</span><span class="reg-stat-label">仓库总数</span></div>
        <div class="reg-stat"><span class="reg-stat-num mono" :class="{muted:!selImg}">{{selImg||'-'}}</span><span class="reg-stat-label">选中仓库</span></div>
        <div class="reg-stat"><span class="reg-stat-num mono" :class="{muted:appliedTags.length===0}">{{appliedTags.length}}</span><span class="reg-stat-label">当前标签</span></div>
      </div>

      <div class="reg-split">
        <div class="reg-col reg-left">
          <div class="reg-col-head"><span>仓库</span><span class="reg-col-count mono">{{filteredImgs.length}}</span></div>
          <div class="reg-list" v-loading="loadingCatalog">
            <div v-for="img in filteredImgs" :key="img.name"
                 class="reg-item" :class="{active: selImg===img.name}"
                 @click="selectImg(img.name)" :title="img.name">
              <span class="reg-name">{{img.name}}</span>
            </div>
            <empty-state v-if="!loadingCatalog && !filteredImgs.length" text="暂无镜像" />
          </div>
        </div>
        <div class="reg-col reg-right">
          <div class="reg-col-head">
            <template v-if="selImg">仓库标签：<span class="reg-col-selected mono">{{selImg}}</span></template>
            <template v-else>从左侧选择一个仓库查看标签</template>
          </div>
          <div class="reg-list" v-loading="loadingTags">
            <template v-if="selImg">
              <el-table class="hosts-table" :data="tags" style="width:100%" size="small" v-loading="loadingTags">
                <el-table-column label="#" width="44">
                  <template #default="{row,$index}"><span class="mono reg-tag-seq">{{String($index+1).padStart(2,'0')}}</span></template>
                </el-table-column>
                <el-table-column label="标签" min-width="120">
                  <template #default="{row}">
                    <span class="reg-tag-name mono" :title="row">{{row}}</span>
                    <span class="action-badge other" style="margin-left:7px;font-size:10px">tag</span>
                  </template>
                </el-table-column>
                <el-table-column label="大小" width="90">
                  <template #default="{row}"><span class="mono" style="font-size:12px;color:var(--text-sub)">{{tagMeta(row).size!=null ? fmtSize(tagMeta(row).size) : '—'}}</span></template>
                </el-table-column>
                <el-table-column label="创建时间" min-width="156">
                  <template #default="{row}"><span class="mono reg-tag-created" :title="tagMeta(row).created">{{tagMeta(row).created || '—'}}</span></template>
                </el-table-column>
                <el-table-column label="Digest" min-width="200">
                  <template #default="{row}"><span class="reg-tag-digest mono" :title="tagMeta(row).digest">{{tagMeta(row).digest || '—'}}</span></template>
                </el-table-column>
                <el-table-column label="拉取命令" min-width="260">
                  <template #default="{row}">
                    <span class="reg-pull mono" :title="pullCmd(row)">{{pullCmd(row)}}</span>
                  </template>
                </el-table-column>
                <el-table-column label="" fixed="right" width="140">
                  <template #default="{row}">
                    <div class="ops-cell">
                      <el-button size="small" text type="primary" @click="copyPull(row)">复制命令</el-button>
                      <el-button size="small" text type="danger" @click="delTag(row)">删除</el-button>
                    </div>
                  </template>
                </el-table-column>
              </el-table>
              <empty-state v-if="!loadingTags && !tags.length" text="该仓库暂无标签" />
            </template>
            <div v-else class="placeholder-text" style="padding:24px">从左侧选择一个仓库以查看其标签</div>
          </div>
        </div>
      </div>
    </div>
  </template>
</div>`,
  data() {
    return {
      conns: [], loadingConn: false, pg: 1, total: 0, pinging: false,
      viewMode: localStorage.getItem('registry-view-mode') || 'table',
      dialog: false, form: {}, saving: false,
      current: null,
      allImgs: [], filteredImgs: [], appliedTags: [], kw: '', selImg: '',
      loadingCatalog: false, loadingTags: false, tags: [], tagsMeta: {}, enriching: false,
    }
  },
  watch: { viewMode(v) { localStorage.setItem('registry-view-mode', v) } },
  computed: {
    onlineCount() { return this.conns.filter(c => c._st === 1).length }
  },
  mounted() { this.loadConns() },
  methods: {
    async loadConns() {
      this.loadingConn = true
      try { const r = await api.get('/registry', { params: { page: this.pg, page_size: 20 } }); if (r.code === 0) { this.conns = r.data?.list || []; this.total = r.data?.total || 0; this.pingAll() } } catch (e) { /* */ } finally { this.loadingConn = false }
    },
    // 对每个连接做轻量探测，得到在线/不可用/未验证状态
    async pingAll() {
      this.pinging = true
      this.conns.forEach(row => { row._st = undefined })
      await Promise.allSettled(this.conns.map(async (row) => {
        try { const r = await api.get('/registry/' + row.id + '/ping'); row._st = r.code === 0 ? 1 : 0 } catch (e) { row._st = 0 }
      }))
      this.pinging = false
    },
    stText(s) { return s === 1 ? '在线' : s === 0 ? '不可用' : '未验证' },
    stClass(s) { return s === 1 ? 'ok' : s === 0 ? 'fail' : 'idle' },
    openForm(row) {
      this.form = row ? { id: row.id, name: row.name, url: row.url, insecure: !!row.insecure, auth_type: row.auth_type || 'anonymous', username: row.username || '', password: '', remark: row.remark, has_password: row.has_password } : { name: '', url: '', insecure: false, auth_type: 'anonymous', username: '', password: '', remark: '' }
      this.dialog = true
    },
    onAuthChange() { this.form.username = ''; this.form.password = '' },
    async save() {
      this.saving = true
      try {
        const d = { ...this.form }
        if (d.auth_type !== 'basic') { delete d.username; delete d.password }
        else if (!d.password) delete d.password
        if (this.form.id) await api.put('/registry/' + this.form.id, d)
        else await api.post('/registry', d)
        this.dialog = false; this.loadConns()
      } catch (e) { /* */ } finally { this.saving = false }
    },
    delConn(row) {
      this.$confirm('确认删除该镜像仓库连接？连接记录被删除不影响仓库本身。', '提示', { type: 'warning' }).then(async () => { try { await api.delete('/registry/' + row.id); this.loadConns() } catch (e) { /* */ } }).catch(() => {})
    },
    async testConn(row) {
      row._testing = true
      try { const r = await api.get('/registry/' + row.id + '/ping'); if (r.code === 0) this.$message.success('连接正常，延迟 ' + (r.data?.latency_ms ?? '') + ' ms') } catch (e) { /* 拦截器已提示 */ } finally { row._testing = false }
    },
    async enter(row) {
      this.current = row
      this.kw = ''; this.selImg = ''; this.tags = []; this.appliedTags = []; this.filteredImgs = []
      this.loadCatalog()
    },
    exit() { this.current = null; this.selImg = ''; this.kw = ''; this.tags = []; this.appliedTags = [] },
    async loadCatalog() {
      this.loadingCatalog = true
      try { const r = await api.get('/registry/' + this.current.id + '/catalog'); if (r.code === 0) { this.allImgs = r.data?.list || []; this.filterImgs() } } catch (e) { /* */ } finally { this.loadingCatalog = false }
    },
    refreshCatalog() { this.loadCatalog() },
    filterImgs() {
      const k = this.kw.trim().toLowerCase()
      this.filteredImgs = k ? this.allImgs.filter(i => i.name.toLowerCase().includes(k)) : this.allImgs.slice()
    },
    async selectImg(name) {
      this.selImg = name; this.tags = []; this.appliedTags = []; this.tagsMeta = {}; this.loadingTags = true
      try { const r = await api.get('/registry/' + this.current.id + '/tags', { params: { name } }); if (r.code === 0) { this.appliedTags = (r.data?.tags || []).slice().reverse(); this.tags = this.appliedTags } } catch (e) { /* */ } finally { this.loadingTags = false }
      this.enrichTags()
    },
    // 逐 tag 拉取 manifest，行内填充大小/创建时间/digest
    async enrichTags() {
      this.enriching = true
      this.tagsMeta = {}
      await Promise.allSettled(this.tags.map(async (tag) => {
        try {
          const r = await api.get('/registry/' + this.current.id + '/manifest', { params: { name: this.selImg, reference: tag } })
          if (r.code === 0 && r.data) {
            const m = r.data
            const meta = { size: this.layerSum(m), created: m.created || '', digest: m.digest || '' }
            this.tagsMeta = { ...this.tagsMeta, [tag]: meta }
          }
        } catch (e) { /* 单个 tag 失败不影响其它 */ }
      }))
      this.enriching = false
    },
    tagMeta(tag) { return this.tagsMeta[tag] || {} },
    layerSum(m) {
      let t = m?.config?.size || 0
      for (const l of m?.layers || []) t += l.size || 0
      return t
    },
    delTag(tag) {
      const name = this.selImg
      this.$confirm('确认删除镜像 ' + name + ':' + tag + ' ？\n删除为「标记删除」，后续需在仓库侧执行 GC 才能释放存储空间。', '删除镜像', { type: 'warning' }).then(async () => {
        try {
          const r = await api.delete('/registry/' + this.current.id + '/repo', { params: { name, reference: tag } })
          if (r.code === 0) { this.$message.success('已删除 ' + name + ':' + tag); this.selectImg(name) }
        } catch (e) { /* */ }
      }).catch(() => {})
    },
    // 由仓库地址（去掉 scheme 与尾部斜杠）拼接 docker pull 全命令
    pullCmd(tag) {
      let addr = String(this.current?.url || '').replace(/^[a-zA-Z]+:\/\//, '').replace(/\/+$/, '')
      return 'docker pull ' + addr + '/' + this.selImg + ':' + tag
    },
    async copyPull(tag) {
      try {
        await navigator.clipboard.writeText(this.pullCmd(tag))
        this.$message.success('拉取命令已复制')
      } catch (e) { this.$message.error('复制失败，请手动框选') }
    },
    fmtSize(n) {
      if (!n && n !== 0) return '-'
      if (n < 1024) return n + ' B'
      if (n < 1048576) return (n / 1024).toFixed(1) + ' KB'
      if (n < 1073741824) return (n / 1048576).toFixed(1) + ' MB'
      return (n / 1073741824).toFixed(2) + ' GB'
    }
  }
}