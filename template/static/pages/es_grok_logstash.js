// ES 控制台 · Grok 解析 tab 的「Logstash 配置」区块：把已经调通的 pattern 组装成可直接粘贴的 filter。
//
// 为什么单拆一个组件：配置生成是「解析 → 生成」链路的下游产物，状态（tag / source / 配置文本 /
// 校验回报 / 字段清单）与解析面板互不相干，塞进 es_grok.js 只会让那个文件继续膨胀。
//
// 与后端的接口约定（改之前先读）：
//   - **骨架全部由后端本地拼装**：if 条件、grok 块、缩进、引号转义。模型只回结构化的 gsub 建议，
//     所以拿到的配置一定是语法完整的；enhance 三态 ok / skipped / failed，后两者配置照样可用。
//   - **remove_field 不看模型**：删哪些字段由「删除默认字段」开关 + 该连接的系统自带字段清单决定，
//     清单来自后端按 ES 版本探测（候选池 ∩ ECS 定义 ∩ 集群实际字段）+ 人工补充。
//     本组件只负责展示清单、改补充项、点重新探测，不自己推导字段名。
window.EsGrokLogstash = {
  props: {
    conn: { type: Object, required: true },
    patterns: { type: Array, default: () => [] },
    definitions: { type: Object, default: () => ({}) },
    sample: { type: String, default: '' },
    hint: { type: String, default: '' }
  },
  template: `
<div class="es-grok-ls">
  <div class="es-grok-toggle" @click="open = !open">
    <span>{{ open ? '▾' : '▸' }} Logstash 配置</span>
    <span class="es-grok-hint">按 tag 生成完整 filter（grok + mutate），可直接粘贴进 logstash.conf</span>
  </div>

  <template v-if="open">
    <div class="es-grok-ls-form">
      <el-input v-model="tag" size="small" spellcheck="false" placeholder="匹配 tag，如 nginx（留空则不加 if 条件）" />
      <el-input v-model="source" size="small" spellcheck="false" placeholder="待解析字段（默认 message）" />
      <el-button size="small" type="primary" :loading="loading" @click="generate">生成配置</el-button>
    </div>

    <div class="es-grok-ls-opts">
      <el-switch v-model="dropDefaults" size="small" />
      <span class="es-grok-hint">删除采集器默认字段（agent / host / log 等）。关闭后这些字段保留，但 message 仍会删除——原始整行已经被 grok 拆开了</span>
    </div>

    <!-- 字段清单：自动发现的只能来自后端探测，这里只做「看得见 + 能补」。补的那部分存库，探测不覆盖 -->
    <div class="es-grok-toggle" @click="showFields = !showFields">
      <span>{{ showFields ? '▾' : '▸' }} 默认字段清单</span>
      <span class="es-grok-hint">{{ fieldSummary }}</span>
    </div>
    <template v-if="showFields">
      <div class="es-grok-ls-meta">
        <span class="es-grok-hint">{{ cacheStatusText }}</span>
        <el-button text size="small" :loading="refreshing" @click="refreshFields">重新探测</el-button>
      </div>
      <div class="es-grok-chips">
        <span v-for="f in autoFields" :key="'a-' + f" class="es-grok-chip">{{ f }}</span>
        <span v-for="f in extraFields" :key="'e-' + f" class="es-grok-chip is-extra">{{ f }}</span>
        <span class="es-grok-chip is-fixed">message</span>
        <span class="es-grok-hint" v-if="!autoFields.length && !extraFields.length">尚未探测到字段，点「重新探测」</span>
      </div>
      <div class="es-grok-ls-extra">
        <el-input v-model="extraText" size="small" spellcheck="false"
          placeholder="人工补充：逗号或空格分隔，如 offset、logYear、logMillis（这几个不在 ECS 定义里）" />
        <el-button size="small" :loading="savingExtra" @click="saveExtra">保存</el-button>
      </div>
    </template>

    <!-- 等待期间必须有话说：一次模型调用可能十几秒，只让按钮转圈会被当成卡死 -->
    <div class="es-grok-hint" v-if="loading && phase">{{ phase }}</div>
    <div class="es-grok-hint" v-if="!patterns.length">先在上面填好 grok pattern（或点「AI 生成」），再生成配置。</div>

    <div class="es-grok-error" v-if="error">
      <div class="es-grok-error-title">生成失败</div>
      <pre>{{ error }}</pre>
    </div>

    <template v-if="config">
      <div class="es-grok-ai-status" :class="statusClass">
        <span class="es-grok-dot"></span>{{ statusText }}
      </div>
      <div class="es-grok-ls-head">
        <span class="es-grok-hint">解析出 {{ fields.length }} 个字段 · 删除 {{ removed.length }} 个采集器字段 · {{ patterns.length }} 条 pattern</span>
        <el-button text size="small" @click="copy">复制配置</el-button>
      </div>
      <pre class="es-grok-ls-code">{{ config }}</pre>
      <!-- 被本地校验拦下的模型建议：不静默吞掉，用户才知道模型提过什么、为什么没进配置 -->
      <div class="es-grok-ls-drop" v-if="droppedGsub.length">
        <b>已拦下模型建议：</b>
        <span>gsub 的 {{ droppedGsub.join('、') }} 不在 pattern 字段里</span>
      </div>
      <div class="es-grok-ls-drop" v-if="warnings.length">
        <b>注意：</b>
        <span v-for="w in warnings" :key="w">{{ w }}</span>
      </div>
    </template>
    <div class="es-grok-blank" v-else-if="!error && patterns.length">
      <p class="es-grok-hint">点「生成配置」按 tag 生成完整 filter。已接入 AI 时会顺带补上 gsub（占位符替换）；要删哪些采集器字段按上面的开关与清单来。</p>
    </div>
  </template>
</div>`,
  data() {
    return {
      open: true, tag: '', source: '',
      config: '', error: '', loading: false, phase: '',
      fields: [], gsub: [], removed: [], notes: '',
      enhance: '', enhanceError: '', warnings: [],
      droppedGsub: [], droppedRemove: [],
      // 删采集器默认字段的开关 + 它依赖的字段清单（后端按 ES 版本探测并缓存）
      dropDefaults: true, showFields: false,
      esVersion: '', cacheStatus: '', cacheError: '', syncedAt: '',
      autoFields: [], extraFields: [], extraText: '',
      refreshing: false, savingExtra: false
    }
  },
  computed: {
    statusClass() {
      if (this.enhance === 'ok') return 'is-ok'
      if (this.enhance === 'failed') return 'is-warn'
      return 'is-idle' // AI 没配：中性提示，不是告警
    },
    // 一句话说明这份配置是怎么来的：有增强就说清补了什么，没增强就说明为什么没有——
    // 用户据此判断该不该再手动加 mutate。
    statusText() {
      if (this.enhance === 'ok') {
        const parts = []
        if (this.gsub.length) parts.push('替换 ' + this.gsub.length + ' 个占位符')
        const tail = parts.length ? parts.join(' · ') : '没有需要额外处理的项'
        return 'AI 已按样例补充 gsub：' + tail + (this.notes ? '（' + this.notes + '）' : '')
      }
      if (this.enhance === 'failed') {
        return 'AI 增强未生效：' + (this.enhanceError || '调用失败') + '；当前为纯解析配置，可直接使用'
      }
      return 'AI 未启用，当前为纯解析配置（grok 部分完整可用）'
    },
    // 折叠状态也要能看出「有多少个、是哪来的」
    fieldSummary() {
      const n = this.autoFields.length + this.extraFields.length
      if (this.cacheStatus === 'failed') return '探测失败'
      if (!this.esVersion) return '尚未探测'
      const tail = this.cacheStatus === 'no_ecs' ? '（该集群无 ECS 定义，已退化）' : ''
      return 'ES ' + this.esVersion + ' · ' + n + ' 个' + tail
    },
    cacheStatusText() {
      switch (this.cacheStatus) {
        case 'ok': return '按 ECS 定义 ∩ 集群里实际存在的字段得出' + (this.syncedAt ? ' · 探测于 ' + this.syncedAt : '')
        case 'no_ecs': return '该集群没有 ECS 组件模板，已退化为内置候选清单' + (this.syncedAt ? ' · 探测于 ' + this.syncedAt : '')
        case 'failed': return '探测失败：' + (this.cacheError || '原因未知')
        default: return '尚未探测（后台启动后会跑一轮，也可以点「重新探测」）'
      }
    }
  },
  methods: {
    async loadFields() {
      try {
        const r = await api.get('/es/' + this.conn.id + '/default-fields')
        if (r.code === 0) this.applyFields(r.data || {})
      } catch (e) { /* 拦截器已提示 */ }
    },
    applyFields(d) {
      this.esVersion = d.es_version || ''
      this.cacheStatus = d.status || ''
      this.cacheError = d.error || ''
      this.syncedAt = d.synced_at || ''
      this.autoFields = d.fields || []
      this.extraFields = d.extra || []
      this.extraText = (d.extra || []).join(', ')
    },
    // 重新探测要打三个只读请求、最长 20s，必须关掉 axios 的 15 秒默认超时
    //（与导出 / 导入 / 备份那些长耗时接口同一套做法）
    async refreshFields() {
      if (this.refreshing) return
      this.refreshing = true
      try {
        const r = await api.post('/es/' + this.conn.id + '/default-fields/refresh', {}, { timeout: 0 })
        if (r.code === 0) {
          this.applyFields(r.data || {})
          this.$message.success(this.cacheStatus === 'failed' ? '探测失败，原因见上方说明' : '字段清单已更新')
        } else {
          this.$message.warning(r.message || '探测失败')
        }
      } catch (e) { /* 拦截器已提示 */ } finally { this.refreshing = false }
    },
    async saveExtra() {
      if (this.savingExtra) return
      this.savingExtra = true
      try {
        const extra = this.extraText.split(/[,\s]+/).map(s => s.trim()).filter(Boolean)
        const r = await api.put('/es/' + this.conn.id + '/default-fields', { extra: extra })
        if (r.code === 0) {
          this.applyFields(r.data || {})
          this.$message.success('补充字段已保存')
        } else {
          this.$message.warning(r.message || '保存失败')
        }
      } catch (e) { /* 拦截器已提示 */ } finally { this.savingExtra = false }
    },
    // 走流式通道，不能普通 POST：一次模型调用几秒到几十秒，远超 axios 的 15 秒默认超时 ——
    // 同步返回的表现是「每次点生成都报请求超时 · POST /es/:id/grok/logstash」（实测踩到），
    // 其余 AI 生成面板也全是流式。phase 帧用来在等待期间说明在干什么。
    async generate() {
      if (this.loading) return
      if (!this.patterns.length) { this.$message.warning('请先填写 grok pattern'); return }
      const sse = window.__INFRA_SSE__
      if (!sse) { this.$message.error('流式通道不可用：请通过桌面应用运行后重试'); return }
      this.loading = true
      this.error = ''
      this.phase = ''
      let settled = false
      const finish = () => { this.loading = false; this.phase = ''; this._stream = null }
      this._stream = sse.post('/api/es/' + this.conn.id + '/grok/logstash', {
        patterns: this.patterns,
        definitions: this.definitions,
        tag: this.tag.trim(),
        source: this.source.trim(),
        sample: this.sample,
        hint: this.hint,
        drop_defaults: this.dropDefaults
      }, {
        onEvent: (type, data) => {
          let ev = {}
          try { ev = JSON.parse(data) } catch (e) { return }
          if (type === 'phase') { this.phase = ev.message || '正在生成配置…'; return }
          if (type === 'error') {
            settled = true
            // 编译类错误留在面板里（用户要照着改 pattern），不只弹一句提示
            this.error = ev.message || '生成失败'
            this.config = ''
            finish()
            return
          }
          if (type === 'final' && !settled) {
            settled = true
            const d = ev || {}
            this.config = d.config || ''
            this.fields = d.fields || []
            this.gsub = d.gsub || []
            this.removed = d.remove || []
            this.notes = d.notes || ''
            this.enhance = d.enhance || ''
            this.enhanceError = d.enhance_error || ''
            this.warnings = d.warnings || []
            this.droppedGsub = d.dropped_gsub || []
            this.droppedRemove = d.dropped_remove || []
            finish()
          }
        },
        onEnd: () => {
          if (!settled) { this.error = '生成未完成（连接提前结束），请重试'; this.config = ''; finish() }
        },
        onError: (err) => {
          if (!settled) { this.error = (err && err.message) || '生成失败'; this.config = ''; finish() }
        }
      })
    },
    copy() {
      if (!this.config) return
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(this.config).then(
          () => this.$message.success('配置已复制'),
          () => this.$message.warning('复制失败，请手动选择文本'))
      } else {
        this.$message.warning('当前环境不支持直接复制，请手动选择文本')
      }
    }
  },
  mounted() {
    // 进面板就把清单读出来（折叠着也能看到摘要），不在生成时才拉 —— 那会让生成多等一个往返
    this.loadFields()
  },
  beforeUnmount() {
    if (this._stream) { this._stream.close(); this._stream = null } // 生成流随组件走：关掉即取消服务端请求
  }
}
