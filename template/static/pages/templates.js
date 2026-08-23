window.TemplatesPage = {
  props: ['page', 'user', 'versionData'],
  template: `
<div class="tpl-page">
  <section class="tpl-hero">
    <div class="tpl-hero-grid"></div>
    <div class="tpl-hero-glow"></div>
    <div class="tpl-hero-content">
      <span class="tpl-eyebrow">DEPLOY TEMPLATES</span>
      <h1>部署模板</h1>
      <p>管理可复用的 Shell 部署脚本与变量声明</p>
    </div>
    <div class="tpl-hero-stats">
      <div class="tpl-hero-stat"><span class="tpl-hero-stat-val">{{list.length}}</span><span class="tpl-hero-stat-lbl">模板总数</span></div>
      <div class="tpl-hero-stat"><span class="tpl-hero-stat-val">{{builtinCount}}</span><span class="tpl-hero-stat-lbl">内置模板</span></div>
    </div>
  </section>

  <div class="page-card tpl-panel">
    <div class="card-header">
      <span class="title">模板列表</span>
      <el-button type="primary" @click="openDialog(null)">新增模板</el-button>
    </div>
    <el-table :data="list" style="width:100%" v-loading="loading" class="tpl-table">
      <el-table-column label="模板名称" min-width="160">
        <template #default="{row}"><span style="font-weight:600">{{row.name}}</span><el-tag v-if="row.is_builtin" size="small" type="info" style="margin-left:8px">内置</el-tag></template>
      </el-table-column>
      <el-table-column label="描述" min-width="200">
        <template #default="{row}"><span style="color:var(--text-sub);font-size:12.5px">{{row.description || '-'}}</span></template>
      </el-table-column>
      <el-table-column label="变量数" width="80" align="center">
        <template #default="{row}"><span class="mono">{{(row.variables||[]).length}}</span></template>
      </el-table-column>
      <el-table-column label="更新时间" width="170">
        <template #default="{row}"><span class="mono" style="font-size:12px;color:var(--text-faint)">{{formatTime(row.updated_at)}}</span></template>
      </el-table-column>
      <el-table-column label="" width="220" fixed="right">
        <template #default="{row}"><div class="ops-cell">
          <el-button size="small" text @click="openView(row)">查看</el-button>
          <el-button v-if="row.is_builtin" size="small" text @click="duplicate(row)">另存为副本</el-button>
          <template v-else>
            <el-button size="small" text @click="openDialog(row)">编辑</el-button>
            <el-button size="small" text type="danger" @click="delTemplate(row)">删除</el-button>
          </template>
        </div></template>
      </el-table-column>
    </el-table>
    <div v-if="!loading && !list.length" class="empty-state tpl-empty">
      <strong>暂无模板</strong><span>点击「新增模板」创建第一个部署脚本</span>
    </div>
  </div>

  <!-- 查看 / 编辑弹窗 -->
  <el-dialog v-model="dlgVisible" :title="dlgTitle" width="680px" :close-on-click-modal="false">
    <div v-if="viewMode && editing.is_builtin" class="tpl-view-hint">
      <svg viewBox="0 0 16 16" width="13" height="13" fill="currentColor"><path d="M8 15A7 7 0 1 1 8 1a7 7 0 0 1 0 14zm0 1A8 8 0 1 0 8 0a8 8 0 0 0 0 16z"/><path d="m8.93 6.588-2.29.287-.082.38.45.083c.294.07.352.176.288.469l-.738 3.468c-.194.897.105 1.319.808 1.319.545 0 1.178-.252 1.465-.598l.088-.416c-.2.176-.492.246-.686.246-.275 0-.375-.193-.304-.533L8.93 6.588zM9 4.5a1 1 0 1 1-2 0 1 1 0 0 1 2 0z"/></svg>
      <span>内置模板为只读，可另存为副本后修改</span>
    </div>
    <el-form :model="editing" label-position="top" ref="editFormRef" :rules="formRules">
      <div class="form-grid">
        <el-form-item label="模板名称" prop="name" required>
          <el-input v-model="editing.name" placeholder="如：nginx_reload" :disabled="viewMode" />
        </el-form-item>
        <el-form-item label="描述">
          <el-input v-model="editing.description" placeholder="简要说明模板用途" :disabled="viewMode" />
        </el-form-item>
      </div>

      <!-- 变量声明表 -->
      <el-form-item label="变量声明">
        <div class="tpl-var-table">
          <div class="tpl-var-head"><span>变量名</span><span>标签</span><span>默认值</span><span>必填</span><span v-if="!viewMode"></span></div>
          <div v-for="(v, i) in editing.variables" :key="i" class="tpl-var-row">
            <el-input v-model="v.name" size="small" placeholder="var_name" :disabled="viewMode" />
            <el-input v-model="v.label" size="small" placeholder="显示标签" :disabled="viewMode" />
            <el-input v-model="v.default" size="small" placeholder="默认值" :disabled="viewMode" />
            <el-switch v-model="v.required" size="small" :disabled="viewMode" />
            <el-button v-if="!viewMode" text type="danger" size="small" @click="editing.variables.splice(i,1)">
              <svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor"><path d="M5.5 5.5A.5.5 0 0 1 6 6v6a.5.5 0 0 1-1 0V6a.5.5 0 0 1 .5-.5zm2.5 0a.5.5 0 0 1 .5.5v6a.5.5 0 0 1-1 0V6a.5.5 0 0 1 .5-.5zm3 .5a.5.5 0 0 0-1 0v6a.5.5 0 0 0 1 0V6z"/><path fill-rule="evenodd" d="M14.5 3a1 1 0 0 1-1 1H13v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V4h-.5a1 1 0 0 1-1-1V2a1 1 0 0 1 1-1H5.5l1-1h3l1 1h2.5a1 1 0 0 1 1 1v1zM4.118 4L4 4.059V13a1 1 0 0 0 1 1h6a1 1 0 0 0 1-1V4.059L11.882 4H4.118zM2.5 3V2h11v1h-11z"/></svg>
            </el-button>
          </div>
          <el-button v-if="!viewMode" size="small" @click="addVar" class="tpl-add-var">+ 添加变量</el-button>
        </div>
      </el-form-item>

      <!-- 脚本区 -->
      <el-form-item label="脚本内容" prop="script">
        <el-input v-model="editing.script" type="textarea" :autosize="{minRows:6,maxRows:16}" :readonly="viewMode" placeholder="# 支持 {{var_name}} 占位符，变量须在上方声明&#10;#!/bin/bash&#10;echo 'Deploying to {{target}}...'" :class="{'tpl-script-readonly': viewMode}" class="tpl-script-input" />
        <div v-if="!viewMode && scriptWarnings.length" class="tpl-script-warn">
          <svg viewBox="0 0 16 16" width="13" height="13" fill="currentColor"><path d="M8.982 1.566a1.13 1.13 0 0 0-1.96 0L.165 13.233c-.457.778.091 1.767.98 1.767h13.713c.889 0 1.438-.99.98-1.767L8.982 1.566zM8 5c.535 0 .954.462.9.995l-.35 3.507a.552.552 0 0 1-1.1 0L7.1 5.995A.905.905 0 0 1 8 5zm.002 6a1 1 0 1 1 0 2 1 1 0 0 1 0-2z"/></svg>
          <span v-for="(w,i) in scriptWarnings" :key="i">{{w}}</span>
        </div>
      </el-form-item>
    </el-form>
    <template #footer>
      <!-- 只读模式 -->
      <template v-if="viewMode">
        <el-button @click="dlgVisible=false">关闭</el-button>
        <el-button v-if="editing.is_builtin" @click="dlgVisible=false;duplicate(editing)">另存为副本</el-button>
        <el-button v-else type="primary" @click="viewMode=false">编辑</el-button>
      </template>
      <!-- 编辑 / 新增模式 -->
      <template v-else>
        <el-button @click="dlgVisible=false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveTemplate">保存</el-button>
      </template>
    </template>
  </el-dialog>
</div>`,

  data() {
    return {
      list: [], loading: false, saving: false, dlgVisible: false, viewMode: false,
      editing: { name: '', description: '', script: '', variables: [] },
      formRules: {
        name: [{ required: true, message: '请输入模板名称', trigger: 'blur' }],
        script: [{ required: true, message: '请输入脚本内容', trigger: 'blur' }]
      }
    }
  },
  computed: {
    builtinCount() { return this.list.filter(t => t.is_builtin).length },
    dlgTitle() {
      if (this.viewMode) return '查看模板'
      return this.editing.id ? '编辑模板' : '新增模板'
    },
    scriptWarnings() {
      const vars = new Set(this.editing.variables.map(v => v.name).filter(Boolean))
      const placeholders = [...(this.editing.script || '').matchAll(/\{\{(\w+)\}\}/g)].map(m => m[1])
      const undeclared = [...new Set(placeholders)].filter(p => !vars.has(p))
      return undeclared.map(p => '未声明的占位符: {{' + p + '}}')
    }
  },
  mounted() { this.load() },
  methods: {
    async load() {
      this.loading = true
      try {
        const r = await api.get('/deploy/templates')
        if (r.code === 0) this.list = r.data || []
      } catch (e) { /* */ } finally { this.loading = false }
    },
    formatTime(t) {
      if (!t) return '-'
      const d = new Date(t.replace(' ', 'T') + (t.includes('Z') ? '' : 'Z'))
      return d.getFullYear() + '-' + String(d.getMonth()+1).padStart(2,'0') + '-' + String(d.getDate()).padStart(2,'0') + ' ' + String(d.getHours()).padStart(2,'0') + ':' + String(d.getMinutes()).padStart(2,'0')
    },
    openDialog(row) {
      this.viewMode = false
      if (row) {
        this.editing = { ...row, variables: (row.variables || []).map(v => ({...v})) }
      } else {
        this.editing = { name: '', description: '', script: '', variables: [] }
      }
      this.dlgVisible = true
    },
    openView(row) {
      this.viewMode = true
      this.editing = { ...row, variables: (row.variables || []).map(v => ({...v})) }
      this.dlgVisible = true
    },
    addVar() { this.editing.variables.push({ name: '', label: '', default: '', required: false }) },
    async saveTemplate() {
      if (!this.editing.name?.trim()) { ElMessage.warning('请输入模板名称'); return }
      if (!this.editing.script?.trim()) { ElMessage.warning('请输入脚本内容'); return }
      if (this.scriptWarnings.length) { ElMessage.warning('请先处理脚本中未声明的占位符'); return }
      for (const v of this.editing.variables) {
        if (!v.name || !/^[a-zA-Z_][a-zA-Z0-9_]*$/.test(v.name)) { ElMessage.warning('变量名 "' + (v.name||'') + '" 不合法'); return }
      }
      this.saving = true
      try {
        const body = { name: this.editing.name, description: this.editing.description, script: this.editing.script, variables: this.editing.variables }
        if (this.editing.id) { await api.put('/deploy/templates/' + this.editing.id, body) }
        else { await api.post('/deploy/templates', body) }
        ElMessage.success('保存成功'); this.dlgVisible = false; this.load()
      } catch (e) { ElMessage.error(e.response?.data?.message || '保存失败') } finally { this.saving = false }
    },
    delTemplate(row) {
      ElMessageBox.confirm('确认删除模板「' + row.name + '」？', '提示', { type: 'warning' }).then(async () => {
        try {
          const r = await api.delete('/deploy/templates/' + row.id)
          if (r.code === 0) { ElMessage.success('已删除'); this.load() }
          else ElMessage.error(r.message || '删除失败')
        } catch (e) { ElMessage.error(e.response?.data?.message || '删除失败') }
      }).catch(() => {})
    },
    async duplicate(row) {
      const body = { name: row.name + ' (副本)', description: row.description, script: row.script, variables: (row.variables || []).map(v => ({...v})) }
      try {
        await api.post('/deploy/templates', body)
        ElMessage.success('已创建副本'); this.load()
      } catch (e) { ElMessage.error(e.response?.data?.message || '复制失败') }
    }
  }
}
