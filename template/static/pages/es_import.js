// ES 控制台 · 数据导入 tab：把本地文件（JSON / 文本 / 压缩包，可一次选多份）读进 ES 索引。
//
// 三条路径共用一个面板：
//   - JSON / JSONL：内容本身就是文档，直接导入；
//   - 普通文本：点「检查」由后端抽样并自动生成 grok pattern，点「预览」核实后再导入；
//   - 压缩包：后端解压后按上面两种情况逐个文件处理，全部导入同一索引。
// 多份文件按选择顺序串成一条流（后端 multiSource），检查/预览/导入三处一致。
//
// 文件选择两种方式：原生对话框（可多选）与直接拖拽 —— 面板整体带 data-file-drop-target，
// 拖入时由 Wails 运行时原生捕获（真实路径经 desktop/file_drop.go → infra:files-dropped
// 转发回来，wails-shim.js 封装为 window.__INFRA_DROP__）。
//
// 面板只有：文件、目标索引、「检查」（SSE 日志）、「预览」四件事；pattern 由检查自动
// 生成并留在内存里（没有手工编辑入口），预览里调整的只有字段类型。「预览」在检查出结果
// 之后才出现 —— 没结果就没有可核实的东西。
//
// 只支持桌面端：文件路径由原生对话框 / 拖拽取得、Go 侧流式读。压缩包动辄几个 GB，
// 走 webview 的 multipart 会被整段缓冲在内存（见 desktop/file_service.go 文件头）。
//
// 导入是同步请求 + 进度轮询（照 mysql 导入的口径），不是 SSE —— 见 api/tool/es/import_progress.go。
// 进度放在弹框里且**关掉弹框不中断导入**（后台静默跑），面板上留「查看导入进度」入口随时回来看。
window.EsImportTab = {
  props: ['conn'],
  template: `
<div class="es-imp-wrap" id="es-imp-drop" data-file-drop-target>
  <div class="es-imp-head">
    <div class="es-imp-row">
      <span class="es-imp-label">数据文件</span>
      <el-button size="small" :disabled="importing" @click="pickFile">选择文件</el-button>
      <span class="es-imp-path mono" :title="fileTitle">{{ fileLabel || '未选择（可多选，或把文件拖到面板上）' }}</span>
      <span class="es-imp-hint" v-if="!desktop">仅桌面端可用</span>
    </div>
    <div class="es-imp-row">
      <span class="es-imp-label">目标索引</span>
      <el-input v-model="index" size="small" class="es-imp-short" placeholder="logs-2026.10" :disabled="importing" />
      <el-checkbox v-model="createIndex" :disabled="importing">不存在时创建</el-checkbox>
      <!-- 输入索引名的即时校验（防抖 400ms）：存在与否直接决定「追加还是新建」 -->
      <span class="es-imp-hint" v-if="indexExists === true">索引已存在，数据将追加</span>
      <span class="es-imp-hint is-warn" v-else-if="indexExists === false && !createIndex">索引不存在且未勾选「不存在时创建」，写入会失败</span>
    </div>
    <!-- 包内过滤：只在选中压缩包时出现（日志包常把 access / error 混放，格式只认其中一类） -->
    <div class="es-imp-row" v-if="isArchive">
      <span class="es-imp-label">包内过滤</span>
      <el-input v-model="filter" size="small" class="es-imp-short" placeholder="如 *access*.log" :disabled="importing" />
      <span class="es-imp-hint">按文件名匹配：含 * ? 用通配符，否则按包含；留空 = 导入全部</span>
    </div>
    <!-- 日志格式说明（可留空）：不填时 AI 只能望着样例猜字段名，容易「凭空想象」；
         贴 logback / nginx log_format 这类格式定义后，「变量 ↔ 字段」的对应就有了权威依据 -->
    <div class="es-imp-row is-block">
      <span class="es-imp-label">日志格式</span>
      <el-input v-model="hint" type="textarea" :rows="2" size="small" maxlength="4000"
        placeholder="可留空。贴日志格式定义（nginx log_format / logback pattern）或补充说明——字段命名与拆分将以其为准；不填则完全按样例推断"
        :disabled="importing" />
    </div>
  </div>

  <div class="es-imp-foot">
    <el-button type="primary" :loading="analyzing" :disabled="importing || !sources.length" @click="doAnalyze">检查</el-button>
    <!-- 预览只在检查出结果之后出现：没出结果就没东西可核实 -->
    <el-button v-if="analyzed" :loading="previewing" :disabled="importing" @click="doPreview">预览</el-button>
    <!-- 后台静默导入的唯一入口：关掉进度弹框后从这里回来（开始导入在预览弹框里） -->
    <el-button v-if="importing && !showProgress" type="warning" plain @click="showProgress = true">
      查看导入进度
    </el-button>
  </div>

  <!-- 检查日志：SSE 按步骤追加（文件类型 → 抽样 → AI 生成），时间 + 状态标记 + 结果，
       像流水线日志一样一眼看清处理到哪一步 -->
  <div class="es-imp-analyze" v-if="analyzing || logs.length">
    <div class="es-imp-sub">检查日志</div>
    <div class="es-imp-analyze-body">
      <div class="mono es-imp-analyze-line" :class="['is-' + l.kind, { 'is-sub': l.sub }]" v-for="(l, i) in logs" :key="i">
        <span class="es-imp-log-time" v-if="!l.sub">{{ l.time }}</span>
        <span class="es-imp-log-mark" v-if="!l.sub">{{ logMark(l.kind) }}</span>
        <span class="es-imp-log-text">{{ l.text }}</span>
      </div>
      <pre class="mono es-imp-analyze-draft" v-if="aiDraft">{{ aiDraft }}</pre>
      <div class="es-imp-analyze-wait" v-if="analyzing">…</div>
    </div>
  </div>

  <!-- 预览弹框：只有两栏 —— 左「字段定义」（可改类型） / 右「解析预览」（前 20 行，grok 卡片式） -->
  <el-dialog v-model="showPreview" title="导入预览" width="80%" top="10vh" append-to-body
    :close-on-click-modal="false" class="es-imp-preview">
    <div class="es-imp-pv-body" v-if="preview">
      <div class="es-imp-pv-col is-fields">
        <div class="es-imp-sub">字段定义（第二列填目标名会替换第一列；类型用于建索引与写入转换）</div>
        <el-input v-model="fieldKw" size="small" clearable prefix-icon="Search" placeholder="搜索字段..." />
        <el-table :data="fieldRows" size="small" max-height="calc(80vh - 280px)" class="es-imp-table" empty-text="没有识别到字段">
          <el-table-column label="系统生成字段" min-width="150">
            <template #default="{ row }">
              <span class="mono" :title="row.name" :class="{ 'is-date': row.type === 'date' }">{{ row.name }}</span>
            </template>
          </el-table-column>
          <el-table-column label="映射字段" min-width="140">
            <template #default="{ row }">
              <el-input v-model="renames[row.name]" size="small" clearable placeholder="不改名" />
            </template>
          </el-table-column>
          <el-table-column label="类型" width="136">
            <template #default="{ row }">
              <el-select v-model="types[row.name]" size="small" :class="{ 'is-date': row.type === 'date' }">
                <el-option v-for="t in (preview.type_options || [])" :key="t" :value="t" :label="t" />
              </el-select>
            </template>
          </el-table-column>
        </el-table>
      </div>
      <div class="es-imp-pv-col is-rows">
        <div class="es-imp-sub">解析预览（前 {{ (preview.rows || []).length }} 行 · 最多 20 行）</div>
        <div class="es-imp-pv-rows">
          <div v-for="r in (preview.rows || [])" :key="r.no" class="es-grok-card" :class="rowClass(r)">
            <div class="es-grok-card-head">
              <span class="es-grok-no mono">#{{ r.no }}</span>
              <span class="es-imp-pv-origin mono" v-if="r.origin" :title="r.origin">{{ r.origin }}</span>
              <span class="es-grok-badge">{{ rowLabel(r) }}</span>
              <span class="es-grok-raw mono">{{ r.raw }}</span>
              <el-button v-if="(r.fields || []).length" text size="small" @click="copyRow(r)">复制字段</el-button>
            </div>
            <div class="es-grok-fields" v-if="(r.fields || []).length">
              <div v-for="f in r.fields" :key="f.name" class="es-grok-frow">
                <span class="es-grok-fname mono">{{ f.name }}</span>
                <span class="es-grok-fval mono">{{ f.value }}</span>
                <span class="es-grok-ftype" v-if="f.type">{{ f.type }}</span>
              </div>
            </div>
            <div class="es-grok-tip is-miss" v-else-if="r.reason">{{ r.reason }}</div>
          </div>
          <div v-if="!(preview.rows || []).length" class="es-lc-empty-hint">没有可展示的行</div>
        </div>
      </div>
    </div>
    <template #footer>
      <el-button @click="showPreview = false">关 闭</el-button>
      <!-- 重建导入：索引已存在时才有意义（删除既有索引 + 用当前规则重新导入） -->
      <el-button v-if="preview && preview.index_exists" type="danger" plain :disabled="importing" @click="reimport">
        删除索引重建导入
      </el-button>
      <el-button type="primary" :disabled="importing" @click="importFromPreview">开始导入</el-button>
    </template>
  </el-dialog>

  <!-- 进度抽屉：关掉 = 转后台继续导，不中断；逐文件清单（完成打勾 / 当前转圈）看处理到哪了 -->
  <el-drawer v-model="showProgress" title="导入进度" size="440px"
    :close-on-click-modal="false" class="es-imp-progress-drawer">
    <div class="es-imp-hint">关闭抽屉不会中断导入，可随时点「查看导入进度」回来看</div>
    <!-- 文件清单：多文件 / 压缩包多条目时逐个打勾；当前处理中的转圈 -->
    <div class="es-imp-filelist" v-if="progressFiles.length">
      <div class="es-imp-file" v-for="(f, i) in progressFiles" :key="i" :class="'is-' + (fileState(i) || 'pending')">
        <span class="es-imp-file-mark">
          <template v-if="fileState(i) === 'done'">✓</template>
          <el-icon v-else-if="fileState(i) === 'active'" class="is-loading"><Loading /></el-icon>
          <template v-else>·</template>
        </span>
        <span class="mono es-imp-file-name" :title="f">{{ f }}</span>
      </div>
    </div>
    <div class="es-imp-progress" v-if="importing">
      <div class="es-imp-bar"><span></span></div>
      <div class="es-imp-progress-meta" v-if="progress">
        <span>已读 <b>{{ fmtNum(progress.read) }}</b></span>
        <span>已写入 <b>{{ fmtNum(progress.indexed) }}</b></span>
        <span v-if="progress.skipped">跳过 <b>{{ fmtNum(progress.skipped) }}</b></span>
        <span v-if="progress.origin">当前 {{ progress.origin }}</span>
        <span>{{ fmtMs(progress.elapsed_ms) }}</span>
      </div>
      <div class="es-imp-hint" v-else>正在等待服务端返回进度…</div>
    </div>
    <div class="es-imp-result" v-if="result">
      <span class="is-ok">导入完成</span>
      读取 <b>{{ fmtNum(result.read) }}</b> 条 · 写入 <b>{{ fmtNum(result.indexed) }}</b> 条
      <span v-if="result.skipped" class="is-warn"> · 跳过 {{ fmtNum(result.skipped) }}</span>
      <span v-if="result.failed" class="is-bad"> · 失败 {{ fmtNum(result.failed) }}</span>
      <span> · 用时 {{ fmtMs(result.elapsed_ms) }}</span>
      <div class="es-imp-hint" v-if="result.recreated">已删除原索引并按当前字段类型重建</div>
      <!-- 写入核实：每 5 秒查一次目标索引计数（最长 1 分钟），对齐「导入前基数 + 本次写入」 -->
      <div class="es-imp-hint" v-if="verify">
        <span v-if="verify.done && verify.over" class="is-warn">索引计数 {{ fmtNum(verify.current) }} 多于期望 {{ fmtNum(verify.expected) }}（可能有历史数据或重复写入，请核查）</span>
        <span v-else-if="verify.done" class="is-ok">✓ 索引计数核实一致：{{ fmtNum(verify.current) }} 条全部可查</span>
        <span v-else>索引计数核实中：{{ verify.current == null ? '—' : fmtNum(verify.current) }} / 期望 {{ fmtNum(verify.expected) }}（每 5 秒刷新一次，最长 1 分钟）</span>
      </div>
    </div>
    <!-- 跳过样例 + 补救动作：漏网行可直接喂回 AI 重新生成 pattern，再删除重建导入 -->
    <div class="es-imp-detail" v-if="result && (result.skip_samples || []).length">
      <div class="es-imp-sub is-warn">被跳过的记录示例（{{ result.skip_samples.length }} 条，可用于重新生成 pattern）</div>
      <pre v-for="(s, i) in result.skip_samples" :key="i">{{ s }}</pre>
    </div>
    <div class="es-imp-error" v-if="error"><b>导入失败</b><pre>{{ error }}</pre></div>
    <div class="es-imp-remedy" v-if="result">
      <el-button v-if="(result.skip_samples || []).length" size="small" @click="refineFromSkips">用跳过样本重新生成 pattern</el-button>
      <el-button size="small" type="danger" plain :disabled="importing" @click="reimport">重新导入（删除索引重建）</el-button>
    </div>
    <div class="es-imp-drawer-actions">
      <el-button v-if="importing" @click="cancel">取消导入</el-button>
      <el-button type="primary" @click="showProgress = false">{{ importing ? '后台运行' : '关 闭' }}</el-button>
    </div>
  </el-drawer>

  <!-- 跳过超阈值暂停：导入已挂起（不再读写），等用户决定继续还是重新生成。
       65% 宽 + 上下各留 15%，跳过的 20 条样例在弹框内部滚动，不把弹框撑出屏幕 -->
  <el-dialog v-model="pauseAsk" title="跳过数量超过 20 条，导入已暂停" width="65%" top="15vh" append-to-body
    :close-on-click-modal="false" class="es-imp-pause-dlg" @close="onPauseClosed">
    <div class="es-imp-hint">已跳过 {{ fmtNum(pauseInfo && pauseInfo.skipped) }} 条记录（超过 20 条阈值），导入暂停中（不再读写）。</div>
    <div class="es-imp-hint">选择「重新生成」：用这 20 条被跳过的 + 文件抽样的 20 条合并重新生成解析规则，然后<b>删除当前索引重新写入</b>。</div>
    <div class="es-imp-detail" v-if="pauseInfo && (pauseInfo.skip_samples || []).length">
      <div class="es-imp-sub is-warn">被跳过的记录示例（最多 20 条）</div>
      <pre v-for="(s, i) in pauseInfo.skip_samples" :key="i">{{ s }}</pre>
    </div>
    <template #footer>
      <el-button @click="pauseDecision('continue')">继续导入（忽略）</el-button>
      <el-button type="primary" @click="pauseDecision('refine')">重新生成并删除重建</el-button>
    </template>
  </el-dialog>

  <div class="es-imp-result" v-if="result">
    <span class="is-ok">导入完成</span>
    读取 <b>{{ fmtNum(result.read) }}</b> 条 · 写入 <b>{{ fmtNum(result.indexed) }}</b> 条
    <span v-if="result.partial"> · 部分命中 {{ fmtNum(result.partial) }}</span>
    <span v-if="result.skipped" class="is-warn"> · 跳过 {{ fmtNum(result.skipped) }}</span>
    <span v-if="result.failed" class="is-bad"> · 失败 {{ fmtNum(result.failed) }}</span>
    <span> · 用时 {{ fmtMs(result.elapsed_ms) }}</span>
    <div class="es-imp-hint" v-if="result.created">已新建索引 {{ result.index }} 并按确认过的类型建好 mapping</div>
  </div>

  <div class="es-imp-detail" v-if="result && (result.failures || []).length">
    <div class="es-imp-sub is-bad">写入失败的条目{{ result.failures_truncated ? '（更多已省略）' : '' }}</div>
    <pre v-for="(f, i) in result.failures" :key="i">{{ f }}</pre>
  </div>

  <div class="es-imp-detail" v-if="result && (result.dropped_fields || []).length">
    <div class="es-imp-sub is-warn">按声明类型转不掉、被丢掉的字段</div>
    <div class="es-imp-hint">{{ result.dropped_fields.join('、') }}</div>
  </div>

  <div class="es-imp-error" v-if="error"><b>导入失败</b><pre>{{ error }}</pre></div>
</div>`,
  data() {
    return {
      sources: [], index: '', createIndex: true, filter: '', hint: '',
      // pattern 由「检查」自动生成后留在这里（无手工编辑入口），随预览/导入送回后端
      patterns: [], definitions: {},
      previewing: false, preview: null, types: {}, renames: {}, fieldKw: '',
      analyzing: false, analyzed: false, logs: [], aiDraft: '',
      showPreview: false,
      indexExists: null, // null = 还没查过；true/false = 即时校验的结果
      importing: false, showProgress: false, progress: null, result: null, error: '',
      verify: null, // 导入完成后的计数核实：{ expected, current, done, over, tries }
      pauseAsk: false, pauseInfo: null, // 跳过超阈值暂停：决策弹框与现场信息
      timer: null, key: ''
    }
  },
  watch: {
    // 输入目标索引就即时校验存在性：等到预览/真跑才发现 already exists 太晚了。
    // 防抖 400ms —— 每敲一个字符打一发请求太浪费。
    index(v) {
      // 用户手输过之后就不再被「按文件名自动填」覆盖（自动填的值带 _lastAuto 标记）
      if (v !== this._lastAuto) this._indexAuto = false
      if (this._existsTimer) clearTimeout(this._existsTimer)
      this._existsTimer = setTimeout(() => this.checkExists(), 400)
    },
    // 改包内过滤 / 日志格式说明 = 输入变了，旧的分析/预览结果立刻作废
    filter() { this.resetAnalysis() },
    hint() { this.resetAnalysis() }
  },
  computed: {
    desktop() { return !!window.__INFRA_DESKTOP__ },
    typeRows() {
      const t = this.types || {}
      return Object.keys(t).sort().map(name => ({ name: name, type: t[name] }))
    },
    // fieldRows 字段定义表的搜索视图：字段多时（上百个）靠它快速定位，不改动 types 本身
    fieldRows() {
      const k = (this.fieldKw || '').trim().toLowerCase()
      if (!k) return this.typeRows
      return this.typeRows.filter(r => r.name.toLowerCase().includes(k))
    },
    // fileLabel 数据文件行：单文件显示全路径，多文件显示「N 个文件：name1、name2 …」
    fileLabel() {
      const ss = this.sources || []
      if (!ss.length) return ''
      if (ss.length === 1) return ss[0]
      const names = ss.slice(0, 3).map(p => this.baseName(p))
      return '共 ' + ss.length + ' 个文件：' + names.join('、') + (ss.length > 3 ? ' 等' : '')
    },
    fileTitle() { return (this.sources || []).join('\n') || '未选择' },
    // isArchive 选中的文件里有没有压缩包（决定「包内过滤」行显不显示；按扩展名判断即可）
    isArchive() {
      return (this.sources || []).some(p => /\.(zip|tar\.gz|tgz)$/i.test(String(p)))
    },
    // progressFiles 进度抽屉的文件清单（后端随进度下发：压缩包看条目、多选看各文件）
    progressFiles() { return (this.progress && this.progress.files) || [] }
  },
  mounted() {
    // 拖倒入库：Wails 原生捕获窗口文件拖放 → Go 转发 infra:files-dropped（见 wails-shim.js）
    const drop = window.__INFRA_DROP__
    if (drop) this._unsubDrop = drop.on((data) => this.onFilesDropped(data))
  },
  beforeUnmount() {
    this.stopPolling()
    this.stopVerifyCount()
    if (this._existsTimer) { clearTimeout(this._existsTimer); this._existsTimer = null }
    if (this._analyzeStream) { try { this._analyzeStream.close() } catch (e) { /* 已结束 */ } this._analyzeStream = null }
    if (this._unsubDrop) { try { this._unsubDrop() } catch (e) { /* */ } this._unsubDrop = null }
    if (this.key) this.cancel() // 面板关掉就别把导入继续挂着
  },
  methods: {
    base() { return '/es/' + this.conn.id },
    fmtNum(n) { return (n == null ? 0 : n).toLocaleString('en-US') },
    // ---- 检查日志的行样式：时间 + 状态标记 + 文案；sub 为子行（缩进、不重复时间/标记）----
    nowHM() {
      const d = new Date(), p = (n) => String(n).padStart(2, '0')
      return p(d.getHours()) + ':' + p(d.getMinutes()) + ':' + p(d.getSeconds())
    },
    logMark(kind) {
      return kind === 'ok' ? '✓' : (kind === 'warn' ? '!' : (kind === 'err' ? '✗' : '·'))
    },
    pushLine(text, kind, sub) {
      if (!text) return
      this.logs.push({ time: sub ? '' : this.nowHM(), kind: kind || 'info', text: String(text), sub: !!sub })
    },
    // 逐行预览的三态与「Grok 解析」页签同一套视觉：整行命中 / 部分命中 / 未命中
    rowClass(r) {
      return r.status === 'matched' ? 'is-ok' : (r.status === 'partial' ? 'is-part' : 'is-miss')
    },
    rowLabel(r) {
      return r.status === 'matched' ? '整行命中' : (r.status === 'partial' ? '部分命中' : '未命中')
    },
    copyRow(r) {
      const obj = {}
      const fs = r.fields || []
      fs.forEach(f => { obj[f.name] = f.value })
      const text = JSON.stringify(obj, null, 2)
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text).then(
          () => this.$message.success('字段 JSON 已复制'),
          () => this.$message.warning('复制失败，请手动选择字段值'))
      } else {
        this.$message.warning('当前环境不支持直接复制，请手动选择字段值')
      }
    },
    fmtMs(ms) {
      if (!ms) return '0s'
      return ms < 1000 ? ms + 'ms' : (ms / 1000).toFixed(1) + 's'
    },
    fmtSize(n) {
      if (!n) return '0 B'
      const u = ['B', 'KB', 'MB', 'GB', 'TB']
      let i = 0
      let v = n
      while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
      return (i === 0 ? v : v.toFixed(1)) + ' ' + u[i]
    },
    esc(s) {
      return String(s == null ? '' : s).replace(/[&<>"]/g, c => (
        { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]
      ))
    },
    // payload 构造请求体；confirmed/key 只有真跑时才带上。
    // 文档 _id 一律由 ES 自动生成：按 id 覆盖做幂等导入的场景目前没有，界面不再暴露。
    payload(confirmed, key, recreate) {
      return {
        sources: this.sources,
        index: this.index.trim(),
        create_index: this.createIndex || !!recreate,
        recreate_index: !!recreate,
        types: (this.createIndex || recreate) ? this.types : {},
        renames: this.renames || {},
        patterns: this.patterns || [],
        definitions: this.definitions || {},
        filter: (this.filter || '').trim(),
        confirmed: !!confirmed,
        progress_key: key || ''
      }
    },
    // deriveIndex 按所选文件名生成目标索引名：去扩展名（含 .tar.gz 这类双重后缀）→
    // 小写 → 非法字符替换为 '-'。生成不出合法名（全非法字符）时返回空串，交给用户手填。
    deriveIndex(p) {
      let name = String(this.baseName(p) || '').toLowerCase()
      const exts = ['.log', '.jsonl', '.ndjson', '.json', '.txt', '.gz', '.zip', '.tar']
      let changed = true
      while (changed) {
        changed = false
        for (const ext of exts) {
          if (name.endsWith(ext)) { name = name.slice(0, -ext.length); changed = true }
        }
      }
      name = name.replace(/[^a-z0-9\-_+.]/g, '-').replace(/-+/g, '-').replace(/^[-_+.]+/, '').replace(/[-_+]+$/, '')
      if (!name || name === '.' || name === '..' || name.length > 255) return ''
      return name
    },
    baseName(p) { return String(p == null ? '' : p).split(/[\\/]/).pop() },
    // resetAnalysis 分析产物整体作废（换文件 / 改包内过滤时用）：检查日志、pattern、预览全清
    resetAnalysis() {
      this.preview = null
      this.showPreview = false
      this.analyzed = false
      this.logs = []
      this.aiDraft = ''
      this.patterns = []
      this.definitions = {}
      this.renames = {}
    },
    // acceptFiles 接住一次选择（对话框多选 / 拖拽）的结果：整体替换所选文件并复位分析态
    acceptFiles(paths) {
      const list = (paths || []).map(p => String(p == null ? '' : p).trim()).filter(Boolean)
      if (!list.length) return
      this.sources = list
      // 目标索引自动按文件名填（用户手输过就不再覆盖；空着的顺手补上）
      if (!this.index.trim() || this._indexAuto) {
        const auto = this.deriveIndex(list[0])
        if (auto) {
          this._lastAuto = auto
          this._indexAuto = true
          this.index = auto
        }
      }
      this.result = null
      this.error = ''
      this.resetAnalysis()
    },
    async pickFile() {
      if (!this.desktop) { this.$message.warning('从本地文件导入需要在桌面端使用'); return }
      try {
        const picked = await window.wails.Call.ByName(
          'infra-ops/desktop.FileService.PickImportFiles', '选择要导入的数据文件（可多选）')
        this.acceptFiles(picked)
      } catch (e) { this.$message.error((e && e.message) || '选择文件失败') }
    },
    // onFilesDropped 拖拽投递：只认落在导入面板（id=es-imp-drop）上的文件 ——
    // 面板外（预览弹框、其它页面）没有 data-file-drop-target，elementId 对不上就忽略
    onFilesDropped(data) {
      if (!this.desktop || this.importing) return
      if ((data && data.elementId) !== 'es-imp-drop') return
      this.acceptFiles((data && data.files) || [])
    },
    // doAnalyze 「检查」：按步骤跑一条 SSE 流水线 —— 文件类型 → 抽样 → 调用 AI 生成 pattern。
    // 每步落一行带时间与状态标记的日志；done(ready=true) 出结果后「预览」按钮才出现。
    async doAnalyze() {
      if (this.analyzing || this.importing) return
      if (!this.sources.length) { this.$message.warning('请先选择要导入的文件'); return }
      const sse = window.__INFRA_SSE__
      if (!sse) { this.$message.error('流式通道不可用：请在桌面应用中运行'); return }
      this.analyzing = true
      this.analyzed = false
      this.logs = []
      this.aiDraft = ''
      this._draft = []
      // 补救生成（用跳过样本重新生成）一次性生效：取走后清空，下次手动「检查」不再重复携带
      const extra = this._extraSamples || []
      this._extraSamples = []
      const body = {
        sources: this.sources,
        index: this.index.trim(),
        filter: (this.filter || '').trim(),
        hint: (this.hint || '').trim(),
        extra_samples: extra,
        definitions: this.definitions || {}
      }
      const log = (t, kind, sub) => this.pushLine(t, kind, sub)
      this._analyzeStream = sse.post('/api/es/' + this.conn.id + '/import/analyze', body, {
        onEvent: (type, data) => {
          let ev = {}
          try { ev = JSON.parse(data) } catch (e) { return }
          if (type === 'log') { log(ev.text, ev.kind, ev.sub); return }
          if (type === 'delta') {
            // AI 生成的流式草稿：边生成边看（正式结果在 final 帧落进 this.patterns）
            if (ev.field === 'patterns') {
              const idx = Math.max(0, ev.index || 0)
              while (this._draft.length <= idx) this._draft.push('')
              this._draft[idx] += ev.text || ''
              this.aiDraft = this._draft.filter(s => s.trim()).join('\n')
            }
            return
          }
          if (type === 'phase') {
            log('首稿整行命中 ' + (ev.matched || 0) + '/' + (ev.total || 0) + '，服务端正按样例修正…', 'warn')
            this._draft = []; this.aiDraft = ''
            return
          }
          if (type === 'error') { this._chainReimport = false; log('AI 生成失败：' + (ev.message || '未知错误'), 'err'); return }
          if (type === 'final') {
            this._draft = []; this.aiDraft = ''
            const pats = ev.patterns || []
            const v = ev.verify || {}
            const hit = (v.matched || 0) + '/' + (v.total || 0)
            if (v.ok) {
              log('AI 处理完成：样例整行命中 ' + hit + '，本次抽样全部通过'
                + (v.adopted_retry ? '（服务端已按失败样例修正一次）' : ''), 'ok')
            } else {
              log('AI 处理完成：样例整行命中 ' + hit + '，有 ' + (v.missed || 0) + ' 行未命中（预览里会列出）', 'warn')
            }
            // pattern 不再打进日志（用户口径：去掉输出的正则）；结果只在预览的逐行解析里体现
            this.patterns = pats
            this.definitions = ev.definitions || {}
            return
          }
          if (type === 'done') {
            if (ev.ready === false) {
              this._chainReimport = false // 补救链断在这里：没生成出 pattern 就重建没有意义
              log('分析未完成：没有生成可用的解析 pattern，暂时无法预览', 'err')
              return
            }
            log('分析完成，点击「预览」查看结果', 'ok')
            this.analyzed = true
            // 暂停补救链：重新生成完成后直接删除索引重建重写（暂停弹框里已明确告知）
            if (this._chainReimport) {
              this._chainReimport = false
              this.start(true)
            }
          }
        },
        onEnd: () => { this.analyzing = false; this._analyzeStream = null },
        onError: (err) => {
          this.analyzing = false
          this._analyzeStream = null
          log('分析中断：' + ((err && err.message) || '连接错误'), 'err')
        }
      })
    },
    async doPreview() {
      if (this.previewing || this.importing) return
      if (!this.index.trim()) { this.$message.warning('请先填写目标索引名'); return }
      this.previewing = true
      this.error = ''
      try {
        const r = await api.post(this.base() + '/import/preview', this.payload(false, ''))
        if (!r || r.code !== 0) { this.error = (r && r.message) || '预览失败'; return }
        this.preview = r.data || {}
        // 类型与字段映射都是可编辑的副本：直接改服务端回的对象，重跑预览时看不出差异
        this.types = Object.assign({}, this.preview.types || {})
        this.renames = {}
        // 索引已存在时自动关掉「不存在时创建」：不关的话真跑那一轮 PUT 必报 already exists
        if (this.preview.index_exists) this.createIndex = false
        this.indexExists = !!this.preview.index_exists // 与输入框的即时校验共用一个提示位
        this.result = null
        this.showPreview = true
      } catch (e) { /* 拦截器已提示 */ } finally { this.previewing = false }
    },
    // checkExists 即时校验目标索引是否存在。seq 只认最后一次输入的结果：
    // 网络回包乱序时，旧输入的回应不能覆盖新输入的结论。
    async checkExists() {
      const idx = this.index.trim()
      if (!idx || this.importing) { this.indexExists = null; return }
      const seq = (this._existsSeq = (this._existsSeq || 0) + 1)
      try {
        const r = await api.get(this.base() + '/import/exists', { params: { index: idx } })
        if (seq !== this._existsSeq) return
        this.indexExists = !!(r && r.code === 0 && r.data && r.data.exists)
        // 与预览同一条规则：已存在还勾着「不存在时创建」，真跑那轮 PUT 必报 already exists
        if (this.indexExists && this.createIndex) this.createIndex = false
      } catch (e) {
        if (seq === this._existsSeq) this.indexExists = null // 查不到就静默回到「未知」，别打扰输入
      }
    },
    importFromPreview() {
      this.showPreview = false
      this.start()
    },
    async confirmImport(d) {
      const ss = d.sources || []
      const fileText = ss.length > 1
        ? ss.length + ' 个文件（' + ss.slice(0, 3).map(p => this.esc(this.baseName(p))).join('、')
          + (ss.length > 3 ? ' 等' : '') + '）'
        : this.esc(ss[0] || '')
      // 时间字段单独摆出来：它是导入后建数据视图最先要选的字段，识别错了在这一步就能发现
      // （types 是预览弹框里用户改过的副本，即真正会用于建 mapping / 写入转换的类型）
      const dateFields = Object.keys(this.types || {}).filter(k => this.types[k] === 'date')
      const rows = [
        '文件：<b>' + fileText + '</b>（' + this.fmtSize(d.size) + '）',
        '目标索引：<b>' + this.esc(d.index) + '</b>' + (d.create_index && !d.index_exists ? '（将新建）' : '（已存在，数据追加）'),
        '提取规则：' + (this.patterns.length
          ? this.patterns.length + ' 条 pattern'
          : '整段文本存进 message 字段'),
        '时间字段：' + (dateFields.length
          ? '<b>' + dateFields.map(f => this.esc(f)).join('、') + '</b>'
          : '未识别到（可在预览里把时间字段的类型改为 date）')
      ]
      try {
        await this.$confirm(rows.join('<br>'), '确认导入', {
          type: 'warning', dangerouslyUseHTMLString: true, customClass: 'es-imp-confirm',
          confirmButtonText: '开始导入', cancelButtonText: '取消'
        })
        return true
      } catch (e) { return false }
    },
    // start 导入主流程。recreate=true 表示「删除索引重建再导入」——入口处已经弹过
    // 不可撤销的确认，这里不再重复第二遍 need_confirm。
    async start(recreate) {
      if (this.importing) return
      if (!this.sources.length) { this.$message.warning('请先选择要导入的文件'); return }
      if (!this.index.trim()) { this.$message.warning('请先填写目标索引名'); return }
      this.error = ''
      this.result = null
      this._pauseDecided = false
      this.pauseAsk = false
      this.pauseInfo = null
      this.stopVerifyCount(); this.verify = null

      // 先探一次：服务端回文件信息与索引是否存在，也可能直接拒绝（文件不可读等）
      let pre
      try {
        pre = await api.post(this.base() + '/import', this.payload(false, '', recreate))
      } catch (e) { this.error = this.errText(e, '导入前检查失败'); return }
      if (!pre || pre.code !== 0) { this.error = (pre && pre.message) || '导入前检查失败'; return }
      if (pre.data && pre.data.need_confirm && !recreate && !(await this.confirmImport(pre.data))) return

      this.importing = true
      this.progress = null
      this.showProgress = true // 进度弹框自动弹出；关掉不中断，面板上有「查看导入进度」入口
      this.key = 'imp-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 8)
      this.startPolling(this.key)
      try {
        // timeout: 0 —— 导入耗时由数据量决定，默认 15 秒必定不够
        const r = await api.post(this.base() + '/import', this.payload(true, this.key, recreate), { timeout: 0 })
        if (!r || r.code !== 0) {
          if (!this._pendingRefine) this.error = (r && r.message) || '导入失败'
          return
        }
        this.result = r.data || {}
        this.startVerifyCount()
      } catch (e) {
        if (!this._pendingRefine) this.error = this.errText(e, '导入失败')
      } finally {
        this.stopPolling()
        this.importing = false
        this.key = ''
        // 暂停时选了「重新生成」：中止响应回来后静默接上补救链（重新生成 → 自动删除重建）
        if (this._pendingRefine) {
          this._pendingRefine = false
          this._chainReimport = true
          this.refineFromSkips()
        }
      }
    },
    // startVerifyCount 导入完成后的写入核实：每 5 秒查一次目标索引计数，最长 1 分钟。
    // 期望值 = 导入前文档数 + 本次写入数（新建/重建时 before=0）；ES 写入是近实时的，
    // 刚返回的 _bulk 回执不代表立刻可查，用计数收口才算「全部写入」。
    startVerifyCount() {
      this.stopVerifyCount()
      if (!this.result) return
      const expected = (this.result.count_before || 0) + (this.result.indexed || 0)
      this.verify = { expected: expected, current: null, done: false, tries: 0 }
      const tick = async () => {
        if (!this.verify || this.verify.done) return
        this.verify.tries++
        try {
          const r = await api.get(this.base() + '/import/count', { params: { index: this.result.index } })
          if (r && r.code === 0 && r.data) this.verify.current = r.data.count
        } catch (e) { /* 计数是旁路信息：取不到就下次再试 */ }
        if (this.verify.current != null && this.verify.current >= this.verify.expected) {
          this.verify.done = true
          // 计数多于期望：可能有历史数据残留或重复写入，如实标记（不静默当成功）
          this.verify.over = this.verify.current > this.verify.expected
          this.stopVerifyCount()
          return
        }
        if (this.verify.tries >= 12) this.stopVerifyCount() // 12 × 5s = 1 分钟
      }
      tick()
      this._verifyTimer = setInterval(tick, 5000)
    },
    stopVerifyCount() {
      if (this._verifyTimer) { clearInterval(this._verifyTimer); this._verifyTimer = null }
    },
    // refineFromSkips 「用跳过样本重新生成 pattern」：把跳过原文附进检查请求，
    // 与文件里抽的 20 行样例合并（共 40 行）重新生成，让漏网行的结构也进提示词。
    // 样例来源优先取暂停现场的（_pauseSamples），否则取上次导入结果里的。
    refineFromSkips() {
      const skips = (this._pauseSamples && this._pauseSamples.length)
        ? this._pauseSamples
        : ((this.result && this.result.skip_samples) || [])
      this._pauseSamples = null
      if (!skips.length) return
      this._extraSamples = skips.slice()
      this.showProgress = false
      this.showPreview = false
      this.doAnalyze()
    },
    // reimport 「重新导入（删除索引重建）」：不可逆操作，先弹明确的风险确认；
    // 确认后带着当前 pattern / 字段类型走重建导入，不再弹第二遍确认。
    async reimport() {
      if (this.importing) return
      const idx = this.index.trim()
      if (!idx) { this.$message.warning('请先填写目标索引名'); return }
      try {
        await this.$confirm(
          '将删除索引 <b>' + this.esc(idx) + '</b> 及其全部数据，然后用当前的 pattern 与字段类型重新导入。<br>此操作不可撤销。',
          '删除并重新导入', {
            type: 'warning', dangerouslyUseHTMLString: true, customClass: 'es-imp-confirm',
            confirmButtonText: '删除并重新导入', cancelButtonText: '取消'
          })
      } catch (e) { return } // 用户取消
      this.showPreview = false
      this.showProgress = false
      this.start(true)
    },
    errText(e, fallback) {
      if (e && e.friendly) return e.friendly
      if (e && e.message) return e.message
      return fallback
    },
    startPolling(key) {
      this.stopPolling()
      this.timer = setInterval(async () => {
        try {
          const r = await api.get(this.base() + '/import/progress', { params: { key: key } })
          if (r && r.code === 0 && r.data && r.data.found) this.applyProgress(r.data.status)
        } catch (e) { /* 进度是旁路信息，取不到不影响导入本身 */ }
      }, 500)
    },
    // fileState 抽屉里的逐文件状态：done（打勾）/ active（转圈）/ pending（待处理）。
    // 依据后端下发的 file_step（正在读的序号，1-based）；导入结束后全部打勾。
    fileState(i) {
      const files = (this.progress && this.progress.files) || []
      if (!files.length) return ''
      const st = this.progress || {}
      if (this.result || st.phase === 'done') return 'done'
      const step = st.file_step || 0
      if (i + 1 < step) return 'done'
      if (i + 1 === step) return 'active'
      return 'pending'
    },
    // applyProgress 落进度；waiting=true（跳过超阈值挂起）就弹决策框（每次导入只弹一次）
    applyProgress(st) {
      this.progress = st
      if (st && st.waiting && !this._pauseDecided && !this.pauseAsk) {
        this.pauseInfo = { key: this.key, skipped: st.skipped, skip_samples: st.skip_samples || [] }
        this.pauseAsk = true
      }
    },
    // pauseDecision 暂停决策：continue = 继续导入（忽略）；refine = 先中止，
    // 再走补救链（跳过样例 + 文件抽样重新生成 → 自动删除索引重建重写）。
    async pauseDecision(action) {
      const info = this.pauseInfo
      this._pauseDecided = true
      this.pauseAsk = false
      this.pauseInfo = null
      if (!info || !info.key) return
      if (action === 'continue') {
        try {
          const r = await api.post(this.base() + '/import/resume', { key: info.key, action: 'continue' })
          if (r && r.code === 0) this.$message.info('已继续导入')
        } catch (e) { /* 拦截器已提示 */ }
        return
      }
      // refine：中止当前导入；start() 的 finally 识别 _pendingRefine 后静默接上补救链
      this._pauseSamples = (info.skip_samples || []).slice()
      this._pendingRefine = true
      try {
        await api.post(this.base() + '/import/resume', { key: info.key, action: 'abort' })
      } catch (e) { /* 中止请求失败也照走：用户还可以点「取消导入」兜底 */ }
    },
    onPauseClosed() {
      // 点 × / 因其它原因关闭：视为「继续导入」（不把导入无限挂住）
      if (!this._pauseDecided) this.pauseDecision('continue')
    },
    stopPolling() {
      if (this.timer) { clearInterval(this.timer); this.timer = null }
    },
    async cancel() {
      const key = this.key
      if (!key) return
      try {
        const r = await api.post(this.base() + '/import/cancel', { key: key })
        if (r && r.code === 0 && r.data && r.data.cancelled) this.$message.info('已发出取消信号，正在收尾…')
        else this.$message.warning('没有正在执行的导入任务')
      } catch (e) { /* 拦截器已提示 */ }
    }
  }
}
