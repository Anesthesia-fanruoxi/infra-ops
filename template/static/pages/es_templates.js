// ES 控制台 · 索引模板 tab：索引模板 CRUD + _simulate 保存前模拟。
// 仅展示业务模板（剔除 ES 系统默认），不做系统/业务切换；组件模板不在此展示。
// 两种创建入口并存：可视化创建（es_template_quick.js，表单 + 实时预览，可连带生成公共层组件模板）
// 与 JSON 直接提交（本文件，覆盖 aliases / analyzer / dynamic_templates 等未表单化的能力）。
window.EsTemplatesTab = {
  props: ['conn'],
  components: { 'es-template-quick': window.EsTemplateQuickDialog },
  template: `
<div class="es-tpl-wrap">
  <div class="es-toolbar">
    <el-button type="primary" @click="openQuick">可视化创建 · 索引模板</el-button>
    <el-button @click="newIdx">JSON 直接提交</el-button>
    <el-button @click="loadIndexTemplates">刷新</el-button>
  </div>
  <div class="es-qc-hint" style="margin-bottom:12px">两种创建方式：<b>可视化创建</b> 表单化填写并实时预览将发出的请求，自动读取已存在的生命周期策略、探测 pattern 命中的时间字段、检测与已有模板的优先级重叠；<b>JSON 直接提交</b> 任意贴入模板 JSON 原样提交。</div>
  <el-table class="hosts-table" :data="indexTemplates" size="small" v-loading="loadingIdx">
    <el-table-column label="名称" min-width="180"><template #default="{row}"><span class="mono es-link" @click="showIdxDetail(row)">{{row.name}}</span></template></el-table-column>
    <el-table-column label="匹配 pattern" min-width="180"><template #default="{row}"><span class="mono">{{(row.index_patterns||[]).join(', ')}}</span></template></el-table-column>
    <el-table-column label="优先级" width="90"><template #default="{row}"><span class="mono">{{row.priority ?? '-'}}</span></template></el-table-column>
    <el-table-column label="组件拼装" min-width="160"><template #default="{row}"><span class="mono">{{(row.composed_of||[]).join(' → ')}}</span></template></el-table-column>
    <el-table-column label="" width="170" fixed="right">
      <template #default="{row}">
        <div class="ops-cell">
          <el-button size="small" text type="primary" @click="editIdx(row)">编辑</el-button>
          <el-button size="small" text type="primary" @click="simulate(row)">模拟</el-button>
          <el-button size="small" text type="danger" @click="delIdx(row)">删除</el-button>
        </div>
      </template>
    </el-table-column>
    <template #empty><empty-state text="无索引模板" /></template>
  </el-table>

  <el-dialog v-model="editDialog" :title="'索引模板' + (editMode==='new' ? ' · 新建' : ' · 编辑 · ' + editName)" width="680px" append-to-body>
    <el-input v-if="editMode==='new'" v-model="editName" placeholder="模板名称（不得含 * , ? 或以 _ . 开头）" style="margin-bottom:10px" />
    <el-input v-model="editJSON" type="textarea" :rows="16" spellcheck="false" style="font-family:var(--font-mono)" />
    <template #footer>
      <el-button @click="editDialog=false">取消</el-button>
      <el-button @click="doSimulate" :loading="simulating">保存前模拟</el-button>
      <el-button type="primary" :loading="saving" @click="save">保存</el-button>
    </template>
  </el-dialog>

  <el-drawer v-model="simDrawer" title="模板模拟结果（_simulate）" size="55%" class="es-raw-drawer">
    <div class="es-mapping-hint">给定假想索引名的合并结果：settings / mappings / aliases</div>
    <pre class="es-source" v-loading="simulating">{{simText}}</pre>
  </el-drawer>

  <es-template-quick ref="quick" :conn="conn" @saved="loadIndexTemplates" />
</div>`,
  data() {
    return {
      indexTemplates: [], loadingIdx: false,
      editDialog: false, editMode: 'new', editName: '', editJSON: '', saving: false,
      simDrawer: false, simText: '', simulating: false
    }
  },
  mounted() { this.loadIndexTemplates() },
  methods: {
    async loadIndexTemplates() {
      this.loadingIdx = true
      try {
        const r = await api.get('/es/' + this.conn.id + '/index-templates')
        if (r.code === 0) {
          const t = (r.data && r.data.index_templates) || []
          this.indexTemplates = t.map(x => ({ name: x.name, ...(x.index_template || {}) }))
        }
      } catch (e) { /* */ } finally { this.loadingIdx = false }
    },
    newIdx() { this.editMode = 'new'; this.editName = ''; this.editJSON = '{\n  "index_patterns": ["logs-*"],\n  "template": {\n    "settings": {},\n    "mappings": {}\n  }\n}'; this.editDialog = true },
    openQuick() { this.$refs.quick.open() },
    editIdx(row) { this.editMode = 'edit'; this.editName = row.name; this.editJSON = JSON.stringify(row, null, 2); this.editDialog = true },
    async save() {
      let body
      try { body = JSON.parse(this.editJSON) } catch (e) { this.$message.error('JSON 不合法：' + e.message); return }
      try {
        await this.$confirm('即将向 ES 发出请求：\nPUT /_index_template/' + this.editName, '确认保存', { type: 'warning' })
      } catch (e) { return }
      this.saving = true
      try {
        const r = await api.put('/es/' + this.conn.id + '/index-templates/' + encodeURIComponent(this.editName), body)
        if (r.code === 0) { this.editDialog = false; this.$message.success('模板已保存'); this.loadIndexTemplates() }
      } catch (e) { /* */ } finally { this.saving = false }
    },
    async delIdx(row) {
      try { await this.$confirm('即将向 ES 发出请求：\nDELETE /_index_template/' + row.name, '确认删除', { type: 'warning' }) } catch (e) { return }
      try { const r = await api.delete('/es/' + this.conn.id + '/index-templates/' + encodeURIComponent(row.name)); if (r.code === 0) { this.$message.success('已删除'); this.loadIndexTemplates() } } catch (e) { /* */ }
    },
    async showIdxDetail(row) {
      try {
        const r = await api.get('/es/' + this.conn.id + '/index-templates/' + encodeURIComponent(row.name))
        if (r.code === 0) this.editJSON = JSON.stringify(r.data.template, null, 2)
      } catch (e) { /* */ }
    },
    async simulate(row) {
      // 详情页试算：按已保存模板 + 假想索引名
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