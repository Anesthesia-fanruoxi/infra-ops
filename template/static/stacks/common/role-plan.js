// 套件部署页 · 通用骨架 · 角色计划：主角色落点、预览、缩容保护与角色文案
// 迁自 pages/stacks.js（逐字保留），套件专属分支改为按槽位分发。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  // 源文件模块级助手（逗号列表 → 数组）：骨架统一提供在 StacksParts.splitList
  const splitList = P.splitList
  P.mixins.push({
    computed: {
    needMaster() { return this.wizardOp === 'create' && !!this.selectedMode?.assign_master && !this.useRolePlan },
    // 部署前角色预览门禁（全部套件，docs/角色物化设计.md §二·全套件覆盖）：
    // bigdata 走 useRolePlan 分支自带预览；其余套件在主机勾选后自动生成，未出预览不能进下一步
    needOpPlan() {
      return !this.useRolePlan && (this.wizardOp === 'create' || this.wizardOp === 'scale_out')
    },
    // 大数据等：表格勾选 + 按组件指定主角色（不再用单一「主节点」单选）
    useRolePlan() {
      return !!(this.formEntry?.masterComps?.length) &&
        (this.wizardOp === 'create' || this.wizardOp === 'add_component')
    },
    },
    methods: {
    // 角色规划：清理未勾选组件 / 已移除主机的指向
    syncMasters() {
      const fe = this.formEntry
      if (!fe?.masterComps && !fe?.haRoles) return
      const valid = new Set(this.selectedHostIds)
      const keep = new Set()
      ;(fe.masterComps || []).forEach(mc => { if (this.bdSel.includes(mc.key)) keep.add(mc.key) })
      ;(fe.haRoles || []).forEach(g => { if (this.bdSel.includes(g.comp)) g.roles.forEach(r => keep.add(r.key)) })
      Object.keys(this.masters).forEach(k => {
        const v = this.masters[k]
        if (!keep.has(k)) { delete this.masters[k]; return }
        if (Array.isArray(v)) this.masters[k] = v.filter(id => valid.has(id))
        else if (!valid.has(v)) delete this.masters[k]
      })
    },
    isHaMode() { return this.sharedParams?.ha === 'true' },
    // 提交用：组件→主角色/从角色主机 IP 的 JSON（未指定的回落 NameNode / 主节点）
    mastersForSubmit() {
      const fe = this.formEntry
      if (!fe?.masterComps && !fe?.haRoles) return ''
      const byId = {}
      this.hosts.forEach(h => { byId[h.id] = h.ip })
      const nnId = this.masters.hdfs || this.resolveMasterHostId()
      const primary = (nnId && byId[nnId]) || (this.hosts.find(h => h.id === this.masterHostId) || {}).ip || ''
      const out = {}
      if (!this.isHaMode() || !fe.haRoles) {
        ;(fe.masterComps || []).forEach(mc => {
          if (!this.bdSel.includes(mc.key)) return
          const ip = (this.masters[mc.key] && byId[this.masters[mc.key]]) || primary
          if (ip) out[mc.key] = ip
        })
        return Object.keys(out).length ? JSON.stringify(out) : ''
      }
      // HA：全角色矩阵（主角色行与从角色 key 一同提交，后端确定性算法接管自动落点）
      ;(fe.haRoles || []).forEach(grp => {
        if (!this.bdSel.includes(grp.comp)) return
        grp.roles.forEach(r => {
          const v = this.masters[r.key]
          if (r.multi) {
            const ips = (Array.isArray(v) ? v : []).map(id => byId[id]).filter(Boolean)
            if (ips.length) out[r.key] = ips.join(',')
          } else {
            let ip = v && byId[v]
            if (r.key === grp.comp && !ip) ip = primary
            if (ip) out[r.key] = ip
          }
        })
      })
      return Object.keys(out).length ? JSON.stringify(out) : ''
    },
    resolveMasterHostId() {
      if (this.useRolePlan) {
        const nn = this.masters.hdfs
        if (nn && this.selectedHostIds.has(nn)) return nn
        if (this.masterHostId && this.selectedHostIds.has(this.masterHostId)) return this.masterHostId
        return this.selectedHosts[0]?.id || 0
      }
      if (this.needMaster) return this.masterHostId
      return this.masterHostId || 0
    },
    hostRolePlanSummary(h) {
      const fe = this.formEntry
      if ((!fe?.masterComps && !fe?.haRoles) || !h) return ''
      const labels = []
      ;(fe.masterComps || []).forEach(mc => {
        if (!this.bdSel.includes(mc.key)) return
        if (this.masters[mc.key] === h.id) labels.push(mc.label)
      })
      ;(fe.haRoles || []).forEach(grp => {
        if (!this.bdSel.includes(grp.comp)) return
        grp.roles.forEach(r => {
          const v = this.masters[r.key]
          if (r.multi) {
            if (Array.isArray(v) && v.includes(h.id)) labels.push(r.label.replace(/ ×\d+$/, ''))
          } else if (v === h.id && !labels.includes(r.label)) {
            labels.push(r.label)
          }
        })
      })
      return labels.join(' · ')
    },
    ensureDefaultRoles() {
      if (!this.useRolePlan || !this.selectedHosts.length) return
      const first = this.selectedHosts[0].id
      // 新建：NameNode 默认落在首台已选主机
      if (this.wizardOp === 'create' && this.bdSel.includes('hdfs')) {
        if (!this.masters.hdfs || !this.selectedHostIds.has(this.masters.hdfs)) {
          this.masters = { ...this.masters, hdfs: first }
        }
      }
      this.masterHostId = this.resolveMasterHostId()
    },
    // 角色类型 → 文字颜色（底框颜色区分手动/自动，文字颜色区分角色，docs/角色物化设计.md §五）
    roleTextClass(r) {
      const role = (r || {}).role || ''
      if (['nn1','rm1','spark_m1','jm1','hm1','ms1','hs2a','db','coordinator','master','boot_master','boot','seed'].includes(role)) return 'rt-primary'
      if (['nn2','rm2','spark_m2','jm2','hm2','ms2','hs2b','replica','slave','data'].includes(role)) return 'rt-standby'
      if (['zk','jn','zkfc','broker'].includes(role)) return 'rt-quorum'
      if (['ui','sentinel'].includes(role)) return 'rt-aux'
      return 'rt-worker'
    },
    // 部署前角色预览（全部套件，docs/角色物化设计.md §二）：create 取所选主机；scale_out 取既有成员 + 新选主机
    async refreshOpPlan() {
      if (!this.needOpPlan || !this.selectedKey || !this.mode) return
      const ids = this.wizardOp === 'scale_out'
        ? [...new Set([...this.memberHostIds, ...this.selectedHostIds])]
        : [...this.selectedHostIds]
      if (!ids.length) { this.opPlan = null; this.opPlanErr = ''; return }
      const masterHost = ((this.targetInstance || {}).hosts || []).find(h => h.role === 'master' && h.status !== 'removed')
      this.opPlanLoading = true
      this.opPlanErr = ''
      try {
        const r = await api.post('/stacks/plan/preview', {
          stack_key: this.selectedKey,
          mode: this.mode,
          host_ids: ids,
          master_host_id: this.masterHostId || (this.wizardOp === 'scale_out' && masterHost ? masterHost.host_id : 0),
          params: this.sharedParams || {},
          host_params: this.hostParams || {}
        })
        if (r.code === 0) { this.opPlan = r.data; this.opPlanErr = '' }
        else { this.opPlan = null; this.opPlanErr = r.message || '角色计划预览失败' }
      } catch (e) {
        this.opPlan = null; this.opPlanErr = (e && e.message) || '角色计划预览失败'
      }
      this.opPlanLoading = false
    },
    async replanInst() {
      if (!this.instDetail) return
      try {
        const r = await api.post('/stacks/instances/' + this.instDetail.id + '/replan')
        if (r.code !== 0) { ElMessage.error(r.message || '重规划失败'); return }
        ElMessage.success('角色计划已重规划' + (r.data && r.data.rev ? '（rev ' + r.data.rev + '）' : ''))
        this.instPlan = r.data
      } catch (e) { ElMessage.error(e.message || '重规划失败') }
    },
    hostRoleTag(h) {
      const isMaster = h.id === this.masterHostId
      const t = this.formEntry?.roleTag?.(this, isMaster)
      if (t) return t
      const hk = this.hook(this.selectedKey, 'memberRoleTag')
      const v = hk ? (hk.call(this, this, isMaster) || '') : ''
      if (v) return v
      if (isMaster) return '主'
      return '工作节点'
    },
    },
  })
})()
