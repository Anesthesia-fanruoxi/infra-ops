// 监控查询 · AI 面板：自然语言生成 PromQL（MetricsAIGen）与查询结果解读（MetricsAIExplain）。
// 生成走「指标清单筛选上下文 → 模型生成 → 本地试跑校验」闭环（服务端做），试跑不通过不阻断；
// 生成成功即自动填入下方表达式框（含时间范围识别结果），面板不再重复展示生成结果，
// 本次开销（token / 参考指标 / 耗时）展示在面板头部；「时间范围识别」与「试跑未通过」保留为反馈行。
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
      @keydown.ctrl.enter.prevent="generate"
      placeholder="用一句话描述想看什么，例如：最近一小时各节点内存使用率最高的前五名"></textarea>
    <div class="metrics-ai-actions">
      <span class="metrics-ai-tip">Ctrl + Enter 生成 · 自动拉取该端点的指标与标签（含实际取值）作上下文</span>
      <span class="metrics-ai-spacer"></span>
      <el-button type="primary" size="small" :loading="loading" @click="generate">AI 生成</el-button>
    </div>

    <div class="metrics-ai-error" v-if="error"><pre>{{ error }}</pre></div>

    <!-- 生成结果不再重复展示（表达式已自动填入下方查询框）；这里只留「时间范围识别」与「试跑未通过」两类要人看的反馈 -->
    <div class="metrics-ai-range" v-if="timeRange">识别到时间范围 <b>{{ timeRange.label }}</b>，已同步切换查询时间</div>
    <div class="metrics-ai-warn" v-if="verify_error">
      <b>本地试跑未通过</b>，填入后可能需要在表达式框里微调（常见原因：标签值不存在、时间范围选择器不匹配）：{{ verify_error }}
    </div>
  </template>
</div>`,
  data() {
    return {
      cfg: {}, question: '', loading: false, error: '',
      promql: '', explain: '', used_metrics: 0, verify_error: '', usage: {}, timeRange: null
    }
  },
  mounted() { this.loadConfig() },
  methods: {
    async loadConfig() {
      try {
        // AI 接入已上提为平台设置：与 MySQL AI 面板同源，未启用时面板停在引导态
        const r = await api.get('/settings/ai')
        if (r.code === 0) this.cfg = r.data || {}
      } catch (e) { /* 拦截器已提示 */ }
    },
    async generate() {
      const q = this.question.trim()
      if (this.loading) return
      if (!q) { ElMessage.warning('请先用一句话描述你想看什么'); return }
      this.loading = true
      this.error = ''
      this.promql = ''
      this.explain = ''
      this.verify_error = ''
      this.usage = {}
      this.timeRange = null
      try {
        // 生成链路含两次模型调用（首稿 + 可能的报错重试）与本地试跑，超时放宽到 3 分钟
        const r = await api.post('/metrics/conns/' + this.connId + '/ai/promql',
          { question: q }, { timeout: 180000 })
        if (r.code === 0 && r.data) {
          this.promql = r.data.promql || ''
          this.explain = r.data.explain || ''
          this.used_metrics = r.data.used_metrics || 0
          this.verify_error = r.data.verify_error || ''
          this.usage = r.data.usage || {}
          this.timeRange = r.data.range || null
          // 生成即自动填入（需求里带时间范围时一并切好时间控件）；「填入表达式」按钮保留可重填
          if (this.promql) this.$emit('apply', this.promql, this.timeRange)
        } else {
          this.error = r.message || '生成失败'
        }
      } catch (e) { /* 拦截器已提示 */ } finally { this.loading = false }
    },
    fmtNum(n) { return Number(n || 0).toLocaleString('zh-CN') },
    fmtMs(ms) {
      const v = Number(ms || 0)
      return v < 1000 ? v + ' ms' : (v / 1000).toFixed(v < 10000 ? 1 : 0) + ' s'
    }
  }
}

// 解读面板由工作台的「AI 解读」按钮通过 ref 调 analyze() 驱动；
// 请求参数与手动查询完全一致，后端会重跑一遍查询，保证解读基于当前真实数据。
window.MetricsAIExplain = {
  props: {
    connId: { type: Number, required: true }
  },
  emits: ['open-config'],
  template: `
