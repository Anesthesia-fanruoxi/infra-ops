// ES 控制台 · 数据视图 tab（F2）：视图列表 + 抽屉新建（左表单 / 右索引过滤）+ 编辑/删除。
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

    <!-- 新建：抽屉 · 左表单 / 右匹配索引 -->
    <el-drawer v-model="wizard" title="新建数据视图" size="760px" append-to-body :close-on-click-modal="false" class="es-view-drawer" @opened="onDrawerOpened">
      <div class="es-view-create" v-loading="loadingIdx">
        <div class="es-view-create-left">
          <el-form label-position="top" @submit.prevent>
            <el-form-item label="名称" required>
              <el-input v-model="form.name" maxlength="64" placeholder="视图名称（同一连接内唯一）" @input="checkDup" />
              <div v-if="dupMsg" class="es-sync-fail" style="margin-top:6px">{{dupMsg}}</div>
            </el-form-item>
            <el-form-item label="索引匹配" required>
              <el-input v-model="form.index_pattern" placeholder="如 ysh* 或 logs-*"
                        style="font-family:var(--font-mono)" />
              <div class="es-qc-hint">支持 * / ? 通配；无通配时右侧按前缀过滤预览（创建仍按你填写的 pattern）</div>
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
            <el-button @click="wizard=false">取消</el-button>
            <el-button type="primary" :loading="creating" :disabled="!canCreate" @click="create">创建并进入</el-button>
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

    <!-- 编辑 -->
    <el-dialog v-model="editDialog" title="编辑视图" width="480px" append-to-body>
      <el-form label-position="top">
        <el-form-item label="名称" required><el-input v-model="editForm.name" maxlength="64" /></el-form-item>
        <el-form-item label="索引匹配" required><el-input v-model="editForm.index_pattern" style="font-family:var(--font-mono)" /></el-form-item>
        <el-form-item label="时间字段"><el-input v-model="editForm.time_field" placeholder="留空自动挑选" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="editDialog=false">取消</el-button><el-button type="primary" :loading="saving" @click="save">保存</el-button></template>
    </el-dialog>
  </template>
</div>`,
  data() {
    return {
      views: [], loading: false,
      activeView: null,
      wizard: false, creating: false,
      form: { name: '', index_pattern: '', time_field: '' },
      dupMsg: '',
      allIndexNames: [], loadingIdx: false,
      timeFields: [], fetchingTF: false,
      editDialog: false, editForm: { name: '', index_pattern: '', time_field: '' }, saving: false
    }
  },
  computed: {
    matchedIndices() {
      return this.filterIndices(this.allIndexNames, this.form.index_pattern)
    },
    canCreate() {
      return !!(this.form.name && !this.dupMsg && this.form.index_pattern.trim() && this.form.time_field.trim())
    }
  },
  mounted() { this.loadViews() },
  methods: {
    async loadViews() {
      this.loading = true
      try { const r = await api.get('/es/' + this.conn.id + '/views'); if (r.code === 0) this.views = r.data || [] } catch (e) { /* */ } finally { this.loading = false }
    },
    openWizard() {
      this.wizard = true
      this.form = { name: '', index_pattern: '', time_field: '' }
      this.dupMsg = ''
      this.timeFields = []
      this.fetchingTF = false
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
      this.dupMsg = this.views.some(v => v.name === this.form.name) ? '视图名称在同一连接内已存在' : ''
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
    async create() {
      if (!this.canCreate) return
      // 创建用用户填写的 pattern；无通配且有前缀匹配时提示建议加 *
      const p = this.form.index_pattern.trim()
      if (!/[*?]/.test(p) && this.matchedIndices.length && !this.matchedIndices.includes(p)) {
        try {
          await this.$confirm(
            '当前 pattern「' + p + '」无通配符，创建时 ES 按精确名匹配，可能失败。建议改为「' + p + '*」。仍要按原样创建？',
            '提示', { type: 'warning', confirmButtonText: '仍创建', cancelButtonText: '去修改' }
          )
        } catch (e) { return }
      }
      this.creating = true
      try {
        const r = await api.post('/es/' + this.conn.id + '/views', {
          name: this.form.name, index_pattern: this.form.index_pattern.trim(), time_field: this.form.time_field.trim()
        })
        if (r.code === 0) {
          this.wizard = false
          this.$message.success('视图已创建')
          await this.loadViews()
          const v = this.views.find(x => x.id === r.data.id)
          if (v) this.enterView(v)
          this.form = { name: '', index_pattern: '', time_field: '' }
          this.timeFields = []
        }
      } catch (e) { /* */ } finally { this.creating = false }
    },
    enterView(row) { this.activeView = row },
    async refresh(row) {
      row._refreshing = true
      try {
        const r = await api.post('/es/' + this.conn.id + '/views/' + row.id + '/refresh')
        if (r.code === 0) { this.$message.success('字段已刷新'); this.loadViews() }
      } catch (e) { /* */ } finally { row._refreshing = false }
    },
    openEdit(row) {
      this.editForm = { id: row.id, name: row.name, index_pattern: row.index_pattern, time_field: row.time_field }
      this.editDialog = true
    },
    async save() {
      this.saving = true
      try {
        const r = await api.put('/es/' + this.conn.id + '/views/' + this.editForm.id, {
          name: this.editForm.name, index_pattern: this.editForm.index_pattern, time_field: this.editForm.time_field
        })
        if (r.code === 0) { this.editDialog = false; this.$message.success('已保存，正在重新同步字段'); this.loadViews() }
      } catch (e) { /* */ } finally { this.saving = false }
    },
    async del(row) {
      try { await this.$confirm('确认删除视图「' + row.name + '」？不影响 ES 中的任何数据。', '提示', { type: 'warning' }) } catch (e) { return }
      try { const r = await api.delete('/es/' + this.conn.id + '/views/' + row.id); if (r.code === 0) { this.$message.success('已删除'); this.loadViews() } } catch (e) { /* */ }
    }
  }
}
