// 监控查询工具：连接 Prometheus / VictoriaMetrics 的 PromQL HTTP 端点，
// 查询工作台围绕「AI 生成表达式 → 执行查询 → 图表 / 统计表呈现 → AI 解读」这条主线。
//
// 文件内定义顺序有讲究：window.MetricsWorkbench 在前，页面壳的 components 表在
// 求值时就要求它已存在（两个组件同文件，不能靠外部加载顺序兜底）。
//
// 时间语义：前端一律传 Unix 毫秒；步长传秒（0 = 让后端按窗宽自适应）。
window.MetricsWorkbench = {
  props: { conn: { type: Object, required: true } },
  emits: ['exit', 'open-config'],
  components: {
    'metrics-ai-gen': window.MetricsAIGen,
    'metrics-ai-explain': window.MetricsAIExplain,
    'metrics-chart': window.MetricsChart
  },
  template: `
<div class="metrics-workbench">
  <div class="metrics-bar">
    <div class="metrics-bar-id">
      <span class="metrics-bar-name">{{conn.name}}</span>
      <span class="tool-status" :class="stClass(st)"><span class="st-dot"></span>{{pinging ? '探测中' : stText(st)}}</span>
      <span class="action-badge" :class="conn.kind === 'victoriametrics' ? 'vm' : 'info'">{{kindText}}</span>
      <span class="mono metrics-bar-addr" :title="conn.url">{{conn.url}}</span>
    </div>
    <div class="metrics-bar-ops">
      <el-button text :loading="pinging" @click="testConn(false)"><el-icon style="margin-right:4px"><Connection /></el-icon>测试连接</el-button>
      <el-button text @click="logsDialog = true"><el-icon style="margin-right:4px"><Tickets /></el-icon>调用记录</el-button>
      <el-button text @click="$emit('exit')"><el-icon style="margin-right:4px"><Back /></el-icon>返回列表</el-button>
    </div>
  </div>

  <div class="page-card tool-panel">
    <div class="card-header">
      <div class="tool-list-title">
        <span class="title">查询</span>
        <span class="tool-list-sub">AI 生成或手写 PromQL · Ctrl + Enter 执行</span>
      </div>
    </div>

    <metrics-ai-gen :conn-id="conn.id" @apply="onApplyExpr" @open-config="$emit('open-config')" />

    <div class="metrics-expr">
      <textarea class="metrics-expr-input mono" v-model="expr" spellcheck="false" :disabled="loading"
        @keydown.ctrl.enter.prevent="run"
        placeholder='输入 PromQL，例如：rate(node_cpu_seconds_total{mode!="idle"}[5m])'></textarea>
      <div class="metrics-expr-side">
        <el-button type="primary" :loading="loading" @click="run"><el-icon style="margin-right:4px"><VideoPlay /></el-icon>执行查询</el-button>
      </div>
    </div>

    <div class="metrics-time">
      <el-radio-group v-model="mode" size="small" class="metrics-mode-switch">
        <el-radio-button label="range">范围查询</el-radio-button>
        <el-radio-button label="instant">即时查询</el-radio-button>
      </el-radio-group>

      <template v-if="mode === 'range'">
        <span class="metrics-chips">
          <a v-for="p in presets" :key="p.label" class="metrics-chip" :class="{active: preset === p.label}" @click="pickPreset(p)">{{ p.label }}</a>
          <a class="metrics-chip" :class="{active: preset === '自定义'}" @click="pickCustom">自定义</a>
        </span>
        <el-date-picker v-if="preset === '自定义'" v-model="customRange" type="datetimerange" size="small"
          class="metrics-range-picker" start-placeholder="开始时间" end-placeholder="结束时间"
          :clearable="false" @change="onCustomRange" />
        <span class="metrics-time-note">步长</span>
        <el-select v-model="stepSel" size="small" class="metrics-step">
          <el-option v-for="o in stepOptions" :key="o.value" :label="o.label" :value="o.value" />
        </el-select>
      </template>

      <template v-else>
        <el-date-picker v-model="instantAt" type="datetime" size="small" class="metrics-range-picker"
          placeholder="查询时刻（默认现在）" />
      </template>
      <span class="metrics-time-hint">{{ timeHint }}</span>
    </div>
  </div>

  <div class="page-card tool-panel metrics-result-panel">
    <div class="card-header">
      <div class="tool-list-title">
        <span class="title">查询结果</span>
        <span class="tool-list-sub" v-if="res">{{ res.series.length }} 条序列 · {{ res.result_type }} · 耗时 {{ res.elapsed_ms }} ms</span>
        <span class="tool-list-sub" v-else>执行查询后在这里看图与统计</span>
      </div>
      <div class="header-extra">
        <el-radio-group v-model="tab" size="small" class="hosts-view-switch" v-if="res && res.series.length">
          <el-radio-button label="chart">图表</el-radio-button>
          <el-radio-button label="table">统计表</el-radio-button>
        </el-radio-group>
        <el-button v-if="res && res.series.length" type="primary" plain :loading="explaining" @click="explain">
          <el-icon style="margin-right:4px"><MagicStick /></el-icon>AI 解读
        </el-button>
      </div>
    </div>

    <div class="metrics-result-body" v-loading="loading">
      <div class="metrics-error" v-if="err">{{ err }}</div>
      <template v-else-if="res">
        <div class="metrics-warnings" v-if="(res.warnings || []).length">
          <div v-for="(w, i) in res.warnings" :key="'w' + i">{{ w }}</div>
        </div>
        <empty-state v-if="!res.series.length" text="查询成功，但时间范围内没有数据：可能指标名 / 标签不匹配，或样本早已停报" />
        <template v-else>
          <metrics-chart v-if="tab === 'chart'" :result="res" />
          <el-table v-else :data="statRows" class="hosts-table" style="width:100%" size="small" :max-height="420">
            <el-table-column label="序列" min-width="340">
              <template #default="{row}"><span class="mono metrics-series-cell" :title="row.label">{{ row.label }}</span></template>
            </el-table-column>
            <el-table-column label="点数" width="80" align="right">
              <template #default="{row}"><span class="mono">{{ row.count }}</span></template>
            </el-table-column>
            <el-table-column label="末值" width="120" align="right">
              <template #default="{row}"><span class="mono">{{ fmtVal(row.last) }}</span></template>
            </el-table-column>
            <el-table-column label="最小" width="120" align="right">
              <template #default="{row}"><span class="mono">{{ fmtVal(row.min) }}</span></template>
            </el-table-column>
            <el-table-column label="最大" width="120" align="right">
              <template #default="{row}"><span class="mono">{{ fmtVal(row.max) }}</span></template>
            </el-table-column>
            <el-table-column label="平均" width="120" align="right">
              <template #default="{row}"><span class="mono">{{ fmtVal(row.avg) }}</span></template>
            </el-table-column>
          </el-table>
        </template>
      </template>
      <empty-state v-else text="还没有查询：上方输入表达式，或让 AI 帮你生成一条" />
    </div>
  </div>

  <div class="page-card tool-panel" ref="aiPanel">
    <metrics-ai-explain ref="aiExp" :conn-id="conn.id" @open-config="$emit('open-config')" />
  </div>

  <!-- 调用记录：统一记录组件（menu=metrics），固定当前连接的过滤。
       版式与 MySQL 使用记录弹窗一致：宽 65%、高 70%，上下各留 15%。 -->
  <el-dialog v-model="logsDialog" title="AI 调用记录" width="65%" append-to-body class="mysql-logs-dialog">
    <ai-records v-if="logsDialog" menu="metrics" :conn-id="conn.id" :conn-name="conn.name" :kinds="aiKinds" />
  </el-dialog>
</div>`,
  data() {
    return {
      st: undefined, pinging: false,
      expr: '', loading: false, err: '', res: null,
      tab: 'chart', explaining: false, logsDialog: false,
      mode: 'range',
      preset: '1h', customRange: null, instantAt: null,
      stepSel: 15,
      // 预设窗宽各带一个惯用步长（与后端「约 720 点」的自适应目标同量级）
      presets: [
        { label: '5m', span: 5 * 60e3, step: 15 },
        { label: '15m', span: 15 * 60e3, step: 15 },
        { label: '1h', span: 60 * 60e3, step: 15 },
        { label: '6h', span: 6 * 3600e3, step: 30 },
        { label: '24h', span: 24 * 3600e3, step: 60 },
        { label: '7d', span: 7 * 24 * 3600e3, step: 300 }
      ],
      stepOptions: [
        { label: '自动（按窗宽适配）', value: 0 },
        { label: '15 秒', value: 15 },
        { label: '30 秒', value: 30 },
        { label: '1 分钟', value: 60 },
        { label: '5 分钟', value: 300 },
        { label: '10 分钟', value: 600 },
        { label: '30 分钟', value: 1800 },
        { label: '1 小时', value: 3600 }
      ]
    }
  },
  computed: {
    kindText() { return this.conn.kind === 'victoriametrics' ? 'VictoriaMetrics' : 'Prometheus' },
    // 本工作台只会产生这两类调用记录（生成表达式 / 结果解读）
    aiKinds() {
      return [
        { value: 'promql', label: '生成 PromQL' },
        { value: 'explain', label: '结果解读' }
      ]
    },
    timeHint() {
      if (this.mode === 'instant') return '取该时刻的瞬时值（instant query）'
      if (this.preset === '自定义') return '自定义区间最长 90 天，步长建议留「自动」'
      return '区间查询按窗宽自动挑步长（目标约 720 个点，最小 15 秒）'
    },
    statRows() {
      const series = (this.res && this.res.series) || []
      return series.map(s => {
        const vals = (s.points || []).map(p => p.v).filter(v => v !== null && v !== undefined)
        const n = vals.length
        let sum = 0
        for (const v of vals) sum += v
        return {
          label: this.seriesLabel(s.metric),
          count: (s.points || []).length,
          last: n ? vals[n - 1] : null,
          min: n ? Math.min(...vals) : null,
          max: n ? Math.max(...vals) : null,
          avg: n ? sum / n : null
        }
      })
    }
  },
  mounted() { this.testConn(true) },
  methods: {
    stText(s) { return s === 1 ? '在线' : s === 0 ? '不可用' : '未验证' },
    stClass(s) { return s === 1 ? 'ok' : s === 0 ? 'fail' : 'idle' },
    // silent = 进工作台时的静默探测：只亮状态灯，不弹成功提示
    async testConn(silent) {
      this.pinging = true
      try {
        const r = await api.post('/metrics/conns/' + this.conn.id + '/ping')
        if (r.code === 0) {
          this.st = 1
          if (!silent) {
            const d = r.data || {}
            ElMessage.success('连接正常' + (d.version ? '（' + d.version + '）' : '') + ' · 延迟 ' + (d.latency_ms ?? '?') + ' ms')
          }
        } else {
          this.st = 0
        }
      } catch (e) { this.st = 0 } finally { this.pinging = false }
    },
    // AI 生成后自动填入（「填入表达式」按钮也能手动重填）；带时间范围识别结果时同步切控件：
    // 命中预设的滚动窗口直接切 chip，其余（今天 / 上周这类日历边界与非常规窗口）走
    // 「自定义」区间，起止用后端换算好的毫秒时间戳
    onApplyExpr(expr, range) {
      this.expr = expr
      if (range && range.start && range.end) {
        // 需求里带了时间跨度，即时查询放不下这个语义，切回范围查询
        if (this.mode === 'instant') this.mode = 'range'
        const hit = range.preset && this.presets.find(p => p.label === range.preset)
        if (hit) {
          this.pickPreset(hit)
        } else {
          this.preset = '自定义'
          this.customRange = [new Date(range.start), new Date(range.end)]
          this.stepSel = 0 // 与 pickCustom 同口径：自定义跨度不可控，步长回自动
        }
        ElMessage.success('已填入表达式，时间范围切到「' + (range.label || '自定义') + '」')
        return
      }
      ElMessage.success('已填入表达式框，确认后点「执行查询」')
    },
    pickPreset(p) {
      this.preset = p.label
      this.customRange = null
      this.stepSel = p.step
    },
    // 自定义区间跨度不可控（最长 90 天），步长一律回到「自动」防误配出百万点
    pickCustom() {
      this.preset = '自定义'
      this.customRange = null
      this.stepSel = 0
    },
    onCustomRange(v) {
      if (v && v.length === 2) this.stepSel = 0
    },
    buildParams() {
      const expr = this.expr.trim()
      if (this.mode === 'instant') {
        return { expr, mode: 'instant', start: 0, end: this.instantAt ? this.instantAt.getTime() : 0, step: 0 }
      }
      if (this.preset === '自定义' && this.customRange && this.customRange.length === 2) {
        return { expr, mode: 'range', start: this.customRange[0].getTime(), end: this.customRange[1].getTime(), step: this.stepSel || 0 }
      }
      const p = this.presets.find(x => x.label === this.preset) || this.presets[2]
      const end = Date.now()
      return { expr, mode: 'range', start: end - p.span, end, step: this.stepSel || 0 }
    },
    async run() {
      if (this.loading) return
      if (!this.expr.trim()) { ElMessage.warning('请先输入 PromQL 表达式，或让 AI 生成一条'); return }
      if (this.mode === 'range' && this.preset === '自定义' && (!this.customRange || this.customRange.length !== 2)) {
        ElMessage.warning('请先选择自定义时间区间'); return
      }
      this.loading = true
      this.err = ''
      try {
        const r = await api.post('/metrics/conns/' + this.conn.id + '/query', this.buildParams(), { timeout: 120000 })
        if (r.code === 0 && r.data) {
          this.res = r.data
          this.tab = 'chart'
        } else {
          this.err = r.message || '查询失败'
        }
      } catch (e) { /* 拦截器已提示 */ } finally { this.loading = false }
    },
    // 解读用结果里回传的实际查询参数：与「产生这份图」的那次查询逐字段一致
    // （后端拿到后会重跑查询再解读，不接受前端传结果快照）
    async explain() {
      if (this.explaining || !this.res || !this.res.series.length) return
      const panel = this.$refs.aiExp
      if (!panel) return
      this.explaining = true
      const box = this.$refs.aiPanel
      if (box) this.$nextTick(() => box.scrollIntoView({ behavior: 'smooth', block: 'nearest' }))
      try {
        await panel.analyze({ expr: this.res.expr, mode: this.res.mode, start: this.res.start, end: this.res.end, step: this.res.step })
      } finally { this.explaining = false }
    },
    // 序列名渲染：__name__ 优先，其余标签按名称排序（与图表组件、后端 digest 同款口径）
    seriesLabel(m) {
      if (!m) return ''
      const name = m.__name__ || ''
      const rest = Object.keys(m).filter(k => k !== '__name__').sort()
        .map(k => k + '="' + m[k] + '"').join(', ')
      return rest ? (name ? name + '{' + rest + '}' : '{' + rest + '}') : (name || '(无标签)')
    },
    // 数值展示对齐后端 fmtVal（G 语义 6 位有效数字）：大数/极小数走科学计数，避免长串数字
    fmtVal(v) {
      if (v === null || v === undefined) return '—'
      const x = Number(v)
      if (!isFinite(x)) return '—'
      const a = Math.abs(x)
      if (a === 0) return '0'
      if (a >= 1e6 || a < 1e-4) return x.toExponential(4).replace(/e([+-])(\d)$/, 'e$10$2')
      return String(Number(x.toPrecision(6)))
    }
  }
}

