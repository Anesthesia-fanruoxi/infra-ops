// 该套件独有的表单向导组件；app.js 按 window.StackFormBigdataRoles 全局注册，名称保持不变。
window.StackFormBigdataRoles = {
  props: ['ctx'],
  computed: {
    isHa() { return this.ctx.sharedParams?.ha === 'true' },
    roleComps() { return (window.StackForms.bigdata.masterComps || []).filter(c => this.ctx.bdSel.includes(c.key)) },
    haRoleGroups() {
      const fe = window.StackForms.bigdata
      return (fe.haRoles || []).filter(g => this.ctx.bdSel.includes(g.comp))
    },
    roleHint() {
      return this.ctx.wizardOp === 'add_component'
        ? '先勾选安装目标成员，再为本次加装组件指定主角色；留空则跟随集群 NameNode / 主节点'
        : '先勾选主机，再为各组件指定主角色所在主机；NameNode 必选，其余留空则跟随 NameNode'
    },
    conflictList() { return this.isHa ? haConflictList(this.ctx) : [] },
    haConflictText() {
      if (!this.isHa || !this.conflictList.length) return ''
      return '同组件主备角色不能在同一台主机：' + this.conflictList.map(c => c.label).join('、') + '（系统已按「全自动分配」落位可选覆盖）'
    },
    // 预览角色计划（docs/角色物化设计.md §5.1/§5.2）：只提交用户显式指定的角色，
    // 其余交由后端 planner 自动落位并标注 manual/auto 来源
    previewSummary() {
      const p = this.ctx.rolePreview
      if (!p) return ''
      let single = 0, manual = 0, all = 0
      ;(p.hosts || []).forEach(h => (h.roles || []).forEach(r => {
        if (r.scope === 'all') all++
        else { single++; if (r.source === 'manual') manual++ }
      }))
      return '落点角色 ' + single + ' 个 · 手动 ' + manual + ' · 自动 ' + (single - manual) + ' · 全员角色 ' + all + ' 项'
    }
  },
  methods: {
    // 仅收集显式指定的角色（空值不提交，保证后端来源标注准确）
    manualOverrides() {
      const fe = window.StackForms.bigdata
      const byId = {}
      this.ctx.hosts.forEach(h => { byId[h.id] = h.ip })
      const out = {}
      const push = (key, v) => {
        if (Array.isArray(v)) {
          const ips = v.map(id => byId[id]).filter(Boolean)
          if (ips.length) out[key] = ips.join(',')
        } else if (v && byId[v]) out[key] = byId[v]
      }
      ;(fe.haRoles || []).forEach(g => {
        if (!this.ctx.bdSel.includes(g.comp)) return
        ;(g.roles || []).forEach(r => push(r.key, this.ctx.masters[r.key]))
      })
      ;(fe.masterComps || []).forEach(mc => {
        if (!this.ctx.bdSel.includes(mc.key)) return
        push(mc.key, this.ctx.masters[mc.key])
      })
      return out
    },
    async previewPlan() {
      const ctx = this.ctx
      ctx.rolePreviewBusy = true
      try {
        const r = await api.post('/stacks/plan/preview', {
          mode: ctx.mode,
          host_ids: ctx.selectedHosts.map(h => h.id),
          master_host_id: ctx.resolveMasterHostId(),
          params: { ...ctx.sharedParams, components: ctx.bdSel.join(',') },
          masters: this.manualOverrides()
        })
        if (r.code !== 0) { ElMessage.error(r.message || '角色计划校验未通过'); ctx.rolePreview = null; return }
        ctx.rolePreview = r.data
      } catch (e) { ElMessage.error(e.message || '预览失败') }
      finally { ctx.rolePreviewBusy = false }
    },
    rolePlaceholder(c) {
      if (c.key === 'hdfs' && this.ctx.wizardOp === 'create') return '请指定 NameNode 主机'
      return this.ctx.wizardOp === 'add_component' ? '跟随集群主角色' : '跟随 NameNode'
    },
    rowConflict(grp, role) {
      if (role.multi || !this.isHa || !this.ctx.masters[role.key]) return false
      return (grp.roles || []).some(o => o !== role && !o.multi && this.ctx.masters[o.key] && this.ctx.masters[o.key] === this.ctx.masters[role.key])
    },
    groupConflict(grp) {
      return (grp.roles || []).some(r => this.rowConflict(grp, r))
    },
    haPlaceholder(role) {
      if (role.multi) return (role.min || 3) + ' 个自动分配 / 可多选覆盖'
      return role.required ? '请指定主机' : '自动分配'
    }
  },
  template: `
<div v-if="(ctx.wizardOp==='create' || ctx.wizardOp==='add_component') && (isHa ? haRoleGroups.length : roleComps.length)" class="stk-bd-role-plan">
  <div class="deploy-host-vars-title">角色规划 <span class="deploy-var-hint">{{isHa ? 'HA 全角色矩阵·留空自动分配' : roleHint}}</span></div>
  <!-- HA：分组角色矩阵，卡片小方块（一行多组件） -->
  <template v-if="isHa">
    <div class="stk-bd-ha-grid">
      <div v-for="grp in haRoleGroups" :key="grp.comp" class="stk-bd-ha-card" :class="{'is-conflict': groupConflict(grp)}">
        <div class="stk-bd-ha-head">
          <span class="stk-bd-ha-name">{{grp.label}}</span>
          <span class="stk-bd-ha-badge">HA</span>
          <span v-if="groupConflict(grp)" class="stk-bd-ha-conflict-tag">角色冲突</span>
        </div>
        <div class="stk-bd-ha-rows">
          <div v-for="r in grp.roles" :key="r.key" class="stk-bd-ha-row" :class="{'role-conflict': rowConflict(grp, r)}">
            <span class="stk-bd-ha-role">{{r.label}}<b v-if="r.required && !r.multi">*</b></span>
            <el-select v-if="r.multi" v-model="ctx.masters[r.key]" multiple collapse-tags :collapse-tags-tooltip="true" :placeholder="haPlaceholder(r)" size="small" class="stk-bd-ha-select">
              <el-option v-for="h in ctx.selectedHosts" :key="h.id" :label="h.name + '（' + h.ip + '）'" :value="h.id" />
            </el-select>
            <el-select v-else v-model="ctx.masters[r.key]" clearable :placeholder="haPlaceholder(r)" size="small" class="stk-bd-ha-select">
              <el-option v-for="h in ctx.selectedHosts" :key="h.id" :label="h.name + '（' + h.ip + '）'" :value="h.id" />
            </el-select>
          </div>
        </div>
      </div>
    </div>
    <div v-if="haConflictText" class="stk-bd-ha-conflict">{{haConflictText}}</div>
  </template>
  <!-- 非 HA：保持现状 -->
  <div v-else class="stk-bd-role-grid">
    <div v-for="c in roleComps" :key="c.key" class="stk-bd-role-item">
      <span class="stk-bd-role-label">{{c.label}}<b v-if="c.key==='hdfs' && ctx.wizardOp==='create'">*</b></span>
      <el-select v-model="ctx.masters[c.key]" :clearable="!(c.key==='hdfs' && ctx.wizardOp==='create')" :placeholder="rolePlaceholder(c)" size="small" style="flex:1">
        <el-option v-for="h in ctx.selectedHosts" :key="h.id" :label="h.name + '（' + h.ip + '）'" :value="h.id" />
      </el-select>
    </div>
  </div>
  <!-- 角色计划预览：部署前物化，冲突在提交前暴露（§5.1/§5.2） -->
  <div v-if="ctx.wizardOp==='create' || ctx.wizardOp==='add_component'" class="stk-plan-preview-bar">
    <el-button size="small" :loading="!!ctx.rolePreviewBusy" @click="previewPlan">{{ctx.rolePreview ? '刷新角色计划' : '预览角色计划'}}</el-button>
    <span class="deploy-var-hint">提交前物化校验：主备冲突、IP 越界在此暴露；「手」= 手动指定，「自」= 自动分配</span>
  </div>
  <div v-if="ctx.rolePreview" class="stk-plan-preview">
    <div class="stk-plan-preview-head">
      <span>角色计划 · rev {{ctx.rolePreview.rev}} · {{ctx.rolePreview.ha ? 'HA' : '单机'}}</span>
      <span class="deploy-var-hint">{{previewSummary}}</span>
    </div>
    <div class="stk-plan-preview-grid">
      <div v-for="h in ctx.rolePreview.hosts" :key="h.host_ip" class="stk-plan-preview-row">
        <span class="mono stk-plan-preview-ip">{{h.host_ip}}</span>
        <span class="stk-plan-preview-host">{{h.host_name}}</span>
        <div class="stk-plan-preview-chips">
          <span v-for="(r, i) in h.roles" :key="i" class="stk-plan-chip" :class="{'is-manual': r.source==='manual', 'is-all': r.scope==='all'}" :title="(r.source==='manual' ? '手动指定' : '自动分配') + ' · ' + (r.scope==='all' ? '全员角色' : '落点角色（部署后冻结）')">{{r.label}}<i>{{r.source==='manual' ? '手' : '自'}}</i></span>
          <span v-if="!(h.roles||[]).length" class="deploy-var-hint">无角色</span>
        </div>
      </div>
    </div>
    <div v-if="ctx.rolePreview.warnings && ctx.rolePreview.warnings.length" class="stk-plan-preview-warn">
      <div v-for="(w, i) in ctx.rolePreview.warnings" :key="i">{{w}}</div>
    </div>
  </div>
</div>`
}

// 组件自注册（StacksParts.register 合并语义）：套件内 <script> 顺序不影响装配，
// 也不再依赖 index.js 的加载时机。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  P.register('bigdata', { components: { 'stack-form-bigdata-roles': window.StackFormBigdataRoles } })
})()


// 计算 HA 角色矩阵中「同组件主备同机」的冲突组（后端同样规则兜底拦截）
function haConflictList(ctx) {
  const fe = window.StackForms.bigdata
  const groups = (fe.haRoles || []).filter(g => ctx.bdSel.includes(g.comp))
  const conflict = []
  groups.forEach(g => {
    const single = (g.roles || []).filter(r => !r.multi)
    const seen = {}
    const rowLabel = {}
    single.forEach(r => {
      const v = ctx.masters[r.key]
      if (!v) return
      if (seen[v]) {
        if (!rowLabel[v]) rowLabel[v] = [seen[v]]
        rowLabel[v].push(r.label)
      } else seen[v] = r.key
    })
    Object.keys(rowLabel).forEach(hid => {
      if (rowLabel[hid].length > 1) conflict.push({ comp: g.comp, label: g.label, hostId: Number(hid), roles: rowLabel[hid] })
    })
  })
  return conflict
}
