// MySQL 查询导出：把编辑器里的查询结果写成文件（INSERT 语句 / Excel / CSV）。
//
// 两种入口（同一组件，靠 table prop 区分）：
// 1. 查询导出 —— 工作台「导出」按钮，按编辑器里的 SQL 重新查询并全量导出；
// 2. 整表转储 —— 表详情「转储」按钮，固定 SELECT * FROM 库.表（类似 Navicat 的转储表数据）。
//
// 不走 HTTP 响应体，而是由服务端边查边写本地文件——桌面端的资产服务器会把整个
// 响应缓冲在内存里（几百万行足以把窗口拖垮）。所以先弹原生保存对话框拿到路径，
// 再把路径交给服务端；请求期间用进度接口轮询，让人看得见进展。
window.MySQLExport = {
  props: {
    connId: { type: Number, required: true },
    sql: { type: String, default: '' },
    schema: { type: String, default: '' },
    table: { type: String, default: '' } // 非空 = 整表转储模式，导出对象固定为这张表
  },
  emits: ['close'],
  template: `
<el-dialog v-model="open" :title="isDump ? '整表转储' : '导出查询结果'" width="660px" append-to-body class="mysql-transfer-dialog"
  :close-on-click-modal="!busy" :close-on-press-escape="!busy" :show-close="!busy" @closed="$emit('close')">
  <div class="mysql-transfer">
    <div class="mysql-transfer-note" v-if="isDump">
      把 <b class="mono">{{ schema }}.{{ table }}</b> 整表全量转储到本地文件（等同 SELECT *，不受预览行数限制）。
      服务端边查边写文件，内存里只留当前一行。
    </div>
    <div class="mysql-transfer-note" v-else>
      按编辑器里的 SQL <b>重新查询并全量导出</b>（不受预览行数限制）。服务端边查边写文件，内存里只留当前一行。
    </div>

    <div class="mysql-transfer-form" :class="{'is-locked': busy}">
      <div class="mysql-transfer-row">
        <span class="mysql-transfer-label">导出格式</span>
        <el-radio-group v-model="format" size="small">
          <el-radio-button value="sql">INSERT 语句 .sql</el-radio-button>
          <el-radio-button value="xlsx">Excel .xlsx</el-radio-button>
          <el-radio-button value="csv">CSV .csv</el-radio-button>
        </el-radio-group>
      </div>

      <div class="mysql-transfer-row" v-if="isSQL">
        <span class="mysql-transfer-label">目标表名</span>
        <el-input v-model="insertTable" size="small" placeholder="如 db.users" style="width:220px" />
        <span class="mysql-transfer-hint">{{ isDump ? '默认转储回原表，可改' : (insertTable ? '从 SQL 推断，可改' : '未能从 SQL 推断，请手填') }}</span>
      </div>

      <div class="mysql-transfer-row" v-if="isXLSX">
        <span class="mysql-transfer-label">工作表名</span>
        <el-input v-model="sheet" size="small" :placeholder="plainTable || '结果'" style="width:220px" />
      </div>

      <div class="mysql-transfer-row" v-if="isSQL">
        <span class="mysql-transfer-label">每批行数</span>
        <el-select v-model="batchRows" size="small" style="width:160px">
          <el-option v-for="n in batchOptions" :key="n" :value="n" :label="n + ' 行 / 条 INSERT'" />
        </el-select>
        <span class="mysql-transfer-hint">行数或 1MB 谁先到就切一条</span>
      </div>

      <div class="mysql-transfer-row">
        <span class="mysql-transfer-label">保存位置</span>
        <span class="mysql-transfer-path mono" :title="target">{{ target || '未选择' }}</span>
        <el-button size="small" :disabled="busy" @click="pickTarget">选择位置</el-button>
      </div>
    </div>

    <div class="mysql-transfer-preview mono" :title="effSQL">{{ tu.oneLine(effSQL, 220) }}</div>

    <div class="mysql-transfer-progress" v-if="busy">
      <div class="mysql-transfer-bar"><span></span></div>
      <div class="mysql-transfer-progress-meta">
        <span>已导出 <b>{{ tu.fmtNum(progress.rows) }}</b> 行</span>
        <span>已写入 <b>{{ tu.fmtSize(progress.bytes) }}</b></span>
        <span>用时 {{ tu.fmtMs(progress.elapsed_ms) }}</span>
      </div>
    </div>

    <div class="mysql-transfer-result" v-if="result">
      <span class="is-ok">导出完成</span>
      共 <b>{{ tu.fmtNum(result.rows) }}</b> 行 · {{ tu.fmtSize(result.bytes) }} · 用时 {{ tu.fmtMs(result.elapsed_ms) }}
      <span v-if="result.batches"> · 切成 {{ tu.fmtNum(result.batches) }} 条语句</span>
      <div class="mysql-transfer-path mono">{{ result.target }}</div>
    </div>

    <div class="mysql-transfer-error" v-if="error"><b>导出失败</b><pre>{{ error }}</pre></div>
  </div>

  <template #footer>
    <el-button :disabled="busy" @click="onClose">关闭</el-button>
    <el-button type="primary" :loading="busy" :disabled="!target || !effSQL.trim()" @click="start">
      {{ busy ? '导出中…' : '开始导出' }}
    </el-button>
  </template>
</el-dialog>`,
  data() {
    return {
      open: true,
      format: 'sql',
      insertTable: '',
      sheet: '',
      batchRows: 200,
      batchOptions: [100, 200, 500, 1000],
      target: '',
      busy: false,
      progress: { rows: 0, bytes: 0, elapsed_ms: 0 },
      result: null,
      error: '',
      timer: null
    }
  },
  computed: {
    tu() { return window.MySQLTransferUtil },
    isDump() { return !!this.table },
    isSQL() { return this.format === 'sql' },
    isXLSX() { return this.format === 'xlsx' },
    ext() { return this.format === 'sql' ? '.sql' : (this.format === 'xlsx' ? '.xlsx' : '.csv') },
    // 实际送去导出的查询：转储模式固定取整表；查询模式用编辑器里的 SQL
    effSQL() {
      if (!this.isDump) return this.sql
      return 'SELECT * FROM ' + quoteIdent(this.schema) + '.' + quoteIdent(this.table)
    },
    // 去掉库名前缀的纯表名：默认文件名 / 工作表名用它
    plainTable() {
      const name = this.isDump ? this.table : this.insertTable
      return String(name || '').split('.').pop()
    }
  },
  mounted() {
    this.insertTable = this.isDump
      ? (this.schema ? this.schema + '.' + this.table : this.table)
      : window.MySQLTransferUtil.guessTable(this.sql)
    this.sheet = this.plainTable
  },
  beforeUnmount() { this.stopPolling() },
  methods: {
    onClose() {
      if (this.busy) { ElMessage.warning('导出进行中，完成后再关闭'); return }
      this.open = false
    },
    async pickTarget() {
      if (!window.__INFRA_DESKTOP__) { ElMessage.warning('导出到本地文件需要在桌面端使用'); return }
      try {
        const saved = await window.wails.Call.ByName('infra-ops/desktop.FileService.SaveFile',
          '保存导出文件', this.defaultName(), this.filterName(), '*' + this.ext)
        if (saved) this.target = saved
      } catch (e) { ElMessage.error((e && e.message) || '选择保存位置失败') }
    },
    defaultName() {
      const base = String(this.plainTable || this.schema || 'result').replace(/[^\w\u4e00-\u9fa5.-]/g, '_')
      const ts = new Date().toISOString().slice(0, 19).replace(/[:T]/g, '')
      return base + '_' + ts + this.ext
    },
    filterName() {
      if (this.format === 'xlsx') return 'Excel 工作簿 (*.xlsx)'
      if (this.format === 'csv') return 'CSV 文件 (*.csv)'
      return 'SQL 文件 (*.sql)'
    },
    async start() {
      if (this.busy) return
      if (!this.target) { ElMessage.warning('请先选择保存位置'); return }
      if (this.isSQL && !this.insertTable.trim()) { ElMessage.warning('请填写 INSERT 的目标表名'); return }

      this.busy = true
      this.error = ''
      this.result = null
      this.progress = { rows: 0, bytes: 0, elapsed_ms: 0 }
      const key = window.MySQLTransferUtil.newKey('exp')
      this.startPolling(key)
      try {
        // timeout: 0 —— 本地直连导出大表本来就慢，卡在 axios 默认的 15 秒没有意义
        const r = await api.post('/mysql/' + this.connId + '/export', {
          sql: this.effSQL,
          schema: this.schema,
          format: this.format,
          target: this.target,
          table: this.insertTable.trim(),
          sheet: this.sheet.trim(),
          batch_rows: this.batchRows,
          progress_key: key
        }, { timeout: 0 })
        if (!r || r.code !== 0) { this.error = (r && r.message) || '导出失败'; return }
        this.result = r.data || {}
      } catch (e) {
        this.error = window.MySQLTransferUtil.errText(e, '导出失败')
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
        } catch (e) { /* 进度是旁路信息，取不到不影响导出本身 */ }
      }, 500)
    },
    stopPolling() {
      if (this.timer) { clearInterval(this.timer); this.timer = null }
    }
  }
}

// quoteIdent 转义 MySQL 标识符（反引号内翻倍）。转储模式的 SQL 在前端拼，
// 库表名带特殊字符时不能把语句拼坏。
function quoteIdent(s) {
  return '`' + String(s || '').replace(/`/g, '``') + '`'
}
