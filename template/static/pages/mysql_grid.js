// MySQL 工作台 · 结果网格：列头带类型、NULL 与空串区分、行号、CSV 导出。
window.MySQLGrid = {
  props: {
    result: { type: Object, default: null },
    error: { type: String, default: '' },
    connId: { type: Number, default: 0 },
    schema: { type: String, default: '' },
    // 产生当前结果的 SQL（工作台在本次执行成功时存下）：AI 分析的对象必须与结果同源，
    // 不能读编辑器现内容——用户可能已改动而没执行，拿改后的 SQL 分析旧计划就张冠李戴了
    analyzeSql: { type: String, default: '' }
  },
  template: `
<div class="mysql-grid-wrap" :class="{ 'is-explain-open': analyzing || analysis || analysisError }">
  <div class="mysql-grid-bar">
    <template v-if="result">
      <span class="mysql-type-badge" :class="'t-' + (result.sql_type || 'other').toLowerCase()">{{ result.sql_type || '—' }}</span>
      <span class="mysql-grid-stat" v-if="result.columns.length === 0">
        <b>{{ result.statement_kind }}</b> 执行完成 · 影响 {{ result.affected_rows }} 行<span v-if="result.last_insert_id"> · 自增 ID {{ result.last_insert_id }}</span>
      </span>
      <span class="mysql-grid-stat" v-else>
        <b>{{ result.row_count }}</b> 行 · {{ result.columns.length }} 列 · {{ result.elapsed_ms }} ms
      </span>
      <span v-if="result.statement_count > 1" class="mysql-grid-stat muted">共 {{ result.statement_count }} 条语句</span>
      <span v-if="result.truncated" class="mysql-grid-warn">结果已截断（仅显示前 {{ result.rows.length }} 行）</span>
      <span class="mysql-grid-spacer"></span>
      <!-- 仅 EXPLAIN 执行计划的结果可分析（can_analyze 由后端判定）；分析在服务端重跑取计划，结果数据不上传 AI -->
      <el-button v-if="result.can_analyze" text size="small" :loading="analyzing" @click="analyze()"><el-icon style="margin-right:4px"><MagicStick /></el-icon>AI 分析</el-button>
      <el-button text size="small" v-if="result.rows.length" @click="exportCsv"><el-icon style="margin-right:4px"><Download /></el-icon>导出 CSV</el-button>
    </template>
    <span v-else class="mysql-grid-stat muted">尚未执行语句</span>
  </div>

  <div class="mysql-grid" v-if="result && result.rows && result.rows.length">
    <table class="mysql-table">
      <thead>
        <tr>
          <th class="mysql-rownum">#</th>
          <th v-for="(c, i) in result.columns" :key="i">
            <span class="mysql-col-name">{{ c.name }}</span>
            <span class="mysql-col-type">{{ c.type }}</span>
          </th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="(row, ri) in result.rows" :key="ri">
          <td class="mysql-rownum">{{ ri + 1 }}</td>
          <td v-for="(v, ci) in row" :key="ci" :class="{ 'is-null': v === null }" :title="cellText(v)" @dblclick="copyCell(v)">
            <span v-if="v === null" class="mysql-null">NULL</span>
            <span v-else class="mysql-cell">{{ cellText(v) }}</span>
          </td>
        </tr>
      </tbody>
    </table>
  </div>

  <div class="mysql-grid-empty" v-else-if="error">
    <div class="mysql-error-box"><b>执行失败</b><pre>{{ error }}</pre></div>
  </div>

  <div class="mysql-grid-empty" v-else-if="result">
    <empty-state :text="result.columns.length ? '查询无结果' : '语句已执行，无结果集'" />
  </div>

  <div class="mysql-grid-empty" v-else>
    <empty-state text="在上方输入 SQL 后按 Ctrl + Enter 执行" />
  </div>

  <!-- AI 分析面板（仅 EXPLAIN 执行计划）：结论 + 观察发现 + 优化建议 + 建索引语句 / 优化后语句（代码块，一键复制）
       + 解读依据（计划 + 相关表结构统计）+ 用量；面板放在结果表格下方整块展开——EXPLAIN 计划行数少、分析内容更长，
       先看结果后看分析；计划与表结构都来自服务端重跑 / information_schema 元信息，面板不接收任何业务数据 -->
  <div class="mysql-explain" v-if="analyzing || analysis || analysisError">
    <div class="mysql-explain-head">
      <span class="mysql-explain-title">AI 分析 · 执行计划解读</span>
      <span class="mysql-explain-spacer"></span>
      <el-button text size="small" :disabled="analyzing" @click="analyze()">重新分析</el-button>
      <el-button text size="small" :disabled="analyzing" @click="closeAnalysis">关闭</el-button>
    </div>
    <div class="mysql-explain-loading" v-if="analyzing">
      <span class="mysql-explain-dot"></span>正在重跑 EXPLAIN 并分析执行计划，通常需要几秒到十几秒…
    </div>
    <div class="mysql-explain-error" v-else-if="analysisError"><pre>{{ analysisError }}</pre></div>
    <template v-else>
      <div class="mysql-explain-summary" v-if="analysis.summary">{{ analysis.summary }}</div>
      <div class="mysql-explain-section" v-if="(analysis.findings || []).length">
        <div class="mysql-explain-section-title">观察发现</div>
        <ul class="mysql-explain-list">
          <li v-for="(f, i) in analysis.findings" :key="'f' + i">{{ f }}</li>
        </ul>
      </div>
      <div class="mysql-explain-section" v-if="(analysis.suggestions || []).length">
        <div class="mysql-explain-section-title">优化建议</div>
        <ul class="mysql-explain-list is-suggest">
          <li v-for="(s, i) in analysis.suggestions" :key="'s' + i">{{ s }}</li>
        </ul>
      </div>
      <!-- 建索引语句：代码块展示 + 一键复制；只展示，绝不自动执行 -->
      <div class="mysql-explain-section" v-if="(analysis.index_statements || []).length">
        <div class="mysql-explain-section-title">建索引语句（仅展示，确认后手动执行）</div>
        <div class="mysql-explain-code" v-for="(s, i) in analysis.index_statements" :key="'d' + i">
          <pre>{{ s }}</pre>
          <el-button class="mysql-explain-copy" text size="small" @click="copySql(s)">复制</el-button>
        </div>
      </div>
      <!-- 优化后语句：仅语义不变的局部小改；保留 EXPLAIN 前缀，可直接执行对比新计划 -->
      <div class="mysql-explain-section" v-if="analysis.optimized_sql">
        <div class="mysql-explain-section-title">优化后语句（语义不变的局部小改，执行可对比新计划）</div>
        <div class="mysql-explain-code">
          <pre>{{ analysis.optimized_sql }}</pre>
          <el-button class="mysql-explain-copy" text size="small" @click="copySql(analysis.optimized_sql)">复制</el-button>
        </div>
      </div>
      <div class="mysql-explain-digest">
        <a class="mysql-explain-toggle" @click="digestOpen = !digestOpen">
          {{ digestOpen ? '收起' : '展开' }}解读依据（{{ digestRowCount }} 行执行计划{{ digestTruncated ? '，仅列前若干行' : '' }}{{ digestTables.length ? ' + ' + digestTables.length + ' 张表结构' : '' }}）
        </a>
        <div class="mysql-explain-digest-body" v-show="digestOpen">
          <div class="mysql-explain-line mono" v-for="(l, i) in digestLines" :key="'l' + i">{{ l }}</div>
          <!-- 相关表结构与统计（字段 / 索引 / 行数大小）：服务端从 information_schema 取的元信息，随分析上下文送模型 -->
          <template v-for="(t, ti) in digestTables" :key="'t' + ti">
            <div class="mysql-explain-line mono is-ts">—— 表 {{ t.schema ? t.schema + '.' : '' }}{{ t.name }} · {{ t.engine || '—' }} · 估算行数 {{ fmtNum(t.rows) }} · 数据 {{ fmtBytes(t.data_length) }} · 索引 {{ fmtBytes(t.index_length) }} ——</div>
            <div class="mysql-explain-line mono is-col" v-for="(c, ci) in (t.columns || [])" :key="'c' + ci">列 {{ c }}</div>
            <div class="mysql-explain-line mono is-idx" v-for="(x, xi) in (t.indexes || [])" :key="'x' + xi">索引 {{ x }}</div>
            <div class="mysql-explain-line mono is-note" v-if="t.note">注：{{ t.note }}</div>
          </template>
        </div>
      </div>
      <div class="mysql-explain-usage" v-if="analysis.usage && analysis.usage.total_tokens">
        本次消耗 <b>{{ fmtNum(analysis.usage.total_tokens) }}</b> tokens（输入 {{ fmtNum(analysis.usage.prompt_tokens) }} · 输出 {{ fmtNum(analysis.usage.completion_tokens) }}）· 耗时 {{ fmtMs(analysis.usage.elapsed_ms) }}<span v-if="analysis.usage.source === 'estimated'"> · 接口未返回用量，按字符估算</span>
      </div>
    </template>
  </div>
</div>`,
  data() {
    return {
      analyzing: false, // 分析请求进行中
      analysis: null,   // 本次分析结果（summary / findings / suggestions / digest / usage）
      analysisError: '', // 分析失败原因
      digestOpen: false, // 解读依据（计划原文）展开态
      runSeq: 0          // 结果批次号：每换一批结果 +1，用来丢弃挂起中的过期分析响应
    }
  },
  watch: {
    // 换了一批结果（重新执行）：上一批的分析面板随之清掉，避免张冠李戴
    result() {
      this.runSeq++
      this.analyzing = false
      this.analysis = null
      this.analysisError = ''
      this.digestOpen = false
    }
  },
  computed: {
    // 解读依据面板的数据面（analysis 可能为空，模板里少写重复判空）
    digestLines() { return (this.analysis && this.analysis.digest && this.analysis.digest.lines) || [] },
    digestTables() { return (this.analysis && this.analysis.digest && this.analysis.digest.tables) || [] },
    digestRowCount() { return (this.analysis && this.analysis.digest && this.analysis.digest.row_count) || 0 },
    digestTruncated() { return !!(this.analysis && this.analysis.digest && this.analysis.digest.truncated) }
  },
  methods: {
    // AI 分析执行计划：仅后端标记 can_analyze 的 EXPLAIN 结果可进（按钮同判据）；
    // 请求只带 SQL 与库名——后端重跑 EXPLAIN 取计划送模型，查询结果数据不上传 AI
    async analyze() {
      if (this.analyzing || !this.analyzeSql) return
      const target = this.analyzeSql
      const seq = this.runSeq // 批号快照：响应回来时批号变了就丢弃（重跑同一句 SQL 也不算数）
      this.analyzing = true
      this.analysisError = ''
      this.digestOpen = false
      try {
        const r = await api.post('/mysql/' + this.connId + '/query/analyze', {
          sql: target, schema: this.schema
        }, { timeout: 0 })
        if (this.runSeq !== seq) return // 已执行新一批：丢弃过期响应
        if (r && r.code === 0 && r.data) {
          this.analysis = {
            summary: r.data.summary || '',
            findings: r.data.findings || [],
            suggestions: r.data.suggestions || [],
            index_statements: r.data.index_statements || [],
            optimized_sql: r.data.optimized_sql || '',
            digest: r.data.digest || { row_count: 0, truncated: false, lines: [] },
            usage: r.data.usage || {}
          }
        } else {
          this.analysisError = (r && r.message) || '分析失败'
        }
      } catch (e) {
        if (this.runSeq === seq) this.analysisError = '请求失败，请重试'
      } finally {
        if (this.runSeq === seq) this.analyzing = false
      }
    },
    closeAnalysis() {
      this.analyzing = false
      this.analysis = null
      this.analysisError = ''
      this.digestOpen = false
    },
    fmtNum(n) { return Number(n || 0).toLocaleString('zh-CN') },
    // 毫秒在千以下按毫秒看，超过就换成秒 —— 模型调用耗时常在秒级
    fmtMs(ms) {
      const v = Number(ms || 0)
      if (v < 1000) return v + ' ms'
      return (v / 1000).toFixed(v < 10000 ? 1 : 0) + ' s'
    },
    // 字节数转可读大小（表统计：数据 / 索引长度；与后端 explainBytes 同口径）
    fmtBytes(n) {
      const v = Number(n || 0)
      if (v < 1024) return v + ' B'
      if (v < 1048576) return (v / 1024).toFixed(1) + ' KB'
      if (v < 1073741824) return (v / 1048576).toFixed(1) + ' MB'
      return (v / 1073741824).toFixed(1) + ' GB'
    },
    cellText(v) {
      if (v === null || v === undefined) return 'NULL'
      if (typeof v === 'object') return JSON.stringify(v)
      return String(v)
    },
    async copyCell(v) {
      if (v === null || v === undefined) return
      try {
        await navigator.clipboard.writeText(this.cellText(v))
        ElMessage.success('已复制单元格内容')
      } catch (e) { /* 剪贴板不可用时静默 */ }
    },
    // 复制代码块内容（建索引语句 / 优化后语句）：一键复制 + 成功提示；剪贴板不可用时提示手选
    async copySql(s) {
      try {
        await navigator.clipboard.writeText(String(s))
        ElMessage.success('已复制')
      } catch (e) {
        ElMessage.warning('复制失败，请手动选择复制')
      }
    },
    exportCsv() {
      const cols = this.result.columns.map(c => c.name)
      const lines = [cols.map(csvCell).join(',')]
      for (const row of this.result.rows) {
        lines.push(row.map(v => (v === null || v === undefined ? '' : csvCell(this.cellText(v)))).join(','))
      }
      // 加 BOM 让 Excel 正确识别 UTF-8
      const blob = new Blob(['\ufeff' + lines.join('\r\n')], { type: 'text/csv;charset=utf-8' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = 'mysql-result-' + new Date().toISOString().slice(0, 19).replace(/[:T]/g, '') + '.csv'
      document.body.appendChild(a)
      a.click()
      a.remove()
      URL.revokeObjectURL(url)
    }
  }
}

// csvCell 按 RFC 4180 转义：含逗号/引号/换行的字段加引号并把内部引号翻倍。
function csvCell(s) {
  const t = String(s)
  return /[",\r\n]/.test(t) ? '"' + t.replace(/"/g, '""') + '"' : t
}
