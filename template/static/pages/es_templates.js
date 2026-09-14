// ES 控制台 · 索引模板 tab（F8）：索引模板 / 组件模板 CRUD（W5–W8）+ _simulate 保存前模拟。
window.EsTemplatesTab = {
  props: ['conn'],
  template: `
<div>
  <div class="es-sub-head">索引模板</div>
  <el-table class="hosts-table" :data="indexTemplates" size="small" v-loading="loadingIdx">
    <el-table-column label="名称" min-width="180"><template #default="{row}"><span class="mono es-link" @click="showIdxDetail(row)">{{row.name}}</span><span v-if="row.managed" class="es-managed" style="margin-left:6px">ES 管理</span></template></el-table-column>
    <el-table-column label="匹配 pattern" min-width="180"><template #default="{row}"><span class="mono">{{(row.index_patterns||[]).join(', ')}}</span></template></el-table-column>
    <el-table-column label="优先级" width="90"><template #default="{row}"><span class="mono">{{row.priority ?? '-'}}</span></template></el-table-column>
    <el-table-column label="组件拼装" min-width="160"><template #default="{row}"><span class="mono">{{(row.composed_of||[]).join(' → ')}}</span></template></el-table-column>
    <el-table-column label="" width="170" fixed="right">
      <template #default="{row}">
        <div class="ops-cell">
          <el-button size="small" text type="primary" @click="editIdx(row)">编辑</el-button>
          <el-button size="small" text type="primary" @click="simulate(row)">模拟</el-button>
          <el-button size="small" text type="danger" :disabled="row.managed" @click="delIdx(row)">删除</el-button>
        </div>
      </template>
    </el-table-column>
    <template #empty><empty-state text="无索引模板" /></template>
  </el-table>
  <div class="es-toolbar" style="margin-top:8px"><el-button type="primary" @click="newIdx">新建索引模板</el-button></div>

  <div class="es-sub-head">组件模板</div>
  <el-table class="hosts-table" :data="compTemplates" size="small" v-loading="loadingComp">
    <el-table-column label="名称" min-width="200"><template #default="{row}"><span class="mono">{{row.name}}</span><span v-if="row.managed" class="es-managed" style="margin-left:6px">ES 管理</span></template></el-table-column>
    <el-table-column label="版本" width="90"><template #default="{row}"><span class="mono">{{row.version ?? '-'}}</span></template></el-table-column>
    <el-table-column label="" width="170" fixed="right">
      <template #default="{row}">
        <div class="ops-cell">
          <el-button size="small" text type="primary" @click="editComp(row)">编辑</el-button>
          <el-button size="small" text type="danger" :disabled="row.managed" @click="delComp(row)">删除</el-button>
        </div>
      </template>
    </el-table-column>
    <template #empty><empty-state text="无组件模板" /></template>
  </el-table>
  <div class="es-toolbar" style="margin-top:8px"><el-button @click="newComp">新建组件模板</el-button></div>

  <el-dialog v-model="editDialog" :title="dialogTitle" width="680px" append-to-body>
    <el-input v-if="editMode==='new'" v-model="editName" placeholder="模板名称（不得含 * , ? 或以 _ . 开头）" style="margin-bottom:10px" />
    <el-input v-model="editJSON" type="textarea" :rows="16" spellcheck="false" style="font-family:var(--font-mono)" />
    <template #footer>
      <el-button @click="editDialog=false">取消</el-button>
      <el-button @click="doSimulate" :loading="simulating">保存前模拟</el-button>
      <el-button type="primary" :loading="saving" @click="save">保存</el-button>
    </template>
  </el-dialog>

  <el-drawer v-model="simDrawer" title="模板模拟结果（_simulate）" size="55%">
    <div class="es-mapping-hint">给定假想索引名的合并结果：settings / mappings / aliases</div>
    <pre class="es-source" v-loading="simulating">{{simText}}</pre>
  </el-drawer>
</div>`,
  data() {
    return {
      indexTemplates: [], compTemplates: [], loadingIdx: false, loadingComp: false,
      editDialog: false, editMode: 'new', editKind: 'index', editName: '', editJSON: '', saving: false,
      simDrawer: false, simText: '', simulating: false, simTemplate: null
    }
  },
  computed: {
    dialogTitle() {
      const kind = this.editKind === 'index' ? '索引模板' : '组件模板'
      return (this.editMode === 'new' ? '新建' : '编辑') + kind + (this.editMode === 'edit' ? ' · ' + this.editName : '')
    }
  },
  mounted() { this.loadAll() },
  methods: {
    loadAll() { this.loadIndexTemplates(); this.loadCompTemplates() },
    async loadIndexTemplates() {
      this.loadingIdx = true
      try {
        const r = await api.get('/es/' + this.conn.id + '/index-templates')
        if (r.code === 0) {
          const t = (r.data && r.data.index_templates) || []
          this.indexTemplates = t.map(x => ({ name: x.name, managed: (x.name || '').startsWith('.'), ...(x.index_template || {}) }))
        }
      } catch (e) { /* */ } finally { this.loadingIdx = false }
    },
    async loadCompTemplates() {
      this.loadingComp = true
      try {
        const r = await api.get('/es/' + this.conn.id + '/component-templates')
        if (r.code === 0) {
          const t = (r.data && r.data.component_templates) || []
          this.compTemplates = t.map(x => ({ name: x.name, managed: (x.name || '').startsWith('.'), ...(x.component_template || {}) }))
        }
      } catch (e) { /* */ } finally { this.loadingComp = false }
    },
    newIdx() { this.editKind = 'index'; this.editMode = 'new'; this.editName = ''; this.editJSON = '{\n  "index_patterns": ["logs-*"],\n  "template": {\n    "settings": {},\n    "mappings": {}\n  }\n}'; this.editDialog = true },
    editIdx(row) { this.editKind = 'index'; this.editMode = 'edit'; this.editName = row.name; this.editJSON = JSON.stringify(row, null, 2); this.editDialog = true },
    newComp() { this.editKind = 'comp'; this.editMode = 'new'; this.editName = ''; this.editJSON = '{\n  "template": {\n    "settings": {},\n    "mappings": {}\n  }\n}'; this.editDialog = true },
    editComp(row) { this.editKind = 'comp'; this.editMode = 'edit'; this.editName = row.name; this.editJSON = JSON.stringify(row, null, 2); this.editDialog = true },
    async save() {
      let body
      try { body = JSON.parse(this.editJSON) } catch (e) { this.$message.error('JSON 不合法：' + e.message); return }
      const kind = this.editKind === 'index' ? 'index-templates' : 'component-templates'
      const verb = this.editKind === 'index' ? 'PUT /_index_template/' : 'PUT /_component_template/'
      try {
        await this.$confirm('即将向 ES 发出请求：\n' + verb + this.editName, '确认保存', { type: 'warning' })
      } catch (e) { return }
      this.saving = true
      try {
        const r = await api.put('/es/' + this.conn.id + '/' + kind + '/' + encodeURIComponent(this.editName), body)
        if (r.code === 0) { this.editDialog = false; this.$message.success('模板已保存'); this.loadAll() }
      } catch (e) { /* */ } finally { this.saving = false }
    },
    async delIdx(row) {
      try { await this.$confirm('即将向 ES 发出请求：\nDELETE /_index_template/' + row.name, '确认删除', { type: 'warning' }) } catch (e) { return }
      try { const r = await api.delete('/es/' + this.conn.id + '/index-templates/' + encodeURIComponent(row.name)); if (r.code === 0) { this.$message.success('已删除'); this.loadIndexTemplates() } } catch (e) { /* */ }
    },
    async delComp(row) {
      try { await this.$confirm('即将向 ES 发出请求：\nDELETE /_component_template/' + row.name, '确认删除', { type: 'warning' }) } catch (e) { return }
      try { const r = await api.delete('/es/' + this.conn.id + '/component-templates/' + encodeURIComponent(row.name)); if (r.code === 0) { this.$message.success('已删除'); this.loadCompTemplates() } } catch (e) { /* */ }
    },
    async showIdxDetail(row) {
      try {
        const r = await api.get('/es/' + this.conn.id + '/index-templates/' + encodeURIComponent(row.name))
        if (r.code === 0) this.editJSON = JSON.stringify(r.data.template, null, 2)
      } catch (e) { /* */ }
    },
    async simulate(row) {
      // 详情页试算：按已保存模板 + 假想索引名
      this.simTemplate = row
      try {
        const { value } = await this.$prompt('输入假想索引名（如 logs-2026.09.11）', '模板模拟 · ' + row.name, { inputValue: (row.index_patterns || ['logs-*'])[0] + '-2026.09.12' })
        this.simulating = true; this.simDrawer = true; this.simText = ''
        const r = await api.post('/es/' + this.conn.id + '/index-templates/_simulate', { name: row.name, index_name: value })
        if (r.code === 0) this.simText = JSON.stringify(r.data.template || r.data, null, 2)
      } catch (e) { /* 取消 */ } finally { this.simulating = false }
    },
    async doSimulate() {
      // 保存前模拟：以当前编辑内容 + 假想索引名
      let body
      try { body = JSON.parse(this.editJSON) } catch (e) { this.$message.error('JSON 不合法：' + e.message); return }
      try {
        const { value } = await this.$prompt('输入假想索引名', '保存前模拟 · ' + this.editName, { inputValue: (body.index_patterns || ['test-*'])[0] + '-test' })
        this.simulating = true; this.simDrawer = true; this.simText = ''
        const r = await api.post('/es/' + this.conn.id + '/index-templates/_simulate', { template: body, index_name: value })
        if (r.code === 0) this.simText = JSON.stringify(r.data.template || r.data, null, 2)
      } catch (e) { /* 取消 */ } finally { this.simulating = false }
    }
  }
}
