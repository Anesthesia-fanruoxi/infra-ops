// ES 控制台 · 数据视图 tab（F2）：视图列表 + 抽屉新建/编辑（左表单 / 右索引过滤）+ 删除。
// 新建与编辑共用同一抽屉：仅标题、提交文案、回填与提交报文（只传变更字段）不同。
// 点视图名进入 Discover（es_discover.js，页面内展开，不新增路由）。
window.EsViewsTab = {
  props: ['conn'],
  components: { 'es-discover': window.EsDiscover },
  template: `
<div>
  <!-- Discover 全宽视图 -->
  <es-discover v-if="activeView" :conn="conn" :view="activeView" @back="activeView=null" />

  <template v-else>
    <div class="es-toolbar">
      <el-button type="primary" @click="openWizard">新建数据视图</el-button>
      <el-button @click="loadViews">刷新</el-button>
    </div>
    <el-table class="hosts-table" :data="views" style="width:100%" size="small" v-loading="loading">
      <el-table-column label="视图" min-width="170">
        <template #default="{row}"><span class="mono es-link" @click="enterView(row)">{{row.name}}</span></template>
      </el-table-column>
      <el-table-column label="索引匹配" min-width="170"><template #default="{row}"><span class="mono">{{row.index_pattern}}</span></template></el-table-column>
      <el-table-column label="时间字段" width="140"><template #default="{row}"><span class="mono">{{row.time_field}}</span></template></el-table-column>
      <el-table-column label="字段数" width="80"><template #default="{row}"><span class="mono">{{row.field_count}}</span></template></el-table-column>
      <el-table-column label="同步" width="170">
        <template #default="{row}">
          <span class="es-sync-badge" :class="'s-' + row.sync_status">
            <span class="st-dot"></span>{{syncText(row)}}
          </span>
          <div v-if="row.sync_status==='failed' && row.sync_error" class="es-sync-err">{{row.sync_error}}</div>
        </template>
      </el-table-column>
      <el-table-column label="同步于" width="150"><template #default="{row}"><span class="mono">{{(row.synced_at||'').slice(0,16) || '-'}}</span></template></el-table-column>
      <el-table-column label="" width="210" fixed="right">
        <template #default="{row}">
          <div class="ops-cell">
            <el-button size="small" text type="primary" @click="enterView(row)">检索</el-button>
            <el-button size="small" text :loading="row._refreshing" @click="refresh(row)">刷新字段</el-button>
            <el-button size="small" text @click="openEdit(row)">编辑</el-button>
            <el-button size="small" text type="danger" @click="del(row)">删除</el-button>
          </div>
        </template>
      </el-table-column>
      <template #empty><empty-state text="暂无数据视图：先创建一个视图（名称 / 索引匹配 / 时间字段），再进入检索" /></template>
    </el-table>

    <!-- 新建 / 编辑：同一抽屉 · 左表单 / 右匹配索引 -->
    <el-drawer v-model="drawer" :title="drawerTitle" size="760px" append-to-body :close-on-click-modal="false" class="es-view-drawer" @opened="onDrawerOpened">
      <div class="es-view-create" v-loading="loadingIdx">
        <div class="es-view-create-left">
          <el-form label-position="top" @submit.prevent>
            <el-form-item label="名称" required>
              <el-input v-model="form.name" maxlength="64" placeholder="视图名称（同一连接内唯一）" @input="checkDup" />
              <div v-if="dupMsg" class="es-sync-fail" style="margin-top:6px">{{dupMsg}}</div>
            </el-form-item>
            <el-form-item label="索引匹配" required>
              <el-input v-model="form.index_pattern" placeholder="如 ysh 或 logs-*"
                        style="font-family:var(--font-mono)" />
              <div class="es-qc-hint">
                支持 * / ? 与逗号分隔多段；未写通配符时保存自动补 *<span v-if="autoSuffix" class="mono">（{{form.index_pattern.trim()}} → {{autoSuffix}}）</span>
              </div>
            </el-form-item>
            <el-form-item label="时间字段" required>
              <div class="es-tf-row">
                <el-select v-if="timeFields.length" v-model="form.time_field" filterable allow-create default-first-option
                           placeholder="选择或输入时间字段" style="flex:1;min-width:0">
                  <el-option v-for="t in timeFields" :key="t" :label="t" :value="t" />
                </el-select>
                <el-input v-else v-model="form.time_field" placeholder="@timestamp" style="flex:1;min-width:0;font-family:var(--font-mono)" />
                <el-button :loading="fetchingTF" :disabled="!matchedIndices.length" @click="fetchTimeFields">获取</el-button>
              </div>
              <div class="es-qc-hint">点击「获取」读取右侧第一个匹配索引的 date 字段</div>
            </el-form-item>
          </el-form>
          <div class="es-view-create-footer">
            <el-button @click="drawer=false">取消</el-button>
            <el-button type="primary" :loading="saving" :disabled="!canSubmit" @click="submit">{{isEdit ? '保存' : '创建并进入'}}</el-button>
          </div>
        </div>
        <div class="es-view-create-right">
          <div class="es-view-match-head">
            <span>匹配索引</span>
            <span class="mono">{{matchedIndices.length}} / {{allIndexNames.length}}</span>
          </div>
          <div class="es-view-match-list">
            <div v-for="name in matchedIndices" :key="name" class="es-view-match-item mono">{{name}}</div>
            <div v-if="!matchedIndices.length" class="es-lc-empty-hint">
              {{ form.index_pattern.trim() ? '无匹配索引，可试加 * 如 ysh*' : '输入索引匹配后在此过滤'}}
            </div>
          </div>
        </div>
      </div>
    </el-drawer>
  </template>
</div>`,
  data() {
    return {
      views: [], loading: false,
      activeView: null,
      drawer: false, mode: 'create', editId: 0, saving: false,
      origin: { name: '', index_pattern: '', time_field: '' },
      form: { name: '', index_pattern: '', time_field: '' },
      dupMsg: '',
      allIndexNames: [], loadingIdx: false,
      timeFields: [], fetchingTF: false
    }
  },
  computed: {
    isEdit() { return this.mode === 'edit' },
    drawerTitle() { return this.isEdit ? '编辑数据视图' : '新建数据视图' },
    matchedIndices() {
      return this.filterIndices(this.allIndexNames, this.form.index_pattern)
    },
    // 保存时会被自动补 * 的预览（仅当补全结果与用户输入不同才提示）
    autoSuffix() {
      const p = (this.form.index_pattern || '').trim()
      if (!p) return ''
      const n = this.normalizePattern(p)
      return n && n !== p ? n : ''
    },
    canSubmit() {
      return !!(this.form.name.trim() && !this.dupMsg && this.form.index_pattern.trim() && this.form.time_field.trim())
    }
  },
  mounted() { this.loadViews() },
  methods: {
    async loadViews() {
      this.loading = true
      try { const r = await api.get('/es/' + this.conn.id + '/views'); if (r.code === 0) this.views = r.data || [] } catch (e) { /* */ } finally { this.loading = false }
    },
    // 与后端 normalizeIndexPattern 同规则：逐分量补 *，已含 * / ? 的原样
    normalizePattern(p) {
      return (p || '').split(',').map(s => s.trim()).filter(Boolean)
        .map(s => (/[*?]/.test(s) ? s : s + '*')).join(',')
    },
    openWizard() { this.openDrawer('create', null) },
    openEdit(row) { this.openDrawer('edit', row) },
    openDrawer(mode, row) {
      this.mode = mode
      this.editId = row ? row.id : 0
      this.origin = row
        ? { name: row.name, index_pattern: row.index_pattern, time_field: row.time_field }
        : { name: '', index_pattern: '', time_field: '' }
      this.form = { name: this.origin.name, index_pattern: this.origin.index_pattern, time_field: this.origin.time_field }
      this.dupMsg = ''
      this.timeFields = []
      this.fetchingTF = false
      this.drawer = true
    },
    async onDrawerOpened() {
      await this.loadIndexNames()
    },
    async loadIndexNames() {
      this.loadingIdx = true
      try {
        const r = await api.get('/es/' + this.conn.id + '/indices')
        if (r.code === 0) {
          const list = r.data?.list || []
          this.allIndexNames = list.map(x => x.index).filter(Boolean).sort()
        }
      } catch (e) { /* */ } finally { this.loadingIdx = false }
    },
    // Kibana 风格：本地按 pattern 过滤；无 * / ? 时按前缀预览（ysh → ysh*）
    filterIndices(names, pattern) {
      const raw = (pattern || '').trim()
      if (!raw) return names.slice()
      const parts = raw.split(',').map(s => s.trim()).filter(Boolean)
      if (!parts.length) return names.slice()
      const regs = parts.map(p => {
        let glob = p
        if (!/[*?]/.test(glob)) glob = glob + '*'
        const esc = glob.replace(/[.+^${}()|[\]\\]/g, '\\$&').replace(/\*/g, '.*').replace(/\?/g, '.')
        return new RegExp('^' + esc + '$')
      })
      return names.filter(n => regs.some(re => re.test(n)))
    },
    syncText(row) {
      return row.sync_status === 'idle' ? '正常' : row.sync_status === 'syncing' ? '同步中' : '失败'
    },
    checkDup() {
      const name = (this.form.name || '').trim()
      this.dupMsg = this.views.some(v => v.name === name && v.id !== this.editId) ? '视图名称在同一连接内已存在' : ''
    },
    async fetchTimeFields() {
      const first = this.matchedIndices[0]
      if (!first) { this.$message.warning('右侧暂无匹配索引'); return }
      this.fetchingTF = true
      try {
        const r = await api.post('/es/' + this.conn.id + '/index-pattern/probe', { index_pattern: first })
        if (r.code === 0) {
          this.timeFields = r.data?.time_fields || []
          if (this.timeFields.length) {
            if (!this.form.time_field || !this.timeFields.includes(this.form.time_field)) {
              this.form.time_field = this.timeFields[0]
            }
            this.$message.success('已从索引 ' + first + ' 获取时间字段')
          } else {
            this.$message.warning('索引 ' + first + ' 未发现 date 类型时间字段，请手动填写')
          }
        }
      } catch (e) { /* */ } finally { this.fetchingTF = false }
    },
    async submit() {
      if (!this.canSubmit) return
      const name = this.form.name.trim()
      const pattern = this.normalizePattern(this.form.index_pattern)
      const timeField = this.form.time_field.trim()
      if (!pattern) { this.$message.warning('索引匹配不能为空'); return }
      const body = {}
      if (!this.isEdit) {
        body.name = name
        body.index_pattern = pattern
        body.time_field = timeField
      } else {
        // 编辑只提交真正变更的字段（后端对缺省字段沿用当前值）
        if (name !== this.origin.name) body.name = name
        if (pattern !== this.origin.index_pattern) body.index_pattern = pattern
        if (timeField !== this.origin.time_field) body.time_field = timeField
        if (!Object.keys(body).length) { this.$message.info('未检测到修改'); return }
      }
      this.saving = true
      try {
        if (this.isEdit) {
          const r = await api.put('/es/' + this.conn.id + '/views/' + this.editId, body)
          if (r.code === 0) {
            this.drawer = false
            const resynced = ('index_pattern' in body) || ('time_field' in body)
            this.$message.success(resynced ? '已保存，字段已重新同步' : '已保存')
            await this.loadViews()
          }
        } else {
          const r = await api.post('/es/' + this.conn.id + '/views', body)
          if (r.code === 0) {
            this.drawer = false
            this.$message.success('视图已创建')
            await this.loadViews()
            const v = this.views.find(x => x.id === r.data.id)
            if (v) this.enterView(v)
          }
        }
      } catch (e) { /* */ } finally { this.saving = false }
    },
    enterView(row) { this.activeView = row },
    async refresh(row) {
      row._refreshing = true
      try {
        const r = await api.post('/es/' + this.conn.id + '/views/' + row.id + '/refresh')
        if (r.code === 0) { this.$message.success('字段已刷新'); this.loadViews() }
      } catch (e) { /* */ } finally { row._refreshing = false }
    },
    async del(row) {
      try { await this.$confirm('确认删除视图「' + row.name + '」？不影响 ES 中的任何数据。', '提示', { type: 'warning' }) } catch (e) { return }
      try { const r = await api.delete('/es/' + this.conn.id + '/views/' + row.id); if (r.code === 0) { this.$message.success('已删除'); this.loadViews() } } catch (e) { /* */ }
    }
  }
}
