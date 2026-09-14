// ES 控制台 · 数据视图 tab（F2）：视图列表 + 三步向导（名称 / 索引匹配 / 时间字段）+ 刷新/编辑/删除。
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

    <!-- 三步向导 -->
    <el-dialog v-model="wizard" title="新建数据视图" width="600px" append-to-body :close-on-click-modal="false">
      <el-steps :active="step" finish-status="success" simple style="margin-bottom:16px">
        <el-step title="名称" /><el-step title="索引匹配" /><el-step title="时间字段" />
      </el-steps>

      <div v-if="step===0">
        <el-input v-model="form.name" maxlength="64" placeholder="视图名称（同一连接内唯一）" @input="checkDup" />
        <div v-if="dupMsg" class="es-sync-fail" style="margin-top:6px">{{dupMsg}}</div>
      </div>

      <div v-else-if="step===1">
        <el-input v-model="form.index_pattern" placeholder="如 logs-* 或 logs-2026.09.10,logs-2026.09.11"
                  @input="debounceProbe" style="font-family:var(--font-mono)" />
        <div v-if="probing" style="margin-top:8px;color:var(--text-sub)">探测中…</div>
        <template v-if="probe">
          <div class="es-probe">
            <span>命中：<b>{{probe.indices.length}}</b> 索引 · <b>{{probe.data_streams.length}}</b> 数据流 · <b>{{probe.aliases.length}}</b> 别名</span>
            <span>文档总数：<b class="mono">{{probe.docs_count}}</b></span>
          </div>
          <div class="es-probe-names mono">{{allNames.slice(0,20).join('  ')}}<span v-if="allNames.length>20"> …共 {{allNames.length}} 项</span></div>
          <div v-if="emptyHit" class="es-sync-fail" style="margin-top:8px">未命中任何索引或数据流，无法继续</div>
          <div v-else-if="tooWide" class="es-sync-warn" style="margin-top:8px">范围过宽（pattern 为 * 或命中超过 50），字段表可能被截断，建议收窄</div>
        </template>
      </div>

      <div v-else>
        <el-radio-group v-model="form.time_field" class="es-tf-list">
          <el-radio v-for="t in probe.time_fields" :key="t" :value="t" border>
            <span class="mono">{{t}}</span>
          </el-radio>
        </el-radio-group>
        <div v-if="!probe.time_fields || !probe.time_fields.length" class="es-sync-fail">未探测到 date 类型时间字段：换一个 pattern，或给索引补时间字段</div>
      </div>

      <template #footer>
        <el-button v-if="step>0" @click="step--">上一步</el-button>
        <el-button v-if="step===0" type="primary" :disabled="!form.name || !!dupMsg" @click="step=1">下一步</el-button>
        <el-button v-else-if="step===1" type="primary" :disabled="emptyHit" @click="step=2">下一步</el-button>
        <el-button v-else type="primary" :loading="creating" :disabled="!form.time_field" @click="create">创建并进入</el-button>
      </template>
    </el-dialog>

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
      wizard: false, step: 0, creating: false,
      form: { name: '', index_pattern: '', time_field: '' },
      dupMsg: '', probe: null, probing: false, probeTimer: null,
      editDialog: false, editForm: { name: '', index_pattern: '', time_field: '' }, saving: false
    }
  },
  computed: {
    allNames() {
      if (!this.probe) return []
      return [...(this.probe.indices || []), ...(this.probe.data_streams || []), ...(this.probe.aliases || [])]
    },
    emptyHit() {
      if (!this.probe) return true
      return !(this.probe.indices || []).length && !(this.probe.data_streams || []).length
    },
    tooWide() {
      if (!this.probe) return false
      const n = this.allNames.length
      return this.form.index_pattern === '*' || n > 50
    }
  },
  mounted() { this.loadViews() },
  methods: {
    async loadViews() {
      this.loading = true
      try { const r = await api.get('/es/' + this.conn.id + '/views'); if (r.code === 0) this.views = r.data || [] } catch (e) { /* */ } finally { this.loading = false }
    },
    syncText(row) {
      return row.sync_status === 'idle' ? '正常' : row.sync_status === 'syncing' ? '同步中' : '失败'
    },
    checkDup() {
      this.dupMsg = this.views.some(v => v.name === this.form.name) ? '视图名称在同一连接内已存在' : ''
    },
    debounceProbe() {
      clearTimeout(this.probeTimer)
      this.probeTimer = setTimeout(() => this.runProbe(), 500)
    },
    async runProbe() {
      const p = (this.form.index_pattern || '').trim()
      if (!p) { this.probe = null; return }
      this.probing = true; this.probe = null
      try {
        const r = await api.post('/es/' + this.conn.id + '/index-pattern/probe', { index_pattern: p })
        if (r.code === 0) this.probe = r.data
      } catch (e) { /* */ } finally { this.probing = false }
    },
    async create() {
      this.creating = true
      try {
        const r = await api.post('/es/' + this.conn.id + '/views', {
          name: this.form.name, index_pattern: this.form.index_pattern, time_field: this.form.time_field
        })
        if (r.code === 0) {
          this.wizard = false
          this.$message.success('视图已创建')
          await this.loadViews()
          const v = this.views.find(x => x.id === r.data.id)
          if (v) this.enterView(v)
          this.form = { name: '', index_pattern: '', time_field: '' }
          this.probe = null; this.step = 0
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
