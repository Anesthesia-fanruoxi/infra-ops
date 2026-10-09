// MySQL 工作台 · AI 面板：自然语言 → SQL。
// 上下文来自「语义目录」（表注释 + 字段注释先经模型归纳并缓存）；
// 生成范围跟着工作台的读写开关走（只读只给查询、可写给写语句），生成即自动追加进编辑器、不自动执行。
window.MySQLAI = {
  props: {
    conn: { type: Object, required: true },
    schema: { type: String, default: '' },
    // 当前会话模式：由工作台透传，只用于展示生成范围与标注结果，真正的放行判定在服务端
    mode: { type: String, default: 'read' },
    // 工作台是否停留在「AI 生成」页签：面板 v-show 常驻挂载，切进来时要刷新目录概况
    active: { type: Boolean, default: false }
  },
  emits: ['use-sql', 'open-config', 'pick-db', 'open-logs'],
  components: { 'mysql-ai-catalog': window.MySQLAICatalog },
  template: `
<div class="mysql-ai">
  <div class="mysql-ai-head">
    <span class="mysql-ai-title">AI 生成 SQL</span>
    <!-- 生成开销当场展示：SQL 已自动追加进编辑器，面板不再单独展示生成结果 -->
    <span class="mysql-ai-usage" v-if="usage.total_tokens">
      本次消耗 <b>{{ fmtNum(usage.total_tokens) }}</b> tokens（输入 {{ fmtNum(usage.prompt_tokens) }} · 输出 {{ fmtNum(usage.completion_tokens) }}）<span v-if="used"> · 参考 {{ used }} 张表作上下文</span> · 耗时 {{ fmtMs(usage.elapsed_ms) }}<span v-if="usage.source === 'estimated'"> · 接口未返回用量，按字符估算</span>
    </span>
    <span class="mysql-ai-spacer"></span>
    <span class="mysql-ai-model mono" v-if="cfg.enabled && cfg.model" :title="cfg.base_url">{{ cfg.model }}</span>
    <el-button text size="small" @click="$emit('open-logs')"><el-icon style="margin-right:4px"><Tickets /></el-icon>调用记录</el-button>
    <el-button text size="small" @click="$emit('open-config')"><el-icon style="margin-right:4px"><Setting /></el-icon>AI 设置</el-button>
  </div>

  <div v-if="!cfg.enabled" class="mysql-ai-blank">
    <p>还没有启用 AI 生成 SQL。</p>
    <p class="mysql-ai-blank-hint">到「设置 · AI 接入」填好 OpenAI 兼容接口的地址、密钥与模型名即可（DeepSeek / 通义 / Kimi / 本地 Ollama 都走同一套）。</p>
    <el-button type="primary" size="small" @click="$emit('open-config')">去设置</el-button>
  </div>

  <template v-else>
    <div class="mysql-ai-catalog">
      <span class="mysql-ai-catalog-label">目标库</span>
      <el-select v-model="db" class="mysql-ai-dbsel" size="small" filterable :loading="loadingDbs"
        placeholder="选择要分析的库" no-data-text="没有可选的库" @change="onDbChange">
        <el-option v-for="d in dbs" :key="d" :label="d" :value="d" />
      </el-select>
      <span class="mysql-ai-catalog-label">语义目录</span>
      <b :class="catalogCount ? 'is-ok' : 'is-empty'">{{ catalogCount }} 张表</b>
      <span class="mysql-ai-spacer"></span>
      <el-button size="small" text :disabled="!db" title="勾选要生成目录的库；表结构没变会自动跳过，不重复花 token" @click="openGenDialog('gen')">生成</el-button>
      <el-button size="small" text :disabled="!db" v-if="catalogCount" title="强制重新归纳（结构没变也重新生成）" @click="openGenDialog('rebuild')">重建</el-button>
      <el-button size="small" text title="勾选要生成目录的库（默认全选非系统库），表结构没变的自动跳过；生成后 AI 可直接写跨库查询"
        @click="openGenDialog('batch')">批量生成</el-button>
      <el-button size="small" text @click="catalogOpen = true">浏览目录</el-button>
    </div>
    <!-- 后台任务的两态提示行：运行中给实时进度与快捷停止；结束后提醒失败/停止可补齐 -->
    <div class="mysql-ai-task" v-if="genRunning">
      <span class="mysql-ai-task-dot"></span>
      <span>正在生成语义目录：{{ genTask.finished }}/{{ genTask.total }} 个库<span v-if="genTask.current"> · 当前 <b class="mono">{{ genTask.current }}</b></span></span>
      <span class="mysql-ai-spacer"></span>
      <el-button text size="small" @click="openGenDialog('view')">查看进度</el-button>
      <el-button text size="small" type="danger" @click="stopGenTask">停止</el-button>
    </div>
    <div class="mysql-ai-task is-warn" v-else-if="genFailed || genStopped">
      <span>{{ genFailed ? '上次有 ' + genFailed + ' 个库未完成（失败/被停止）' : '上次生成已停止' }}：未完成的部分可在弹框里直接补齐 / 重试（成功的表命中缓存会跳过）。</span>
      <span class="mysql-ai-spacer"></span>
      <el-button text size="small" @click="openGenDialog('view')">去处理</el-button>
    </div>
    <div class="mysql-ai-catalog-msg mysql-ai-tip" v-if="catalogMsg">{{ catalogMsg }}</div>
    <div class="mysql-ai-catalog-msg mysql-ai-tip" v-else-if="!db">先在上面选一个库再点「生成」：AI 会读这个库的表与字段注释，归纳成可长期复用的语义目录；点「批量生成」则一次把多个库都建一遍。</div>
    <div class="mysql-ai-catalog-msg mysql-ai-tip mysql-ai-nocatalog" v-else-if="!connTotal">
      <span>当前没有缓存语义目录：由模型读表与字段注释归纳，生成一次后长期复用，AI 生成 SQL 会自动引用。</span>
      <el-button type="primary" size="small" @click="openGenDialog('batch')">立即生成</el-button>
    </div>
    <div class="mysql-ai-catalog-msg mysql-ai-tip" v-else-if="!catalogCount">当前库还没有目录：目录由模型读表与字段注释归纳而成，生成一次后长期复用，表结构变了才重算。其他库的目录也可直接用（AI 会生成「库名.表名」形式的跨库查询）。</div>
    <div class="mysql-ai-usage" v-if="catalogUsage.total_tokens">
      上次生成语义目录消耗 <b>{{ fmtNum(catalogUsage.total_tokens) }}</b> tokens（{{ fmtNum(catalogUsage.calls) }} 次模型调用，输入 {{ fmtNum(catalogUsage.prompt_tokens) }} · 输出 {{ fmtNum(catalogUsage.completion_tokens) }}）<span v-if="catalogUsage.source === 'estimated'"> · 其中含按字符估算</span>
    </div>

    <div class="mysql-ai-scope" :class="isWrite ? 'is-write' : 'is-read'">
      <span class="mysql-ai-scope-tag">{{ isWrite ? '可写模式' : '只读模式' }}</span>
      <span class="mysql-ai-scope-text">{{ scopeText }}</span>
    </div>

    <textarea class="mysql-ai-input" v-model="question" spellcheck="false" :disabled="loading"
      @keydown.ctrl.enter.prevent="generate"
      :placeholder="placeholder"></textarea>
    <div class="mysql-ai-actions">
      <span class="mysql-ai-tip">Ctrl + Enter 生成</span>
      <span class="mysql-ai-spacer"></span>
      <el-button type="primary" size="small" :loading="loading" @click="generate">生成 SQL</el-button>
    </div>

    <div class="mysql-ai-error" v-if="error"><pre>{{ error }}</pre></div>

    <!-- 生成反馈（SQL 已自动追加进编辑器，不再重复展示）：涉及表徽标 + 写语句告警 -->
    <div class="mysql-ai-tables" v-if="sql && tables.length">
      <span class="action-badge info" v-for="t in tables" :key="t">{{ t }}</span>
    </div>
    <div class="mysql-ai-warn" v-if="sql && isWriteOp">
      {{ sqlType === 'DDL' ? '这是结构变更语句，执行后不可回滚' : '这是写语句' }}，不会自动执行：已追加进编辑器，点「执行」时会先弹确认<span v-if="confirmLevel === 'readonly'">，只读模式下本次确认只放行这一遍</span>。
    </div>
  </template>

  <!-- 语义目录浏览：按库折叠，看每张表的用途与字段说明，快速了解这个连接里都有什么 -->
  <mysql-ai-catalog v-if="catalogOpen" :conn-id="conn.id" @close="catalogOpen = false" />

  <!-- 生成 / 重建 / 批量生成共用一个弹框（宽 50%、高 60% 固定，上下各留 20%）：
       生成 / 重建只针对当前库——单库确认视图，点「开始」后再弹二次确认；
       批量生成出现勾选框，勾好库再开始（同样二次确认）。
       开始后转为服务端后台任务，弹框切进度视图并逐批追加「生成记录」；
       关弹框 / 切页面都不影响，回来还能继续看进度、随时停止。
       部分库失败时自动切回勾选视图并勾上失败库，再点一次就是重试（成功的表命中缓存跳过） -->
  <el-dialog v-model="genOpen" :title="genTitle" width="50%" class="mysql-ai-gen-dialog" append-to-body>
    <!-- 运行中：进度视图。任务跑在服务端，这个弹框只是查看窗口，关掉随你 -->
    <template v-if="genRunning">
      <div class="mysql-ai-gen-tip">任务在服务端后台运行：关掉弹框、切去别的页面都不影响；每跑完一个库即落库，随时可停止，已完成的会保留。</div>
      <div class="mysql-ai-gen-progress">
        <div class="mysql-ai-gen-progress-top">
          已完成 <b>{{ genTask.finished }}</b> / {{ genTask.total }} 个库<template v-if="genTask.current"> · 正在处理 <b class="mono">{{ genTask.current }}</b><template v-if="genTask.batch_total > 1">（第 {{ genTask.batch_done }}/{{ genTask.batch_total }} 批）</template></template>
        </div>
        <el-progress :percentage="genPct" :stroke-width="8" :show-text="false" />
      </div>
      <div class="mysql-ai-gen-dbs">
        <div class="mysql-ai-gen-db" v-for="s in (genTask.schemas || [])" :key="s.schema">
          <span class="mono">{{ s.schema }}</span>
          <span class="mysql-ai-spacer"></span>
          <span class="mysql-ai-gen-state" :class="'is-' + (s.state === 'done' && s.failed ? 'warn' : s.state)">{{ schemaStateText(s) }}</span>
        </div>
      </div>
    </template>
    <!-- 非运行：单库确认视图（生成 / 重建只针对当前库，无勾选框，点「开始」进二次确认） -->
    <template v-else-if="genSingle">
      <div class="mysql-ai-gen-cur">
        <span class="mysql-ai-gen-cur-tag">当前库</span>
        <span class="mono mysql-ai-gen-cur-name">{{ db }}</span>
        <span class="mysql-ai-gen-cached" v-if="schemaCounts[db]">已缓存 {{ schemaCounts[db] }} 张</span>
      </div>
      <div class="mysql-ai-gen-tip" v-if="genMode === 'rebuild'">只针对当前库重建：强制重新归纳，结构没变的表也会再次调用模型（消耗 token），建议仅在目录质量不满意时使用。点「开始重建」后还有一次确认；开始后任务在后台跑，关掉弹框不影响。</div>
      <div class="mysql-ai-gen-tip" v-else>只针对当前库生成：模型读表与字段注释归纳成语义目录，结构没变的表会自动跳过，不重复花 token。点「开始生成」后还有一次确认；开始后任务在后台跑，关掉弹框不影响。</div>
    </template>
    <!-- 非运行：勾选视图（批量生成 / 失败重试 / 停止后补齐共用） -->
    <template v-else>
      <div class="mysql-ai-gen-tip" v-if="genMode === 'rebuild'">重建会强制重新归纳：结构没变的库也会再次调用模型（消耗 token），建议仅在目录质量不满意时使用。开始后任务在后台跑，关掉弹框不影响。</div>
      <div class="mysql-ai-gen-tip" v-else>勾选要生成目录的库（会逐库跑，互不影响）：结构没变的表会自动跳过，不重复花 token。开始后任务在后台跑，关掉弹框不影响。</div>
      <div class="mysql-ai-gen-failed" v-if="genFailed">上次有 {{ genFailed }} 个库存在失败批次：失败的表没有落库，已自动勾选，直接再点一次「重试」只补失败部分（成功的表命中缓存会自动跳过，不重复花 token）。</div>
      <div class="mysql-ai-gen-stopped" v-else-if="genStopped">上次生成已停止：跑完的批次已保留（下面每库的「已缓存」就是当前进度），未完成的库已勾选，再点「开始生成」即可补齐。</div>
      <div class="mysql-ai-gen-bar">
        <span class="mysql-ai-gen-count">已选 {{ genPicked.length }} / {{ pickableDbs.length }} 个库</span>
        <span class="mysql-ai-spacer"></span>
        <el-button text size="small" @click="genPicked = pickableDbs.slice()">全选</el-button>
        <el-button text size="small" :disabled="!genPicked.length" @click="genPicked = []">清空</el-button>
      </div>
      <el-checkbox-group v-if="pickableDbs.length" v-model="genPicked" class="mysql-ai-gen-dbs">
        <div class="mysql-ai-gen-db" v-for="d in pickableDbs" :key="d">
          <el-checkbox :value="d"><span class="mono">{{ d }}</span></el-checkbox>
          <span class="mysql-ai-gen-cached" v-if="schemaCounts[d]">已缓存 {{ schemaCounts[d] }} 张</span>
        </div>
      </el-checkbox-group>
      <empty-state v-else text="连接里没有可生成目录的非系统库（系统库不参与生成）" />
    </template>
    <!-- 生成记录：运行中随任务进度逐批追加（时间 / 库 / 批次 / 表数 / tokens / 耗时 / 成败），
         结束后留作「上次生成记录」复盘；失败行的 title 是截断后的失败原因 -->
    <div class="mysql-ai-gen-records" v-if="genShowRecords">
      <div class="mysql-ai-gen-records-head">
        <span>{{ genRunning ? '生成记录' : '上次生成记录' }}</span>
        <span class="mysql-ai-spacer"></span>
        <span>{{ genRecords.length }} 条 · 按批追加</span>
      </div>
      <div class="mysql-ai-gen-records-list" ref="genRecords">
        <div class="mysql-ai-gen-record" v-for="(r, i) in genRecords" :key="i" :class="r.status === 'ok' ? '' : 'is-bad'">
          <span class="mono mysql-ai-gen-record-time">{{ r.time }}</span>
          <span class="mono mysql-ai-gen-record-schema">{{ r.schema }}</span>
          <span class="mysql-ai-gen-record-meta">第 {{ r.batch_no }}/{{ r.batch_total }} 批 · {{ r.tables }} 张表</span>
          <span class="mysql-ai-gen-record-meta" v-if="r.total_tokens">tokens {{ fmtNum(r.total_tokens) }}<i class="mysql-ai-gen-record-est" v-if="r.estimated">估</i></span>
          <span class="mysql-ai-gen-record-meta">{{ fmtMs(r.elapsed_ms) }}</span>
          <span class="mysql-ai-spacer"></span>
          <span class="mysql-ai-gen-record-state" :title="r.message || null">{{ r.status === 'ok' ? '成功' : '失败' }}</span>
        </div>
      </div>
    </div>
    <template #footer>
      <template v-if="genRunning">
        <el-button @click="genOpen = false">后台运行</el-button>
        <el-button type="danger" @click="stopGenTask">停止生成</el-button>
      </template>
      <template v-else>
        <el-button @click="genOpen = false">取消</el-button>
        <el-button type="primary" :disabled="!genPicked.length"
          @click="startGenTask">{{ genFailed && !genSingle ? '重试失败的库（' + genPicked.length + '）' : (genMode === 'rebuild' ? '开始重建' : '开始生成') + (genSingle || !genPicked.length ? '' : '（' + genPicked.length + ' 个库）') }}</el-button>
      </template>
    </template>
  </el-dialog>
</div>`,
  data() {
    return {
      cfg: {}, dbs: [], db: this.schema || '', loadingDbs: false,
      question: '', sql: '', explain: '', tables: [], used: 0,
      // sqlType / stmtCount / confirmLevel 由服务端随生成结果下发（静态分类，不是让模型自己声明）
      sqlType: '', stmtCount: 0, confirmLevel: '',
      // token 用量也由服务端下发：usage = 本次生成，catalogUsage = 上次目录生成
      usage: {}, catalogUsage: {},
      loading: false,
      catalogCount: 0, catalogMsg: '', catalogOpen: false, error: '',
      // 连接级目录概况：connTotal = 全连接缓存条数（0 = 提示「当前没有缓存语义目录」）；schemaCounts = 每库条数
      connTotal: 0, schemaCounts: {},
      // 生成弹框：生成 / 重建 / 批量生成共用（genMode 决定口径：rebuild = 强制重算；
      // genSingle = 单库确认视图——生成 / 重建只针对当前库；批量 / 重试 / 补齐走勾选视图）。
      // 点「开始」后转为服务端后台任务（genTask = 任务快照，含逐批追加的 records 生成记录），
      // 弹框只是查看窗口——关掉不影响任务。genFailed / genStopped 是最近一次任务的结果状态，
      // 用于恢复「失败重试 / 停止补齐」视图（刷新页面后向服务端重取，不靠本地记忆）
      genOpen: false, genPicked: [], genTask: null, genFailed: 0, genStopped: false, genMode: 'build', genSingle: false
    }
  },
  computed: {
    isWrite() { return this.mode === 'write' },
    // 生成范围随模式变，必须显式说出来：否则使用者不知道切读写开关也会改变 AI 给什么
    scopeText() {
      return this.isWrite
        ? '可生成增删改与建表改表语句；结果自动追加进编辑器，执行前仍需确认'
        : '只生成 SELECT 查询；需要 AI 给写操作请先切到可写模式'
    },
    placeholder() {
      return this.isWrite
        ? '用大白话说你想查什么、或者想改什么，例如：把 users 里 status=0 的用户改成 1，或给 orders 加一个 remark 字段'
        : '用大白话说你想查什么，例如：最近 7 天各渠道的订单金额合计，按金额倒序取前 10'
    },
    isWriteOp() { return !!this.sqlType && this.sqlType !== 'DQL' && this.sqlType !== 'SESSION' },
    // 弹框可勾选的库：目录生成不碰系统库（与后端 isSystemSchema 同口径）
    pickableDbs() { return this.dbs.filter(d => !isAISystemDb(d)) },
    // 有任务且处于 running：弹框切进度视图、面板出进度行
    genRunning() { return !!(this.genTask && this.genTask.state === 'running') },
    genPct() {
      const t = this.genTask
      return t && t.total ? Math.round(t.finished * 100 / t.total) : 0
    },
    // 弹框标题：单库生成 / 重建、进度、失败补齐都沿用「生成 / 重建」；只有纯批量入口标「批量」
    genTitle() {
      if (this.genSingle || this.genRunning || this.genFailed || this.genStopped) {
        return this.genMode === 'rebuild' ? '重建语义目录' : '生成语义目录'
      }
      return '批量生成语义目录'
    },
    // 弹框内的生成记录：运行中随任务进度逐批追加；结束后留作「上次生成记录」复盘
    genRecords() { return (this.genTask && this.genTask.records) || [] },
    genShowRecords() {
      return this.genRecords.length > 0 && (this.genRunning || this.genFailed > 0 || this.genStopped)
    }
  },
  watch: {
    // 工作台侧换了库（点左侧树的库 / 表）时同步过来
    schema(v) { if (v && v !== this.db) this.onDbChange(v) },
    // 切进「AI 生成」页签时刷新目录概况与任务状态：面板常驻挂载，
    // 切走期间后台任务可能已经跑完 / 被停，进度行与提醒要能对得上
    active(v) { if (v) { this.refreshCatalog(); this.loadGenTask(true) } },
  },
  mounted() {
    this.init()
    // 页面重建时静默恢复未看完的后台任务（生成中会继续轮询、已结束的只摆好界面状态）
    this.loadGenTask(true)
    // AI 配置在「设置」页维护，改动经全局事件回来——触发源不在本组件树里，props 逐层透传够不着
    this.onCfgChanged = () => this.init()
    window.addEventListener('ai-config-changed', this.onCfgChanged)
  },
  beforeUnmount() {
    window.removeEventListener('ai-config-changed', this.onCfgChanged)
    this.stopTaskPoll() // 组件卸载不丢任务：轮询只是查看通道，任务本身在服务端继续
  },
  methods: {
    async init() {
      await this.loadConfig()
      if (!this.cfg.enabled) {
        // 未启用时清空全部目录状态，避免残留上一次连接的数据误导展示
        this.dbs = []; this.catalogCount = 0; this.catalogMsg = ''; this.connTotal = 0; this.schemaCounts = {}
        return
      }
      await this.loadDbs()
      this.refreshCatalog()
    },
    // 目录概况刷新：当前库条数 + 连接级总条数/每库条数（无缓存提示与弹框标注都依赖后者）
    refreshCatalog() {
      if (this.db) this.loadCatalog()
      this.loadConnCatalog()
    },
    async loadConfig() {
      try {
        // AI 接入配置已上提为平台设置，工具内不再自带配置读写接口（旧路径 /mysql/ai/config 已摘，
        // 打它会 404 —— 面板会一直停在「未启用」，看起来像开关打不开）
        const r = await api.get('/settings/ai')
        if (r.code === 0) this.cfg = r.data || {}
      } catch (e) { /* 拦截器已提示 */ }
    },
    // 库列表由面板自己拉：工作台的「当前库」只看左侧树选中的那个，连接没配默认库时它是空的，
    // 面板就落进「没有目标库」的状态（旧版此时点「生成」会静默 return，看起来就是点击没反应）。
    async loadDbs() {
      this.loadingDbs = true
      try {
        const r = await api.get('/mysql/' + this.conn.id + '/databases')
        if (r.code === 0) {
          this.dbs = r.data?.list || []
          if (!this.db || this.dbs.indexOf(this.db) < 0) {
            const first = this.dbs.filter(d => !isAISystemDb(d))[0] || this.dbs[0] || ''
            if (first) this.onDbChange(first)
          }
        }
      } catch (e) { /* 拦截器已提示 */ } finally { this.loadingDbs = false }
    },
    // 切库：清掉上一个库的目录计数与生成结果，并通知工作台把「当前库」一起改过去
    onDbChange(db) {
      if (!db) return
      this.db = db
      this.catalogCount = 0
      this.catalogMsg = ''
      this.sql = ''; this.explain = ''; this.tables = []; this.error = ''
      this.sqlType = ''; this.stmtCount = 0; this.confirmLevel = ''
      this.usage = {}; this.catalogUsage = {}
      this.$emit('pick-db', db)
      this.loadCatalog()
    },
    async loadCatalog() {
      if (!this.db) { this.catalogCount = 0; return }
      try {
        const r = await api.get('/mysql/' + this.conn.id + '/ai/catalog', { params: { schema: this.db } })
        if (r.code === 0) this.catalogCount = r.data?.total || 0
      } catch (e) { /* 拦截器已提示 */ }
    },
    // 连接级目录概况（GET 不带 schema）：0 条时提示「立即生成」，弹框里显示每库已缓存多少张
    async loadConnCatalog() {
      try {
        const r = await api.get('/mysql/' + this.conn.id + '/ai/catalog')
        if (r.code === 0) {
          const d = r.data || {}
          this.connTotal = d.total || 0
          const counts = {}
          for (const s of (d.schemas || [])) counts[s.name] = s.total
          this.schemaCounts = counts
        }
      } catch (e) { /* 拦截器已提示 */ }
    },
    // 打开生成弹框（生成 / 重建 / 批量生成 / 查看进度共用）：
    // entry='gen' 生成、'rebuild' 重建——都只针对当前库，进单库确认视图
    // （当前库不在可生成列表里时退回勾选视图兜底）；
    // 'batch' 批量 / 立即生成——勾选框全选；'view' 查看进度或去处理——运行中直接进进度视图，
    // 失败 / 停止保留勾选（待补齐）
    openGenDialog(entry) {
      this.genMode = entry === 'rebuild' ? 'rebuild' : 'build'
      this.genSingle = !this.genRunning && (entry === 'gen' || entry === 'rebuild')
        && this.pickableDbs.indexOf(this.db) >= 0
      if (!this.genRunning) {
        if (this.genSingle) {
          this.genPicked = [this.db] // 单库入口：生成目标就是当前库
        } else if (!this.genFailed && !this.genStopped) {
          this.genPicked = this.pickableDbs.slice()
        }
      }
      this.genOpen = true
      this.$nextTick(() => this.scrollGenRecords())
    },
    // 启动生成任务：所有语义生成（含重试 / 补齐）都先二次确认——这动作要花 token，
    // 说清处理哪些库、用哪种口径再提交；接口立即返回任务快照，生成在服务端后台跑，
    // 关弹框 / 切页面都不会中断
    async startGenTask() {
      if (this.genRunning) return
      const picked = this.genPicked.slice()
      if (!picked.length) { ElMessage.warning('请先勾选要生成目录的库'); return }
      // 单库视图（genSingle）是全新入口：即使之前有失败 / 停止状态也按「生成 / 重建」算，不叫「重试」
      const mode = this.genMode === 'rebuild' ? '重建'
        : this.genSingle ? '生成'
        : this.genFailed ? '重试' : this.genStopped ? '补齐' : '生成'
      const names = picked.length > 4 ? picked.slice(0, 4).join('、') + ' 等 ' + picked.length + ' 个库' : picked.join('、')
      const phrase = mode === '重建' ? '强制重建语义目录（结构没变也会重新消耗 token）'
        : mode === '重试' ? '重试未完成的库（只补失败部分）'
        : mode === '补齐' ? '补齐未完成的库（成功的表命中缓存跳过）'
        : '生成语义目录（结构没变的表自动跳过）'
      const title = mode === '生成' ? '开始生成语义目录？' : mode === '重建' ? '开始重建语义目录？'
        : mode === '重试' ? '重试失败的库？' : '补齐未完成的库？'
      try {
        await this.$confirm('即将为 ' + names + ' ' + phrase + '。任务在后台运行，可随时停止，已完成的会保留。',
          title, { type: 'warning', confirmButtonText: '开始' + mode, cancelButtonText: '取消' })
      } catch (e) { return } // 用户取消：不启动、不花 token
      this.genFailed = 0
      this.genStopped = false
      try {
        const r = await api.post('/mysql/' + this.conn.id + '/ai/catalog/task', { schemas: picked, force: this.genMode === 'rebuild' })
        if (r.code === 0 && r.data && r.data.task) {
          this.setGenTask(r.data.task)
          if (r.data.started) {
            this.catalogMsg = '已开始为 ' + picked.length + ' 个库生成语义目录（后台运行）：关掉弹框也不影响，进度随时可看…'
            ElMessage.success('已开始生成，可关掉弹框让它后台跑')
          } else {
            // 服务端拒了重复启动：已有任务在跑，切到它的进度视图就好
            this.catalogMsg = '已有生成任务在运行，为你切到进度视图…'
            ElMessage.info('已有生成任务在运行')
          }
        } else {
          this.catalogMsg = r.message || '启动生成任务失败'
          ElMessage.error(this.catalogMsg)
        }
      } catch (e) { /* 拦截器已提示 */ }
    },
    // 停止后台任务：后端取消 ctx，当前批次即刻中止，已跑完的批次已落库保留；
    // 快照一般还是 running（异步收尾），轮询到 stopped 后弹框会切到「补齐」视图
    async stopGenTask() {
      if (!this.genRunning) return
      try {
        const r = await api.post('/mysql/' + this.conn.id + '/ai/catalog/task/stop')
        if (r.code === 0) {
          if (r.data && r.data.task) this.setGenTask(r.data.task)
          ElMessage.info('正在停止：已完成的批次会保留')
        } else {
          ElMessage.error(r.message || '停止失败')
        }
      } catch (e) { /* 拦截器已提示 */ }
    },
    // 拉取当前任务快照（挂载 / 切回页签时调用）：运行中会开启轮询，
    // 已终结的静默恢复「失败重试 / 已停止」视图状态（silent = 不再补弹一条提醒）
    async loadGenTask(silent) {
      try {
        const r = await api.get('/mysql/' + this.conn.id + '/ai/catalog/task')
        if (r.code === 0) this.setGenTask((r.data && r.data.task) || null, silent)
      } catch (e) { /* 拦截器已提示 */ }
    },
    // 任务快照统一入口：运行中 → 保证轮询在跑；终结 → 停轮询并收尾。
    // 弹框开着时，记录条数变多就把列表滚到底——新记录永远在眼前（与看日志同款习惯）
    setGenTask(t, silent) {
      const hadRec = this.genTask && this.genTask.records ? this.genTask.records.length : 0
      this.genTask = t
      const nowRec = t && t.records ? t.records.length : 0
      if (t && t.state === 'running') {
        this.ensureTaskPoll()
        if (this.genOpen && nowRec !== hadRec) this.$nextTick(() => this.scrollGenRecords())
        return
      }
      this.stopTaskPoll()
      if (t) this.applyTaskEnded(t, !!silent)
    },
    scrollGenRecords() {
      const el = this.$refs.genRecords
      if (el) el.scrollTop = el.scrollHeight
    },
    // 轮询任务进度：1.5s 一次，仅任务运行期间有请求；桌面单机下开销可忽略，
    // 换来「后台跑、随时看」的确定性（与导入导出的进度接口同一思路，不走 SSE）
    ensureTaskPoll() {
      if (this._genTimer) return
      this._genTimer = setInterval(() => {
        api.get('/mysql/' + this.conn.id + '/ai/catalog/task').then(r => {
          if (r.code === 0) this.setGenTask((r.data && r.data.task) || null)
        }).catch(() => { /* 单次轮询失败不打扰：下个周期自会重试 */ })
      }, 1500)
    },
    stopTaskPoll() {
      if (this._genTimer) { clearInterval(this._genTimer); this._genTimer = null }
    },
    // 任务终结收尾：刷新目录概况与用量，并把「失败 / 停止」落成可继续操作的界面状态——
    // 未完成的库自动勾上，再点一次「开始生成」就是补齐 / 重试
    applyTaskEnded(t, silent) {
      if (this._genEndedFor === t.task_id) return // 同一任务只收尾一次（轮询与恢复可能先后到达）
      this._genEndedFor = t.task_id
      this.refreshCatalog()
      this.catalogUsage = t.usage || {}
      this.catalogMsg = t.message || ''
      // 未完成 = 失败的库 + 被停止时没跑完的库；全部成功时列表为空
      const pending = (t.schemas || []).filter(s => s.state !== 'done' || (s.failed || 0) > 0)
      if (t.state === 'stopped') {
        this.genStopped = true
        this.genFailed = 0
        this.genSingle = false // 结束后一律切回勾选视图：待补齐的库用勾选框承接
        this.genPicked = pending.map(s => s.schema)
        if (!silent) ElMessage.info('已停止生成：已完成的部分已保留')
      } else if (pending.length) {
        this.genFailed = pending.length
        this.genStopped = false
        this.genSingle = false
        this.genPicked = pending.map(s => s.schema)
        if (!silent) ElMessage.warning('有 ' + pending.length + ' 个库未完成，可在弹框里直接重试')
      } else {
        this.genFailed = 0
        this.genStopped = false
        if (!silent) ElMessage.success(this.catalogMsg || '目录已生成')
        if (this.genOpen) this.genOpen = false // 弹框开着且全部成功：自动关闭
      }
    },
    // 进度视图里每库一行的状态文案
    schemaStateText(s) {
      if (s.state === 'done') {
        let t = '完成：新生成 ' + (s.generated || 0) + '，缓存 ' + (s.cached || 0)
        if (s.failed) t += '，失败 ' + s.failed
        return t
      }
      if (s.state === 'running') return '处理中…'
      if (s.state === 'stopped') return '已停止' + (s.generated ? '（已生成 ' + s.generated + ' 张）' : '')
      if (s.state === 'failed') return '失败'
      return '排队中'
    },
    async generate() {
      const q = this.question.trim()
      if (this.loading) return
      if (!this.db) { ElMessage.warning('请先选择要查询的库'); return }
      // 不再因「当前库没有目录」拦下：上下文含连接内所有库的目录，
      // 当前库没建也能用其他库的表生成跨库查询；真一个目录都没有时服务端会明确报错
      if (!q) { ElMessage.warning('请先用一句话描述你想查什么'); return }
      this.loading = true
      this.error = ''
      this.usage = {}
      try {
        const r = await api.post('/mysql/' + this.conn.id + '/ai/sql',
          { schema: this.db, question: q }, { timeout: 180000 })
        if (r.code === 0 && r.data) {
          this.sql = r.data.sql || ''
          this.explain = r.data.explain || ''
          this.tables = r.data.tables || []
          this.used = r.data.used_catalog || 0
          this.sqlType = r.data.sql_type || ''
          this.stmtCount = r.data.statement_count || 0
          this.confirmLevel = r.data.confirm_level || ''
          this.usage = r.data.usage || {}
          // 生成即自动追加进编辑器（追加语义，不自动执行）
          if (this.sql) this.$emit('use-sql', this.sql, { auto: true })
        } else {
          this.error = r.message || '生成失败'
        }
      } catch (e) { /* 拦截器已提示 */ } finally { this.loading = false }
    },
    // token 数按千分位展示；耗时过千按秒看（模型调用常在秒级）
    fmtNum(n) { return Number(n || 0).toLocaleString('zh-CN') },
    fmtMs(ms) {
      const v = Number(ms || 0)
      return v < 1000 ? v + ' ms' : (v / 1000).toFixed(v < 10000 ? 1 : 0) + ' s'
    }
  }
}

// 系统库名单：与左侧树的 MYSQL_SYSTEM_DBS 同口径（顶层 const 不能跨文件重复声明，故这里另起名），
// 自动挑默认目标库时跳过这四个。
const AI_SYSTEM_DBS = ['information_schema', 'performance_schema', 'mysql', 'sys']
function isAISystemDb(name) {
  return AI_SYSTEM_DBS.indexOf(String(name).toLowerCase()) >= 0
}