// 页面壳：连接列表（表格 / 卡片）与配置弹窗；进入工作台后由 MetricsWorkbench 接管。
window.MetricsPage = {
  props: ['page', 'versionData'],
  components: { 'metrics-workbench': window.MetricsWorkbench },
  template: `
<div>
  <template v-if="!current">
    <div class="tool-page tool-page--metrics">
      <section class="tool-intro">
        <div class="tool-intro-copy">
          <span class="tool-eyebrow">METRICS QUERY</span>
          <h1>监控查询</h1>
          <p>Prometheus / VictoriaMetrics · PromQL 查询 · AI 生成与解读 · 趋势绘图</p>
        </div>
        <div class="tool-intro-stats">
          <div class="tool-mini-stat"><span>连接</span><strong>{{total}}</strong></div>
          <div class="tool-mini-stat"><span>在线</span><strong>{{onlineCount}}</strong></div>
        </div>
      </section>

      <div class="page-card tool-panel">
        <div class="card-header">
          <div class="tool-list-title">
            <span class="title">连接列表</span>
            <span class="tool-list-sub">表格 / 卡片切换 · 支持探测连通性</span>
          </div>
          <div class="header-extra">
            <el-radio-group v-model="viewMode" size="small" class="hosts-view-switch">
              <el-radio-button label="table">表格</el-radio-button>
              <el-radio-button label="card">卡片</el-radio-button>
            </el-radio-group>
            <el-button text :loading="pinging" @click="loadConns"><el-icon style="margin-right:4px"><Refresh /></el-icon>刷新</el-button>
            <el-button type="primary" @click="openForm(null)">新增连接</el-button>
          </div>
        </div>

        <div v-if="viewMode==='table'">
          <el-table class="hosts-table" :data="conns" style="width:100%" v-loading="loadingConn">
            <el-table-column label="连接" min-width="250">
              <template #default="{row}">
                <div>
                  <span style="font-weight:600">{{row.name}}</span>
                  <span class="action-badge" :class="row.kind === 'victoriametrics' ? 'vm' : 'info'" style="margin-left:7px;font-size:10.5px">{{kindText(row)}}</span>
                </div>
                <div class="mono" style="font-size:12px;color:#9CA3AF" :title="row.url">{{row.url}}</div>
              </template>
            </el-table-column>
            <el-table-column label="状态" width="98">
              <template #default="{row}"><span class="tool-status" :class="stClass(row._st)"><span class="st-dot"></span>{{row._testing ? '探测中' : stText(row._st)}}</span></template>
            </el-table-column>
            <el-table-column label="认证" width="108">
              <template #default="{row}"><span class="mono" style="font-size:12px;color:#6B7280">{{authText(row)}}</span></template>
            </el-table-column>
            <el-table-column label="TLS" width="100">
              <template #default="{row}"><span style="color:#6B7280">{{row.insecure ? '跳过校验' : '默认'}}</span></template>
            </el-table-column>
            <el-table-column label="备注" min-width="120">
              <template #default="{row}"><span :title="row.remark" style="color:#6B7280">{{row.remark || '-'}}</span></template>
            </el-table-column>
            <el-table-column label="更新时间" width="158">
              <template #default="{row}"><span class="mono" style="font-size:12.5px;color:#6B7280">{{row.updated_at||'-'}}</span></template>
            </el-table-column>
            <el-table-column label="" width="200" fixed="right">
              <template #default="{row}">
                <div class="ops-cell">
                  <el-button size="small" text @click="openForm(row)">编辑</el-button>
                  <el-button size="small" text :loading="row._testing" @click="testConn(row)">测试</el-button>
                  <el-button size="small" text type="primary" @click="enter(row)">查询</el-button>
                  <el-button size="small" text type="danger" @click="delConn(row)">删除</el-button>
                </div>
              </template>
            </el-table-column>
          </el-table>
          <div class="tool-empty-table" v-if="!loadingConn && !conns.length"><empty-state text="暂无连接，点击右上角「新增连接」开始" /></div>
        </div>

        <div v-else class="hosts-card-grid tool-card-grid" v-loading="loadingConn">
          <article v-for="row in conns" :key="row.id" class="tool-card tool-card--metrics">
            <header class="tool-card-head">
              <div class="tool-card-icon">{{row.kind === 'victoriametrics' ? 'VM' : 'PM'}}</div>
              <div class="tool-card-idbox">
                <h3 class="tool-card-name" :title="row.name">{{row.name}}</h3>
                <div class="tool-card-endpoint mono" :title="row.url">{{row.url}}</div>
                <span class="tool-status" :class="stClass(row._st)"><span class="st-dot"></span>{{row._testing ? '探测中' : stText(row._st)}}</span>
              </div>
              <el-button text class="host-card-more" @click.stop="openForm(row)" title="编辑">···</el-button>
            </header>
            <div class="tool-card-meta">
              <span class="action-badge" :class="row.kind === 'victoriametrics' ? 'vm' : 'info'">{{kindText(row)}}</span>
              <span class="action-badge">{{authText(row)}}</span>
              <span v-if="row.insecure" class="action-badge">跳过 TLS 校验</span>
            </div>
            <div class="tool-card-remark" v-if="row.remark" :title="row.remark">{{row.remark}}</div>
            <footer class="tool-card-actions">
              <button type="button" class="hca-btn" @click="testConn(row)" :disabled="row._testing">{{row._testing?'测试中…':'测试'}}</button>
              <button type="button" class="hca-btn" @click="openForm(row)">编辑</button>
              <button type="button" class="hca-btn hca-btn-primary" @click="enter(row)">查询</button>
              <button type="button" class="hca-btn hca-btn-danger" @click="delConn(row)">删除</button>
            </footer>
          </article>
          <empty-state v-if="!loadingConn && !conns.length" text="暂无连接，点击右上角「新增连接」开始" />
        </div>
        <div class="tool-pagination" v-if="total>20"><el-pagination v-model:current-page="pg" :page-size="20" :total="total" layout="total, prev, pager, next" @current-change="loadConns" /></div>
      </div>
    </div>

    <el-dialog v-model="dialog" :title="form.id?'编辑连接':'新增连接'" width="620px" append-to-body>
      <el-form :model="form" label-position="top">
        <el-form-item label="名称" required><el-input v-model="form.name" placeholder="例如：Prometheus-生产" /></el-form-item>
        <el-form-item label="类型">
          <el-radio-group v-model="form.kind">
            <el-radio-button label="prometheus">Prometheus</el-radio-button>
            <el-radio-button label="victoriametrics">VictoriaMetrics</el-radio-button>
          </el-radio-group>
          <div class="metrics-form-hint">两者共用同一套 PromQL HTTP API（/api/v1/*）；VictoriaMetrics 常挂路径前缀，例如 http://host:8428/prometheus</div>
        </el-form-item>
        <el-form-item label="地址" required><el-input v-model="form.url" placeholder="http://10.0.0.1:9090 或 http://10.0.0.1:8428/prometheus" /></el-form-item>
        <div class="metrics-two-col">
          <el-form-item label="认证方式">
            <el-select v-model="form.auth_type" style="width:100%">
              <el-option label="无认证" value="none" />
              <el-option label="Basic 认证" value="basic" />
              <el-option label="Bearer Token" value="bearer" />
            </el-select>
          </el-form-item>
          <el-form-item label="跳过 TLS 证书校验">
            <el-switch v-model="form.insecure" />
          </el-form-item>
        </div>
        <el-form-item label="用户名" v-if="form.auth_type==='basic'">
          <el-input v-model="form.username" placeholder="Basic 认证用户名" />
        </el-form-item>
        <el-form-item :label="form.auth_type==='bearer' ? 'Token' : '密码'" v-if="form.auth_type!=='none'">
          <el-input v-model="form.secret" type="password" show-password
            :placeholder="form.has_secret ? '留空则不修改' : (form.auth_type==='bearer' ? 'Bearer Token' : 'Basic 密码')" />
        </el-form-item>
        <el-form-item label="备注"><el-input v-model="form.remark" type="textarea" :rows="2" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="dialog=false">取消</el-button><el-button type="primary" :loading="saving" @click="save">保存</el-button></template>
    </el-dialog>
  </template>

  <!-- 视图二：查询工作台 -->
  <metrics-workbench v-else :conn="current" @exit="current=null" @open-config="gotoSettings" />
</div>`,
  data() {
    return {
      conns: [], loadingConn: false, pg: 1, total: 0, pinging: false,
      viewMode: localStorage.getItem('metrics-view-mode') || 'table',
      dialog: false, form: {}, saving: false,
      current: null
    }
  },
  computed: {
    onlineCount() { return this.conns.filter(c => c._st === 1).length }
  },
  watch: { viewMode(v) { localStorage.setItem('metrics-view-mode', v) } },
  mounted() { this.loadConns() },
  methods: {
    async loadConns() {
      this.loadingConn = true
      try {
        const r = await api.get('/metrics/conns', { params: { page: this.pg, page_size: 20 } })
        if (r.code === 0) {
          this.conns = r.data?.list || []
          this.total = r.data?.total || 0
          this.pingAll()
        }
      } catch (e) { /* 拦截器已提示 */ } finally { this.loadingConn = false }
    },
    async pingAll() {
      this.pinging = true
      this.conns.forEach(row => { row._st = undefined })
      await Promise.allSettled(this.conns.map(async (row) => {
        try {
          const r = await api.post('/metrics/conns/' + row.id + '/ping')
          row._st = r.code === 0 ? 1 : 0
        } catch (e) { row._st = 0 }
      }))
      this.pinging = false
    },
    stText(s) { return s === 1 ? '在线' : s === 0 ? '不可用' : '未验证' },
    stClass(s) { return s === 1 ? 'ok' : s === 0 ? 'fail' : 'idle' },
    kindText(row) { return row.kind === 'victoriametrics' ? 'VictoriaMetrics' : 'Prometheus' },
    authText(row) { return row.auth_type === 'basic' ? 'Basic' : row.auth_type === 'bearer' ? 'Bearer' : '无认证' },
    openForm(row) {
      this.form = row
        ? { id: row.id, name: row.name, kind: row.kind || 'prometheus', url: row.url, insecure: !!row.insecure, auth_type: row.auth_type || 'none', username: row.username || '', secret: '', remark: row.remark, has_secret: row.has_secret }
        : { name: '', kind: 'prometheus', url: '', insecure: false, auth_type: 'none', username: '', secret: '', remark: '' }
      this.dialog = true
    },
    async save() {
      if (!this.form.name) { ElMessage.warning('请填写名称'); return }
      if (!this.form.url) { ElMessage.warning('请填写地址'); return }
      this.saving = true
      try {
        const d = { ...this.form }
        if (!d.secret) delete d.secret // 留空 = 不修改已存密钥（后端按 nil 密文保留原值）
        if (this.form.id) await api.put('/metrics/conns/' + this.form.id, d)
        else await api.post('/metrics/conns', d)
        this.dialog = false
        this.loadConns()
      } catch (e) { /* 拦截器已提示 */ } finally { this.saving = false }
    },
    delConn(row) {
      this.$confirm('确认删除该监控连接？不影响远端服务本身。', '提示', { type: 'warning' })
        .then(async () => { try { await api.delete('/metrics/conns/' + row.id); this.loadConns() } catch (e) { /* */ } })
        .catch(() => {})
    },
    async testConn(row) {
      row._testing = true
      try {
        const r = await api.post('/metrics/conns/' + row.id + '/ping')
        if (r.code === 0) {
          const d = r.data || {}
          this.$message.success('连接正常' + (d.version ? '（' + d.version + '）' : '') + ' · 延迟 ' + (d.latency_ms ?? '?') + ' ms')
        }
      } catch (e) { /* 拦截器已提示 */ } finally { row._testing = false }
    },
    enter(row) { this.current = row },
    // AI 接入配置是平台级的，统一在「设置」页维护；这里只负责跳过去
    gotoSettings() { location.hash = '#/settings' }
  }
}
