// 统一 AI 调用记录组件：所有工具的 AI 调用都写进同一张 ai_logs 表（menu 存页面 id），
// 本组件按 menu（可叠加 conn_id）拉取展示，三处复用：
//
//   审计页「AI 调用日志」标签页 —— show-menu，看全部菜单（含按菜单用量汇总）；
//   MySQL 使用记录弹窗          —— menu=mysql，选中连接时由上层叠加 conn-id；
//   监控查询工作台弹窗          —— menu=metrics，固定当前连接。
//
// 接口：GET /api/ai/logs?menu=&conn_id=&kind=&status=&page=&page_size=
//       → { list, total, summary, retention_days }；DELETE 同参数清空。
// 展示名：menu 存页面 id（metrics / mysql），显示名经 window.INFRA_ROUTES 映射 ——
// 页面名称随时会改（如「操作日志」→「审计日志」），id 才是稳定的关联键。
window.AIRecords = {
  props: {
    // 页面 id（metrics / mysql）；留空 = 全部菜单（审计页）
    menu: { type: String, default: '' },
    // 连接过滤：0 = 全部连接
    connId: { type: [Number, String], default: 0 },
    // 连接展示名（清空确认文案用，可空）
    connName: { type: String, default: '' },
    // 类型筛选项 [{value,label}]；默认全量四类，工具视图按自身能力传子集
    kinds: {
      type: Array,
      default: () => [
        { value: 'promql', label: '生成 PromQL' },
        { value: 'explain', label: '结果解读' },
        { value: 'sql', label: '生成 SQL' },
        { value: 'catalog', label: '语义目录' }
      ]
    },
    // 是否展示「菜单」列与按菜单汇总（审计页用）
    showMenu: { type: Boolean, default: false }
  },
  template: `
<div class="ai-records">
  <div class="ai-records-bar">
    <el-select v-if="kinds.length" v-model="filters.kind" size="small" clearable placeholder="调用类型" style="width:128px" @change="reload">
      <el-option v-for="k in kinds" :key="k.value" :label="k.label" :value="k.value" />
    </el-select>
    <el-select v-model="filters.status" size="small" clearable placeholder="状态" style="width:100px" @change="reload">
      <el-option label="成功" value="ok" />
      <el-option label="失败" value="error" />
    </el-select>
    <span class="ai-records-spacer"></span>
    <el-button text size="small" @click="reload"><el-icon style="margin-right:4px"><Refresh /></el-icon>刷新</el-button>
    <el-button text size="small" type="danger" :disabled="!total" @click="clear">清空</el-button>
  </div>

  <div class="mysql-logs-sum">
    <span class="mysql-logs-stat">共 <b>{{ fmtNum(summary.calls) }}</b> 次调用</span>
    <span class="mysql-logs-stat">成功 <b class="is-ok">{{ fmtNum(summary.ok) }}</b></span>
    <span class="mysql-logs-stat">失败 <b :class="summary.failed ? 'is-bad' : ''">{{ fmtNum(summary.failed) }}</b></span>
    <span class="mysql-logs-stat">输入 <b>{{ fmtNum(summary.prompt_tokens) }}</b></span>
    <span class="mysql-logs-stat">输出 <b>{{ fmtNum(summary.completion_tokens) }}</b></span>
    <span class="mysql-logs-stat">合计 <b class="is-token">{{ fmtNum(summary.total_tokens) }}</b> tokens</span>
    <span class="mysql-logs-stat">平均耗时 <b>{{ fmtMs(summary.avg_elapsed_ms) }}</b></span>
  </div>
  <div class="ai-records-menus" v-if="showMenu && (summary.by_menu || []).length">
    <span class="ai-records-menu-chip" v-for="m in summary.by_menu" :key="m.menu">
      {{ menuText(m.menu) }} <b>{{ fmtNum(m.calls) }}</b> 次 · <b>{{ fmtNum(m.total_tokens) }}</b> tokens
    </span>
  </div>
  <div class="mysql-logs-warn" v-if="summary.estimated_calls">
    其中 {{ summary.estimated_calls }} 次接口没有返回用量，token 是按字符估算的（界面标注为「估算」）。
  </div>
  <div class="mysql-logs-tip" v-if="retentionDays">记录只保留最近 {{ retentionDays }} 天，超期由服务端自动清理（与部署历史同一保留期设置）。</div>

  <el-table class="mysql-logs-table" :data="list" size="small" v-loading="loading"
    style="width:100%" empty-text="暂无调用记录">
    <el-table-column type="expand">
      <template #default="{row}">
        <div class="mysql-logs-detail">
          <template v-if="row.question">
            <div class="mysql-logs-detail-label">你的需求</div>
            <div class="mysql-logs-detail-text">{{ row.question }}</div>
          </template>
          <template v-if="row.expr">
            <div class="mysql-logs-detail-label">{{ row.menu === 'metrics' ? '生成的表达式' : '生成的语句' }}</div>
            <pre class="mysql-ddl">{{ row.expr }}</pre>
          </template>
          <template v-if="row.content">
            <div class="mysql-logs-detail-label">{{ row.kind === 'catalog' ? '归纳结果' : '模型返回原文' }}</div>
            <pre class="mysql-ddl">{{ row.content }}</pre>
          </template>
          <div class="mysql-logs-detail-meta">
            <span>类型 {{ kindText(row.kind) }}</span>
            <span v-if="showMenu">来源 {{ menuText(row.menu) }}</span>
            <span v-if="!connId && row.conn_name">连接 {{ row.conn_name }}</span>
            <span v-if="row.schema">库 {{ row.schema }}</span>
            <span v-if="row.mode">模式 {{ row.mode === 'write' ? '可写' : '只读' }}</span>
            <span v-if="row.batch_total">第 {{ row.batch_no }}/{{ row.batch_total }} 批</span>
            <span v-if="row.table_count">送入 {{ row.table_count }} 张表</span>
            <span v-if="row.metric_count">送入 {{ row.metric_count }} 个指标</span>
            <span>耗时 {{ fmtMs(row.elapsed_ms) }}</span>
            <span v-if="row.model">{{ row.model }}</span>
            <span v-if="row.base_url">{{ row.base_url }}</span>
          </div>
          <div class="mysql-ai-error" v-if="row.error"><pre>{{ row.error }}</pre></div>
        </div>
      </template>
    </el-table-column>
    <el-table-column label="时间" width="152">
      <template #default="{row}"><span class="mono mysql-logs-time">{{ row.created_at }}</span></template>
    </el-table-column>
    <el-table-column v-if="showMenu" label="菜单" width="100">
      <template #default="{row}"><span class="action-badge other">{{ menuText(row.menu) }}</span></template>
    </el-table-column>
    <el-table-column label="类型" width="104">
      <template #default="{row}"><span class="action-badge" :class="kindClass(row.kind)">{{ kindText(row.kind) }}</span></template>
    </el-table-column>
    <el-table-column v-if="!connId" label="连接" width="140">
      <template #default="{row}">
        <div>{{ row.conn_name || ('#' + row.conn_id) }}</div>
        <div class="mysql-logs-sub mono">{{ row.schema || '-' }}</div>
      </template>
    </el-table-column>
    <el-table-column label="输入" min-width="200">
      <template #default="{row}">
        <div class="mysql-logs-sql">{{ inputText(row) }}</div>
        <div class="mysql-logs-sub mono">{{ row.model || '-' }}<span v-if="row.batch_total"> · 批 {{ row.batch_no }}/{{ row.batch_total }}</span><span v-if="connId && row.schema"> · {{ row.schema }}</span></div>
      </template>
    </el-table-column>
    <el-table-column label="tokens" width="176">
      <template #default="{row}">
        <span class="mono mysql-logs-token">{{ fmtNum(row.prompt_tokens) }} + {{ fmtNum(row.completion_tokens) }} = <b>{{ fmtNum(row.total_tokens) }}</b></span>
        <span class="mysql-logs-est" v-if="row.token_source === 'estimated'" title="接口没有返回用量，此处是按字符估算的近似值">估算</span>
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

  <div class="mysql-logs-pager" v-if="total > pageSize">
    <el-pagination :current-page="page" :page-size="pageSize" :total="total"
      layout="total, prev, pager, next" @current-change="turnPage" />
  </div>
</div>`,
  data() {
    return {
      loading: false, page: 1, pageSize: 20, retentionDays: 0,
      filters: { kind: '', status: '' },
      d: { list: [], total: 0, summary: {} }
    }
  },
  computed: {
    list() { return this.d.list },
    total() { return this.d.total },
    summary() { return this.d.summary || {} }
  },
  mounted() { this.load() },
  methods: {
    async load() {
      this.loading = true
      try {
        const params = { page: this.page, page_size: this.pageSize }
        if (this.menu) params.menu = this.menu
        if (this.connId) params.conn_id = this.connId
        if (this.filters.kind) params.kind = this.filters.kind
        if (this.filters.status) params.status = this.filters.status
        const r = await api.get('/ai/logs', { params })
        if (r.code === 0) {
          const d = r.data || {}
          this.d = { list: d.list || [], total: d.total || 0, summary: d.summary || {} }
          this.retentionDays = d.retention_days || 0
        }
      } catch (e) { /* 拦截器已提示 */ } finally { this.loading = false }
    },
    reload() {
      this.page = 1
      this.load()
    },
    turnPage(p) {
      this.page = p
      this.load()
    },
    // 清空范围与查询条件一致：按菜单（可叠加连接）圈定，不传 = 全部菜单
    clear() {
      const scope = this.connId
        ? '「' + (this.connName || ('#' + this.connId)) + '」的'
        : (this.menu ? '「' + this.menuText(this.menu) + '」的' : '全部菜单的')
      this.$confirm('确认清空' + scope + 'AI 调用记录？该操作不可恢复。', '提示', { type: 'warning' })
        .then(async () => {
          try {
            const params = {}
            if (this.menu) params.menu = this.menu
            if (this.connId) params.conn_id = this.connId
            const r = await api.delete('/ai/logs', { params })
            if (r.code === 0) {
              ElMessage.success('已清空 ' + (r.data?.deleted || 0) + ' 条记录')
              this.reload()
            }
          } catch (e) { /* 拦截器已提示 */ }
        })
        .catch(() => {})
    },
    // menu 存的是页面 id，显示名走前端路由表映射；映射缺失时回落原始值
    menuText(m) {
      const r = window.INFRA_ROUTES && window.INFRA_ROUTES[m]
      return (r && r.title) || m || '未知'
    },
    kindText(kind) {
      const k = this.kinds.find(x => x.value === kind)
      if (k) return k.label
      if (kind === 'promql') return '生成 PromQL'
      if (kind === 'explain') return '结果解读'
      if (kind === 'catalog') return '语义目录'
      if (kind === 'sql') return '生成 SQL'
      return kind || '未知'
    },
    // 四类徽章各用一个色系（蓝 / 绿 / 紫 / 灰），审计页同屏出现时能直接扫出类型
    kindClass(kind) {
      if (kind === 'promql') return 'vm'
      if (kind === 'explain') return 'host'
      if (kind === 'sql') return 'info'
      return 'other'
    },
    // 表格里的「输入」列：优先用户原话；目录批记录没有原话，退到批次摘要
    inputText(row) {
      if (row.question) return row.question
      if (row.kind === 'explain') return row.menu === 'mysql' ? '解读执行计划' : '解读查询结果'
      if (row.batch_total) return '第 ' + row.batch_no + '/' + row.batch_total + ' 批 · ' + row.table_count + ' 张表'
      return '—'
    },
    fmtNum(n) { return Number(n || 0).toLocaleString('zh-CN') },
    // 毫秒在千以下按毫秒看，超过就换成秒 —— 模型调用耗时常在秒级
    fmtMs(ms) {
      const v = Number(ms || 0)
      if (v < 1000) return v + ' ms'
      return (v / 1000).toFixed(v < 10000 ? 1 : 0) + ' s'
    }
  }
}
