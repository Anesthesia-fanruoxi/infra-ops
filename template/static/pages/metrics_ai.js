// 监控查询 · AI 面板：自然语言生成 PromQL（MetricsAIGen）与查询结果解读（MetricsAIExplain）。
// 生成走「指标清单筛选上下文 → 模型生成 → 本地试跑校验」闭环（服务端做），试跑不通过不阻断；
// 生成成功即自动填入下方表达式框（含时间范围识别结果），面板不再重复展示生成结果，
// 本次开销（token / 参考指标 / 耗时）展示在面板头部；「时间范围识别」与「试跑未通过」保留为反馈行。
// 结果不理想时可「追加生成」：带上首轮需求与最新一版表达式，让模型在此基础上改。
// 追加**只给一次**——成功即丢弃上下文，按钮随之消失，回到「只有一个生成按钮」的起始态；
// 想再改就重新提问（这样界面上永远最多两个按钮，也不需要「重新开始」这种清理入口）。
// 解读由后端重跑查询后归纳，前端只展示，不把结果快照回传。
window.MetricsAIGen = {
  props: {
    connId: { type: Number, required: true }
  },
  emits: ['apply', 'open-config'],
  template: `
<div class="metrics-ai-gen">
  <div class="metrics-ai-head">
    <span class="metrics-ai-title">AI 生成 PromQL</span>
    <!-- 生成开销当场展示：结果直接填进下方表达式框，不再单独展示生成结果 -->
    <span class="metrics-ai-usage" v-if="usage.total_tokens">
      本次消耗 <b>{{ fmtNum(usage.total_tokens) }}</b> tokens<span v-if="used_metrics"> · 参考 {{ used_metrics }} 个指标作上下文</span> · 耗时 {{ fmtMs(usage.elapsed_ms) }}<span v-if="usage.source === 'estimated'"> · 接口未返回用量，按字符估算</span>
    </span>
    <span class="metrics-ai-spacer"></span>
    <span class="metrics-ai-model mono" v-if="cfg.enabled && cfg.model" :title="cfg.base_url">{{ cfg.model }}</span>
    <el-button text size="small" @click="$emit('open-config')"><el-icon style="margin-right:4px"><Setting /></el-icon>AI 设置</el-button>
  </div>

  <div v-if="!cfg.enabled" class="metrics-ai-blank">
    <p>还没有启用 AI 查询。</p>
    <p class="metrics-ai-blank-hint">到「设置 · AI 接入」填好 OpenAI 兼容接口的地址、密钥与模型名即可（DeepSeek / 通义 / Kimi / 本地 Ollama 都走同一套）。</p>
    <el-button type="primary" size="small" @click="$emit('open-config')">去设置</el-button>
  </div>

  <template v-else>
    <textarea class="metrics-ai-input" v-model="question" spellcheck="false" :disabled="loading" rows="2"
      @keydown.ctrl.enter.prevent="generate(false)"
      placeholder="用一句话描述想看什么，例如：最近一小时各节点内存使用率最高的前五名"></textarea>
    <div class="metrics-ai-actions">
      <span class="metrics-ai-tip">Ctrl + Enter 生成 · 自动拉取该端点的指标与标签（含实际取值）作上下文</span>
      <span class="metrics-ai-spacer"></span>
      <el-button v-if="prev" size="small" :disabled="loading" :loading="loadingFollowup" @click="generate(true)">追加生成</el-button>
      <el-button type="primary" size="small" :loading="loading && !loadingFollowup" @click="generate(false)">生成 PromQL</el-button>
    </div>

    <!-- 追加生成的上下文提示：带的是首轮需求 + 最新一版表达式，且**只有这一次机会**，
         说清楚「模型看到什么、还能不能追加」，使用者才不用猜 -->
    <div class="metrics-ai-range" v-if="prev">
      已带上首轮需求与最新一版表达式，本次追加将在其基础上修改（只能追加一次）：<b>{{ prev.question }}</b>
    </div>

    <div class="metrics-ai-error" v-if="error"><pre>{{ error }}</pre></div>

    <!-- 流式草稿：表达式边生成边显示；首稿试跑未通过会整体重出一版（reset 帧清空并说明缘由），
         终稿落定后草稿即收（表达式自动填入下方查询框，试跑结论与时间范围识别照旧反馈） -->
    <div class="metrics-ai-draft" v-if="draftActive || draftPromql || draftExplain">
      <div class="metrics-ai-draft-head">
        <span class="metrics-ai-draft-tag" :class="draftActive ? 'is-live' : 'is-dead'">{{ draftActive ? (draftReset ? '重试修正中…' : '生成中…') : '草稿 · 未采用' }}</span>
        <span class="metrics-ai-draft-hint" v-if="draftActive">完成并试跑校验后自动填入表达式框</span>
      </div>
      <div class="metrics-ai-draft-note" v-if="draftReset" :title="draftResetDetail">{{ draftReset }}</div>
      <pre class="metrics-ai-draft-expr" v-if="draftPromql">{{ draftPromql }}</pre>
      <div class="metrics-ai-draft-explain" v-if="draftExplain">{{ draftExplain }}</div>
      <div class="metrics-ai-draft-wait" v-if="draftActive && !draftPromql && !draftExplain">正在等待模型响应…</div>
    </div>

    <!-- 生成结果不再重复展示（表达式已自动填入下方查询框）；这里只留「时间范围识别」与「试跑未通过」两类要人看的反馈 -->
    <div class="metrics-ai-range" v-if="timeRange">识别到时间范围 <b>{{ timeRange.label }}</b>，已同步切换查询时间</div>
    <div class="metrics-ai-warn" v-if="verify_error">
      <b>本地试跑未通过</b>，填入后可能需要在表达式框里微调（常见原因：标签值不存在、时间范围选择器不匹配）：{{ verify_error }}
    </div>
  </template>
</div>`,
  data() {
    return {
      cfg: {}, question: '', loading: false, loadingFollowup: false, error: '',
      // prev = 追加生成的上下文（首轮需求 + 最新一版表达式）：非空才出现「追加生成」按钮，
      // 追加成功即连同 base 一起清空（追加只给一次），于是界面回到只有一个生成按钮的状态
      base: '', prev: null,
      promql: '', explain: '', used_metrics: 0, verify_error: '', usage: {}, timeRange: null,
      // 流式草稿：模型边生成边展示的表达式 / 说明增量；draftReset 是「首稿被弃重出一版」的缘由
      draftPromql: '', draftExplain: '', draftActive: false, draftReset: '', draftResetDetail: ''
    }
  },
  mounted() { this.loadConfig() },
  beforeUnmount() {
    if (this._genStream) { this._genStream.close(); this._genStream = null } // 生成流随组件走：关掉即取消服务端请求
  },
  methods: {
    async loadConfig() {
      try {
        // AI 接入已上提为平台设置：与 MySQL AI 面板同源，未启用时面板停在引导态
        const r = await api.get('/settings/ai')
        if (r.code === 0) this.cfg = r.data || {}
      } catch (e) { /* 拦截器已提示 */ }
    },
    // generate(true) = 追加生成：把首轮需求与最新一版表达式一并带上，让模型在上一版基础上改。
    // 流式口径：delta 帧边收边拼草稿；首稿试跑失败时服务端推 reset 帧（清空草稿并附缘由，
    // 第二稿继续流式）；final 帧才落结果并自动填入表达式框——校验以服务端为准
    async generate(followup) {
      const q = this.question.trim()
      if (this.loading) return
      if (!q) { ElMessage.warning('请先用一句话描述你想看什么'); return }
      // 没有上一轮时「追加」退化为普通生成（按钮此时也不会出现，双保险）
      const usePrev = !!followup && !!this.prev
      const sse = window.__INFRA_SSE__
      if (!sse) { this.error = '流式通道不可用：请通过桌面应用运行后重试'; return }

      this.loading = true
      this.loadingFollowup = usePrev
      this.error = ''
      this.promql = ''
      this.explain = ''
      this.verify_error = ''
      this.usage = {}
      this.timeRange = null
      this.draftPromql = ''
      this.draftExplain = ''
      this.draftReset = ''
      this.draftResetDetail = ''
      this.draftActive = true

      const body = { question: q }
      if (usePrev) body.prev = { question: this.base, promql: this.prev.promql, explain: this.prev.explain }
      let settled = false
      const finish = () => {
        this.loading = false
        this.loadingFollowup = false
        this.draftActive = false
        this._genStream = null
      }
      // final 帧落结果：与旧同步口径一致（自动填入 + 追加一次性语义）
      const applyFinal = (d) => {
        this.promql = d.promql || ''
        this.explain = d.explain || ''
        this.used_metrics = d.used_metrics || 0
        this.verify_error = d.verify_error || ''
        this.usage = d.usage || {}
        this.timeRange = d.range || null
        // 追加只给一次：成功即丢弃上下文（按钮随之消失），回到只有一个生成按钮的起始态；
        // 首轮成功则记下这一版，供下一次追加使用。失败不动上下文，按钮留着可重试。
        if (usePrev) {
          this.prev = null
          this.base = ''
        } else if (this.promql) {
          this.base = q
          this.prev = { question: q, promql: this.promql, explain: this.explain }
        } else {
          this.prev = null
          this.base = ''
        }
        // 生成即自动填入（需求里带时间范围时一并切好时间控件）；「填入表达式」按钮保留可重填
        if (this.promql) this.$emit('apply', this.promql, this.timeRange)
        this.draftPromql = '' // 终稿已落，草稿收起
        this.draftExplain = ''
        this.draftReset = ''
      }
      // 生成链路含两次模型调用（首稿 + 可能的报错重试）与本地试跑，服务端无整体超时，交给 AI 配置的单次超时
      this._genStream = sse.post('/api/metrics/conns/' + this.connId + '/ai/promql', body, {
        onEvent: (type, data) => {
          let ev = {}
          try { ev = JSON.parse(data) } catch (e) { return }
          if (type === 'delta') {
            if (ev.field === 'promql') this.draftPromql += ev.text || ''
            else if (ev.field === 'explain') this.draftExplain += ev.text || ''
            return
          }
          if (type === 'reset') {
            // 首稿被弃重出：清空草稿并说明缘由（详情进 title），第二稿的 delta 随后就到
            this.draftPromql = ''
            this.draftExplain = ''
            this.draftReset = ev.reason || '首稿未通过试跑，正在修正重试'
            this.draftResetDetail = ev.detail || ''
            return
          }
          if (type === 'error') {
            settled = true
            this.error = ev.message || '生成失败'
            finish() // 草稿保留：标成「未采用」的同时能看到模型给了什么
            return
          }
          if (type === 'final' && !settled) {
            settled = true
            applyFinal(ev)
            finish()
          }
        },
        onEnd: () => {
          if (!settled) { this.error = '生成未完成（连接提前结束），请重试'; finish() }
        },
        onError: (err) => {
          if (!settled) { this.error = (err && err.message) || '生成失败'; finish() }
        }
      })
    },
    fmtNum(n) { return Number(n || 0).toLocaleString('zh-CN') },
    fmtMs(ms) {
      const v = Number(ms || 0)
      return v < 1000 ? v + ' ms' : (v / 1000).toFixed(v < 10000 ? 1 : 0) + ' s'
    }
  }
}

