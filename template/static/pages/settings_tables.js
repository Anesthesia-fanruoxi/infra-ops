// 设置 · 数据表：本地 SQLite 各表行数（只读）。
//
// 排查问题时最先想知道的就是「数据堆在哪张表」，所以单独成一个页签、按行数降序；
// 空表在排查场景里没有信息量，默认只显示有数据的表，展开仍可全部查看。
window.SettingsTables = {
  template: `
<div class="page-card">
  <div class="card-header">
    <div class="tool-list-title">
      <span class="tool-list-sub">共 {{ fmtNum(info.table_total_rows) }} 行 / {{ (info.tables || []).length }} 张表 · 按行数降序</span>
    </div>
    <div class="header-extra">
      <el-button text :loading="loading" @click="load"><el-icon style="margin-right:4px"><Refresh /></el-icon>刷新</el-button>
    </div>
  </div>

  <el-table class="set-table" :data="visibleTables" size="small" style="width:100%" empty-text="暂无数据">
    <el-table-column label="表名" min-width="240">
      <template #default="{row}"><span class="mono">{{ row.name }}</span></template>
    </el-table-column>
    <el-table-column label="行数" width="140" align="right">
      <template #default="{row}"><span class="mono">{{ fmtNum(row.rows) }}</span></template>
    </el-table-column>
  </el-table>
  <div class="set-more" v-if="(info.tables || []).length > PREVIEW">
    <el-button text size="small" @click="showAll = !showAll">
      {{ showAll ? '收起' : '展开全部 ' + info.tables.length + ' 张表' }}
    </el-button>
  </div>
</div>`,
  data() {
    return { PREVIEW: 10, info: {}, loading: false, showAll: false }
  },
  computed: {
    // 默认只显示有数据的表：空表在排查场景里没有信息量，但展开时仍可查看
    visibleTables() {
      const all = this.info.tables || []
      if (this.showAll) return all
      return all.filter(t => t.rows > 0).slice(0, this.PREVIEW)
    }
  },
  created() { this.load() },
  methods: {
    async load() {
      this.loading = true
      try {
        const r = await api.get('/settings/system')
        if (r.code === 0) this.info = r.data || {}
      } catch (e) { /* 拦截器已提示 */ } finally { this.loading = false }
    },
    fmtNum(n) { return Number(n || 0).toLocaleString('zh-CN') }
  }
}
