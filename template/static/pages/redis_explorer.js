// Redis key 浏览器：前缀模糊匹配 + SCAN 游标分页。
//
// 只读工具：整页没有任何写命令入口。
// 分页语义靠后端的「扫描会话」：查询开一份会话，之后「展示更多 / 展示全部」都带着 scan_id 继续推游标。
window.RedisExplorer = {
  components: { 'redis-value': window.RedisValue },
  props: { conn: { type: Object, required: true } },
  emits: ['exit'],
  template: `
<div class="redis-explorer">
  <div class="redis-bar">
    <div class="redis-bar-id">
      <span class="redis-bar-name">{{conn.name}}</span>
      <span class="mono redis-bar-addr">{{conn.username ? conn.username + '@' : ''}}{{conn.host}}:{{conn.port}}</span>
      <span v-if="conn.ssh_host_name" class="action-badge info">经 {{conn.ssh_host_name}}</span>
    </div>
    <div class="redis-bar-ops">
      <el-button text :loading="pinging" @click="testConn"><el-icon style="margin-right:4px"><Connection /></el-icon>测试连接</el-button>
      <el-button text @click="$emit('exit')"><el-icon style="margin-right:4px"><Back /></el-icon>返回列表</el-button>
    </div>
  </div>

  <div class="redis-search">
    <el-select v-model="db" class="redis-db" :disabled="loading" @change="onDbChange">
      <el-option v-for="d in dbs" :key="d.index" :label="dbLabel(d)" :value="d.index" />
    </el-select>
    <el-input v-model="prefix" class="redis-input" clearable :disabled="loading"
      placeholder="输入开头内容，例如 ops:" @keyup.enter="search">
      <template #prepend>前缀匹配</template>
    </el-input>
    <el-button type="primary" :loading="loading" :disabled="!prefix.trim()" @click="search">
      <el-icon style="margin-right:4px"><Search /></el-icon>查询
    </el-button>
  </div>
  <div class="redis-hint">
    实际下发 <code class="redis-code">{{pattern || '—'}}</code>，只匹配<strong>以该串开头</strong>的 key；
    输入里的 <code class="redis-code">* ? [ ]</code> 一律按字面量处理。留空会退化成 <code class="redis-code">KEYS *</code>，所以必须填写。
  </div>

  <div class="page-card tool-panel redis-result">
    <div class="card-header">
      <div class="tool-list-title">
        <span class="title">匹配结果</span>
        <span class="tool-list-sub">{{summary}}</span>
      </div>
      <div class="header-extra">
        <span v-if="elapsed" class="tool-list-sub">本次 {{elapsed}} ms</span>
      </div>
    </div>

    <el-table v-if="keys.length" class="redis-table" :data="keys" style="width:100%" size="small"
      :max-height="tableMaxHeight" v-loading="loading" @row-click="openValue">
      <el-table-column label="Key" min-width="320">
        <template #default="{row}"><span class="redis-key mono" :title="row.key">{{row.key}}</span></template>
      </el-table-column>
      <el-table-column label="类型" width="108">
        <template #default="{row}"><span class="redis-type" :class="typeClass(row.type)">{{row.type || '—'}}</span></template>
      </el-table-column>
      <el-table-column label="TTL" width="110">
        <template #default="{row}"><span class="mono redis-ttl" :class="{expired: row.ttl === -2}">{{ttlText(row.ttl)}}</span></template>
      </el-table-column>
      <el-table-column label="" width="88" fixed="right">
        <template #default="{row}">
          <el-button size="small" text type="primary" @click.stop="openValue(row)">查看</el-button>
        </template>
      </el-table-column>
    </el-table>
    <div v-else class="tool-empty-table" v-loading="loading">
      <empty-state :text="emptyText" />
    </div>

    <div class="redis-actions">
      <el-button :disabled="!canMore" @click="more">
        <el-icon style="margin-right:4px"><Plus /></el-icon>展示更多（+{{pageSize}}）
      </el-button>
      <el-button :disabled="!canAll" @click="showAll">
        <el-icon style="margin-right:4px"><DArrowRight /></el-icon>展示全部
      </el-button>
      <span class="redis-actions-note">{{actionNote}}</span>
    </div>

    <div v-if="err" class="redis-error-box">{{err}}</div>
  </div>

  <redis-value v-if="valueOpen" :conn-id="conn.id" :db="db" :key-name="valueKey" @close="valueOpen=false" />
</div>`,
  data() {
    return {
      db: this.conn.default_db || 0,
      dbs: [],
      pinging: false,
      prefix: '',
      pattern: '',
      keys: [],
      scanId: '',
      loading: false,
      found: 0,
      scanned: 0,
      returned: 0,
      done: false,
      hasMore: false,
      limitHit: false,
      maxKeys: 0,
      elapsed: 0,
      err: '',
      valueOpen: false,
      valueKey: ''
    }
  },
  computed: {
    pageSize() { return 50 }, // 与请求里显式带的 limit 同一个数，按钮文案才不会骗人
// 表体可视高度：视口减去「结果区以上」的固定开销，再减去结果区自身的卡片头、尾部按钮与 padding。
// 必须交给 el-table 的 max-height —— 它只认这个 prop，不会因为外层限了高就自己滚。
// 285 是按渲染台实测反推的：上方固定开销 125px（视口 685）+ 卡片头 54 + 尾部按钮 46 + 卡片 padding 32 = 257，留 28px 余量。
// 低于 240 就别再压了（那种高度连 7 行都放不下，继续压只是把内容藏得更深）。
tableMaxHeight() {
      const vh = (typeof window !== 'undefined' && window.innerHeight) ? window.innerHeight : 800
      return Math.max(240, vh - 285)
    },
    canMore() { return !!this.scanId && this.hasMore && !this.loading },
    // 「展示全部」必须先有匹配结果——否则它就是一个换了名字的 KEYS *。
    // 后端也会再挡一次（无命中时直接拒绝），这里只是不让按钮显得可点。
    canAll() { return !!this.scanId && this.hasMore && this.found > 0 && !this.loading },
    summary() {
      if (!this.scanId) return '输入开头内容后点「查询」'
      const more = this.done ? '已扫完' : '还能继续取'
      return '已展示 ' + this.keys.length + ' 条 · 已匹配 ' + this.found + ' 条 · ' + more
    },
    actionNote() {
      if (this.loading) return '正在扫描…'
      if (!this.scanId) return '查询之后才有「更多」与「全部」'
      if (this.limitHit) return '已达单次上限 ' + this.maxKeys + ' 条，请把匹配串写得更具体'
      if (this.done) return '本次匹配已经全部取完'
      if (!this.found) return '这一段里还没有匹配到，可以继续往下找'
      return '当前匹配共 ' + this.found + ' 条，可分批取或一次取完'
    },
    emptyText() {
      if (this.loading) return '查询中…'
      if (this.err) return '查询失败，详见下方提示'
      if (!this.scanId) return '输入开头内容后点「查询」开始匹配'
      if (this.found > 0) return '本页没有内容'
      return '没有匹配的 key —— 换一个开头串再试'
    }
  },
  mounted() { this.loadDbs() },
  methods: {
    dbLabel(d) { return window.RedisUtil.dbLabel(d) },
    typeClass(t) { return window.RedisUtil.typeClass(t) },
    ttlText(t) { return window.RedisUtil.ttlText(t) },
    async loadDbs() {
      try {
        const r = await api.get('/redis/' + this.conn.id + '/databases')
        if (r.code === 0) {
          this.dbs = r.data?.list || []
          if (!this.dbs.some(d => d.index === this.db)) this.db = r.data?.default_db ?? 0
        }
      } catch (e) {
        // 列不出库不影响查询：退化成「只有一个当前库」
        this.dbs = [{ index: this.db, keys: -1 }]
      }
    },
    async testConn() {
      this.pinging = true
      try {
        const r = await api.get('/redis/' + this.conn.id + '/ping')
        if (r.code === 0) {
          const d = r.data || {}
          this.$message.success('连接正常（v' + (d.version || '?') + '）· 延迟 ' + (d.latency_ms ?? '') +
            ' ms · 当前库 ' + (d.db_size ?? 0) + ' 个 key')
        }
      } catch (e) { /* 拦截器已提示 */ } finally {
        this.pinging = false
      }
    },
    onDbChange() {
      // 换了库，旧的扫描游标就不属于这里了
      this.reset()
    },
    reset() {
      this.keys = []
      this.scanId = ''
      this.pattern = ''
      this.found = 0
      this.scanned = 0
      this.returned = 0
      this.done = false
      this.hasMore = false
      this.limitHit = false
      this.maxKeys = 0
      this.elapsed = 0
      this.err = ''
    },
    search() {
      const s = this.prefix.trim()
      if (!s) {
        ElMessage.warning('请输入要匹配的开头内容：留空等价于 KEYS *')
        return
      }
      this.reset()
      this.fetch({ prefix: s, db: this.db, limit: this.pageSize }, true)
    },
    more() {
      if (!this.canMore) return
      this.fetch({ scan_id: this.scanId, limit: this.pageSize }, false)
    },
    showAll() {
      if (!this.canAll) return
      this.fetch({ scan_id: this.scanId, all: true }, false)
    },
    async fetch(payload, replace) {
      this.loading = true
      this.err = ''
      try {
        // timeout: 0 —— 本地直连没有超时问题；「展示全部」要连续推很多轮 SCAN，
        // 卡在 axios 默认的 15 秒上没有任何意义。
        const r = await api.post('/redis/' + this.conn.id + '/keys', payload, { timeout: 0 })
        if (r.code === 0) this.apply(r.data, replace)
        else this.err = r.message || '扫描失败'
      } catch (e) {
        this.err = window.RedisUtil.errText(e, '扫描失败')
      } finally {
        this.loading = false
      }
    },
    apply(d, replace) {
      const data = d || {}
      const list = Array.isArray(data.keys) ? data.keys : []
      this.keys = replace ? list : this.keys.concat(list)
      this.scanId = data.scan_id || ''
      this.pattern = data.pattern || ''
      this.found = data.found || 0
      this.scanned = data.scanned || 0
      this.returned = data.returned || 0
      this.done = !!data.done
      this.hasMore = !!data.has_more
      this.limitHit = !!data.limit_hit
      this.maxKeys = data.max_keys || 0
      this.elapsed = data.elapsed_ms || 0
      if (typeof data.db === 'number') this.db = data.db
    },
    openValue(row) {
      if (!row || !row.key) return
      this.valueKey = row.key
      this.valueOpen = true
    }
  }
}