// 解读以弹框呈现（用户口径）：结果区点「AI 解读」→ 首次执行自动弹出；关闭只是收起，
// 这份结论仍保留着，结果区的按钮转为「查看解读 / 重新分析」——
//   查看 = 把保留的这份重新打开（不重跑、不花 token）；重新分析 = 重跑并整体覆盖。
// **永远只有一份**：每次 run() 整体替换 result，不累积历史版本。
// 请求参数与手动查询完全一致，后端会重跑一遍查询，保证解读基于当前真实数据。
// 三段结论（总体结论 / 观察发现 / 排查建议）走 AiHl 着色，重点信息由前端渲染，不写回后端。
window.MetricsAIExplain = {
  props: {
    connId: { type: Number, required: true }
  },
  emits: ['open-config', 'state'],
  template: `
<div class="metrics-ai-explain-panel">
  <el-dialog v-model="visible" width="820px" top="6vh" append-to-body
    class="metrics-ai-explain-dialog" :close-on-click-modal="false">
    <template #header>
      <div class="metrics-ai-dialog-head">
        <span class="metrics-ai-title">AI 解读</span>
        <span class="metrics-ai-model mono" v-if="cfg.enabled && cfg.model" :title="cfg.base_url">{{ cfg.model }}</span>
        <span class="metrics-ai-spacer"></span>
        <el-button text size="small" @click="$emit('open-config')"><el-icon style="margin-right:4px"><Setting /></el-icon>AI 设置</el-button>
      </div>
    </template>

  <!-- 配置是异步读的：没读完不能当「未启用」处理，否则一进来点解读就误停在引导态 -->
  <div v-if="!cfgLoaded" class="metrics-ai-loading">
    <span class="metrics-ai-loading-dot"></span>
    <span>正在读取 AI 配置…</span>
  </div>

  <div v-else-if="!cfg.enabled" class="metrics-ai-blank">
    <p>还没有启用 AI 解读。</p>
    <p class="metrics-ai-blank-hint">到「设置 · AI 接入」配置 OpenAI 兼容接口后，查询出结果就能一键让模型解读数据。</p>
    <el-button type="primary" size="small" @click="$emit('open-config')">去设置</el-button>
  </div>

  <template v-else>
    <div class="metrics-ai-focus">
      <el-input v-model="focus" size="small" :disabled="loading" clearable
        placeholder="想特别关注的点（可选），例如：为什么某个实例明显高于其他节点" />
    </div>

    <div class="metrics-ai-summary mono-hint" v-if="lastParams">
      解读对象：<span class="mono">{{ lastParams.expr }}</span> · {{ rangeText }}
    </div>

    <div class="metrics-ai-loading" v-if="loading">
      <span class="metrics-ai-loading-dot"></span>
      <span>正在重跑查询并流式解读数据（结论边生成边显示）…</span>
    </div>

    <div class="metrics-ai-error" v-if="error"><pre>{{ error }}</pre></div>

    <div class="metrics-ai-blank" v-if="!loading && !hasResult() && !error">
      <p>先执行一次查询，再点结果区上方的「AI 解读」。</p>
      <p class="metrics-ai-blank-hint">解读会基于当前表达式与时间范围重新拉取数据，归纳走势与异常，并给出排查建议。结论里的数值、异常与正常等关键信息会自动着色。</p>
    </div>

    <template v-if="hasResult()">
      <div class="metrics-ai-summary" v-if="result.summary" v-html="hl(result.summary)"></div>

      <div class="metrics-ai-section" v-if="(result.findings || []).length">
        <div class="metrics-ai-section-title">观察发现</div>
        <ul class="metrics-ai-list">
          <li v-for="(f, i) in result.findings" :key="'f' + i" v-html="hl(f)"></li>
        </ul>
      </div>

      <div class="metrics-ai-section" v-if="(result.suggestions || []).length">
        <div class="metrics-ai-section-title">排查建议</div>
        <ul class="metrics-ai-list is-suggest">
          <li v-for="(s, i) in result.suggestions" :key="'s' + i" v-html="hl(s)"></li>
        </ul>
      </div>

      <div class="metrics-ai-digest" v-if="result.digest">
        <a class="metrics-ai-digest-toggle" @click="digestOpen = !digestOpen">
          {{ digestOpen ? '收起' : '展开' }}解读依据（{{ result.digest.series_count }} 条序列统计{{ result.digest.truncated ? '，仅列前若干条' : '' }}）
        </a>
        <div class="metrics-ai-digest-body" v-show="digestOpen">
          <div class="metrics-ai-digest-line mono" v-for="(l, i) in (result.digest.lines || [])" :key="'l' + i">{{ l }}</div>
        </div>
      </div>

      <div class="metrics-ai-usage" v-if="result.usage && result.usage.total_tokens">
        本次消耗 <b>{{ fmtNum(result.usage.total_tokens) }}</b> tokens（输入 {{ fmtNum(result.usage.prompt_tokens) }} · 输出 {{ fmtNum(result.usage.completion_tokens) }}）· 耗时 {{ fmtMs(result.usage.elapsed_ms) }}<span v-if="result.usage.source === 'estimated'"> · 接口未返回用量，按字符估算</span>
      </div>
    </template>
  </template>

    <template #footer>
      <span class="metrics-ai-dialog-hint">关闭后这份结论仍保留，可在结果区「查看解读」重新打开</span>
      <el-button @click="visible = false">关闭</el-button>
      <el-button type="primary" :loading="loading" :disabled="!lastParams" @click="rerun">重新分析</el-button>
    </template>
  </el-dialog>
</div>`,
  data() {
    return {
      cfg: {}, cfgLoaded: false, focus: '', loading: false, error: '',
      result: null, lastParams: null, digestOpen: false,
      // 弹框开关：首次执行由 analyze() 自动置 true；关闭只是收起，结论仍留着
      visible: false,
      // 配置还没读完就点了「AI 解读」：先记下，等配置到位再补跑（否则误停在引导态）
      pendingRun: false
    }
  },
  computed: {
    rangeText() {
      const p = this.lastParams
      if (!p) return ''
      if (p.mode === 'instant') return '即时查询'
      const start = p.start ? new Date(p.start) : null
      const end = p.end ? new Date(p.end) : new Date()
      const fmt = (d) => d.toLocaleString('zh-CN', { hour12: false })
      return '范围 ' + fmt(start || end) + ' ~ ' + fmt(end)
    }
  },
  mounted() { this.loadConfig() },
  beforeUnmount() {
    if (this._stream) { this._stream.close(); this._stream = null } // 解读流随组件走：关掉即取消服务端请求
  },
  methods: {
    async loadConfig() {
      try {
        const r = await api.get('/settings/ai')
        if (r.code === 0) this.cfg = r.data || {}
      } catch (e) { /* 拦截器已提示 */ } finally {
        this.cfgLoaded = true
        // 读配置期间点过「AI 解读」：此刻补跑（未启用则停在引导态，等用户去设置）
        if (this.pendingRun) { this.pendingRun = false; this.run() }
      }
    },
    // 工作台「AI 解读 / 重新分析」入口：参数与手动查询一致（毫秒时间戳 + 秒级步长）。
    // 首次与重来都走这里——**重新分析一律重跑**，结果整体覆盖（永远只有一份）。
    analyze(params) {
      if (params) this.lastParams = { ...params }
      this.visible = true // 首次执行自动弹出（用户口径）
      if (!this.cfgLoaded) { this.pendingRun = true; return }
      this.run()
    },
    // 工作台「查看解读」入口：只把保留的那份重新打开，绝不重跑（不花 token、不改结论）
    open() {
      this.visible = true
    },
    rerun() {
      if (this.lastParams) this.run()
    },
    // 流式口径：delta 帧直接把增量拼进 result（结论边的生成边显示，三段内容各自攒批）；
    // final 帧以服务端清洗后的完整结果为准（digest 与 usage 只有它有）
    async run() {
      const p = this.lastParams
      if (!p || this.loading || !this.cfg.enabled) return
      const sse = window.__INFRA_SSE__
      if (!sse) { this.error = '流式通道不可用：请通过桌面应用运行后重试'; return }
      this.loading = true
      this.error = ''
      // 空白结果对象：digest 与 usage 只有 final 帧才有，先置 null 让对应区块保持隐藏。
      // 整体替换而非追加——这就是「永远只保留一份」的落点：重新分析不留上一版。
      this.result = { summary: '', findings: [], suggestions: [], digest: null, usage: {} }
      this.emitState()
      const appendDelta = (ev) => {
        if (ev.field === 'summary') this.result.summary += ev.text || ''
        else if (ev.field === 'findings' || ev.field === 'suggestions') {
          const arr = this.result[ev.field]
          const idx = Math.max(0, ev.index || 0)
          while (arr.length <= idx) arr.push('')
          arr[idx] += ev.text || ''
        }
      }
      let settled = false
      // 收尾唯一出口：清 loading + 把 has/busy 冒泡给工作台（结果区按钮态由它决定）
      const settle = (msg) => {
        if (msg) this.error = msg
        this.loading = false
        this.emitState()
      }
      this._stream = sse.post('/api/metrics/conns/' + this.connId + '/ai/explain', {
        expr: p.expr, mode: p.mode, start: p.start || 0, end: p.end || 0, step: p.step || 0,
        question: this.focus.trim()
      }, {
        onEvent: (type, data) => {
          let ev = {}
          try { ev = JSON.parse(data) } catch (e) { return }
          if (type === 'delta') { appendDelta(ev); return }
          if (type === 'error') {
            settled = true
            settle(ev.message || '解读失败')
            return
          }
          if (type === 'final' && !settled) {
            settled = true
            this.result = {
              summary: ev.summary || '',
              findings: ev.findings || [],
              suggestions: ev.suggestions || [],
              digest: ev.digest || { series_count: 0, truncated: false, lines: [] },
              usage: ev.usage || {}
            }
            settle()
          }
        },
        onEnd: () => {
          if (settled) return
          settle('解读未完成（连接提前结束），请重试')
        },
        onError: (err) => {
          if (settled) return
          settle((err && err.message) || '解读失败')
        }
      })
    },
    // 是否已有一份可看的结论（空结论 / 只有报错不算）。模板与状态冒泡共用同一判据，避免两处走样。
    hasResult() {
      const r = this.result
      return !!(r && (r.summary || (r.findings || []).length || (r.suggestions || []).length))
    },
    emitState() {
      this.$emit('state', { has: this.hasResult(), busy: this.loading })
    },
    // 重点信息着色（数值 / 风险 / 正常 / 注意 / 标识符）。实现见 static/ai_highlight.js：
    // 它负责转义 + 包标签，这里只把模型文本交给它，不要自己拼 HTML
    hl(text) { return window.AiHl.html(text) },
    fmtNum(n) { return Number(n || 0).toLocaleString('zh-CN') },
    fmtMs(ms) {
      const v = Number(ms || 0)
      return v < 1000 ? v + ' ms' : (v / 1000).toFixed(v < 10000 ? 1 : 0) + ' s'
    }
  }
}
