// ES 控制台 · 分析抽屉（F6，§12）：去重计数 / 图状分析。
// 数据源是 ES 聚合（全量匹配集），不是当前页；唯一值个数是近似值（UI 带「约」）。
// 出图为前端纯 SVG 自绘，零图表库依赖（§12.3）。
window.EsAnalyzeDrawer = {
  props: ['conn', 'view', 'kql', 'start', 'end', 'preselectField', 'preselectMode', 'modelValue'],
  emits: ['update:modelValue'],
  template: `
<el-drawer :model-value="modelValue" @update:model-value="$emit('update:modelValue', $event)"
  title="分析（基于当前条件的全量匹配集，非当前页）" size="62%">
  <el-tabs v-model="tab">
    <el-tab-pane label="去重计数" name="distinct">
      <div class="es-an-row">
        <el-select v-model="distinctFields" multiple collapse-tags style="width:420px" placeholder="选择字段（1–10 个）">
          <el-option v-for="f in aggFields" :key="f" :label="f" :value="f" />
        </el-select>
        <el-button type="primary" @click="runDistinct">运行</el-button>
      </div>
      <div v-if="distinctTotal" class="es-an-total">全量匹配 <b>{{distinctTotal}}</b> 条</div>
      <div v-for="d in distinctResults" :key="d.field" class="es-dist-card">
        <div class="es-dist-head"><span class="mono">{{d.field}}</span>
          <span>唯一值 <b>约 {{d.cardinality}}</b>
            <el-tooltip content="cardinality 基于 HyperLogLog++，precision_threshold 上限 40000，超过后误差上升"><span class="es-an-approx">?</span></el-tooltip>
          </span>
        </div>
        <div v-for="b in d.top" :key="b.key" class="es-dist-row">
          <span class="es-dist-key mono" :title="String(b.key)">{{b.key}}</span>
          <div class="es-dist-bar"><div class="fill" :style="{width: barPct(b.doc_count, d.top[0].doc_count)}"></div></div>
          <span class="es-dist-count mono">{{b.doc_count}}（{{pct(b.doc_count)}}）</span>
        </div>
      </div>
    </el-tab-pane>
    <el-tab-pane label="图状分析" name="chart">
      <div class="es-an-row es-an-chart-cfg">
        <el-select v-model="dim1.type" style="width:150px" @change="dim1.field=''">
          <el-option label="词项（terms）" value="terms" />
          <el-option label="时间直方图" value="date_histogram" />
          <el-option label="数值直方图" value="histogram" />
        </el-select>
        <el-select v-model="dim1.field" filterable style="width:220px" placeholder="维度字段">
          <el-option v-for="f in dimCandidates" :key="f.path" :label="f.path" :value="f.path" />
        </el-select>
        <el-input v-if="dim1.type!=='terms'" v-model="dim1.interval" placeholder="间隔（如 60s / 10）" style="width:140px" />
        <el-select v-model="metricType" style="width:110px">
          <el-option v-for="m in ['count','sum','avg','min','max','cardinality']" :key="m" :label="m" :value="m" />
        </el-select>
        <el-select v-if="metricType!=='count'" v-model="metricField" filterable style="width:200px" placeholder="指标字段">
          <el-option v-for="f in metricCandidates" :key="f.path" :label="f.path" :value="f.path" />
        </el-select>
        <el-button type="primary" @click="runChart">运行</el-button>
      </div>
      <div v-if="chartTotal" class="es-an-total">全量匹配 <b>{{chartTotal}}</b> 条</div>
      <div class="es-an-view-switch">
        <el-radio-group v-model="chartView" size="small">
          <el-radio-button label="chart">图</el-radio-button>
          <el-radio-button label="table">表</el-radio-button>
        </el-radio-group>
      </div>
      <div v-if="chartView==='chart'" class="es-chart-wrap" v-loading="chartLoading">
        <svg v-if="chartType==='bar'" :viewBox="'0 0 680 300'" class="es-chart-svg">
          <g v-for="(b,i) in bars" :key="i">
            <rect :x="b.x" :y="300 - 34 - b.h" :width="b.w" :height="b.h" class="es-chart-bar">
              <title>{{b.label}} · {{fmtNum(b.value)}}<template v-if="b.err>0">（±{{fmtNum(b.err)}}）</template></title>
            </rect>
            <text :x="b.x + b.w/2" y="296" text-anchor="middle" class="es-chart-x">{{shortLabel(b.label)}}</text>
            <text :x="b.x + b.w/2" :y="300 - 40 - b.h" text-anchor="middle" class="es-chart-y">{{fmtNum(b.value)}}</text>
          </g>
          <line v-for="i in 4" :key="'g'+i" x1="0" :y1="300 - 34 - i*62" x2="680" :y2="300 - 34 - i*62" class="es-chart-grid" />
        </svg>
        <svg v-else-if="chartType==='pie'" :viewBox="'0 0 680 300'" class="es-chart-svg">
          <g v-for="(s,i) in pieSlices" :key="i" :transform="'translate(150,150)'">
            <path :d="s.d" :fill="pieColor(i)"><title>{{s.label}} · {{fmtNum(s.value)}}</title></path>
          </g>
          <g v-for="(s,i) in pieSlices" :key="'l'+i" class="es-pie-legend">
            <rect x="330" :y="34 + i*22" width="12" height="12" :fill="pieColor(i)" />
            <text x="350" :y="45 + i*22" class="es-chart-legend">{{s.label}} · {{fmtNum(s.value)}}</text>
          </g>
        </svg>
        <div v-else class="es-an-empty">无数据</div>
      </div>
      <el-table v-if="chartView==='table'" class="hosts-table" :data="chartRows" size="small">
        <el-table-column label="维度值" min-width="180"><template #default="{row}"><span class="mono">{{row.label}}</span></template></el-table-column>
        <el-table-column label="文档数" width="120"><template #default="{row}"><span class="mono">{{row.doc_count}}</span></template></el-table-column>
        <el-table-column label="指标值" width="140"><template #default="{row}"><span class="mono">{{row.metric}}</span></template></el-table-column>
      </el-table>
    </el-tab-pane>
  </el-tabs>
</el-drawer>`,
  data() {
    return {
      tab: 'distinct',
      distinctFields: [], distinctResults: [], distinctTotal: 0,
      dim1: { type: 'terms', field: '', interval: '' },
      metricType: 'count', metricField: '',
      chartRows: [], chartTotal: 0, chartLoading: false, chartView: 'chart', ranChart: false,
      fields: []
    }
  },
  computed: {
    aggFields() { return (this.fields || []).filter(f => f.aggregatable).map(f => f.path) },
    dimCandidates() {
      if (this.dim1.type === 'date_histogram') return (this.fields || []).filter(f => (f.types || []).some(t => t === 'date'))
      return (this.fields || []).filter(f => f.aggregatable)
    },
    metricCandidates() {
      if (this.metricType === 'cardinality') return this.dimCandidates
      return (this.fields || []).filter(f => f.aggregatable && (f.types || []).every(t => ['long', 'integer', 'short', 'byte', 'float', 'double', 'scaled_float', 'half_float'].includes(t)))
    },
    chartType() {
      if (!this.ranChart) return 'none'
      if (this.dim1.type === 'terms') return this.chartRows.length > 8 ? 'pie' : 'bar'
      return 'bar'
    },
    chartMax() { return Math.max(1, ...this.chartRows.map(r => r._v)) },
    bars() {
      const n = this.chartRows.length
      if (!n) return []
      const W = 680, H = 300 - 34, gap = 6
      const w = Math.max(2, W / n - gap)
      return this.chartRows.map((r, i) => ({
        label: r.label, value: r._v, err: r.err || 0,
        x: i * (W / n), w, h: Math.max(1, r._v / this.chartMax * (H - 40))
      }))
    },
    pieSlices() {
      const rows = this.chartRows.slice(0, 8)
      const rest = this.chartRows.slice(8).reduce((a, r) => a + r._v, 0)
      const all = rest > 0 ? [...rows, { label: '其他', _v: rest, doc_count: rest }] : rows
      const total = all.reduce((a, s) => a + s._v, 0) || 1
      let a0 = -Math.PI / 2
      return all.map(s => {
        const a1 = a0 + s._v / total * Math.PI * 2
        const d = this.arc(150, 150, 120, a0, a1)
        a0 = a1
        return { label: s.label, value: s._v, d }
      })
    }
  },
  watch: {
    modelValue(open) {
      if (open) {
        this.tab = this.preselectMode === 'dim' ? 'chart' : 'distinct'
        this.loadFields()
        if (this.preselectMode === 'dim' && this.preselectField) {
          const f = (this.fields || []).find(x => x.path === this.preselectField)
          this.dim1.field = this.preselectField
          this.dim1.type = f && (f.types || []).includes('date') ? 'date_histogram' : 'terms'
        } else if (this.preselectField) {
          this.distinctFields = [this.preselectField]
        }
      }
    }
  },
  methods: {
    async loadFields() {
      try {
        const r = await api.get('/es/' + this.conn.id + '/views/' + this.view.id)
        if (r.code === 0) this.fields = r.data.fields || []
      } catch (e) { /* */ }
    },
    async runDistinct() {
      if (!this.distinctFields.length) { this.$message.warning('选择 1–10 个字段'); return }
      try {
        const r = await api.post('/es/' + this.conn.id + '/analyze/distinct', {
          view_id: this.view.id, kql: this.kql, start: this.start, end: this.end, fields: this.distinctFields
        })
        if (r.code === 0) {
          this.distinctTotal = r.data.total
          const aggs = r.data.aggs || {}
          this.distinctResults = this.distinctFields.map((f, i) => {
            const card = aggs['f' + i] || {}
            const top = aggs['f' + i + '_top'] || {}
            return { field: f, cardinality: card.value || 0,
              top: (top.buckets || []).map(b => ({ key: b.key_as_string || b.key, doc_count: b.doc_count })) }
          })
        }
      } catch (e) { /* */ }
    },
    async runChart() {
      if (!this.dim1.field) { this.$message.warning('选择维度字段'); return }
      const metric = { type: this.metricType }
      if (this.metricType !== 'count') metric.field = this.metricField
      this.chartLoading = true
      try {
        const r = await api.post('/es/' + this.conn.id + '/analyze/chart', {
          view_id: this.view.id, kql: this.kql, start: this.start, end: this.end,
          dim1: { type: this.dim1.type, field: this.dim1.field, size: 20, interval: this.dim1.interval },
          metric
        })
        if (r.code === 0) {
          this.chartTotal = r.data.total
          const b1 = (r.data.aggs && r.data.aggs.b1) || {}
          const buckets = b1.buckets || []
          this.chartRows = buckets.map(b => ({
            label: String(b.key_as_string || b.key),
            doc_count: b.doc_count,
            metric: this.metricType === 'count' ? '—' : String(b.m1 && (b.m1.value ?? '—')),
            _v: this.metricType === 'count' ? b.doc_count : Number(b.m1 && b.m1.value || 0),
            err: (b.doc_count_error_upper_bound || 0)
          }))
          this.ranChart = true
        }
      } catch (e) { /* */ } finally { this.chartLoading = false }
    },
    barPct(v, max) { return Math.max(2, Math.round(v / (max || 1) * 100)) + '%' },
    pct(v) {
      const t = this.distinctResults.length ? this.distinctResults[0].top.reduce((a, b) => a + b.doc_count, 0) : 0
      return t ? Math.round(v / t * 100) + '%' : '-'
    },
    fmtNum(n) { return n >= 10000 ? (n / 10000).toFixed(1) + 'w' : String(Math.round(n * 100) / 100) },
    shortLabel(s) { return s.length > 10 ? s.slice(0, 9) + '…' : s },
    arc(cx, cy, r, a0, a1) {
      const x0 = cx + r * Math.cos(a0), y0 = cy + r * Math.sin(a0)
      const x1 = cx + r * Math.cos(a1), y1 = cy + r * Math.sin(a1)
      const large = a1 - a0 > Math.PI ? 1 : 0
      return `M ${cx} ${cy} L ${x0} ${y0} A ${r} ${r} 0 ${large} 1 ${x1} ${y1} Z`
    },
    pieColor(i) {
      const tokens = ['var(--el-color-primary, #409EFF)', 'var(--ok, #67C23A)', 'var(--warn, #E6A23C)',
        'var(--danger, #F56C6C)', 'var(--info, #909399)', '#9B59B6', '#1ABC9C', '#34495E', '#BDC3C7']
      return tokens[i % tokens.length]
    }
  }
}
