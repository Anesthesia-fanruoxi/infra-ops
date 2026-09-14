// ES 控制台 · KQL 输入条 + 时间选择器（F4）。
// 强制约束：时间展示/输入/发送三处统一 'YYYY-MM-DD HH:mm:ss' 字符串（UTC+8），前端不做任何换算；
// 快捷预设只做「按北京时间算好填入选择器」，不改变传递格式。不提供任何 DSL 查看入口。
window.EsKqlBar = {
  props: ['connId', 'viewId', 'start', 'end', 'kql'],
  emits: ['update:kql', 'update:start', 'update:end', 'run'],
  template: `
<div class="es-kql-bar">
  <div class="es-kql-row">
    <div class="es-kql-input-wrap">
      <el-input
        v-model="kqlInner" class="es-kql-input" :class="{'es-kql-invalid': err}"
        placeholder='KQL，例如 env:prod and (level:error or status:>=500)，留空查全部'
        spellcheck="false" clearable @keyup.enter="run" />
      <div v-if="err" class="es-kql-error">
        <code class="es-kql-frag">{{frag}}</code> {{err.message}}
      </div>
    </div>
    <el-button type="primary" @click="run">查询</el-button>
  </div>
  <div class="es-time-row">
    <span class="es-time-label">时间</span>
    <el-date-picker v-model="startInner" type="datetime" format="YYYY-MM-DD HH:mm:ss"
      value-format="YYYY-MM-DD HH:mm:ss" placeholder="起始（含）" style="width:180px" />
    <span class="es-time-sep">→</span>
    <el-date-picker v-model="endInner" type="datetime" format="YYYY-MM-DD HH:mm:ss"
      value-format="YYYY-MM-DD HH:mm:ss" placeholder="结束（不含）" style="width:180px" />
    <span class="es-time-notinc" title="区间为下半开 [起始, 结束)：结束时刻本身不计入">结束「不含」</span>
    <el-button-group class="es-presets">
      <el-button size="small" @click="preset(15)">近 15 分</el-button>
      <el-button size="small" @click="preset(60)">近 1 时</el-button>
      <el-button size="small" @click="preset(1440)">近 24 时</el-button>
      <el-button size="small" @click="preset(10080)">近 7 天</el-button>
    </el-button-group>
  </div>
</div>`,
  data() {
    return { err: null, validTimer: null }
  },
  computed: {
    kqlInner: {
      get() { return this.kql || '' },
      set(v) { this.$emit('update:kql', v); this.scheduleValidate(v) }
    },
    startInner: {
      get() { return this.start || '' },
      set(v) { this.$emit('update:start', v || '') }
    },
    endInner: {
      get() { return this.end || '' },
      set(v) { this.$emit('update:end', v || '') }
    },
    frag() {
      if (!this.err || this.err.len == null || this.err.len <= 0) return ''
      return (this.kqlInner || '').slice(this.err.pos, this.err.pos + this.err.len)
    }
  },
  methods: {
    run() { this.$emit('run') },
    scheduleValidate(v) {
      clearTimeout(this.validTimer)
      this.validTimer = setTimeout(() => this.validate(v), 300)
    },
    async validate(v) {
      if (!v || !v.trim()) { this.err = null; return }
      try {
        const r = await api.post('/es/' + this.connId + '/kql/validate', { view_id: this.viewId, kql: v })
        if (r.code === 0 && r.data && r.data.ok === false) this.err = r.data.error || {}
        else this.err = null
      } catch (e) { /* 校验失败静默（恒 200 的约定之外的网络错误） */ }
    },
    // 快捷预设：按北京时间把算好的字符串填入选择器（仅 UI 预填，发送格式不变）
    preset(minutes) {
      const fmt = d => {
        const p = n => String(n).padStart(2, '0')
        return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate()) + ' ' + p(d.getHours()) + ':' + p(d.getMinutes()) + ':' + p(d.getSeconds())
      }
      const now = new Date()
      const start = new Date(now.getTime() - minutes * 60000)
      this.$emit('update:start', fmt(start))
      this.$emit('update:end', fmt(now))
      this.$emit('run')
    }
  }
}
