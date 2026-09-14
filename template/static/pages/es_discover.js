// ES 控制台 · Discover 主视图（F5）：字段侧栏 + KQL 条 + 时间直方图（纯 SVG）+ 文档表（内存分页）。
// 翻页只在本地切片完成、不发请求（首次查询已拿全最多 1000 条）；结果集失效 4011 → 提示重新查询。
window.EsDiscover = {
  props: ['conn', 'view'],
  components: { 'es-fields-panel': window.EsFieldsPanel, 'es-kql-bar': window.EsKqlBar, 'es-analyze-drawer': window.EsAnalyzeDrawer },
  template: `
<div class="es-discover">
  <div class="es-disc-head">
    <el-button text @click="$emit('back')"><el-icon><Back /></el-icon></el-button>
    <span class="es-disc-view">{{view.name}}</span>
    <span class="mono es-disc-pattern">{{view.index_pattern}}</span>
    <el-select v-model="columnSel" multiple collapse-tags size="small" placeholder="列选择" style="width:280px" @change="onColumnsChange">
      <el-option v-for="f in fields" :key="f.path" :label="f.path" :value="f.path" />
    </el-select>
    <el-button style="margin-left:auto" @click="analyze = true">分析</el-button>
  </div>
  <es-kql-bar :conn-id="conn.id" :view-id="view.id" v-model:kql="kql" v-model:start="start" v-model:end="end" @run="runSearch" />
  <div class="es-trunc-hint" v-if="windowTruncated">匹配 <b>{{total}}</b> 条，仅可浏览前 <b>{{windowSize}}</b> 条，请收窄时间范围或补充条件</div>
  <div class="es-disc-body">
    <es-fields-panel class="es-disc-fields" :fields="fields" :synced-at="view.synced_at"
      :sync-status="view.sync_status" :sync-error="view.sync_error"
      @insert="insertQuery" @refresh="refreshFields" @analyze-distinct="openDistinct" @analyze-dim="openDim" />
    <div class="es-disc-main">
      <div class="es-histogram" v-if="histMax>0">
        <svg :viewBox="'0 0 680 ' + histH" preserveAspectRatio="none" class="es-hist-svg"
             @mousedown="dragStart" @mousemove="dragMove" @mouseup="dragEnd">
          <g v-for="(b,i) in histBars" :key="i">
            <rect :x="b.x" :y="histH - b.h" :width="b.w" :height="b.h" class="es-hist-bar">
              <title>{{fmtBucket(b.bucket)}} · {{b.bucket.doc_count}} 条</title>
            </rect>
          </g>
          <rect v-if="dragX1!==null" :x="Math.min(dragX1,dragX2)" y="0" :width="Math.abs(dragX2-dragX1)" :height="histH" class="es-hist-drag" />
        </svg>
        <div class="es-hist-axis"><span>{{fmtBucket(histogram[0])}}</span><span v-if="dragHint" class="es-hist-hint">{{dragHint}}</span><span>{{fmtBucket(histogram[histogram.length-1])}}</span></div>
      </div>
      <el-table class="hosts-table es-docs-table" :data="pageHits" style="width:100%" size="small"
                v-loading="searching" @row-click="toggleExpand" :row-class-name="rowClass">
        <el-table-column v-for="c in columns" :key="c" :label="c" min-width="160">
          <template #default="{row}">
            <span v-if="row.highlight && row.highlight[c]" class="es-hl" v-html="renderHl(row.highlight[c])"></span>
            <span v-else class="mono es-cell">{{cellText(row.source, c)}}</span>
          </template>
        </el-table-column>
        <template #empty><empty-state text="无结果：调整 KQL 或时间范围" /></template>
      </el-table>
      <pre class="es-source es-expand" v-if="expanded" v-loading="false">{{fmtSource(expanded.source)}}</pre>
      <div class="es-result-bar">
        <span>总 <b>{{total}}</b> 条 · 窗口 {{windowSize}} · 当前 {{rangeText}}</span>
        <el-pagination v-model:current-page="page" :page-size="pageSize" :total="resultCount"
                       layout="sizes, prev, pager, next" :page-sizes="[10,20,50,100]"
                       style="margin-left:auto" @size-change="pageSize=$event" />
      </div>
    </div>
  </div>
  <es-analyze-drawer v-model="analyze" :conn="conn" :view="view" :kql="kql" :start="start" :end="end"
    :preselect-field="preselectField" :preselect-mode="preselectMode" />
</div>`,
  data() {
    return {
      fields: [], kql: '', start: '', end: '',
      columns: [], columnSel: [],
      result: null, total: 0, windowSize: 1000, windowTruncated: false,
      histogram: [], histH: 90,
      page: 1, pageSize: 20, searching: false,
      expanded: null, analyze: false,
      preselectField: '', preselectMode: 'distinct',
      dragX1: null, dragX2: null, dragging: false
    }
  },
  computed: {
    resultCount() { return Math.min(this.total, this.windowSize) || (this.result ? this.result.hits.length : 0) },
    pageHits() {
      const hits = (this.result && this.result.hits) || []
      const s = (this.page - 1) * this.pageSize
      return hits.slice(s, s + this.pageSize)
    },
    rangeText() {
      const hits = (this.result && this.result.hits) || []
      const s = (this.page - 1) * this.pageSize + 1
      const e = Math.min(this.page * this.pageSize, hits.length)
      return hits.length ? s + '–' + e : '0'
    },
    histMax() { return Math.max(0, ...this.histogram.map(b => b.doc_count)) },
    histBars() {
      const n = this.histogram.length
      if (!n) return []
      const W = 680, gap = 1
      const w = Math.max(1, W / n - gap)
      return this.histogram.map((b, i) => {
        const h = this.histMax ? Math.max(1, (b.doc_count / this.histMax) * (this.histH - 6)) : 1
        return { bucket: b, x: i * (W / n), y: 0, w, h }
      })
    },
    dragHint() {
      if (this.dragX1 === null || Math.abs(this.dragX2 - this.dragX1) < 4) return ''
      const seg = this.pickedBuckets()
      return seg ? '松开以缩放范围' : ''
    }
  },
  mounted() { this.init() },
  methods: {
    async init() {
      try {
        const r = await api.get('/es/' + this.conn.id + '/views/' + this.view.id)
        if (r.code === 0) {
          this.fields = r.data.fields || []
          const tf = r.data.time_field
          // 默认列：时间字段 + 前 5 个可聚合字段（§10）
          const agg = (this.fields.filter(f => f.aggregatable && f.path !== tf)).slice(0, 5).map(f => f.path)
          this.columns = [tf, ...agg]
          this.columnSel = this.columns.slice()
        }
      } catch (e) { /* */ }
      // 默认时间范围：近 24 小时（北京时间字符串，仅预填）
      const fmt = d => {
        const p = n => String(n).padStart(2, '0')
        return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate()) + ' ' + p(d.getHours()) + ':' + p(d.getMinutes()) + ':' + p(d.getSeconds())
      }
      const now = new Date()
      this.end = fmt(now); this.start = fmt(new Date(now.getTime() - 86400000))
      this.runSearch()
    },
    async runSearch() {
      if (!this.start || !this.end) { this.$message.warning('请选择时间范围'); return }
      this.searching = true; this.expanded = null
      try {
        const r = await api.post('/es/' + this.conn.id + '/search', {
          view_id: this.view.id, kql: this.kql, start: this.start, end: this.end,
          columns: this.columns, histogram_interval: this.histInterval()
        })
        if (r.code === 0) {
          const d = r.data
          this.result = d; this.total = d.total; this.windowSize = d.window
          this.windowTruncated = !!d.window_truncated
          this.histogram = d.histogram || []
          this.page = 1
        }
      } catch (e) { /* */ } finally { this.searching = false }
    },
    // 直方图桶数约 50：interval 由区间毫秒差换算为固定间隔字符串（纯展示参数，非时间换算口径）
    histInterval() {
      const span = new Date(this.end.replace(' ', 'T')).getTime() - new Date(this.start.replace(' ', 'T')).getTime()
      if (!(span > 0)) return '60s'
      const sec = Math.max(1, Math.round(span / 1000 / 50))
      const steps = [1, 5, 10, 30, 60, 300, 600, 900, 1800, 3600, 7200, 14400, 43200, 86400]
      const pick = steps.find(s => s >= sec) || 86400
      return pick >= 86400 ? (pick / 86400) + 'd' : pick + 's'
    },
    // 列选择变化 → 重查并覆盖缓存（§11.4）
    async onColumnsChange(v) {
      this.columns = v.length ? v : [this.fields[0] && this.fields[0].path].filter(Boolean)
      this.$message.info('正在重新查询')
      await this.runSearch()
    },
    async refreshFields() {
      try {
        const r = await api.post('/es/' + this.conn.id + '/views/' + this.view.id + '/refresh')
        if (r.code === 0) {
          this.fields = r.data.fields || []
          this.$message.success('字段已刷新')
        }
      } catch (e) { /* */ }
    },
    insertQuery(f) {
      this.kql = (this.kql ? this.kql.trimEnd() + ' ' : '') + f.path + ':'
    },
    openDistinct(f) { this.preselectField = f.path; this.preselectMode = 'distinct'; this.analyze = true },
    openDim(f) { this.preselectField = f.path; this.preselectMode = 'dim'; this.analyze = true },
    toggleExpand(row) { this.expanded = this.expanded === row ? null : row },
    rowClass() { return 'es-doc-row' },
    cellText(src, path) {
      let v = src
      for (const seg of path.split('.')) {
        if (v == null) break
        v = v[seg]
      }
      if (v == null) return '—'
      return typeof v === 'object' ? JSON.stringify(v) : String(v)
    },
    // 高亮片段渲染前必须转义 < > &（pre_tags 白名单只留 <mark>）
    renderHl(frags) {
      const esc = s => String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      return (Array.isArray(frags) ? frags.join(' … ') : String(frags)).split(/(<mark>|<\/mark>)/)
        .map(s => (s === '<mark>' || s === '</mark>') ? s : esc(s)).join('')
    },
    fmtSource(src) { return JSON.stringify(src, null, 2) },
    fmtBucket(b) {
      if (!b || !b.key) return ''
      const d = new Date(b.key)
      const p = n => String(n).padStart(2, '0')
      return (d.getMonth() + 1) + '-' + p(d.getDate()) + ' ' + p(d.getHours()) + ':' + p(d.getMinutes())
    },
    dragStart(e) { this.dragging = true; this.dragX1 = this.dragX2 = e.offsetX },
    dragMove(e) { if (this.dragging) this.dragX2 = e.offsetX },
    dragEnd(e) {
      if (!this.dragging) return
      this.dragging = false
      const x1 = Math.min(this.dragX1, e.offsetX), x2 = Math.max(this.dragX1, e.offsetX)
      this.dragX1 = null; this.dragX2 = null
      if (x2 - x1 < 6 || !this.histogram.length) return
      const W = 680
      const i1 = Math.floor(x1 / W * this.histogram.length)
      const i2 = Math.min(this.histogram.length - 1, Math.ceil(x2 / W * this.histogram.length) - 1)
      const fmt = ms => {
        const d = new Date(ms)
        const p = n => String(n).padStart(2, '0')
        return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate()) + ' ' + p(d.getHours()) + ':' + p(d.getMinutes()) + ':' + p(d.getSeconds())
      }
      this.start = fmt(this.histogram[i1].key)
      const last = this.histogram[i2]
      const span = (this.histogram[1] && this.histogram[1].key - this.histogram[0].key) || 60000
      this.end = fmt(last.key + span)
      this.runSearch()
    }
  }
}
