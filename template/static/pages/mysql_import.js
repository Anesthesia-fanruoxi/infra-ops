// MySQL SQL 导入：把一个 .sql 文件流式灌进目标库。
//
// 门禁比手写 SQL 更严：一次导入可能改几十万行，所以服务端要求必须处于可写模式，
// 并先回一份待确认信息（文件、大小、目标库），由这里摆出来让人再点一次。
// 结构变更语句默认不放行——几万条的 dump 里混进一条 DROP，肉眼是看不出来的。
window.MySQLImport = {
  props: {
    connId: { type: Number, required: true },
    schema: { type: String, default: '' }
  },
  emits: ['close'],
  template: `
<el-dialog v-model="open" title="导入 SQL 文件" width="680px" append-to-body class="mysql-transfer-dialog"
  :close-on-click-modal="!busy" :close-on-press-escape="!busy" :show-close="!busy" @closed="$emit('close')">
  <div class="mysql-transfer">
    <div class="mysql-transfer-note">
      边读边切语句、按批提交，内存里只留当前一条。文件需为 <b>UTF-8</b> 编码。
    </div>

    <div class="mysql-transfer-form" :class="{'is-locked': busy}">
      <div class="mysql-transfer-row">
        <span class="mysql-transfer-label">SQL 文件</span>
        <span class="mysql-transfer-path mono" :title="source">{{ source || '未选择' }}</span>
        <el-button size="small" :disabled="busy" @click="pickSource">选择文件</el-button>
      </div>

      <div class="mysql-transfer-row">
        <span class="mysql-transfer-label">目标库</span>
        <el-input v-model="targetSchema" size="small" :placeholder="schema || '连接的默认库'" style="width:220px" />
        <span class="mysql-transfer-hint">文件里没有库名前缀的语句会落到这里</span>
      </div>

      <div class="mysql-transfer-row">
        <span class="mysql-transfer-label">执行方式</span>
        <el-select v-model="batchSize" size="small" style="width:140px">
          <el-option v-for="n in batchOptions" :key="n" :value="n" :label="n + ' 条 / 批'" />
        </el-select>
        <el-select v-model="onError" size="small" class="mysql-transfer-select" style="width:190px">
          <el-option value="stop" label="遇错整批回滚并停止" />
          <el-option value="skip" label="遇错跳过并继续（逐条执行）" />
        </el-select>
      </div>

      <div class="mysql-transfer-row">
        <span class="mysql-transfer-label">结构变更</span>
        <el-checkbox v-model="allowSchema" :disabled="busy">允许执行 CREATE / ALTER / DROP 等语句</el-checkbox>
        <span class="mysql-transfer-hint is-warn">默认关闭</span>
      </div>
    </div>

    <div class="mysql-transfer-progress" v-if="busy">
      <div class="mysql-transfer-bar"><span></span></div>
      <div class="mysql-transfer-progress-meta">
        <span>已执行 <b>{{ tu.fmtNum(progress.statements) }}</b> 条</span>
        <span>影响 <b>{{ tu.fmtNum(progress.rows) }}</b> 行</span>
        <span>已读 <b>{{ tu.fmtSize(progress.bytes) }}</b></span>
        <span>用时 {{ tu.fmtMs(progress.elapsed_ms) }}</span>
      </div>
    </div>

    <div class="mysql-transfer-result" v-if="result">
      <span class="is-ok">导入完成</span>
      执行 <b>{{ tu.fmtNum(result.statements) }}</b> 条 · 影响 <b>{{ tu.fmtNum(result.affected) }}</b> 行
      · 读取 {{ tu.fmtSize(result.bytes) }} · 用时 {{ tu.fmtMs(result.elapsed_ms) }}
      <span v-if="result.skipped" class="is-bad"> · 跳过 {{ tu.fmtNum(result.skipped) }} 条</span>
    </div>

    <div class="mysql-transfer-errors" v-if="result && result.errors && result.errors.length">
      <div class="mysql-transfer-errors-title">跳过的语句（最多显示 {{ result.errors.length }} 条）：</div>
      <pre v-for="(item, i) in result.errors" :key="i">{{ item }}</pre>
    </div>

    <div class="mysql-transfer-error" v-if="error"><b>导入失败</b><pre>{{ error }}</pre></div>
  </div>

  <template #footer>
    <el-button :disabled="busy" @click="onClose">关闭</el-button>
    <el-button type="danger" :loading="busy" :disabled="!source" @click="start">
      {{ busy ? '导入中…' : '开始导入' }}
    </el-button>
  </template>
</el-dialog>`,
  data() {
    return {
      open: true,
      source: '',
      targetSchema: '',
      batchSize: 500,
      batchOptions: [100, 500, 1000, 5000],
      onError: 'stop',
      allowSchema: false,
      busy: false,
      progress: { statements: 0, rows: 0, bytes: 0, elapsed_ms: 0 },
      result: null,
      error: '',
      timer: null
    }
  },
  computed: {
    tu() { return window.MySQLTransferUtil }
  },
  mounted() { this.targetSchema = this.schema || '' },
  beforeUnmount() { this.stopPolling() },
  methods: {
    payload(confirmed, key) {
      return {
        source: this.source,
        schema: this.targetSchema.trim(),
        batch_size: this.batchSize,
        on_error: this.onError,
        allow_schema: this.allowSchema,
        confirmed: confirmed,
        progress_key: key
      }
    },
    onClose() {
      if (this.busy) { ElMessage.warning('导入进行中，完成后再关闭'); return }
      this.open = false
    },
    async pickSource() {
      if (!window.__INFRA_DESKTOP__) { ElMessage.warning('从本地文件导入需要在桌面端使用'); return }
      try {
        const picked = await window.wails.Call.ByName('infra-ops/desktop.FileService.PickSQLFile', '选择要导入的 SQL 文件')
        if (picked) this.source = picked
      } catch (e) { ElMessage.error((e && e.message) || '选择文件失败') }
    },
    // confirm 把服务端回的待确认信息摆出来：文件、大小、目标库、执行方式。
    // 这一步不是走形式——导入是唯一能一次性改几十万行的入口。
    async confirm(d) {
      const rows = [
        '文件：<b>' + this.tu.escapeHTML(d.source || this.source) + '</b>（' + this.tu.fmtSize(d.size || 0) + '）',
        '目标库：<b>' + this.tu.escapeHTML(this.targetSchema.trim() || '(连接的默认库)') + '</b>',
        '每批 ' + this.batchSize + ' 条，' + (this.onError === 'skip' ? '遇错跳过并继续' : '遇错整批回滚并停止')
      ]
      if (this.allowSchema) {
        rows.push('<span class="is-danger">已勾选「允许结构变更语句」，文件里的 CREATE / ALTER / DROP 会真的执行</span>')
      }
      try {
        await this.$confirm(rows.join('<br>'), '确认导入', {
          type: this.allowSchema ? 'error' : 'warning',
          dangerouslyUseHTMLString: true,
          confirmButtonText: '开始导入',
          cancelButtonText: '取消'
        })
        return true
      } catch (e) { return false }
    },
    async start() {
      if (this.busy) return
      if (!this.source) { ElMessage.warning('请先选择要导入的 SQL 文件'); return }
      this.error = ''
      this.result = null

      // 先探一次：服务端会回文件大小与目标库，也可能直接拒绝（例如当前是只读模式）
      let pre
      try {
        pre = await api.post('/mysql/' + this.connId + '/import', this.payload(false, ''))
      } catch (e) {
        this.error = this.tu.errText(e, '导入前检查失败')
        return
      }
      if (!pre || pre.code !== 0) { this.error = (pre && pre.message) || '导入前检查失败'; return }
      if (pre.data && pre.data.need_confirm && !(await this.confirm(pre.data))) return

      this.busy = true
      this.progress = { statements: 0, rows: 0, bytes: 0, elapsed_ms: 0 }
      const key = this.tu.newKey('imp')
      this.startPolling(key)
      try {
        const r = await api.post('/mysql/' + this.connId + '/import', this.payload(true, key), { timeout: 0 })
        if (!r || r.code !== 0) { this.error = (r && r.message) || '导入失败'; return }
        this.result = r.data || {}
      } catch (e) {
        this.error = this.tu.errText(e, '导入失败')
      } finally {
        this.stopPolling()
        this.busy = false
      }
    },
    startPolling(key) {
      this.stopPolling()
      this.timer = setInterval(async () => {
        try {
          const r = await api.get('/mysql/' + this.connId + '/transfer/progress', { params: { key: key } })
          if (r && r.code === 0 && r.data && r.data.found && r.data.status) this.progress = r.data.status
        } catch (e) { /* 进度是旁路信息，取不到不影响导入本身 */ }
      }, 500)
    },
    stopPolling() {
      if (this.timer) { clearInterval(this.timer); this.timer = null }
    }
  }
}
