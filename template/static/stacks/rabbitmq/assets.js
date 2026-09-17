// RabbitMQ 套件 · 第三步资产条：延迟队列插件（.ez）。
// 挂载点：forms.step3Comp（骨架第 3 步共享参数区之后）——开关 delayed_plugin 就在同一屏，
//         打开开关即在本步正下方给出「本地是否已存在」的结论与两条补救路由。
// 用户口径「优先使用本地，本地不存在则远程自行下载」：
//   打开开关只做一件事——检测服务端 data/assets 是否已有该 .ez（版本按镜像标签推导）；
//   已就绪由引擎在部署时直接分发到各主机 plugins 目录；
//   未就绪时给「本地下载」「上传文件」两条路由用户二选一，两者都没有时目标机侧仍有 GitHub 下载兜底。
window.StackFormRabbitmqAssets = {
  props: ['ctx'],
  watch: {
    // 开关 / 镜像变化 → 重查本地资产（插件版本按镜像标签推导）
    'ctx.sharedParams.delayed_plugin'() { this.onAssetInputsChange() },
    'ctx.sharedParams.image'() { this.onAssetInputsChange() }
  },
  mounted() { this.onAssetInputsChange() },
  beforeUnmount() { clearTimeout(this._assetTimer) },
  computed: {
    pluginOn() {
      const v = this.ctx.sharedParams?.delayed_plugin
      return v === 'true' || v === 'yes'
    },
    assets() { return this.ctx.assetCheck || [] },
    // 已勾选但还没有结论：显示「检测中」，避免 debounce 窗口里闪出空态
    checking() { return this.ctx.assetCheckBusy && !this.assets.length }
  },
  methods: {
    assetParams() { return { ...this.ctx.sharedParams } },
    onAssetInputsChange() {
      if (!this.pluginOn) { this.ctx.assetCheck = []; this.ctx.assetCheckBusy = false; return }
      this.ctx.assetCheck = []
      this.ctx.assetCheckBusy = true // 先进入「检测中」，避免 debounce 窗口里闪出空态
      this.checkAssetsSoon()
    },
    checkAssetsSoon() {
      clearTimeout(this._assetTimer)
      this._assetTimer = setTimeout(() => this.checkAssets(), 300)
    },
    // 只检测本地是否已存在该 .ez，不自动下载：下载会写服务端目录、消耗外网带宽，
    // 必须由用户点「本地下载」显式触发（离线内网走「上传文件」）。
    async checkAssets() {
      const ctx = this.ctx
      ctx.assetCheckBusy = true
      try {
        const r = await api.post('/stacks/assets/check', { stack_key: 'rabbitmq', mode: 'cluster', params: this.assetParams() })
        ctx.assetCheck = r.code === 0 ? (r.data.items || []) : []
      } catch (e) { ctx.assetCheck = [] }
      finally { ctx.assetCheckBusy = false }
    },
    pickAsset(a) { this._assetPick = a; this.$refs.assetFile.value = ''; this.$refs.assetFile.click() },
    async onAssetPicked(e) {
      const file = e.target.files && e.target.files[0]
      const it = this._assetPick
      if (!file || !it) return
      if (it.file_name && file.name !== it.file_name) { ElMessage.error('文件名应为 ' + it.file_name); return }
      const fd = new FormData()
      fd.append('asset_key', it.key)
      fd.append('version', it.version)
      fd.append('file_name', it.file_name)
      fd.append('file', file)
      try {
        const r = await api.post('/stacks/assets/upload', fd, { timeout: 120000 })
        if (r.code === 0) { ElMessage.success('已上传到服务端：' + it.file_name); this.checkAssets() }
        else ElMessage.error(r.message || '上传失败')
      } catch (err) { ElMessage.error(err.message || '上传失败') }
    },
    // 「本地下载」：平台把这一个 .ez 下到服务端 data/assets（离线内网请走「上传文件」）
    async fetchAsset(a) {
      const ctx = this.ctx
      ctx.assetCheckBusy = true
      try {
        const r = await api.post('/stacks/assets/fetch', {
          stack_key: 'rabbitmq', mode: 'cluster', params: this.assetParams(),
          asset_key: a.key, version: a.version, file_name: a.file_name
        }, { timeout: 600000 })
        if (r.code === 0) { ElMessage.success('已下载到服务端：' + a.file_name); this.checkAssets() }
        else ElMessage.error(r.message || '本地下载失败')
      } catch (e) { ElMessage.error(e.message || '本地下载失败') }
      finally { ctx.assetCheckBusy = false }
    },
    // 资产来源三态：本地上传 / 服务端代下 / 本地目录已有（检查时补登记）
    sourceText(a) {
      const s = a.asset && a.asset.source
      if (s === 'upload') return '本地上传'
      if (s === 'fetch') return '服务端代下'
      return '本地目录已有'
    },
    fmtSize(n) {
      if (n == null) return ''
      if (n >= 1048576) return (n / 1048576).toFixed(1).replace(/\.0$/, '') + 'MB'
      if (n >= 1024) return (n / 1024).toFixed(1).replace(/\.0$/, '') + 'KB'
      return n + 'B'
    }
  },
  template: `  <div v-if="pluginOn" class="stk-rb-assets">
    <div class="stk-rb-assets-head">延迟队列插件 · 离线资产<span class="faint">本地已存在则部署时自动分发；不存在可选「本地下载」或「上传文件」</span></div>
    <div v-if="checking" class="stk-rb-asset is-idle">
      <span class="stk-rb-asset-dot"></span>
      <div class="stk-rb-asset-body"><div class="stk-rb-asset-title">正在检测本地资产…</div></div>
    </div>
    <div v-else-if="!assets.length" class="stk-rb-asset is-idle">
      <span class="stk-rb-asset-dot"></span>
      <div class="stk-rb-asset-body">
        <div class="stk-rb-asset-title">未能确定插件版本</div>
        <div class="stk-rb-asset-sub">插件版本按镜像标签推导（如 rabbitmq:3.13-management → 3.13.0）；标签为 latest 或无标签时无法确定，部署时由目标机现场下载</div>
      </div>
    </div>
    <template v-else>
      <div v-for="a in assets" :key="a.key + '-' + a.version" class="stk-rb-asset" :class="a.satisfied ? 'is-ok' : 'is-miss'">
        <span class="stk-rb-asset-dot"></span>
        <div class="stk-rb-asset-body">
          <div class="stk-rb-asset-title">{{a.file_name}}</div>
          <div class="stk-rb-asset-sub">{{a.desc}}<span v-if="a.satisfied"> · 本地已存在（{{sourceText(a)}} · {{fmtSize(a.asset && a.asset.size_bytes)}}）· 部署时自动分发，无需现场下载</span><span v-else> · 本地不存在：请选择「本地下载」（平台下载到服务端）或「上传文件」</span></div>
        </div>
        <template v-if="!a.satisfied">
          <el-button size="small" type="primary" plain :loading="ctx.assetCheckBusy" @click="fetchAsset(a)">本地下载</el-button>
          <el-button size="small" :disabled="ctx.assetCheckBusy" @click="pickAsset(a)">上传文件</el-button>
        </template>
      </div>
    </template>
    <input ref="assetFile" type="file" accept=".ez" style="display:none" @change="onAssetPicked" />
  </div>`
}
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  P.register('rabbitmq', { components: { 'stack-form-rabbitmq-assets': window.StackFormRabbitmqAssets } })
})()
