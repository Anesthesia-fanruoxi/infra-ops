// ES 控制台 · Discover 主视图（F5）：字段侧栏 + KQL 条 + 时间直方图（纯 SVG）+ 文档卡片。
// 1000 条窗口缓存全在后端（result_cache，新查询覆盖）；前端每次只接 100 条，
// 卡片区内滚动，滚到底点「加载更多」走 /search/page 内存切片增量追加（不访问 ES）；
// 左侧点击字段 = 右侧只显示该字段（时间恒为独立左列，UTC+8；纯前端过滤，不重查）；
// 结果集失效 4011 → 提示重新查询。
// 卡片高度运行时实测：docsBox 顶边到视口底 - 结果条，页面总高刚好一屏，外层 .content 不再出滚动条
// （此前 CSS 估算 max-height(calc(100vh-430px)) 偏小，外层可滚「滚出屏幕」）。
window.EsDiscover = {
  props: ['conn', 'view'],
  components: { 'es-fields-panel': window.EsFieldsPanel, 'es-kql-bar': window.EsKqlBar, 'es-analyze-drawer': window.EsAnalyzeDrawer },
  template: `
<div class="es-discover">
  <div class="es-disc-head">
    <el-button text @click="$emit('back')"><el-icon><Back /></el-icon></el-button>
    <span class="es-disc-view">{{view.name}}</span>
    <span class="mono es-disc-pattern">{{view.index_pattern}}</span>
    <el-button style="margin-left:auto" @click="analyze = true">分析</el-button>
  </div>
  <es-kql-bar :conn-id="conn.id" :view-id="view.id" v-model:kql="kql" v-model:start="start" v-model:end="end" @run="runSearch" />
  <div class="es-trunc-hint" v-if="windowTruncated">匹配 <b>{{total}}</b> 条，仅可浏览前 <b>{{windowSize}}</b> 条，请收窄时间范围或补充条件</div>
  <div class="es-disc-body">
    <es-fields-panel class="es-disc-fields" :fields="fields" :synced-at="view.synced_at"
      :sync-status="view.sync_status" :sync-error="view.sync_error" :selected="selField"
      @select="onFieldSelect" @refresh="refreshFields" />
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
      <div class="es-docs" v-loading="searching" ref="docsBox">
        <div class="es-docs-head"><span class="es-docs-h-time">时间</span><span>内容</span></div>
        <div v-for="(row,i) in hits" :key="(row.id || 'r') + '-' + i" class="es-doc">
          <div class="es-doc-time mono">{{fmtTime(row.source && row.source[timeField])}}</div>
          <div class="es-doc-body">
            <div v-for="kv in docFields(row)" :key="kv.k" class="es-doc-line">
              <span class="mono es-doc-key">{{kv.k}}</span><span class="es-doc-sep">:</span>
              <span v-if="row.highlight && row.highlight[kv.k]" class="mono es-doc-val es-hl" v-html="renderHl(row.highlight[kv.k])"></span>
              <span v-else class="mono es-doc-val">{{fmtVal(kv.v)}}</span>
            </div>
          </div>
        </div>
        <div v-if="canMore" class="es-docs-more">
          <el-button size="small" text type="primary" :loading="loadingMore" @click="loadMore">加载更多（剩余 {{resultCount - hits.length}} 条）</el-button>
        </div>
        <empty-state v-if="!hits.length && !searching" text="无结果：调整 KQL 或时间范围" />
      </div>
      <div class="es-result-bar">
        <span>总 <b>{{total}}</b> 条 · 窗口 {{windowSize}}<span v-if="windowTruncated">（超出部分不可浏览）</span> · 已加载 {{hits.length}} / {{resultCount}}</span>
      </div>
    </div>
  </div>
  <es-analyze-drawer v-model="analyze" :conn="conn" :view="view" :kql="kql" :start="start" :end="end" />
</div>`,
  data() {
    return {
      fields: [], kql: '', start: '', end: '',
      timeField: (this.view && this.view.time_field) || '', selField: '',
      resultId: '', hits: [], total: 0, windowSize: 1000, windowTruncated: false,
      histogram: [], histH: 90,
      pageSize: 100, loadingMore: false, searching: false,
      analyze: false,
      dragX1: null, dragX2: null, dragging: false
    }
  },
  computed: {
    resultCount() { return Math.min(this.total, this.windowSize) || this.hits.length },
    canMore() { return this.hits.length > 0 && this.hits.length < this.resultCount },
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
  mounted() {
    this.init()
    this.$nextTick(this.fitDocsHeight)
    window.addEventListener('resize', this.fitDocsHeight)
  },
  unmounted() { window.removeEventListener('resize', this.fitDocsHeight) },
  methods: {
    // 卡片高度实测：视口底 - docsBox 顶边 - 结果条约 40px；直方图显隐/窗口尺寸变化后需重算。
    // 页面总高收敛到一屏内，外层 .content 不再产生可滚出屏幕的余量。
    fitDocsHeight() {
      const box = this.$refs.docsBox
      if (!box) return
      const h = window.innerHeight - box.getBoundingClientRect().top - 48
      box.style.maxHeight = Math.max(160, h) + 'px'
    },
    async init() {
      try {
        const r = await api.get('/es/' + this.conn.id + '/views/' + this.view.id)
        if (r.code === 0) {
          this.fields = this.cleanFields(r.data.fields)
          if (r.data.time_field) this.timeField = r.data.time_field
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
      this.searching = true
      try {
        const r = await api.post('/es/' + this.conn.id + '/search', {
          view_id: this.view.id, kql: this.kql, start: this.start, end: this.end,
          page_size: this.pageSize,
          // columns 传全部字段：_source 放开为全量（卡片展示完整日志），highlight 随之覆盖所有 text 字段
          columns: this.fields.map(f => f.path), histogram_interval: this.histInterval()
        })
        if (r.code === 0) {
          const d = r.data
          // 新查询直接覆盖旧结果（后端缓存同键覆盖，前端同步重置增量状态）
          this.resultId = d.result_id; this.hits = d.hits || []
          this.total = d.total; this.windowSize = d.window
          this.windowTruncated = !!d.window_truncated
          this.histogram = d.histogram || []
          if (this.$refs.docsBox) this.$refs.docsBox.scrollTop = 0
          // 直方图此时才出现（histMax>0），顶边变化 → 重测高度
          this.$nextTick(this.fitDocsHeight)
        }
      } catch (e) { /* */ } finally { this.searching = false }
    },
    // 加载更多：/search/page 内存切片增量追加（不访问 ES）；结果集失效(4011)提示重查
    async loadMore() {
      if (!this.resultId || this.loadingMore) return
      this.loadingMore = true
      try {
        const r = await api.post('/es/' + this.conn.id + '/search/page', {
          result_id: this.resultId, from: this.hits.length, size: this.pageSize
        })
        if (r.code === 0) {
          this.hits = this.hits.concat(r.data.hits || [])
        }
      } catch (e) {
        this.$message.warning('结果集已更新或已失效，请重新查询')
      } finally { this.loadingMore = false }
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
    // 点击字段：右侧只显示该字段，再点一次取消恢复全部（纯展示过滤，不重新查询）
    onFieldSelect(p) { this.selField = this.selField === p ? '' : p },
    // 字段表净化：去掉 `_` 元字段（旧快照兜底）与多字段子路径（content.keyword 与主字段重复）
    cleanFields(raw) {
      const all = raw || []
      const paths = new Set(all.map(f => f.path))
      return all.filter(f => !f.path.startsWith('_') &&
        !(f.path.includes('.') && paths.has(f.path.slice(0, f.path.lastIndexOf('.')))))
    },
    async refreshFields() {
      try {
        const r = await api.post('/es/' + this.conn.id + '/views/' + this.view.id + '/refresh')
        if (r.code === 0) {
          this.fields = this.cleanFields(r.data.fields)
          if (this.selField && !this.fields.some(f => f.path === this.selField)) this.selField = ''
          this.$message.success('字段已刷新')
        }
      } catch (e) { /* */ }
    },
    // 文档行字段：未过滤 = 全部字段；过滤 = 只保留所选字段（时间字段恒显示在卡片头）
    docFields(row) {
      const src = row.source || {}
      const out = []
      for (const k of Object.keys(src)) {
        if (k === this.timeField) continue
        if (this.selField && k !== this.selField && !this.selField.startsWith(k + '.')) continue
        out.push({ k, v: src[k] })
      }
      return out
    },
    // 值渲染：对象/数组按 JSON 原样展示，其余转字符串
    fmtVal(v) {
      if (v === null || v === undefined) return '—'
      return typeof v === 'object' ? JSON.stringify(v) : String(v)
    },
    // 时间字段展示恒为北京时间（UTC+8，含毫秒）；仅展示换算，不改变任何请求口径
    fmtTime(v) {
      if (v === null || v === undefined || v === '') return '—'
      const raw = String(v)
      const d = /^\d{10,}$/.test(raw) ? new Date(Number(raw)) : new Date(raw)
      if (isNaN(d.getTime())) return raw
      const t = new Date(d.getTime() + 8 * 3600 * 1000)
      const p = n => String(n).padStart(2, '0')
      return t.getUTCFullYear() + '-' + p(t.getUTCMonth() + 1) + '-' + p(t.getUTCDate()) + ' ' +
        p(t.getUTCHours()) + ':' + p(t.getUTCMinutes()) + ':' + p(t.getUTCSeconds()) + '.' + String(t.getUTCMilliseconds()).padStart(3, '0')
    },
    // 高亮片段渲染前必须转义 < > &（pre_tags 白名单只留 <mark>）
    renderHl(frags) {
      const esc = s => String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      return (Array.isArray(frags) ? frags.join(' … ') : String(frags)).split(/(<mark>|<\/mark>)/)
        .map(s => (s === '<mark>' || s === '</mark>') ? s : esc(s)).join('')
    },
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
