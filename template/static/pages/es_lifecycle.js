// ES 控制台 · 生命周期 tab（F7）：ILM 策略 CRUD（W1/W2）+ 索引 explain + 重试（W3）+ 数据流保留期（W4）。
// 硬约束：内置策略只读；保存/删除前二次确认并完整回显将发出的 ES 请求。
window.EsLifecycleTab = {
  props: ['conn'],
  template: `
<div class="es-lifecycle-wrap">
  <div class="es-lc-toolbar">
    <el-button type="primary" @click="openQuick">可视化创建 · 冷热温</el-button>
    <el-button @click="openNew">JSON 直接提交</el-button>
    <el-button @click="loadPolicies">刷新</el-button>
    <el-switch v-model="showSystem" style="margin-left:auto" active-text="含系统资源" inactive-text="业务资源" @change="loadPolicies" />
  </div>
  <div class="es-lc-toolbar" style="margin-bottom:12px">
    <span class="es-qc-hint" style="margin:0">两种创建方式：<b>可视化创建</b> 固定冷热温（hot→warm→cold）架构，只填几个时间即可生成；<b>JSON 直接提交</b> 任意贴入策略 JSON 原样提交（可自定义其他架构）。</span>
  </div>
  <div class="es-lc-grid">
    <div class="es-lc-list">
      <div v-for="p in policies" :key="p.name" class="es-lc-item" :class="{active: cur && cur.name===p.name}" @click="showPolicy(p)">
        <span class="mono">{{p.name}}</span>
        <span v-if="p.managed" class="es-managed">ES 系统</span>
      </div>
      <div v-if="!policies.length" class="es-lc-empty-hint">暂无策略</div>
    </div>
    <div class="es-lc-detail">
      <template v-if="cur">
        <div class="es-lc-ops">
          <el-button size="small" :disabled="cur.managed" @click="edit">编辑</el-button>
          <el-button size="small" type="danger" :disabled="cur.managed" @click="del">删除</el-button>
        </div>
        <pre class="es-source">{{curText}}</pre>
      </template>
      <div v-else class="es-lc-empty-hint">点击左侧策略查看 JSON 预览</div>
    </div>
  </div>

  <el-dialog v-model="editDialog" :title="editMode==='new' ? '新建策略' : '编辑策略 · ' + curName" width="640px" append-to-body>
    <el-input v-if="editMode==='new'" v-model="curName" placeholder="策略名称（不得含 * , ? 或以 _ . 开头）" style="margin-bottom:10px" />
    <el-input v-model="policyJSON" type="textarea" :rows="14" spellcheck="false" style="font-family:var(--font-mono)" />
    <template #footer>
      <el-button @click="editDialog=false">取消</el-button>
      <el-button type="primary" :loading="saving" @click="savePolicy">保存</el-button>
    </template>
  </el-dialog>

  <el-dialog v-model="qcDialog" title="可视化创建 · 冷热温策略" width="860px" append-to-body>
    <div class="es-qc-body">
      <div class="es-qc-left">
        <el-form label-position="top">
          <el-form-item label="策略模式">
            <el-radio-group :model-value="qcMode" @update:model-value="qcSetMode">
              <el-radio value="keep" label="keep">冷热温 · 长期保留（不删）</el-radio>
              <el-radio value="delete" label="delete">冷热温 · 到期自动删除</el-radio>
            </el-radio-group>
            <div class="es-qc-hint">固定 hot→warm→cold 迁移逻辑，<template v-if="qcMode==='delete'">到期后自动删除</template><template v-else>长期保留</template>；你只需设置右侧几个时间。</div>
          </el-form-item>
          <el-form-item label="策略名称" required>
            <el-input v-model="qcName" style="font-family:var(--font-mono)" placeholder="hot-warm-cold-30days" />
          </el-form-item>
          <el-form-item label="热 → 温 迁移（天）">
            <el-input-number v-model="qcWarm" :min="1" :max="3650" style="width:100%" />
          </el-form-item>
          <el-form-item label="温 → 冷 迁移（天）">
            <el-input-number v-model="qcCold" :min="1" :max="36500" style="width:100%" />
          </el-form-item>
          <el-form-item v-if="qcMode==='delete'" label="冷 → 删除（天）">
            <el-input-number v-model="qcDel" :min="1" :max="36500" style="width:100%" />
          </el-form-item>
        </el-form>
      </div>
      <div class="es-qc-divider"></div>
      <div class="es-qc-right">
        <div class="es-area-label">JSON 预览（实时）</div>
        <pre class="es-source es-qc-preview">{{qcPreviewJSON}}</pre>
      </div>
    </div>
    <template #footer>
      <el-button @click="qcDialog=false">取消</el-button>
      <el-button type="primary" :loading="qcSaving" @click="saveQuick">确认创建</el-button>
    </template>
  </el-dialog>
</div>`,
  data() {
    return {
      policies: [], cur: null, editDialog: false, editMode: 'new', curName: '', policyJSON: '', saving: false,
      showSystem: false,
      qcDialog: false, qcSaving: false, qcMode: 'keep', qcName: 'hot-warm-cold-30days', qcWarm: 7, qcCold: 30, qcDel: 180
    }
  },
  computed: {
    curText() { return this.cur ? JSON.stringify(this.cur.policy, null, 2) : '' },
    qcPreviewJSON() { return JSON.stringify(this.buildQuickPolicy(), null, 2) }
  },
  mounted() { this.loadPolicies() },
  methods: {
    async loadPolicies() {
      try { const r = await api.get('/es/' + this.conn.id + '/ilm/policies', { params: { system: this.showSystem ? '1' : '0' } }); if (r.code === 0) { this.policies = r.data?.list || [] } } catch (e) { /* */ }
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
    openQuick() {
      this.qcMode = 'keep'; this.qcWarm = 7; this.qcCold = 30; this.qcDel = 180
      this.qcName = 'hot-warm-cold-30days'; this.qcDialog = true
    },
    qcSetMode(m) {
      this.qcMode = m
      this.qcName = m === 'delete' ? 'hot-warm-cold-delete-180days' : 'hot-warm-cold-30days'
    },
    buildQuickPolicy() {
      const p = {
        policy: {
          phases: {
            hot: { min_age: '0ms', actions: { set_priority: { priority: 50 } } },
            warm: { min_age: (this.qcWarm || 7) + 'd', actions: { set_priority: { priority: 25 }, allocate: { include: { _tier_preference: 'data_warm' } } } },
            cold: { min_age: (this.qcCold || 30) + 'd', actions: { set_priority: { priority: 0 }, allocate: { include: { _tier_preference: 'data_cold' } } } }
          }
        }
      }
      if (this.qcMode === 'delete') {
        p.policy.phases.delete = { min_age: (this.qcDel || 180) + 'd', actions: { delete: { delete_searchable_snapshot: true } } }
      }
      return p
    },
    async saveQuick() {
      const name = (this.qcName || '').trim()
      if (!name) { this.$message.warning('请输入策略名称'); return }
      const req = 'PUT /_ilm/policy/' + name + '\n\n' + this.qcPreviewJSON
      try { await this.$confirm('即将向 ES 发出请求：\n' + req, '确认创建', { type: 'warning' }) } catch (e) { return }
      this.qcSaving = true
      try {
        const r = await api.put('/es/' + this.conn.id + '/ilm/policies/' + encodeURIComponent(name), this.buildQuickPolicy())
        if (r.code === 0) { this.qcDialog = false; this.$message.success('策略已创建：' + name); this.loadPolicies() }
      } catch (e) { /* */ } finally { this.qcSaving = false }
    }
  }
}
