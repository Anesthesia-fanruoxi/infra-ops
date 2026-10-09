// MySQL 使用记录：两张互不相关的记录表，各看各的。
//
//   SQL 执行记录 —— 人在工作台里执行过的语句（含结果与成败），本文件实现；
//   AI 调用记录  —— 统一记录组件（ai_records.js，menu=mysql），
//                   从工作台打开时叠加当前连接的 conn_id。
//
// 两者不做关联：AI 生成的结果有没有被执行，不属于记录维度。
window.MySQLLogs = {
  props: {
    // 打开时落在哪个页签：sql / ai
    defaultTab: { type: String, default: 'sql' },
    // 连接过滤：从工作台打开时传当前连接（0 = 全部连接，列表页入口）
    connId: { type: [Number, String], default: 0 },
    connName: { type: String, default: '' }
  },
  template: `
<div class="mysql-logs">
  <div class="mysql-logs-tabs">
    <button type="button" class="mysql-logs-tab" :class="{active: tab==='sql'}" @click="switchTab('sql')">SQL 执行记录</button>
    <button type="button" class="mysql-logs-tab" :class="{active: tab==='ai'}" @click="switchTab('ai')">AI 调用记录</button>
    <template v-if="tab==='sql'">
      <span class="mysql-logs-spacer"></span>
      <el-select v-model="filters.sqlType" size="small" clearable placeholder="语句类型" style="width:128px" @change="reload">
        <el-option label="查询 DQL" value="DQL" />
        <el-option label="会话 SESSION" value="SESSION" />
        <el-option label="数据变更 DML" value="DML" />
        <el-option label="结构变更 DDL" value="DDL" />
        <el-option label="其他 OTHER" value="OTHER" />
      </el-select>
      <el-select v-model="filters.status" size="small" clearable placeholder="状态" style="width:100px" @change="reload">
        <el-option label="成功" value="ok" />
        <el-option label="失败" value="error" />
      </el-select>
      <el-button text size="small" @click="reload"><el-icon style="margin-right:4px"><Refresh /></el-icon>刷新</el-button>
      <el-button text size="small" type="danger" :disabled="!sql.total" @click="clear">清空</el-button>
    </template>
  </div>

  <div class="mysql-logs-sum" v-if="tab==='sql'">
    <span class="mysql-logs-stat">共 <b>{{ fmtNum(sql.summary.runs) }}</b> 次执行</span>
    <span class="mysql-logs-stat">成功 <b class="is-ok">{{ fmtNum(sql.summary.ok) }}</b></span>
    <span class="mysql-logs-stat">失败 <b :class="sql.summary.failed ? 'is-bad' : ''">{{ fmtNum(sql.summary.failed) }}</b></span>
    <span class="mysql-logs-stat">查询 <b>{{ fmtNum(sql.summary.query_runs) }}</b></span>
    <span class="mysql-logs-stat">写入 <b>{{ fmtNum(sql.summary.write_runs) }}</b></span>
    <span class="mysql-logs-stat">返回 <b>{{ fmtNum(sql.summary.rows_returned) }}</b> 行</span>
    <span class="mysql-logs-stat">影响 <b>{{ fmtNum(sql.summary.rows_affected) }}</b> 行</span>
    <span class="mysql-logs-stat">平均耗时 <b>{{ fmtMs(sql.summary.avg_elapsed_ms) }}</b></span>
  </div>
  <div class="mysql-logs-tip" v-if="tab==='sql' && retentionDays">记录只保留最近 {{ retentionDays }} 天，超期由服务端自动清理（与部署历史同一保留期设置）。</div>

  <el-table v-if="tab==='sql'" class="mysql-logs-table" :data="sql.list" size="small" v-loading="loading"
    style="width:100%" empty-text="暂无执行记录">
    <el-table-column type="expand">
      <template #default="{row}">
        <div class="mysql-logs-detail">
          <div class="mysql-logs-detail-label">执行的语句</div>
          <pre class="mysql-ddl">{{ row.sql }}</pre>
          <div class="mysql-logs-detail-meta">
            <span>类型 {{ row.sql_type }}{{ row.statement_count > 1 ? ' · 共 ' + row.statement_count + ' 条语句' : '' }}</span>
            <span>模式 {{ row.mode === 'write' ? '可写' : '只读' }}</span>
            <span>二次确认 {{ row.confirmed ? '已放行' : '不需要' }}</span>
            <span>耗时 {{ fmtMs(row.elapsed_ms) }}</span>
          </div>
          <div class="mysql-ai-error" v-if="row.error"><pre>{{ row.error }}</pre></div>
        </div>
      </template>
    </el-table-column>
    <el-table-column label="时间" width="152">
      <template #default="{row}"><span class="mono mysql-logs-time">{{ row.created_at }}</span></template>
    </el-table-column>
    <el-table-column label="连接" width="132">
      <template #default="{row}">
        <div>{{ row.conn_name || ('#' + row.conn_id) }}</div>
        <div class="mysql-logs-sub mono">{{ row.schema || '-' }}</div>
      </template>
    </el-table-column>
    <el-table-column label="语句" min-width="250">
      <template #default="{row}">
        <div class="mysql-logs-sql mono">{{ sqlOneLine(row.sql) }}</div>
        <span class="action-badge info mysql-logs-kind">{{ row.statement_kind || row.sql_type }}</span>
      </template>
    </el-table-column>
    <el-table-column label="结果" width="118">
      <template #default="{row}">
        <span v-if="row.status !== 'ok'" class="mysql-logs-sub">未执行完</span>
        <span v-else-if="row.affected_rows > 0" class="mysql-logs-sub">影响 {{ fmtNum(row.affected_rows) }} 行</span>
        <span v-else class="mysql-logs-sub">返回 {{ fmtNum(row.row_count) }} 行</span>
      </template>
    </el-table-column>
    <el-table-column label="耗时" width="92">
      <template #default="{row}"><span class="mono mysql-logs-sub">{{ fmtMs(row.elapsed_ms) }}</span></template>
    </el-table-column>
    <el-table-column label="状态" width="84">
      <template #default="{row}">
        <span class="tool-status" :class="row.status === 'ok' ? 'ok' : 'fail'"><span class="st-dot"></span>{{ row.status === 'ok' ? '成功' : '失败' }}</span>
      </template>
    </el-table-column>
  </el-table>

  <!-- AI 调用记录：统一记录组件（menu=mysql）；从工作台打开时叠加当前连接的过滤 -->
  <ai-records v-else menu="mysql" :conn-id="connId" :conn-name="connName" :kinds="aiKinds" />

  <div class="mysql-logs-pager" v-if="tab==='sql' && sql.total > pageSize">
    <el-pagination :current-page="page" :page-size="pageSize" :total="sql.total"
      layout="total, prev, pager, next" @current-change="turnPage" />
  </div>
</div>`,
  data() {
    return {
      tab: 'sql', loading: false, page: 1, pageSize: 20, retentionDays: 0,
      filters: { status: '', sqlType: '' },
      sql: { list: [], total: 0, summary: {} },
      // AI 页签的类型筛选项（交给统一记录组件展示）
      aiKinds: [
        { value: 'sql', label: '生成 SQL' },
        { value: 'explain', label: '计划解读' },
        { value: 'catalog', label: '语义目录' }
      ]
    }
  },
  mounted() {
    this.tab = this.defaultTab === 'ai' ? 'ai' : 'sql'
    // AI 页签的数据由统一记录组件挂载时自行加载
    if (this.tab === 'sql') this.load()
  },
  methods: {
    switchTab(t) {
      if (this.tab === t) return
      this.tab = t
      this.page = 1
      this.filters.status = ''
      this.filters.sqlType = ''
      // AI 页签的数据由统一记录组件挂载时重新拉取
      if (t === 'sql') this.load()
    },
    reload() {
      this.page = 1
      this.load()
    },
    turnPage(p) {
      this.page = p
      this.load()
    },
    async load() {
      this.loading = true
      try {
        const params = { page: this.page, page_size: this.pageSize }
        if (this.filters.status) params.status = this.filters.status
        if (this.filters.sqlType) params.sql_type = this.filters.sqlType
        if (this.connId) params.conn_id = this.connId
        const r = await api.get('/mysql/sql-logs', { params })
        if (r.code === 0) {
          const d = r.data || {}
          this.sql = { list: d.list || [], total: d.total || 0, summary: d.summary || {} }
          this.retentionDays = d.retention_days || 0
        }
      } catch (e) { /* 拦截器已提示 */ } finally { this.loading = false }
    },
    // 清空 SQL 执行记录（不分页，是整个工具或某个连接的记录）；
    // AI 记录的清空在统一记录组件内，范围口径一致
    clear() {
      const scope = this.connId ? ('「' + (this.connName || ('#' + this.connId)) + '」的') : '全部'
      this.$confirm('确认清空' + scope + 'SQL 执行记录？该操作不可恢复。', '提示', { type: 'warning' })
        .then(async () => {
          try {
            const params = {}
            if (this.connId) params.conn_id = this.connId
            const r = await api.delete('/mysql/sql-logs', { params })
            if (r.code === 0) {
              ElMessage.success('已清空 ' + (r.data?.deleted || 0) + ' 条记录')
              this.reload()
            }
          } catch (e) { /* 拦截器已提示 */ }
        })
        .catch(() => {})
    },
    fmtNum(n) { return Number(n || 0).toLocaleString('zh-CN') },
    // 毫秒在千以下按毫秒看，超过就换成秒 —— 模型调用与建表语句的耗时常在秒级
    fmtMs(ms) {
      const v = Number(ms || 0)
      if (v < 1000) return v + ' ms'
      return (v / 1000).toFixed(v < 10000 ? 1 : 0) + ' s'
    },
    // 表格里只给一行摘要，完整语句在展开行里看
    sqlOneLine(s) {
      const one = String(s || '').replace(/\s+/g, ' ').trim()
      return one.length > 110 ? one.slice(0, 110) + '…' : one
    }
  }
}
