// Redis 值预览抽屉：按类型展示一段内容（只读，不提供任何修改入口）。
// 后端已把内容截断（字符串 64KB、集合类 100 项），这里只负责摆版式。
//
// 字符串值若本身是 JSON，默认格式化 + 语法高亮，并可一键复制（复制的是格式化后的文本）；
// 集合类（list/set/zset/hash/stream）同样支持一键复制，导出为可读 JSON。
(function () {
  function escapeHtml(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
  }

  // 轻量 JSON 高亮：先转义 HTML，再按 token 包 span。键先于字符串匹配，避免 "a": 被当成普通串。
  const JSON_TOKEN = /("(?:\\.|[^"\\])*"\s*:)|("(?:\\.|[^"\\])*")|(\btrue\b|\bfalse\b)|(\bnull\b)|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)/g
  function highlightJson(s) {
    return escapeHtml(s).replace(JSON_TOKEN, (m, key, str, bool, nul) => {
      let cls = 'j-num'
      if (key) cls = 'j-key'
      else if (str) cls = 'j-str'
      else if (bool) cls = 'j-bool'
      else if (nul) cls = 'j-null'
      return '<span class="' + cls + '">' + m + '</span>'
    })
  }

  window.RedisValue = {
    props: {
      connId: { type: Number, required: true },
      db: { type: Number, default: 0 },
      keyName: { type: String, required: true }
    },
    emits: ['close'],
    template: `
<el-drawer :model-value="true" :title="keyName" size="640px" append-to-body @close="$emit('close')">
  <div class="redis-value" v-loading="loading">
    <div v-if="err" class="redis-error-box">{{err}}</div>

    <template v-else-if="detail">
      <div class="redis-value-meta">
        <span class="redis-type" :class="typeClass">{{detail.type || 'none'}}</span>
        <span class="redis-value-meta-item">TTL <b>{{ttlText}}</b></span>
        <span class="redis-value-meta-item">元素数 <b>{{fmt(detail.size)}}</b></span>
        <span class="redis-value-meta-item">耗时 <b>{{detail.elapsed_ms}} ms</b></span>
      </div>

      <empty-state v-if="detail.type === 'none'" text="这个 key 已经不存在了（可能已过期或被删除）" />

      <template v-else-if="detail.type === 'string'">
        <div class="redis-value-toolbar">
          <span class="redis-value-chip" :class="isJson ? 'redis-value-chip--json' : ''">{{ isJson ? 'JSON' : '文本' }}</span>
          <span class="redis-value-toolbar-spacer"></span>
          <el-button v-if="isJson" text size="small" @click="pretty = !pretty">
            <el-icon style="margin-right:4px"><MagicStick /></el-icon>{{ pretty ? '原始文本' : '格式化' }}
          </el-button>
          <el-button text size="small" @click="copyValue">
            <el-icon style="margin-right:4px"><CopyDocument /></el-icon>复制
          </el-button>
        </div>
        <pre class="redis-value-pre" :class="{'redis-value-pre--json': isJson && pretty}" v-html="stringHtml"></pre>
        <div v-if="detail.truncated" class="redis-value-note">
          内容超过 64 KB，这里只展示前 64 KB（原值 {{fmt(detail.size)}} 字节）。
        </div>
      </template>

      <template v-else>
        <div class="redis-value-toolbar">
          <span class="redis-value-chip">{{detail.type}}</span>
          <span class="redis-value-toolbar-spacer"></span>
          <el-button text size="small" @click="copyEntries">
            <el-icon style="margin-right:4px"><CopyDocument /></el-icon>复制
          </el-button>
        </div>
        <el-table :data="rows" size="small" class="redis-value-table" max-height="calc(100vh - 250px)">
          <el-table-column :label="cols[0]" width="130">
            <template #default="{row}"><span class="mono">{{row.a}}</span></template>
          </el-table-column>
          <el-table-column :label="cols[1]" min-width="220">
            <template #default="{row}"><span class="redis-value-text" :title="row.b">{{row.b}}</span></template>
          </el-table-column>
        </el-table>
        <div v-if="detail.truncated" class="redis-value-note">
          只展示前 {{rows.length}} 项，共 {{fmt(detail.size)}} 项。
        </div>
      </template>
    </template>
  </div>
</el-drawer>`,
    data() {
      return { loading: false, detail: null, err: '', pretty: true }
    },
    computed: {
      typeClass() { return window.RedisUtil.typeClass(this.detail?.type) },
      ttlText() { return window.RedisUtil.ttlText(this.detail?.ttl) },
      // 各类型的「第一列」含义不同：zset 是分数、hash 是字段、stream 是 ID、list 是序号。
      cols() {
        switch (this.detail?.type) {
          case 'zset': return ['分数', '成员']
          case 'hash': return ['字段', '值']
          case 'stream': return ['ID', '内容']
          case 'list': return ['序号', '元素']
          default: return ['序号', '元素']
        }
      },
      // 后端把「分数 / ID / 序号」都放在 rank，「字段」放在 field，这里合并成两列。
      rows() {
        const list = this.detail?.entries || []
        return list.map(e => ({ a: e.field || e.rank || '', b: e.value || '' }))
      },
      // 字符串值能被 JSON.parse 时给出格式化结果；否则空串表示按纯文本处理。
      jsonText() {
        if (this.detail?.type !== 'string') return ''
        const raw = (this.detail.preview || '').trim()
        if (!raw) return ''
        const c = raw[0]
        if (c !== '{' && c !== '[') return ''
        try { return JSON.stringify(JSON.parse(raw), null, 2) } catch (e) { return '' }
      },
      isJson() { return !!this.jsonText },
      stringHtml() {
        if (this.detail?.type !== 'string') return ''
        if (this.isJson && this.pretty) return highlightJson(this.jsonText)
        return escapeHtml(this.detail.preview)
      }
    },
    mounted() { this.load() },
    methods: {
      fmt(n) { return window.RedisUtil.fmtNum(n) },
      async load() {
        this.loading = true
        this.err = ''
        try {
          const r = await api.get('/redis/' + this.connId + '/key', {
            params: { key: this.keyName, db: this.db }
          })
          if (r.code === 0) this.detail = r.data
          else this.err = r.message || '读取失败'
        } catch (e) {
          this.err = window.RedisUtil.errText(e, '读取失败')
        } finally {
          this.loading = false
        }
      },
      copyValue() {
        const text = this.isJson && this.pretty ? this.jsonText : (this.detail?.preview || '')
        this.copyText(text)
      },
      // 集合类导出为可读 JSON：hash 是对象，其余按值数组。
      copyEntries() {
        const list = this.detail?.entries || []
        let payload
        if (this.detail?.type === 'hash') {
          payload = {}
          list.forEach(e => { payload[e.field] = e.value })
        } else {
          payload = list.map(e => e.value)
        }
        this.copyText(JSON.stringify(payload, null, 2))
      },
      copyText(text) {
        const done = () => ElMessage.success('已复制到剪贴板')
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
      }
    }
  }
})()