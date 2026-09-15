// 套件部署页 · 通用骨架 · 主机表与逐主机参数：筛选 / 排序 / 勾选 / 参数编辑
// 迁自 pages/stacks.js（逐字保留），套件专属分支改为按槽位分发。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  // 源文件模块级助手（逗号列表 → 数组）：骨架统一提供在 StacksParts.splitList
  const splitList = P.splitList
  P.mixins.push({
    computed: {
    filteredHosts() {
      let list = this.hosts
      if (this.wizardOp === 'scale_out') list = list.filter(h => !this.memberHostIds.has(h.id))
      if (this.wizardOp === 'scale_in' || this.wizardOp === 'add_component') list = list.filter(h => this.memberHostIds.has(h.id))
      if (this.hostFilter) {
        const kw = this.hostFilter.toLowerCase()
        list = list.filter(h => (h.name||'').toLowerCase().includes(kw) || (h.ip||'').toLowerCase().includes(kw) || (h.tag||'').toLowerCase().includes(kw))
      }
      return this.sortHostList(list, this.hostSort)
    },
    hostVars() {
      const list = this.selectedStack?.host_vars || []
      const byMode = list.filter(v => !v.modes || !v.modes.length || v.modes.includes(this.mode))
      const fn = this.formEntry?.filterHostVars
      return fn ? fn(this, byMode) : byMode
    },
    visibleSharedVars() {
      const list = (this.selectedStack?.shared_vars || [])
        .filter(v => !v.modes || !v.modes.length || v.modes.includes(this.mode))
      const fn = this.formEntry?.visibleSharedVars
      return fn ? fn(this, list) : list
    },
    selectedHosts() { return this.hosts.filter(h => this.selectedHostIds.has(h.id)) },
    minHosts() {
      if (this.wizardOp === 'scale_out' || this.wizardOp === 'scale_in' || this.wizardOp === 'add_component') return 1
      const v = this.formEntry?.minHosts?.(this)
      if (typeof v === 'number') return v
      return this.selectedMode?.min_hosts || 2
    },
    hostHint() {
      if (this.wizardOp === 'scale_out') return '选择要加入的新主机'
      if (this.wizardOp === 'scale_in') return '选择要移除的成员（不能全删，请用卸载）'
      if (this.useRolePlan) {
        return this.wizardOp === 'add_component'
          ? '勾选安装目标成员，再在下方为各组件指定主角色'
          : '至少 ' + this.minHosts + ' 台；勾选后在下方按组件指定 NameNode / Master 等主角色'
      }
      return this.selectedMode?.host_hint || ''
    },
    },
    methods: {
    onRoleHostSelection(rows) {
      if (this._syncingRoleTable) return
      this.selectedHostIds = new Set((rows || []).map(h => h.id))
      this.syncMasters()
      this.ensureDefaultRoles()
      this.refreshOpPlanDebounced()
    },
    syncRoleHostTableSelection() {
      if (!this.useRolePlan) return
      const table = this.$refs.roleHostTable
      if (!table) return
      const rows = this.filteredHosts || []
      this._syncingRoleTable = true
      table.clearSelection()
      rows.forEach(h => {
        if (this.selectedHostIds.has(h.id)) table.toggleRowSelection(h, true)
      })
      this.$nextTick(() => { this._syncingRoleTable = false })
    },
    toggleHost(id) {
      const next = new Set(this.selectedHostIds)
      if (next.has(id)) next.delete(id); else next.add(id)
      this.selectedHostIds = next
      if (this.needMaster) {
        if (!next.has(this.masterHostId)) this.masterHostId = next.size ? [...next][0] : 0
      }
      this.syncMasters()
      if (this.useRolePlan) this.ensureDefaultRoles()
      this.$nextTick(() => this.syncRoleHostTableSelection())
      this.refreshOpPlanDebounced()
    },
    toggleSelectAll() {
      // 缩容（bigdata）：全选跳过承载落点角色的成员
      const pool = this.filteredHosts.filter(h => !this.scaleInProtected[h.ip])
      if (this.selectedHosts.length === pool.length && pool.length) this.selectedHostIds = new Set()
      else this.selectedHostIds = new Set(pool.map(h => h.id))
      if (this.needMaster && this.selectedHosts.length && !this.selectedHostIds.has(this.masterHostId)) {
        this.masterHostId = this.selectedHosts[0]?.id || 0
      }
      this.syncMasters()
      if (this.useRolePlan) this.ensureDefaultRoles()
      this.$nextTick(() => this.syncRoleHostTableSelection())
      this.refreshOpPlanDebounced()
    },
    toggleHostSortOrder() { this.hostSort = { ...this.hostSort, order: this.hostSort.order === 'asc' ? 'desc' : 'asc' } },
    sortHostList(list, sort) {
      const arr = list.slice()
      const cmp = sort.field === 'ip' ? window.cmpHostIP : window.cmpHostName
      arr.sort(cmp)
      if (sort.order === 'desc') arr.reverse()
      return arr
    },
    hostVarPlaceholder(v) {
      if (v.name === 'home_dir') return this.selectedMode?.default_home_dir || v.default
      return v.default || ''
    },
    hostParamValue(id, v) { return (this.hostParams[id] && this.hostParams[id][v.name]) || '' },
    onHostParam(id, name, val) {
      const cur = { ...(this.hostParams[id] || {}) }
      cur[name] = val
      this.hostParams = { ...this.hostParams, [id]: cur }
    },
    copyFirstToAll() {
      const first = this.selectedHosts[0]
      if (!first) return
      const src = this.hostParams[first.id] || {}
      const next = { ...this.hostParams }
      this.selectedHosts.slice(1).forEach(h => { next[h.id] = { ...src } })
      this.hostParams = next
    },
    },
  })
})()
