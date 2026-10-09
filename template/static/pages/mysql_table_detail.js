// MySQL 工作台 · 表详情：概览统计 + 字段 / 索引 / DDL 三个子页签。
// 点表不再自动拼 SELECT 执行，而是打开这里；要取数点右上角「查询数据」。
window.MySQLTableDetail = {
  props: {
    conn: { type: Object, required: true },
    schema: { type: String, default: '' },
    table: { type: String, default: '' }
  },
  emits: ['query-data', 'dump'],
  template: `
<div class="mysql-detail">
  <template v-if="table">
    <div class="mysql-detail-head">
      <div class="mysql-detail-title">
        <span class="mysql-detail-name mono" :title="schema + '.' + table">{{ schema }}.{{ table }}</span>
        <span class="action-badge host" v-if="status.engine">{{ status.engine }}</span>
        <span class="action-badge info" v-if="status.collation">{{ status.collation }}</span>
        <span v-if="status.rows">{{ fmtNum(status.rows) }} 行</span>
      </div>
      <div class="mysql-detail-actions">
        <el-button size="small" plain @click="$emit('dump')" title="整表全量导出为 SQL / Excel / CSV">
          <el-icon style="margin-right:4px"><Download /></el-icon>转储
        </el-button>
        <el-button size="small" type="primary" plain @click="$emit('query-data')">
          <el-icon style="margin-right:4px"><Search /></el-icon>查询数据
        </el-button>
      </div>
    </div>

    <div class="mysql-detail-stats">
      <span class="mysql-stat"><i>字段</i><b>{{ columns.length }}</b></span>
      <span class="mysql-stat"><i>行数</i><b>{{ fmtNum(status.rows) }}</b></span>
      <span class="mysql-stat"><i>数据长度</i><b>{{ fmtBytes(status.data_length) }}</b></span>
      <span class="mysql-stat"><i>索引长度</i><b>{{ fmtBytes(status.index_length) }}</b></span>
      <span class="mysql-stat"><i>创建时间</i><b class="mono">{{ status.created_at || '-' }}</b></span>
      <span class="mysql-stat"><i>更新时间</i><b class="mono">{{ status.updated_at || '-' }}</b></span>
      <span class="mysql-stat is-wide" v-if="status.comment"><i>注释</i><b>{{ status.comment }}</b></span>
    </div>

    <div class="mysql-tabs mysql-detail-tabs">
      <button type="button" class="mysql-tab" :class="{active: sub==='fields'}" @click="sub='fields'">字段 {{ columns.length }}</button>
      <button type="button" class="mysql-tab" :class="{active: sub==='indexes'}" @click="sub='indexes'">索引 {{ indexes.length }}</button>
      <button type="button" class="mysql-tab" :class="{active: sub==='ddl'}" @click="sub='ddl'">DDL</button>
      <span class="mysql-spacer"></span>
      <el-button text size="small" :loading="loading" @click="load"><el-icon style="margin-right:4px"><Refresh /></el-icon>刷新</el-button>
    </div>

    <div class="mysql-detail-body" v-loading="loading">
      <el-table v-if="sub==='fields'" class="hosts-table" :data="columns" size="small" style="width:100%">
        <el-table-column label="字段名" min-width="170">
          <template #default="{row}">
            <span class="mono" style="font-weight:600">{{ row.name }}</span>
            <span v-if="row.key==='PRI'" class="mysql-key-tag" title="主键">PK</span>
          </template>
        </el-table-column>
        <el-table-column label="数据类型" min-width="150">
          <template #default="{row}"><span class="mono" style="font-size:12px">{{ row.type }}</span></template>
        </el-table-column>
        <el-table-column label="允许空值" width="86">
          <template #default="{row}">
            <span class="mysql-flag" :class="row.nullable ? 'is-yes' : 'is-no'">{{ row.nullable ? 'YES' : 'NO' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="键" width="80">
          <template #default="{row}">
            <span v-if="row.key" class="action-badge" :class="row.key==='PRI' ? 'credential' : 'info'">{{ row.key }}</span>
            <span v-else class="mysql-dash">-</span>
          </template>
        </el-table-column>
        <el-table-column label="默认值" min-width="130">
          <template #default="{row}">
            <span v-if="row.default === null" class="mysql-dash">-</span>
            <span v-else class="mono" style="font-size:12px">{{ row.default === '' ? '(空串)' : row.default }}</span>
          </template>
        </el-table-column>
        <el-table-column label="额外" min-width="150">
          <template #default="{row}"><span class="mono" style="font-size:12px;color:#6B7280">{{ row.extra || '-' }}</span></template>
        </el-table-column>
        <el-table-column label="注释" min-width="160">
          <template #default="{row}"><span style="color:#6B7280">{{ row.comment || '-' }}</span></template>
        </el-table-column>
      </el-table>

      <el-table v-else-if="sub==='indexes'" class="hosts-table" :data="indexes" size="small" style="width:100%">
        <el-table-column label="索引名" min-width="200">
          <template #default="{row}"><span class="mono" style="font-weight:600">{{ row.name }}</span></template>
        </el-table-column>
        <el-table-column label="唯一" width="100">
          <template #default="{row}">
            <span class="action-badge" :class="row.unique ? 'credential' : 'other'">{{ row.unique ? 'UNIQUE' : '普通' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="列" min-width="260">
          <template #default="{row}"><span class="mono" style="font-size:12px">{{ row.columns.join(' , ') }}</span></template>
        </el-table-column>
      </el-table>

      <div v-else class="mysql-ddl">
        <pre v-if="ddl">{{ ddl }}</pre>
        <empty-state v-else :text="ddlError || '未取到建表语句'" />
      </div>
    </div>
  </template>
  <empty-state v-else text="在左侧点选一张表，这里会显示表详情" />
</div>`,
  data() {
    return { loading: false, sub: 'fields', status: {}, columns: [], indexes: [], ddl: '', ddlError: '' }
  },
  watch: {
    table() { this.reload() },
    schema() { this.reload() }
  },
  methods: {
    // 切表时先把上一个表的内容清干净，避免旧内容闪现在新表名下
    reload() {
      this.status = {}
      this.columns = []
      this.indexes = []
      this.ddl = ''
      this.ddlError = ''
      this.sub = 'fields'
      this.load()
    },
    async load() {
      if (!this.table || !this.schema) return
      this.loading = true
      try {
        const r = await api.get('/mysql/' + this.conn.id + '/table', {
          params: { schema: this.schema, table: this.table }
        })
        if (r.code === 0 && r.data) {
          this.status = r.data.status || {}
          this.columns = r.data.columns || []
          this.indexes = r.data.indexes || []
          this.ddl = r.data.ddl || ''
          this.ddlError = r.data.ddl_error || ''
        }
      } catch (e) { /* 拦截器已提示 */ } finally { this.loading = false }
    },
    fmtNum(n) {
      if (n === null || n === undefined) return '0'
      const v = Number(n)
      if (v >= 1000000) return (v / 1000000).toFixed(1) + 'M'
      if (v >= 1000) return v.toLocaleString('en-US')
      return String(v)
    },
    fmtBytes(n) {
      const v = Number(n || 0)
      if (v <= 0) return '-'
      if (v >= 1073741824) return (v / 1073741824).toFixed(2) + ' GB'
      if (v >= 1048576) return (v / 1048576).toFixed(2) + ' MB'
      if (v >= 1024) return (v / 1024).toFixed(1) + ' KB'
      return v + ' B'
    }
  }
}
