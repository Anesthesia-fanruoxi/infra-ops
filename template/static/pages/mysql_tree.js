// MySQL 工作台 · 左侧库表树：库懒加载展开；点表只上报选中，由工作台决定展示方式。
window.MySQLTree = {
  props: {
    conn: { type: Object, required: true },
    activeDb: { type: String, default: '' },
    activeTable: { type: String, default: '' }
  },
  emits: ['pick-table', 'pick-db'],
  template: `
<div class="mysql-tree">
  <div class="mysql-tree-head">
    <el-input v-model="kw" placeholder="过滤库 / 表" size="small" clearable prefix-icon="Search" />
    <el-button text size="small" :loading="loadingDbs" @click="loadDbs" title="刷新库列表"><el-icon><Refresh /></el-icon></el-button>
  </div>
  <div class="mysql-tree-tools">
    <el-checkbox v-model="hideSystem" size="small">隐藏系统库</el-checkbox>
    <span class="mysql-tree-tools-tip" v-if="hideSystem && sysHiddenCount">已隐藏 {{ sysHiddenCount }} 个</span>
  </div>
  <div class="mysql-tree-body" v-loading="loadingDbs">
    <template v-for="db in visibleDbs" :key="db">
      <div class="mysql-db-row" :class="{active: db===activeDb}" @click="onDbClick(db)">
        <span class="mysql-caret">{{ isOpen(db) ? '▾' : '▸' }}</span>
        <span class="mysql-db-name" :title="db">{{ db }}</span>
        <span class="mysql-tb-count" v-if="loaded(db)">{{ tablesOf(db).length }}</span>
      </div>
      <div v-if="isOpen(db)" class="mysql-tb-list">
        <div v-if="loadingTable[db]" class="mysql-tree-tip">加载中…</div>
        <template v-else>
          <div v-for="t in visibleTables(db)" :key="t.name" class="mysql-tb-row"
            :class="{active: db===activeDb && t.name===activeTable}" @click.stop="onTableClick(db, t)">
            <span class="mysql-tb-icon" :class="t.type==='VIEW'?'view':'base'">{{ t.type==='VIEW' ? 'V' : 'T' }}</span>
            <span class="mysql-tb-name" :title="(t.comment ? t.comment + ' · ' : '') + t.name">{{ t.name }}</span>
            <span class="mysql-tb-rows" v-if="t.rows">{{ fmtRows(t.rows) }}</span>
          </div>
          <div v-if="!visibleTables(db).length" class="mysql-tree-tip">{{ tablesOf(db).length ? '无匹配表' : '无表' }}</div>
        </template>
      </div>
    </template>
    <div v-if="!loadingDbs && !visibleDbs.length" class="mysql-tree-tip">无匹配库</div>
  </div>
</div>`,
  data() {
    return {
      dbs: [], openDbs: [], tables: {}, loadingDbs: false, loadingTable: {}, kw: '',
      hideSystem: localStorage.getItem('mysql-hide-system') !== '0'
    }
  },
  computed: {
    visibleDbs() {
      let list = this.dbs
      if (this.hideSystem) list = list.filter(d => !isSystemDb(d))
      const k = this.kw.trim().toLowerCase()
      if (!k) return list
      return list.filter(d => d.toLowerCase().includes(k) || (this.tables[d] || []).some(t => t.name.toLowerCase().includes(k)))
    },
    sysHiddenCount() {
      return this.dbs.filter(d => isSystemDb(d)).length
    }
  },
  watch: {
    hideSystem(v) {
      localStorage.setItem('mysql-hide-system', v ? '1' : '0')
      // 收起被藏起来的库，避免下次显示时状态错乱
      if (v) this.openDbs = this.openDbs.filter(d => !isSystemDb(d))
    }
  },
  mounted() { this.loadDbs() },
  methods: {
    async loadDbs() {
      this.loadingDbs = true
      try {
        const r = await api.get('/mysql/' + this.conn.id + '/databases')
        if (r.code === 0) this.dbs = r.data?.list || []
      } catch (e) { /* 拦截器已提示 */ } finally { this.loadingDbs = false }
    },
    isOpen(db) {
      if (this.kw.trim() && this.visibleDbs.includes(db)) return true
      return this.openDbs.includes(db)
    },
    loaded(db) { return !!this.tables[db] },
    tablesOf(db) { return this.tables[db] || [] },
    visibleTables(db) {
      const k = this.kw.trim().toLowerCase()
      const list = this.tablesOf(db)
      if (!k) return list
      return list.filter(t => t.name.toLowerCase().includes(k) || db.toLowerCase().includes(k))
    },
    // 点击库：展开/收起的同时把该库设为工作台当前库
    onDbClick(db) {
      this.$emit('pick-db', db)
      this.toggleDb(db)
    },
    // 点击表：只上报选中，由工作台打开「表详情」（改前会自动拼一条 SELECT 直接跑）
    onTableClick(db, t) {
      this.$emit('pick-table', { db, table: t })
    },
    toggleDb(db) {
      const i = this.openDbs.indexOf(db)
      if (i >= 0) { this.openDbs.splice(i, 1); return }
      this.openDbs.push(db)
      if (!this.tables[db]) this.loadTables(db)
    },
    async loadTables(db) {
      this.loadingTable = { ...this.loadingTable, [db]: true }
      try {
        const r = await api.get('/mysql/' + this.conn.id + '/tables', { params: { schema: db } })
        if (r.code === 0) this.tables = { ...this.tables, [db]: r.data?.list || [] }
      } catch (e) { /* 拦截器已提示 */ } finally {
        this.loadingTable = { ...this.loadingTable, [db]: false }
      }
    },
    fmtRows(n) {
      if (n >= 1000000) return (n / 1000000).toFixed(1) + 'M'
      if (n >= 1000) return (n / 1000).toFixed(1) + 'K'
      return String(n)
    }
  }
}

// 系统库名单：MySQL 自带的四个库（大小写不敏感）
const MYSQL_SYSTEM_DBS = ['information_schema', 'performance_schema', 'mysql', 'sys']
function isSystemDb(name) {
  return MYSQL_SYSTEM_DBS.indexOf(String(name).toLowerCase()) >= 0
}
