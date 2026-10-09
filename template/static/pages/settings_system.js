// 设置 · 系统信息：版本、运行时长、数据目录与库大小（只读）。
//
// 桌面形态下工作目录是用户数据目录，数据库、部署资产都在那儿——界面上看不到、配置里也没有，
// 出问题时却最先被问到，所以集中放出来，并给一个「复制路径」省去手抄。
window.SettingsSystem = {
  template: `
<div class="page-card">
  <div class="card-header">
    <div class="tool-list-title">
      <span class="tool-list-sub">只读 · 排查问题与备份时用</span>
    </div>
    <div class="header-extra">
      <el-button text :loading="loading" @click="load"><el-icon style="margin-right:4px"><Refresh /></el-icon>刷新</el-button>
      <el-button text @click="copyPaths"><el-icon style="margin-right:4px"><CopyDocument /></el-icon>复制路径</el-button>
    </div>
  </div>

  <div class="set-kv">
    <div class="set-kv-item"><span>版本</span><code>{{ info.version || '—' }}</code></div>
    <div class="set-kv-item"><span>Go 版本</span><code>{{ info.go_version || '—' }}</code></div>
    <div class="set-kv-item"><span>启动时间</span><code>{{ info.started_at || '—' }}</code></div>
    <div class="set-kv-item"><span>已运行</span><code>{{ fmtUptime(info.uptime_sec) }}</code></div>
    <div class="set-kv-item set-kv-wide"><span>工作目录</span><code>{{ info.work_dir || '—' }}</code></div>
    <div class="set-kv-item set-kv-wide">
      <span>数据库</span>
      <code>{{ info.db_path || '—' }}</code>
      <em v-if="info.db_size_bytes">（{{ fmtSize(info.db_size_bytes) }}）</em>
    </div>
  </div>
</div>`,
  data() {
    return { info: {}, loading: false }
  },
  created() { this.load() },
  methods: {
    async load() {
      this.loading = true
      try {
        const r = await api.get('/settings/system')
        if (r.code === 0) this.info = r.data || {}
      } catch (e) { /* 拦截器已提示 */ } finally { this.loading = false }
    },
    copyPaths() {
      const text = '工作目录：' + (this.info.work_dir || '') + '\n数据库：' + (this.info.db_path || '')
      const done = () => ElMessage.success('路径已复制')
      if (navigator.clipboard?.writeText) {
        navigator.clipboard.writeText(text).then(done).catch(() => this.fallbackCopy(text, done))
      } else {
        this.fallbackCopy(text, done)
      }
    },
    // 桌面 WebView 下 clipboard API 可能被策略拦，退回 execCommand
    fallbackCopy(text, done) {
      const ta = document.createElement('textarea')
      ta.value = text
      ta.style.position = 'fixed'
      ta.style.opacity = '0'
      document.body.appendChild(ta)
      ta.select()
      try { document.execCommand('copy'); done() } catch (e) { ElMessage.warning('复制失败，请手动选择文本') }
      document.body.removeChild(ta)
    },
    fmtSize(b) {
      const v = Number(b || 0)
      if (v < 1024) return v + ' B'
      if (v < 1024 * 1024) return (v / 1024).toFixed(1) + ' KB'
      return (v / 1024 / 1024).toFixed(1) + ' MB'
    },
    // 秒 → 人话（刚起来时精确到分钟，久了只说天）
    fmtUptime(sec) {
      const s = Number(sec || 0)
      if (s < 60) return s + ' 秒'
      if (s < 3600) return Math.floor(s / 60) + ' 分钟'
      if (s < 86400) return (s / 3600).toFixed(1) + ' 小时'
      return (s / 86400).toFixed(1) + ' 天'
    }
  }
}
