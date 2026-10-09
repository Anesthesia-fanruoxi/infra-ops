// 设置 · AI 接入：选择厂商 → 填密钥 → 自动拉取模型，其余交给平台。
//
// 这套配置曾经挂在 MySQL 工具里，现在上提为平台配置——任何功能要用模型都读同一份。
// 内置几十家主流厂商（OpenAI / Claude / Gemini / 通义 / Kimi / 智谱 / MiMo …），
// 都提供 OpenAI 兼容接口，地址由平台按厂商派生，用户不必手填；模型名也从厂商
// /models 自动拉出来供选择，省得拼错。
window.SettingsAI = {
  template: `
<div class="page-card">
  <div class="card-header">
    <div class="tool-list-title">
      <span class="tool-list-sub">选择厂商并填入密钥即可 · 平台内所有 AI 能力共用这套配置</span>
    </div>
    <div class="header-extra">
      <el-tag v-if="form.has_key" type="success" size="small">密钥已配置</el-tag>
      <el-tag v-else type="info" size="small">未配置密钥</el-tag>
    </div>
  </div>

  <el-form :model="form" label-position="top" :disabled="saving || testing">
    <div class="set-grid">
      <el-form-item label="启用 AI 能力">
        <div class="set-inline">
          <el-switch v-model="form.enabled" />
          <span class="set-tip">关闭后需要 AI 的功能会明确提示未启用</span>
        </div>
      </el-form-item>
      <el-form-item label="调用超时（秒）">
        <el-input-number v-model="form.timeout" :min="10" :max="600" :step="10" style="width:100%" />
      </el-form-item>
    </div>

    <div class="set-grid">
      <el-form-item label="厂商 / 接入点">
        <el-select v-model="form.provider" filterable style="width:100%" @change="onProviderChange">
          <el-option v-for="p in form.providers" :key="p.id" :label="p.name" :value="p.id" />
        </el-select>
        <div class="set-tip">选择后接口地址由平台自动确定，无需填写</div>
      </el-form-item>
      <el-form-item label="接口地址（自动）">
        <div class="set-inline">
          <el-tag type="info" size="small">自动</el-tag>
          <code class="set-code">{{ form.base_url || '—' }}</code>
        </div>
      </el-form-item>
    </div>

    <el-form-item label="API 密钥">
      <el-input v-model="form.api_key" type="password" show-password
        :placeholder="keyPlaceholder" />
      <div class="set-tip">密钥仅存于本机，不会下发到前端展示；Token Plan 与按量付费的密钥互不通用</div>
    </el-form-item>

    <el-form-item label="模型">
      <el-select v-model="form.model" filterable allow-create default-first-option clearable
        :loading="loadingModels" placeholder="选择或输入模型名" style="width:100%"
        @visible-change="onModelOpen">
        <el-option v-for="m in models" :key="m" :label="m" :value="m" />
      </el-select>
      <div class="set-tip set-tip--row">
        <span v-if="loadingModels">正在获取可用模型…</span>
        <span v-else-if="modelsError" class="set-warn">{{ modelsError }}</span>
        <span v-else-if="models.length">已从厂商获取 {{ models.length }} 个模型，可直接选择</span>
        <span v-else>点击展开时自动获取可用模型，也可直接输入模型名</span>
        <el-button link type="primary" size="small" :loading="loadingModels"
          :disabled="saving || testing" @click="fetchModels">刷新模型</el-button>
      </div>
    </el-form-item>
  </el-form>

  <div class="set-actions">
    <el-button v-if="form.has_key" text type="danger" :disabled="saving || testing" @click="clearKey">清除密钥</el-button>
    <span class="set-spacer"></span>
    <el-button :loading="testing" :disabled="saving" @click="test">
      <el-icon style="margin-right:4px"><Connection /></el-icon>测试连接
    </el-button>
    <el-button type="primary" :loading="saving" :disabled="testing" @click="save">保存</el-button>
  </div>

  <el-alert v-if="result" class="set-alert" :closable="false" show-icon
    :type="result.ok ? 'success' : 'error'"
    :title="result.ok ? ('连接正常 · 往返 ' + fmtMs(result.latency_ms)) : '连接失败'">
    <div v-if="result.ok" class="set-test-body">
      <span>模型回显 <code>{{ result.model || form.model }}</code></span>
      <span>回复 <code>{{ result.reply || '(空)' }}</code></span>
      <span v-if="result.total_tokens">
        用量 <code>{{ fmtNum(result.total_tokens) }}</code> tokens
        <em v-if="result.token_source === 'estimated'">（估算）</em>
      </span>
    </div>
    <div v-else class="set-test-body">{{ result.error }}</div>
  </el-alert>
</div>`,
  data() {
    return {
      form: { enabled: false, provider: '', providers: [], base_url: '', model: '', api_key: '', has_key: false, timeout: 90 },
      saving: false, testing: false, result: null,
      models: [], loadingModels: false, modelsError: ''
    }
  },
  created() { this.load() },
  computed: {
    curProvider() {
      return (this.form.providers || []).find(p => p.id === this.form.provider) || null
    },
    // 各接入点密钥前缀不同（按量付费 sk- / Token Plan tp-），用当前接入点的提示做占位
    keyPlaceholder() {
      if (this.form.has_key) return '已配置，留空则不修改'
      const hint = this.curProvider && this.curProvider.key_hint
      return hint ? ('如 ' + hint) : '如 sk-xxxx'
    }
  },
  methods: {
    async load() {
      try {
        const r = await api.get('/settings/ai')
        if (r.code === 0) {
          this.form = { ...r.data, api_key: '' }
          // 从未配置过（既无厂商也无地址）时默认第一个厂商，省去一步选择。
          // 这里不拉模型：只有用户展开模型下拉时才调用厂商接口（见 onModelOpen）。
          if (!this.form.provider && !this.form.base_url && (this.form.providers || []).length) {
            this.onProviderChange(this.form.providers[0].id)
          }
        }
      } catch (e) { /* 拦截器已提示 */ }
    },
    // 厂商切换：地址跟随厂商预设，模型清单与已选模型一起清空（多半不适用于新厂商）。
    // 不主动拉取，等用户展开模型下拉时再取。
    onProviderChange(id) {
      const p = (this.form.providers || []).find(x => x.id === id)
      this.form.base_url = p ? p.base_url : ''
      this.form.model = ''
      this.models = []
      this.modelsError = ''
      this.result = null
    },
    // 模型下拉展开时才去厂商拉清单：平时不发这个请求，避免无谓调用，也免得缺密钥时白刷一次 401。
    onModelOpen(visible) {
      if (!visible || this.loadingModels || this.models.length) return
      if (!this.form.has_key && !(this.form.api_key || '').trim()) {
        this.modelsError = '请先填写 API 密钥后再获取模型'
        return
      }
      this.fetchModels()
    },
    async fetchModels() {
      if (!this.form.provider) return
      this.loadingModels = true
      this.modelsError = ''
      try {
        const r = await api.post('/settings/ai/models', {
          provider: this.form.provider, base_url: this.form.base_url,
          api_key: this.form.api_key, timeout: this.form.timeout
        }, { timeout: (Number(this.form.timeout) || 90) * 1000 + 5000 })
        if (r.code === 0 && r.data) {
          if (r.data.ok) {
            this.models = r.data.models || []
            // 尚未选模型时给个默认值，用户可再改
            if (!this.form.model && this.models.length) this.form.model = this.models[0]
          } else {
            this.modelsError = r.data.error || '获取模型失败'
          }
        }
      } catch (e) { /* 拦截器已提示 */ } finally { this.loadingModels = false }
    },
    async save() {
      this.saving = true
      try {
        const r = await api.put('/settings/ai', { ...this.form })
        if (r.code === 0) {
          this.form = { ...r.data, api_key: '' }
          this.result = null
          // 已挂载的工具页（如 MySQL 的 AI 面板）缓存着旧配置，广播一次让它们重新初始化
          window.dispatchEvent(new CustomEvent('ai-config-changed'))
          ElMessage.success('AI 配置已保存')
        }
      } catch (e) { /* 拦截器已提示 */ } finally { this.saving = false }
    },
    // 用表单当前值测试：只改了一个字段也能验，不必先保存
    async test() {
      this.testing = true
      this.result = null
      try {
        // 全局 axios 默认 15s，模型冷启动/长上下文常超过它，必须按配置的超时给足
        const r = await api.post('/settings/ai/test', {
          provider: this.form.provider, base_url: this.form.base_url, model: this.form.model,
          api_key: this.form.api_key, timeout: this.form.timeout
        }, { timeout: (Number(this.form.timeout) || 90) * 1000 + 5000 })
        if (r.code === 0) this.result = r.data
      } catch (e) { /* 拦截器已提示 */ } finally { this.testing = false }
    },
    clearKey() {
      this.$confirm('确认清除已保存的 API 密钥？清除后需要 AI 的功能将不可用。', '提示', { type: 'warning' })
        .then(async () => {
          try {
            const r = await api.put('/settings/ai', { ...this.form, api_key: '', clear_key: true })
            if (r.code === 0) {
              this.form = { ...r.data, api_key: '' }
              this.models = []
              window.dispatchEvent(new CustomEvent('ai-config-changed'))
              ElMessage.success('密钥已清除')
            }
          } catch (e) { /* 拦截器已提示 */ }
        })
        .catch(() => {})
    },
    fmtNum(n) { return Number(n || 0).toLocaleString('zh-CN') },
    fmtMs(ms) {
      const v = Number(ms || 0)
      return v < 1000 ? v + ' ms' : (v / 1000).toFixed(1) + ' s'
    }
  }
}