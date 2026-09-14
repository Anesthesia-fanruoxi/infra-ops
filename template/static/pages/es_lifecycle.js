// ES 控制台 · 生命周期 tab（F7）：ILM 策略 CRUD（W1/W2）+ 索引 explain + 重试（W3）+ 数据流保留期（W4）。
// 硬约束：内置策略只读；保存/删除前二次确认并完整回显将发出的 ES 请求。
window.EsLifecycleTab = {
  props: ['conn'],
  template: `
<div>
  <div class="es-lc-toolbar">
    <el-button type="primary" @click="openNew">新建策略</el-button>
    <el-button @click="loadPolicies">刷新</el-button>
  </div>
  <div class="es-lc-grid">
    <div class="es-lc-list">
      <div v-for="p in policies" :key="p.name" class="es-lc-item" :class="{active: cur && cur.name===p.name}" @click="showPolicy(p)">
        <span class="mono">{{p.name}}</span>
        <span v-if="p.managed" class="es-managed">ES 管理</span>
      </div>
    </div>
    <div class="es-lc-detail" v-if="cur">
      <div class="es-lc-ops">
        <el-button size="small" :disabled="cur.managed" @click="edit">编辑</el-button>
        <el-button size="small" type="danger" :disabled="cur.managed" @click="del">删除</el-button>
      </div>
      <pre class="es-source">{{curText}}</pre>
    </div>
  </div>

  <div class="es-sub-head">索引生命周期状态（explain · 接受 pattern）</div>
  <div class="es-an-row">
    <el-input v-model="explainPattern" placeholder="如 logs-* 或视图的 index_pattern" style="width:320px" />
    <el-button @click="runExplain">查询</el-button>
  </div>
  <el-table v-if="explainRows.length" class="hosts-table" :data="explainRows" size="small">
    <el-table-column label="索引" min-width="200"><template #default="{row}"><span class="mono">{{row.index}}</span></template></el-table-column>
    <el-table-column label="策略" width="160"><template #default="{row}"><span class="mono">{{row.policy}}</span></template></el-table-column>
    <el-table-column label="阶段/动作" width="150"><template #default="{row}"><span class="mono">{{row.phase}}/{{row.action}}</span></template></el-table-column>
    <el-table-column label="已持续" width="110"><template #default="{row}"><span class="mono">{{row.age}}</span></template></el-table-column>
    <el-table-column label="状态" min-width="180">
      <template #default="{row}">
        <span v-if="!row.stuck">正常运行</span>
        <span v-else class="es-sync-fail">{{row.reason}}</span>
        <el-button v-if="row.stuck" size="small" text type="warning" @click="retry(row)">重试</el-button>
      </template>
    </el-table-column>
  </el-table>

  <div class="es-sub-head">数据流与保留期（DLM）</div>
  <el-table class="hosts-table" :data="streams" size="small" v-loading="loadingStreams">
    <el-table-column label="数据流" min-width="200"><template #default="{row}"><span class="mono">{{row.name}}</span></template></el-table-column>
    <el-table-column label="生成数" width="100"><template #default="{row}"><span class="mono">{{row.generations}}</span></template></el-table-column>
    <el-table-column label="保留期" width="130"><template #default="{row}"><span class="mono">{{row.retention || '未设置'}}</span></template></el-table-column>
    <el-table-column label="" width="110">
      <template #default="{row}"><el-button size="small" text type="primary" @click="openRetention(row)">设置保留期</el-button></template>
    </el-table-column>
  </el-table>

  <el-dialog v-model="editDialog" :title="editMode==='new' ? '新建策略' : '编辑策略 · ' + curName" width="640px" append-to-body>
    <el-input v-if="editMode==='new'" v-model="curName" placeholder="策略名称（不得含 * , ? 或以 _ . 开头）" style="margin-bottom:10px" />
    <el-input v-model="policyJSON" type="textarea" :rows="14" spellcheck="false" style="font-family:var(--font-mono)" />
    <template #footer>
      <el-button @click="editDialog=false">取消</el-button>
      <el-button type="primary" :loading="saving" @click="savePolicy">保存</el-button>
    </template>
  </el-dialog>

  <el-dialog v-model="retentionDialog" :title="'设置保留期 · ' + streamName" width="460px" append-to-body>
    <el-form label-position="top">
      <el-form-item label="保留期（毫秒或天数字符串，如 2592000000）" required>
        <el-input v-model="retentionMs" style="font-family:var(--font-mono)" />
      </el-form-item>
    </el-form>
    <template #footer><el-button @click="retentionDialog=false">取消</el-button><el-button type="primary" :loading="saving" @click="saveRetention">保存</el-button></template>
  </el-dialog>
</div>`,
  data() {
    return {
      policies: [], cur: null, editDialog: false, editMode: 'new', curName: '', policyJSON: '', saving: false,
      explainPattern: '', explainRows: [],
      streams: [], loadingStreams: false, retentionDialog: false, streamName: '', retentionMs: ''
    }
  },
  computed: { curText() { return this.cur ? JSON.stringify(this.cur.policy, null, 2) : '' } },
  mounted() { this.loadPolicies(); this.loadStreams() },
  methods: {
    async loadPolicies() {
      try { const r = await api.get('/es/' + this.conn.id + '/ilm/policies'); if (r.code === 0) this.policies = r.data?.list || [] } catch (e) { /* */ }
    },
    showPolicy(p) { this.cur = p },
    openNew() { this.editMode = 'new'; this.curName = ''; this.policyJSON = '{\n  "policy": {\n    "phases": {\n      "hot": {}\n    }\n  }\n}'; this.editDialog = true },
    edit() {
      this.editMode = 'edit'; this.curName = this.cur.name
      this.policyJSON = JSON.stringify(this.cur.policy, null, 2)
      this.editDialog = true
    },
    async savePolicy() {
      let body
      try { body = JSON.parse(this.policyJSON) } catch (e) { this.$message.error('JSON 不合法：' + e.message); return }
      const req = 'PUT /_ilm/policy/' + this.curName
      try {
        await this.$confirm('即将向 ES 发出请求：\n' + req + '\n\n覆盖内容摘要：' + Object.keys((body.policy || {}).phases || {}).join(', '), '确认保存', { type: 'warning' })
      } catch (e) { return }
      this.saving = true
      try {
        const r = await api.put('/es/' + this.conn.id + '/ilm/policies/' + encodeURIComponent(this.curName), body)
        if (r.code === 0) { this.editDialog = false; this.$message.success('策略已保存'); this.loadPolicies() }
      } catch (e) { /* */ } finally { this.saving = false }
    },
    async del() {
      try { await this.$confirm('即将向 ES 发出请求：\nDELETE /_ilm/policy/' + this.cur.name + '\n\n删除被索引引用的策略会被 ES 拒绝（不自动解绑）。', '确认删除', { type: 'warning' }) } catch (e) { return }
      try { const r = await api.delete('/es/' + this.conn.id + '/ilm/policies/' + encodeURIComponent(this.cur.name)); if (r.code === 0) { this.$message.success('已删除'); this.cur = null; this.loadPolicies() } } catch (e) { /* */ }
    },
    async runExplain() {
      if (!this.explainPattern) { this.$message.warning('输入 pattern'); return }
      try {
        const r = await api.get('/es/' + this.conn.id + '/ilm/explain', { params: { pattern: this.explainPattern } })
        if (r.code === 0) {
          const indices = (r.data && r.data.indices) || {}
          this.explainRows = Object.entries(indices).map(([index, v]) => {
            const m = v || {}
            let stuck = false, reason = ''
            const stepInfo = m.step_info || {}
            if (m.step && ['ERROR', 'WAITING'].includes(m.phase) || stepInfo.cause) { stuck = true; reason = stepInfo.reason || '步骤停滞' }
            return { index, policy: m.policy || '-', phase: m.phase || '-', action: m.action || '-', age: m.age || '-', stuck, reason }
          })
        }
      } catch (e) { /* */ }
    },
    async retry(row) {
      try { await this.$confirm('即将向 ES 发出请求：\nPOST /' + row.index + '/_ilm/retry', '确认重试', { type: 'warning' }) } catch (e) { return }
      try { const r = await api.post('/es/' + this.conn.id + '/ilm/retry', { index: row.index }); if (r.code === 0) { this.$message.success('已发起重试'); this.runExplain() } } catch (e) { /* */ }
    },
    async loadStreams() {
      this.loadingStreams = true
      try {
        const r = await api.get('/es/' + this.conn.id + '/data-streams')
        if (r.code === 0) {
          this.streams = (r.data && r.data.data_streams || []).map(d => ({
            name: d.name, generations: (d.generations || []).length,
            retention: d.lifecycle && d.lifecycle.data_retention ? d.lifecycle.data_retention : ''
          }))
        }
      } catch (e) { /* */ } finally { this.loadingStreams = false }
    },
    openRetention(row) { this.streamName = row.name; this.retentionMs = row.retention || ''; this.retentionDialog = true },
    async saveRetention() {
      if (!this.retentionMs) { this.$message.warning('输入保留期'); return }
      try { await this.$confirm('即将向 ES 发出请求：\nPUT /_data_stream/' + this.streamName + '/_lifecycle', '确认设置', { type: 'warning' }) } catch (e) { return }
      this.saving = true
      try {
        const r = await api.put('/es/' + this.conn.id + '/data-streams/' + encodeURIComponent(this.streamName) + '/lifecycle', { data_retention: this.retentionMs })
        if (r.code === 0) { this.retentionDialog = false; this.$message.success('保留期已设置'); this.loadStreams() }
      } catch (e) { /* */ } finally { this.saving = false }
    }
  }
}
