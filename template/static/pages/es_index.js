// ES 控制台 · 索引 tab（F1/F2 拆分承接）：索引列表 + 单索引原始 mapping 抽屉
// + 既有新建/删除索引入口原样保留（设计 §3.2/§14.9）。
window.EsIndexTab = {
  props: ['conn'],
  template: `
<div>
  <div class="reg-statbar es-statbar">
    <div class="reg-stat">
      <span class="reg-stat-num mono" v-if="!loadingTotal">{{allTotal}}</span>
      <!-- 总数与列表拆成两个请求：列表先到先渲染，总数位异步转圈等它回来 -->
      <span class="reg-stat-num" v-else><el-icon class="is-loading"><Loading /></el-icon></span>
      <span class="reg-stat-label">索引总数</span>
    </div>
    <div class="reg-stat"><span class="reg-stat-num mono" :class="{muted:idxKw&&!idxTotal}">{{idxTotal}}</span><span class="reg-stat-label">{{idxKw?'搜索匹配':'当前展示'}}</span></div>
    <div class="reg-stat"><span class="reg-stat-num mono">{{idxSummary.docs>=0?idxSummary.docs:'-'}}</span><span class="reg-stat-label">文档数（展示 {{indices.length}} 条）</span></div>
  </div>
  <div class="es-toolbar">
    <el-input v-model="idxKw" placeholder="搜索索引（在全部索引里匹配）..." clearable prefix-icon="Search" style="width:260px" @input="onIdxKw" />
    <el-button type="primary" style="margin-left:auto" @click="openCreate">新建索引</el-button>
  </div>
  <div class="es-qc-hint" v-if="idxTruncated" style="margin:-2px 0 6px">共匹配 {{idxTotal}} 个索引，仅展示前 {{indices.length}} 条——用搜索缩小范围</div>
  <el-table class="hosts-table" :data="filteredIndices" style="width:100%" size="small" v-loading="loadingIndices">
    <el-table-column label="索引" min-width="200">
      <template #default="{row}"><span class="mono es-link" @click="showMapping(row)">{{row.index}}</span></template>
    </el-table-column>
    <el-table-column label="健康" width="90">
      <template #default="{row}"><span class="es-health"><span class="health-dot" :style="{background:healthColor(row.health)}"></span><span>{{healthLabel(row.health)}}</span></span></template>
    </el-table-column>
    <el-table-column label="状态" width="80"><template #default="{row}"><span class="mono">{{row.status}}</span></template></el-table-column>
    <el-table-column label="主分片" width="76"><template #default="{row}"><span class="mono">{{row.pri}}</span></template></el-table-column>
    <el-table-column label="副本" width="60"><template #default="{row}"><span class="mono">{{row.rep}}</span></template></el-table-column>
    <el-table-column label="文档数" width="96"><template #default="{row}"><span class="mono">{{row.docs_count}}</span></template></el-table-column>
    <el-table-column label="存储大小" width="110"><template #default="{row}"><span class="mono">{{row.store_size}}</span></template></el-table-column>
    <el-table-column label="" fixed="right" width="90">
      <template #default="{row}">
        <div class="ops-cell"><el-button size="small" text type="danger" @click="delIndex(row)">删除</el-button></div>
      </template>
    </el-table-column>
  </el-table>

  <el-dialog v-model="createDialog" title="新建索引" width="480px" append-to-body>
    <el-form label-position="top">
      <el-form-item label="索引名称" required><el-input v-model="createForm.index" placeholder="小写，不含空格" style="font-family:var(--font-mono)" /></el-form-item>
      <el-form-item label="主分片数"><el-input-number v-model="createForm.shards" :min="1" :max="100" /></el-form-item>
      <el-form-item label="副本数"><el-input-number v-model="createForm.replicas" :min="0" :max="10" /></el-form-item>
    </el-form>
    <template #footer><el-button @click="createDialog=false">取消</el-button><el-button type="primary" :loading="creating" @click="doCreateIndex">创建</el-button></template>
  </el-dialog>

  <el-drawer v-model="mappingDrawer" :title="'原始 mapping · ' + mappingIndex" size="55%" class="es-raw-drawer">
    <div class="es-mapping-hint">实时拉取的真实映射（排障用），与视图的合并字段表相互独立</div>
    <pre class="es-source" v-loading="loadingMapping">{{mappingText}}</pre>
  </el-drawer>
</div>`,
  data() {
    return {
      indices: [], filteredIndices: [], idxKw: '', loadingIndices: false,
      idxTotal: 0, allTotal: 0, loadingTotal: false, idxTruncated: false,
      createDialog: false, createForm: { index: '', shards: 1, replicas: 0 }, creating: false,
      mappingDrawer: false, mappingIndex: '', mappingText: '', loadingMapping: false
    }
  },
  computed: {
    idxSummary() {
      let docs = 0
      for (const i of this.filteredIndices) { const n = parseInt(i.docs_count, 10); if (!isNaN(n)) docs += n }
      return { docs }
    }
  },
  mounted() { this.loadIndices() },
  beforeUnmount() { if (this._idxTimer) { clearTimeout(this._idxTimer); this._idxTimer = null } },
  methods: {
    async loadIndices() {
      this.loadingIndices = true
      this.loadTotal() // 总数独立异步：慢一步也不挡列表渲染
      try {
        const r = await api.get('/es/' + this.conn.id + '/indices')
        if (r.code === 0) this.applyIdxResp(r.data)
      } catch (e) { /* */ } finally { this.loadingIndices = false }
    },
    // loadTotal 索引总数：独立请求 + 左上角转圈；只列索引名（不含统计列），比列表请求轻
    async loadTotal() {
      this.loadingTotal = true
      try {
        const r = await api.get('/es/' + this.conn.id + '/indices/total')
        if (r.code === 0 && r.data) this.allTotal = r.data.total || 0
      } catch (e) { /* 总数是旁路展示：取不到保留旧值 */ } finally { this.loadingTotal = false }
    },
    // onIdxKw 搜索框输入：防抖后走后端在全部索引里匹配（不再只过滤已加载的前 100 条）
    onIdxKw() {
      if (this._idxTimer) clearTimeout(this._idxTimer)
      this._idxTimer = setTimeout(() => this.searchIndices(), 300)
    },
    async searchIndices() {
      const q = this.idxKw.trim()
      // 无通配符按「包含」搜（*q*）；用户自己写了 * / ? 就原样交给 ES 当索引导出模式
      const glob = q ? (/[*?]/.test(q) ? q : '*' + q + '*') : ''
      this.loadingIndices = true
      try {
        const r = await api.get('/es/' + this.conn.id + '/indices', { params: glob ? { q: glob } : {} })
        if (r.code === 0) this.applyIdxResp(r.data)
      } catch (e) { /* */ } finally { this.loadingIndices = false }
    },
    // applyIdxResp 落地一次 /indices 结果（列表与搜索匹配数）。
    // 左上角「索引总数」不从这里取：它走 /indices/total 独立异步（见 loadTotal）。
    applyIdxResp(d) {
      d = d || {}
      this.indices = d.list || []
      this.filteredIndices = this.indices.slice()
      this.idxTotal = d.total || 0
      this.idxTruncated = !!d.truncated
    },
    // reload 新建/删除后刷新：有搜索词就沿用搜索，别把用户打回全量列表（总数一并重取）
    reload() {
      if (this.idxKw.trim()) { this.loadTotal(); return this.searchIndices() }
      return this.loadIndices()
    },
    openCreate() { this.createForm = { index: '', shards: 1, replicas: 0 }; this.createDialog = true },
    async doCreateIndex() {
      if (!this.createForm.index) { this.$message.warning('请输入索引名称'); return }
      this.creating = true
      try {
        const r = await api.post('/es/' + this.conn.id + '/index', this.createForm)
        if (r.code === 0) { this.createDialog = false; this.$message.success('索引创建成功'); this.reload() }
      } catch (e) { /* */ } finally { this.creating = false }
    },
    delIndex(row) {
      this.$confirm('确认删除索引「' + row.index + '」？' + row.docs_count + ' 个文档将一并删除，此操作不可恢复！', '删除索引', { type: 'warning' }).then(() => {
        this.$confirm('再次确认：删除索引 ' + row.index + ' 会永久丢失全部数据。', '二次确认', { type: 'warning' }).then(async () => {
          row._deleting = true
          try { const r = await api.delete('/es/' + this.conn.id + '/index/' + encodeURIComponent(row.index)); if (r.code === 0) { this.$message.success('索引已删除'); this.reload() } } catch (e) { /* */ } finally { row._deleting = false }
        }).catch(() => {})
      }).catch(() => {})
    },
    async showMapping(row) {
      this.mappingIndex = row.index; this.mappingDrawer = true
      this.loadingMapping = true; this.mappingText = ''
      try {
        const r = await api.get('/es/' + this.conn.id + '/indices/' + encodeURIComponent(row.index) + '/mapping')
        if (r.code === 0) this.mappingText = JSON.stringify(r.data.mapping, null, 2)
      } catch (e) { /* */ } finally { this.loadingMapping = false }
    },
    healthLabel(h) { return h === 'green' ? '健康' : h === 'yellow' ? '警告' : h === 'red' ? '危险' : h || '-' },
    healthColor(s) { return s === 'green' ? 'var(--ok)' : s === 'yellow' ? 'var(--warn)' : 'var(--danger)' }
  }
}
