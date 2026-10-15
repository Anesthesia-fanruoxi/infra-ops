// ES 控制台 · Grok 解析 tab：贴一段样例日志 + grok pattern，逐行看解析出的字段。
//
// 解析走服务端本地引擎（common/grok，RE2 方言），不访问集群——集群没建好、或手上只有
// 一段日志时也能调 pattern。整行未命中时会回「部分命中」提示（哪一段被吃掉了），
// 这是排错最有用的信号，比一个空结果强。
//
// AI 生成：流式草稿 → 终稿填入 pattern 框 → **自动复跑一次解析**，把结果铺到右栏。
// 自动复跑不是多余的：模型对「自己写的 pattern 到底能不能匹配这条样例」没有判断力
// （实测它会把日志格式定义里的变量顺序当成字面分隔符，样例里并不存在的 `-` 就这么被写进 pattern）。
// 服务端已在 final 前用本地引擎实跑校验、必要时回喂失败现场修正一次，前端再复跑一次是为了让
// 使用者立刻看到「这版 pattern 在样例上是什么结果」，而不是对着上一版的结果发呆。
// 支持一次「追加生成」，补充要求里可直接贴日志格式定义（nginx log_format / logback pattern）。
window.EsGrokTab = {
  props: ['conn'],
  components: {
    // Logstash 配置生成拆成独立组件（状态与解析面板互不相干，详见该文件头部注释）。
    // 取的是 window 上的定义：脚本顺序由 index.html 保证（它必须先于本文件装载），门禁锁了这条。
    'es-grok-logstash': window.EsGrokLogstash
  },
  template: `
<div class="es-grok-wrap">
  <div class="es-grok-grid">
    <!-- 左栏：输入与 AI。与右栏各自滚动 —— 任一边内容变长都不会把整页撑出滚动条 -->
    <div class="es-grok-pane is-input">
      <div class="es-area-label">样例日志（每行一条，空行忽略，最多解析 200 行）</div>
      <el-input v-model="sample" type="textarea" :rows="7" spellcheck="false" class="es-grok-mono"
        placeholder="粘贴几行真实日志，例如：&#10;2026-10-10 14:20:31 ERROR [order] create failed: timeout" />

      <div class="es-area-label">Grok Pattern（每行一条，按顺序尝试，首条整行命中生效）</div>
      <el-input v-model="patternText" type="textarea" :rows="4" spellcheck="false" class="es-grok-mono"
        @keydown.ctrl.enter.prevent="parse"
        placeholder="%{TIMESTAMP_ISO8601:ts} %{LOGLEVEL:level} \\[%{DATA:ctx}\\] %{GREEDYDATA:message}" />

      <div class="es-grok-actions">
        <el-button type="primary" :loading="parsing" @click="parse">解析</el-button>
        <el-button :disabled="parsing || aiLoading" @click="clearAll">清空</el-button>
        <span class="es-grok-hint">Ctrl + Enter 解析 · 支持 %{NAME:field} 与 %{NAME:field:int} 语法</span>
      </div>

      <div class="es-grok-error" v-if="parseError">
        <div class="es-grok-error-title">pattern 有问题</div>
        <pre>{{ parseError }}</pre>
      </div>

      <div class="es-grok-toggle" @click="showDefs = !showDefs">
        <span>{{ showDefs ? '▾' : '▸' }} 自定义 pattern</span>
        <span class="es-grok-hint">每行一条：NAME 正则（覆盖内置同名项）</span>
      </div>
      <el-input v-if="showDefs" v-model="defText" type="textarea" :rows="3" spellcheck="false" class="es-grok-mono"
        placeholder="MY_ID [A-Z]{2}-\\d{4}" />

      <div class="es-grok-sep"></div>

      <div class="es-area-label">AI 生成 pattern（读样例日志，给出 pattern 与字段解释）</div>
      <!-- 补充要求支持直接贴日志格式定义（nginx log_format / logback pattern）：格式里变量
           与字段的对应关系是字段命名的权威依据；但字面结构一律以样例为准 -->
      <el-input v-model="aiHint" type="textarea" :rows="2" spellcheck="false" class="es-grok-mono"
        placeholder="补充要求或日志格式定义（可选），例如：nginx 的 log_format main '[$time_iso8601,$timestamp] [$request_method $request_uri - $request_length $status] ...'；或 logback 的 pattern 模板" />
      <div class="es-grok-actions">
        <el-button size="small" :loading="aiLoading && !aiLoadingFollowup" :disabled="parsing" @click="aiGenerate(false)">AI 生成</el-button>
        <el-button v-if="aiPrev" size="small" :disabled="parsing || aiLoading" :loading="aiLoadingFollowup" @click="aiGenerate(true)">追加生成</el-button>
        <span class="es-grok-hint" v-if="aiPrev">已带上上一版 pattern，本次追加在其基础上修改（只能追加一次）</span>
      </div>

      <!-- 校验状态：服务端在 final 之前已经用本地引擎在样例上实跑过，这里把「命中几行」直接摊开说，
           免得使用者拿到 pattern 才发现整行不匹配（模型自己判断不了，跑一遍才知道） -->
      <div class="es-grok-ai-status" :class="(aiVerify && aiVerify.ok) ? 'is-ok' : 'is-warn'"
        v-if="aiPhase || aiVerify">
        <span class="es-grok-dot"></span>{{ aiPhase || verifyText }}
      </div>

      <!-- 流式草稿：pattern 与说明边生成边显示，终稿落定后自动填入上方 pattern 框、草稿即收 -->
      <div class="es-grok-draft" v-if="aiLoading || aiDraftPatterns.length || aiDraftExplain">
        <div class="es-grok-draft-head">
          <span class="es-grok-draft-tag" :class="aiLoading ? 'is-live' : 'is-dead'">{{ aiLoading ? '生成中…' : '草稿 · 未采用' }}</span>
          <span class="es-grok-hint" v-if="aiLoading">完成即自动填入上方 pattern 框并复跑解析</span>
        </div>
        <pre class="es-grok-draft-body" v-if="aiDraftPatterns.length">{{ aiDraftPatterns.join('\\n') }}</pre>
        <div class="es-grok-draft-explain" v-if="aiDraftExplain">{{ aiDraftExplain }}</div>
        <div class="es-grok-draft-wait" v-if="aiLoading && !aiDraftPatterns.length && !aiDraftExplain">正在等待模型响应…</div>
      </div>
      <div class="es-grok-error" v-if="aiError">
        <div class="es-grok-error-title">AI 生成失败</div>
        <pre>{{ aiError }}</pre>
      </div>
      <div class="es-grok-ai-result" v-if="aiExplain">
        <pre class="es-grok-explain">{{ aiExplain }}</pre>
        <div class="es-grok-fgrid" v-if="aiFields.length">
          <span v-for="f in aiFields" :key="f.name" class="es-grok-fitem">
            <b class="mono">{{ f.name }}</b><span v-if="f.desc">{{ f.desc }}</span>
          </span>
        </div>
        <div class="es-grok-usage" v-if="usage.total_tokens">
          本次消耗 <b>{{ usage.total_tokens }}</b> tokens · 耗时 {{ usage.elapsed_ms }} ms<span v-if="usage.calls > 1"> · 含 1 次修正</span><span v-if="usage.source === 'estimated'"> · 按字符估算</span>
        </div>
      </div>

      <div class="es-grok-sep"></div>
      <!-- Logstash 配置：把上面调通的 pattern 组装成可直接粘贴的 filter（匹配 tag → if 条件） -->
      <es-grok-logstash ref="ls" :conn="conn" :patterns="patterns" :definitions="definitions" :sample="sample" :hint="aiHint" />
    </div>

    <div class="es-grok-pane is-result">
      <template v-if="result">
        <div class="es-grok-stat">
          <span>共 <b>{{ result.total }}</b> 行</span>
          <span class="ok">整行命中 <b>{{ result.matched }}</b></span>
          <span class="part" v-if="result.partial">部分命中 <b>{{ result.partial }}</b></span>
          <span class="bad" v-if="result.missed">未命中 <b>{{ result.missed }}</b></span>
          <span class="warn" v-if="result.truncated">超出上限，仅解析前 {{ result.total }} 行</span>
        </div>
        <div class="es-grok-lines">
          <div v-for="l in result.lines" :key="l.no" class="es-grok-card"
            :class="l.matched ? 'is-ok' : (l.partial ? 'is-part' : 'is-miss')">
            <div class="es-grok-card-head">
              <span class="es-grok-no mono">#{{ l.no }}</span>
              <span class="es-grok-badge">{{ l.matched ? '整行命中' : (l.partial ? '部分命中' : '未命中') }}</span>
              <span class="es-grok-raw mono">{{ l.raw }}</span>
              <el-button v-if="l.fields.length" text size="small" @click="copyFields(l)">复制字段</el-button>
            </div>
            <div class="es-grok-fields" v-if="l.fields.length">
              <div v-for="f in l.fields" :key="f.name" class="es-grok-frow">
                <span class="es-grok-fname mono">{{ f.name }}</span>
                <span class="es-grok-fval mono">{{ f.value }}</span>
                <span class="es-grok-ftype" v-if="f.type">{{ f.type }}</span>
              </div>
            </div>
            <div class="es-grok-tip" :class="l.matched ? 'is-ok' : (l.partial ? 'is-part' : 'is-miss')" v-else>
              {{ l.partial ? '只匹配到片段：pattern 需覆盖整行，可在末尾补 %{GREEDYDATA:message} 之类兜底。'
                : (l.matched ? '整行命中，但 pattern 里没有命名字段（把要拆的值写成 %{NAME:field} 才会出现在这里）。'
                  : '整行未匹配：检查 pattern 的空白/方括号是否与实际日志一致，或这行结构本来就不同（可再加一条 pattern）。') }}
            </div>
          </div>
        </div>
      </template>
      <div v-else class="es-grok-blank">
        <p>左侧贴样例日志与 pattern，点「解析」看每行拆出的字段。</p>
        <p class="es-grok-hint">不想手写 pattern？点「AI 生成」，模型读完样例给出 pattern 与字段解释，服务端会在样例上实跑校验、必要时自动修正一次，终稿自动填入并复跑解析。</p>
      </div>
    </div>
  </div>
</div>`,
  data() {
    return {
      sample: '', patternText: '', defText: '', showDefs: false,
      result: null, parsing: false, parseError: '',
      aiHint: '', aiLoading: false, aiLoadingFollowup: false, aiExplain: '', aiFields: [], usage: {},
      // aiVerify = 服务端在 final 帧里回的本地实跑结果（命中几行 / 是否修正过），界面据此说明质量；
      // aiPhase = 生成中途的服务端阶段说明（首稿未命中、正在按样例修正）
      aiVerify: null, aiPhase: '',
      // aiPrev = 追加生成的上下文（上一版 pattern + 最初的补充要求）：非空才出现「追加生成」按钮，
      // 追加成功即清空（只给一次）；aiBase 存首轮补充要求原文，供追加时回灌
      aiPrev: null, aiBase: '',
      // AI 流式草稿：pattern 数组与说明增量；aiError 与草稿并存（被弃稿上也标「未采用」）
      aiDraftPatterns: [], aiDraftExplain: '', aiError: ''
    }
  },
  computed: {
    patterns() { return this.patternText.split('\n').map(s => s.trim()).filter(Boolean) },
    // 自定义 pattern 文本 → 定义表：每行「NAME 正则」，# 起头为注释
    definitions() {
      const out = {}
      this.defText.split('\n').forEach(line => {
        const s = line.trim()
        if (!s || s.startsWith('#')) return
        const i = s.search(/\s/)
        if (i <= 0) return
        out[s.slice(0, i)] = s.slice(i).trim()
      })
      return out
    },
    // 校验状态一句话：命中几行 + 这版是首稿直接过的还是服务端修正过的
    verifyText() {
      const v = this.aiVerify
      if (!v) return ''
      const base = '样例整行命中 ' + v.matched + '/' + v.total
      if (v.ok) return base + (v.adopted_retry ? ' · 首稿未命中，服务端已按失败样例自动修正一次' : ' · 首稿一次通过')
      // 编译失败要给出下一步动作：这版 pattern 连加载都过不了，用户盯着错误串也不知道该改哪
      if (v.error) return base + ' · pattern 编译失败：' + v.error
        + ' —— 这版不能直接用（最常见的是日志里的字面方括号没转义成 \\[），建议点「追加生成」让它修'
      return base + (v.attempts > 1
        ? ' · 自动修正未成功，可在补充要求里补上日志格式定义再试'
        : ' · 未能覆盖整行，右栏已列出逐行结果')
    }
  },
  methods: {
    async parse() {
      if (this.parsing) return
      if (!this.sample.trim()) { this.$message.warning('请先粘贴一段样例日志'); return }
      if (!this.patterns.length) { this.$message.warning('请先填写 grok pattern'); return }
      this.parsing = true
      this.parseError = ''
      try {
        const r = await api.post('/es/' + this.conn.id + '/grok/parse',
          { patterns: this.patterns, text: this.sample, definitions: this.definitions })
        if (r.code === 0) {
          this.result = r.data || null
        } else {
          // 编译类错误留在面板里（用户要照着改 pattern），不只弹一句提示
          this.parseError = r.message || '解析失败'
          this.result = null
        }
      } catch (e) { /* 拦截器已提示 */ } finally { this.parsing = false }
    },
    // aiGenerate(true) = 追加生成：把最初补充要求与最新一版 pattern 一并带上，让模型在其基础上改。
    // 流式口径：delta 帧边收边拼草稿；phase 帧表示服务端要按失败样例修正（草稿清空重来）；
    // final 帧才落结果、填入 pattern 框，并自动复跑一次解析把结果铺到右栏
    async aiGenerate(followup) {
      if (this.aiLoading) return
      if (!this.sample.trim()) { this.$message.warning('AI 生成需要样例日志：先贴几行真实日志'); return }
      const sse = window.__INFRA_SSE__
      if (!sse) { this.$message.error('流式通道不可用：请通过桌面应用运行后重试'); return }
      // 没有上一轮时「追加」退化为普通生成（按钮此时也不会出现，双保险）
      const usePrev = !!followup && !!this.aiPrev
      this.aiLoading = true
      this.aiLoadingFollowup = usePrev
      this.aiError = ''
      this.aiExplain = ''
      this.aiFields = []
      this.usage = {}
      this.aiDraftPatterns = []
      this.aiDraftExplain = ''
      this.aiPhase = ''
      this.aiVerify = null
      let settled = false
      const finish = () => { this.aiLoading = false; this.aiLoadingFollowup = false; this._aiStream = null }
      // final 帧落结果：填入 pattern 框 → 复跑解析（生成与「看到结果」之间不留空档）
      const applyFinal = (d) => {
        this.patternText = (d.patterns || []).join('\n')
        this.mergeDefs(d.definitions || {})
        this.aiExplain = d.explain || ''
        this.aiFields = d.fields || []
        this.usage = d.usage || {}
        this.aiVerify = d.verify || null
        this.aiDraftPatterns = [] // 终稿已填入 pattern 框，草稿收起
        this.aiDraftExplain = ''
        // 追加只给一次：成功即丢弃上下文（按钮随之消失）；首轮成功则记下这一版，供下一次追加使用。
        // 失败不动上下文，按钮留着可重试。
        if (usePrev) {
          this.aiPrev = null
          this.aiBase = ''
        } else if (this.patterns.length) {
          this.aiBase = this.aiHint.trim()
          this.aiPrev = { hint: this.aiBase, patterns: this.patterns, explain: this.aiExplain }
        } else {
          this.aiPrev = null
          this.aiBase = ''
        }
        // 复跑解析：右栏立刻反映新 pattern 的结果（这是本地调用，秒回、无成本）
        if (this.patterns.length) this.parse()
      }
      const body = { sample: this.sample, hint: this.aiHint.trim(), definitions: this.definitions }
      if (usePrev) body.prev = { hint: this.aiBase, patterns: this.aiPrev.patterns, explain: this.aiPrev.explain }
      this._aiStream = sse.post('/api/es/' + this.conn.id + '/grok/ai', body, {
        onEvent: (type, data) => {
          let ev = {}
          try { ev = JSON.parse(data) } catch (e) { return }
          if (type === 'delta') {
            if (ev.field === 'patterns') {
              const idx = Math.max(0, ev.index || 0)
              while (this.aiDraftPatterns.length <= idx) this.aiDraftPatterns.push('')
              this.aiDraftPatterns[idx] += ev.text || ''
            } else if (ev.field === 'explain') {
              this.aiDraftExplain += ev.text || ''
            }
            return
          }
          if (type === 'phase') {
            // 首稿在样例上没整行命中：服务端把失败现场回喂给模型重新生成，草稿清空重来
            this.aiPhase = '首稿整行命中 ' + (ev.matched || 0) + '/' + (ev.total || 0)
              + '，服务端正按样例修正…'
            this.aiDraftPatterns = []
            this.aiDraftExplain = ''
            return
          }
          if (type === 'error') {
            settled = true
            this.aiPhase = '' // 修正提示不能留在界面上：这次生成已经以失败收场
            this.aiError = ev.message || 'AI 生成失败'
            finish() // 草稿保留：搭配「未采用」标记能看到模型给了什么
            return
          }
          if (type === 'final' && !settled) {
            settled = true
            this.aiPhase = ''
            applyFinal(ev)
            finish()
          }
        },
        onEnd: () => {
          if (!settled) { this.aiPhase = ''; this.aiError = '生成未完成（连接提前结束），请重试'; finish() }
        },
        onError: (err) => {
          if (!settled) { this.aiPhase = ''; this.aiError = (err && err.message) || 'AI 生成失败'; finish() }
        }
      })
    },
    // 模型给的定义并入「自定义 pattern」框：已有行（含注释）原样保留，只追加模型新引入的名字。
    // 不能整体覆盖——用户自己写过的定义被抹掉，下一次「解析」就会报「未定义的 pattern」。
    mergeDefs(fromModel) {
      const names = Object.keys(fromModel)
      if (!names.length) return
      const have = this.definitions
      const add = names.filter(k => !(k in have)).map(k => k + ' ' + fromModel[k])
      if (!add.length) return
      const keep = this.defText.split('\n').map(s => s.trimEnd()).filter(s => s.trim())
      this.defText = keep.concat(add).join('\n')
      this.showDefs = true
    },
    defsToText(defs) {
      return Object.keys(defs).map(k => k + ' ' + defs[k]).join('\n')
    },
    copyFields(l) {
      const obj = {}
      l.fields.forEach(f => { obj[f.name] = f.value })
      const text = JSON.stringify(obj, null, 2)
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text).then(
          () => this.$message.success('字段 JSON 已复制'),
          () => this.$message.warning('复制失败，请手动选择字段值'))
      } else {
        this.$message.warning('当前环境不支持直接复制，请手动选择字段值')
      }
    },
    clearAll() {
      this.sample = ''; this.patternText = ''; this.defText = ''
      this.result = null; this.parseError = ''
      this.aiExplain = ''; this.aiFields = []; this.usage = {}
      this.aiDraftPatterns = []; this.aiDraftExplain = ''; this.aiError = ''
      this.aiPrev = null; this.aiBase = ''
      this.aiPhase = ''; this.aiVerify = null
    }
  },
  beforeUnmount() {
    if (this._aiStream) { this._aiStream.close(); this._aiStream = null } // 生成流随组件走：关掉即取消服务端请求
  }
}
