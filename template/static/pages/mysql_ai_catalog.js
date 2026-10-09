// MySQL AI · 语义目录浏览抽屉：按库折叠展示目录内容（表用途 + 字段说明），
// 让人在提问前快速了解「这个连接里都有什么、每张表大概是干嘛的」。
// 数据来自 GET /mysql/:id/ai/catalog（不带 schema = 整个连接）。
window.MySQLAICatalog = {
  props: {
    connId: { type: Number, required: true }
  },
  emits: ['close'],
  template: `
<el-drawer v-model="open" title="语义目录" size="520px" append-to-body class="mysql-catalog-drawer"
  :destroy-on-close="false" @closed="$emit('close')">
  <div class="mysql-catalog" v-loading="loading">
    <div class="mysql-catalog-bar">
      <el-input v-model="kw" size="small" clearable placeholder="按表名 / 用途 / 字段过滤"
        prefix-icon="Search" class="mysql-catalog-kw" />
      <span class="mysql-catalog-total">{{ totalText }}</span>
      <el-button text size="small" :loading="loading" @click="load"><el-icon><Refresh /></el-icon></el-button>
    </div>

    <div class="mysql-catalog-tip mysql-ai-tip">
      目录由 AI 读表与字段注释归纳而成，结构变了重新生成即可；生成 SQL 时只把与问题相关的表送进上下文。
    </div>

    <div class="mysql-catalog-body">
      <div v-for="g in groups" :key="g.name" class="mysql-catalog-db">
        <div class="mysql-catalog-db-head" @click="toggleDb(g.name)">
          <span class="mysql-caret">{{ openDbs[g.name] === false ? '▸' : '▾' }}</span>
          <span class="mysql-catalog-db-name" :title="g.name">{{ g.name }}</span>
          <span class="mysql-tb-count">{{ g.tables.length }} 张表</span>
        </div>
        <div v-if="openDbs[g.name] !== false" class="mysql-catalog-tables">
          <div v-for="t in g.tables" :key="g.name + '.' + t.table_name" class="mysql-catalog-table"
            :class="{expanded: expanded[g.name + '.' + t.table_name]}"
            @click="toggleTable(g.name, t.table_name)">
            <div class="mysql-catalog-table-row">
              <span class="mysql-tb-icon base">T</span>
              <span class="mysql-catalog-tname mono" :title="t.table_name">{{ t.table_name }}</span>
              <span class="mysql-catalog-purpose" :title="t.purpose">{{ t.purpose || '（无归纳说明）' }}</span>
            </div>
            <div v-if="expanded[g.name + '.' + t.table_name]" class="mysql-catalog-cols">
              <div v-for="c in t.columns" :key="c.name" class="mysql-catalog-col">
                <span class="mono">{{ c.name }}</span>
                <span class="mysql-catalog-col-note">{{ c.note || '-' }}</span>
              </div>
              <div v-if="!t.columns.length" class="mysql-catalog-col-empty">无字段说明</div>
            </div>
          </div>
          <div v-if="!g.tables.length" class="mysql-catalog-col-empty">无匹配表</div>
        </div>
      </div>
      <empty-state v-if="!loading && !groups.length" :text="kw.trim() ? '没有匹配的表' : '还没有语义目录：回 AI 面板点「立即生成」或「批量生成」'" />
    </div>
  </div>
</el-drawer>`,
  data() {
    return {
      open: true,
      loading: false,
      list: [],
      schemas: [],
      kw: '',
      // 默认全部展开（目录本来就是为了浏览）；openDbs 只记录「被手动收起」的库
      openDbs: {},
      expanded: {}
    }
  },
  computed: {
    totalText() {
      if (!this.schemas.length) return ''
      return this.schemas.length + ' 个库 · ' + this.list.length + ' 张表'
    },
    // 按库分组 + 关键字过滤：命中表名/用途/字段名/字段说明任一处即保留该表；
    // 库内全部被滤掉时该库整组隐藏
    groups() {
      const k = this.kw.trim().toLowerCase()
      const out = []
      this.schemas.forEach(s => {
        const tables = this.list.filter(e => {
          if (e.schema_name !== s.name) return false
          if (!k) return true
          if (e.table_name.toLowerCase().includes(k) || (e.purpose || '').toLowerCase().includes(k)) return true
          return (e.columns || []).some(c =>
            c.name.toLowerCase().includes(k) || (c.note || '').toLowerCase().includes(k))
        })
        if (tables.length) out.push({ name: s.name, tables: tables })
      })
      return out
    }
  },
  mounted() { this.load() },
  methods: {
    async load() {
      this.loading = true
      try {
        // 不带 schema = 整个连接的目录；抽屉一次性拿全，本地过滤
        const r = await api.get('/mysql/' + this.connId + '/ai/catalog')
        if (r.code === 0) {
          this.list = r.data?.list || []
          this.schemas = r.data?.schemas || []
        }
      } catch (e) { /* 拦截器已提示 */ } finally { this.loading = false }
    },
    toggleDb(name) {
      // 默认展开，故只记「收起」状态
      this.openDbs = { ...this.openDbs, [name]: this.openDbs[name] === false }
    },
    toggleTable(db, table) {
      const key = db + '.' + table
      this.expanded = { ...this.expanded, [key]: !this.expanded[key] }
    }
  }
}
