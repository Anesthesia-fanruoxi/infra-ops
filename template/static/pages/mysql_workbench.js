// MySQL 工作台壳：顶栏 + 左侧库表树 + SQL 编辑器 + 结果 / 表详情 / AI 三块主面板。
window.MySQLWorkbench = {
  props: {
    conn: { type: Object, required: true }
  },
  emits: ['exit', 'open-ai-config', 'open-logs'],
  components: {
    'mysql-tree': window.MySQLTree,
    'mysql-grid': window.MySQLGrid,
    'mysql-table-detail': window.MySQLTableDetail,
    'mysql-ai': window.MySQLAI,
    'mysql-export': window.MySQLExport,
    'mysql-import': window.MySQLImport
  },
  template: `
<div class="page-card reg-workspace mysql-workspace">
  <div class="card-header reg-whd">
    <el-button text @click="$emit('exit')" style="margin-right:2px"><el-icon><Back /></el-icon></el-button>
    <div class="reg-whd-title">
      <div class="reg-whd-name">
        <span class="title">{{ conn.name }}</span>
        <span class="action-badge host">MySQL</span>
        <span v-if="conn.ssh_host_name" class="action-badge info" :title="'经跳板机 ' + conn.ssh_host_name + ' 转发'">隧道</span>
      </div>
      <div class="mono reg-whd-url">{{ conn.username }}@{{ conn.host }}:{{ conn.port }}<span v-if="conn.default_schema">/{{ conn.default_schema }}</span></div>
    </div>
    <div class="header-extra">
      <el-button text @click="reloadTree"><el-icon style="margin-right:4px"><Refresh /></el-icon>刷新库表</el-button>
    </div>
  </div>

  <div class="mysql-body">
    <mysql-tree ref="tree" :conn="conn" :active-db="schema" :active-table="currentTable"
      @pick-table="onPickTable" @pick-db="onPickDb" />

    <section class="mysql-main">
      <div class="mysql-editor-bar">
        <span class="mysql-cur-db">当前库 <b>{{ schema || '—' }}</b></span>
        <span class="mysql-mode-group">
          <span class="mysql-mode-label">模式</span>
          <el-switch class="mysql-mode-switch" v-model="writeMode" :loading="switchingMode" :disabled="switchingMode"
            inline-prompt active-text="写" inactive-text="读" />
          <span class="mysql-mode-hint" :class="writeMode ? 'is-write' : 'is-read'">{{ modeHint }}</span>
        </span>
        <span class="mysql-spacer"></span>
        <el-button text :disabled="running" @click="openImport"><el-icon style="margin-right:4px"><Upload /></el-icon>导入</el-button>
        <el-button text :disabled="running || !sql.trim()" @click="openExport"><el-icon style="margin-right:4px"><Download /></el-icon>导出</el-button>
        <!-- 执行中才出现：慢 SQL 不想等了直接中断，不用等后端超时 -->
        <el-button v-if="running" text type="danger" :loading="canceling" @click="cancelSql()">取消</el-button>
        <el-button type="primary" :loading="running" @click="runSql(false)">执行<span class="mysql-kbd">Ctrl+Enter</span></el-button>
      </div>
      <textarea class="mysql-editor" v-model="sql" spellcheck="false"
        @keydown.ctrl.enter.prevent="runSql(false)"
        placeholder="输入 SQL，Ctrl + Enter 执行；点左侧表名看表详情，表详情里再点「查询数据」取数"></textarea>

      <div class="mysql-tabs">
        <button type="button" class="mysql-tab" :class="{active: tab==='data'}" @click="tab='data'">结果</button>
        <button type="button" class="mysql-tab" :class="{active: tab==='table'}" @click="tab='table'">表详情</button>
        <button type="button" class="mysql-tab" :class="{active: tab==='ai'}" @click="tab='ai'">AI 生成</button>
        <span class="mysql-tab-name mono" v-if="tab==='data' && currentTable">{{ schema }}.{{ currentTable }}</span>
      </div>

      <div class="mysql-pane" v-show="tab==='data'">
        <mysql-grid :result="result" :error="error" :conn-id="conn.id" :schema="schema" :analyze-sql="lastRunSql" />
      </div>

      <div class="mysql-pane" v-show="tab==='table'">
        <mysql-table-detail :conn="conn" :schema="schema" :table="currentTable"
          @query-data="queryCurrentTable" @dump="openTableDump" />
      </div>

      <div class="mysql-pane" v-show="tab==='ai'">
        <mysql-ai :conn="conn" :schema="schema" :mode="mode" :active="tab==='ai'"
          @use-sql="onUseSQL" @pick-db="onPickDb"
          @open-config="$emit('open-ai-config')" @open-logs="$emit('open-logs')" />
      </div>
    </section>
  </div>

  <!-- 导出 / 导入：都以编辑器里的 SQL 与当前库为上下文，故由工作台持有；
       exportTable 非空 = 整表转储模式（表详情的「转储」入口） -->
  <mysql-export v-if="exportOpen" :conn-id="conn.id" :sql="sql" :schema="schema" :table="exportTable"
    @close="exportOpen=false" />
  <mysql-import v-if="importOpen" :conn-id="conn.id" :schema="schema"
    @close="importOpen=false" />
</div>`,
  data() {
    return {
      sql: '',
      schema: this.conn.default_schema || '',
      currentTable: '',
      tab: 'data',
      result: null,
      error: '',
      // 产生当前结果的 SQL：AI 分析（仅 EXPLAIN 类结果）以它为准，而不是编辑器现内容
      lastRunSql: '',
      running: false,
      // 正在发取消请求（防重复点；中断是毫秒级，只是兜个竞态）
      canceling: false,
      // 运行模式：连接之后自行管控，初值取连接配置里的默认模式，随后以服务端状态为准
      mode: this.conn.read_only ? 'read' : 'write',
      switchingMode: false,
      exportOpen: false,
      // 非空 = 整表转储模式（来自表详情的「转储」按钮）；查询导出时保持为空
      exportTable: '',
      importOpen: false
    }
  },
  computed: {
    // 开关语义：关 = 绿（只读），开 = 黄（可写）
    writeMode: {
      get() { return this.mode === 'write' },
      set(target) { this.switchMode(target ? 'write' : 'read') }
    },
    modeHint() {
      return this.writeMode ? '可写 · 写语句执行前仍需确认' : '只读 · 写语句需逐条确认放行'
    }
  },
  mounted() { this.loadMode() },
  methods: {
    // 取服务端记录的模式：刷新页面后仍是切换过的那个，而不是回落到默认值
    async loadMode() {
      try {
        const r = await api.get('/mysql/' + this.conn.id + '/mode')
        if (r.code === 0 && r.data) this.mode = r.data.mode || this.mode
      } catch (e) { /* 拦截器已提示 */ }
    },
    async switchMode(mode) {
      if (mode === this.mode) return
      const prev = this.mode
      this.mode = mode // 先动 UI，接口失败再回滚
      this.switchingMode = true
      try {
        const r = await api.put('/mysql/' + this.conn.id + '/mode', { mode: mode })
        if (!r || r.code !== 0) throw new Error((r && r.message) || '')
        ElMessage.success(mode === 'write' ? '已切换为可写模式' : '已切换为只读模式')
      } catch (e) {
        this.mode = prev
        ElMessage.error('切换模式失败')
      } finally { this.switchingMode = false }
    },
    reloadTree() {
      if (this.$refs.tree) this.$refs.tree.loadDbs()
    },
    openExport() {
      if (!this.sql.trim()) { ElMessage.warning('请先输入要导出的查询语句'); return }
      this.exportTable = ''
      this.exportOpen = true
    },
    // 表详情的「转储」：整表全量导出为 SQL / Excel / CSV，不依赖编辑器里的 SQL
    openTableDump() {
      if (!this.currentTable) return
      this.exportTable = this.currentTable
      this.exportOpen = true
    },
    // 导入会批量改数据，先在这儿拦一道：让使用者切模式比让他填完表单再被拒要有用
    openImport() {
      if (this.mode !== 'write') {
        ElMessage.warning('导入会批量修改数据，请先切换到可写模式')
        return
      }
      this.importOpen = true
    },
    onPickDb(db) {
      this.schema = db
    },
    // 点表：切库 + 打开「表详情」；不再自动拼 SELECT 执行，要取数在详情里点「查询数据」
    onPickTable({ db, table }) {
      this.schema = db
      this.currentTable = table.name
      this.tab = 'table'
    },
    // 表详情里的「查询数据」：按选中表生成一条取数语句并立即执行
    queryCurrentTable() {
      if (!this.currentTable) return
      this.sql = 'SELECT * FROM ' + ident(this.schema) + '.' + ident(this.currentTable) + ' LIMIT 200'
      this.tab = 'data'
      this.runSql(false)
    },
    // AI 生成的 SQL 追加进编辑器（编辑器在三个页签下都可见），不自动执行。
    // opts.auto = 生成后自动追加：留在 AI 页签看用量与涉及表，不切结果页。
    // 追加而不覆盖：连续生成就是在拼一段可整段执行的脚本（每条语句自带分号，后端保证）。
    onUseSQL(text, opts) {
      const add = String(text || '').trim()
      if (!add) return
      const cur = this.sql.replace(/\s+$/, '')
      this.sql = cur ? cur + '\n\n' + add : add
      if (!(opts && opts.auto)) this.tab = 'data'
      ElMessage.success((cur ? '已追加到' : '已填入') + '编辑器，确认后点「执行」')
    },
    async runSql(confirmed) {
      const text = this.sql.trim()
      if (!text || this.running) return
      // 点执行即先切到结果页签并清掉上一次的展示：执行中 / 被确认框拦下 / 失败都不该残留旧记录
      this.tab = 'data'
      this.result = null
      this.lastRunSql = ''
      this.error = ''
      this.running = true
      let r
      try {
        // timeout 覆盖全局 15s：慢 SQL 由后端超时兜底，前端保持挂起不提前断线，
        // 执行中才有机会点「取消」把服务端那条查询真正掐掉
        r = await api.post('/mysql/' + this.conn.id + '/query', {
          sql: text,
          schema: this.schema,
          limit: 1000,
          confirmed: !!confirmed
        }, { timeout: 0 })
      } catch (e) { this.running = false; return }

      if (!r || r.code !== 0) {
        const msg = r?.message || '执行失败'
        this.result = null
        this.running = false
        // 「取消」不是故障：后端中断成功也回这条文案，轻提示即可、不落红色错误框
        if (msg.indexOf('已取消') >= 0) { this.error = ''; ElMessage.info(msg); return }
        this.error = msg
        ElMessage.error(msg)
        return
      }
      // 门禁：服务端判出这条不能直接执行（判断 1 会话模式 × 判断 2 语句类型），
      // 先回 need_confirm，用户确认后带 confirmed 重发；只读模式下的确认仅放行本次。
      if (r.data && r.data.need_confirm) {
        this.running = false
        if (await this.confirmGate(r.data)) this.runSql(true)
        return
      }

      this.result = r.data
      this.lastRunSql = text
      this.error = ''
      this.running = false
      if (this.result && this.result.sql_type === 'DDL') this.reloadTree() // 结构可能已变，刷新库表树
    },
    // 取消慢 SQL：通知后端中断本连接正在执行的查询；挂起的 /query 会立刻
    // 以「已取消执行」返回，由上面 runSql 的取消分支收尾（轻提示、复位 running）
    async cancelSql() {
      if (!this.running || this.canceling) return
      this.canceling = true
      try {
        const r = await api.post('/mysql/' + this.conn.id + '/query/cancel')
        // 极端竞态才会命中：点取消的瞬间查询刚好跑完，没有可中断的目标
        if (r && r.code === 0 && r.data && !r.data.canceled) ElMessage.warning('当前没有正在执行的查询')
      } catch (e) { /* 拦截器已提示 */ } finally { this.canceling = false }
    },
    // confirmGate 按服务端给出的分级选择弹窗强度与文案。
    async confirmGate(d) {
      const label = d.sql_type + (d.statement_count > 1 ? ' · 共 ' + d.statement_count + ' 条语句' : '')
      const levels = {
        readonly: {
          title: '只读模式 · 本次放行',
          type: 'error',
          tip: '当前连接处于<b>只读模式</b>，而这段 SQL 属于 <b>' + label + '</b>。'
            + '本次确认只放行这一条，模式保持不变；需要连续写入请切换到可写模式。'
        },
        ddl: {
          title: '结构变更确认',
          type: 'error',
          tip: '这段 SQL 属于 <b>' + label + '</b>，会修改表结构，<b>执行后不可回滚</b>。'
        },
        write: {
          title: '数据变更确认',
          type: 'warning',
          tip: '这段 SQL 属于 <b>' + label + '</b>，会修改数据。'
        }
      }
      const cfg = levels[d.confirm_level] || levels.write
      let tip = cfg.tip
      if (d.statement_count > 1 && d.statements && d.statements.length) {
        tip += '<div class="mysql-confirm-list">'
          + d.statements.map((s, i) => (i + 1) + '. ' + s.kind + ' · ' + s.type).join('<br>')
          + '</div>'
      }
      try {
        await this.$confirm(tip, cfg.title, {
          type: cfg.type,
          dangerouslyUseHTMLString: true,
          confirmButtonText: '执行',
          cancelButtonText: '取消'
        })
        return true
      } catch (e) { return false }
    }
  }
}

// ident 转义 MySQL 标识符（反引号内翻倍），避免库表名含特殊字符时拼出的 SQL 出错。
function ident(s) {
  return '`' + String(s).replace(/`/g, '``') + '`'
}