<div class="metrics-ai-explain-panel">
  <div class="metrics-ai-head">
    <span class="metrics-ai-title">AI 解读</span>
    <span class="metrics-ai-spacer"></span>
    <span class="metrics-ai-model mono" v-if="cfg.enabled && cfg.model" :title="cfg.base_url">{{ cfg.model }}</span>
    <el-button text size="small" @click="$emit('open-config')"><el-icon style="margin-right:4px"><Setting /></el-icon>AI 设置</el-button>
  </div>

  <div v-if="!cfg.enabled" class="metrics-ai-blank">
    <p>还没有启用 AI 解读。</p>
    <p class="metrics-ai-blank-hint">到「设置 · AI 接入」配置 OpenAI 兼容接口后，查询出结果就能一键让模型解读数据。</p>
    <el-button type="primary" size="small" @click="$emit('open-config')">去设置</el-button>
  </div>

  <template v-else>
    <div class="metrics-ai-focus">
      <el-input v-model="focus" size="small" :disabled="loading" clearable
        placeholder="想特别关注的点（可选），例如：为什么某个实例明显高于其他节点" />
      <el-button v-if="lastParams" size="small" :disabled="loading" :loading="loading" @click="rerun">重新解读</el-button>
    </div>

    <div class="metrics-ai-summary mono-hint" v-if="lastParams">
      解读对象：<span class="mono">{{ lastParams.expr }}</span> · {{ rangeText }}
    </div>

    <div class="metrics-ai-loading" v-if="loading">
      <span class="metrics-ai-loading-dot"></span>
      <span>正在重跑查询并解读数据，通常需要几秒到十几秒…</span>
    </div>

    <div class="metrics-ai-error" v-else-if="error"><pre>{{ error }}</pre></div>

    <div class="metrics-ai-blank" v-else-if="!result">
      <p>先执行一次查询，再点结果区上方的「AI 解读」。</p>
      <p class="metrics-ai-blank-hint">解读会基于当前表达式与时间范围重新拉取数据，归纳走势与异常，并给出排查建议。</p>
    </div>

    <template v-else>
      <div class="metrics-ai-summary" v-if="result.summary">{{ result.summary }}</div>

      <div class="metrics-ai-section" v-if="(result.findings || []).length">
        <div class="metrics-ai-section-title">观察发现</div>
        <ul class="metrics-ai-list">
          <li v-for="(f, i) in result.findings" :key="'f' + i">{{ f }}</li>
        </ul>
      </div>

      <div class="metrics-ai-section" v-if="(result.suggestions || []).length">
        <div class="metrics-ai-section-title">排查建议</div>
        <ul class="metrics-ai-list is-suggest">
          <li v-for="(s, i) in result.suggestions" :key="'s' + i">{{ s }}</li>
        </ul>
      </div>

      <div class="metrics-ai-digest">
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
</div>`,
  data() {
    return {
      cfg: {}, focus: '', loading: false, error: '',
      result: null, lastParams: null, digestOpen: false
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
  methods: {
    async loadConfig() {
      try {
        const r = await api.get('/settings/ai')
        if (r.code === 0) this.cfg = r.data || {}
      } catch (e) { /* 拦截器已提示 */ }
    },
    // 工作台调用入口：参数与手动查询一致（毫秒时间戳 + 秒级步长）
    analyze(params) {
      this.lastParams = { ...params }
      return this.run()
    },
    rerun() {
      if (this.lastParams) this.run()
    },
    async run() {
      const p = this.lastParams
      if (!p || this.loading) return
      this.loading = true
      this.error = ''
      try {
        const r = await api.post('/metrics/conns/' + this.connId + '/ai/explain', {
          expr: p.expr, mode: p.mode, start: p.start || 0, end: p.end || 0, step: p.step || 0,
          question: this.focus.trim()
        }, { timeout: 180000 })
        if (r.code === 0 && r.data) {
          this.result = {
            summary: r.data.summary || '',
            findings: r.data.findings || [],
            suggestions: r.data.suggestions || [],
            digest: r.data.digest || { series_count: 0, truncated: false, lines: [] },
            usage: r.data.usage || {}
          }
        } else {
          this.error = r.message || '解读失败'
        }
      } catch (e) { /* 拦截器已提示 */ } finally { this.loading = false }
    },
    fmtNum(n) { return Number(n || 0).toLocaleString('zh-CN') },
    fmtMs(ms) {
      const v = Number(ms || 0)
      return v < 1000 ? v + ' ms' : (v / 1000).toFixed(v < 10000 ? 1 : 0) + ' s'
    }
  }
}
