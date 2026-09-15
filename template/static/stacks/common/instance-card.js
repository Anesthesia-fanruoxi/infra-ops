// 套件部署页 · 通用骨架 · 实例卡片与实例抽屉：状态、组件标签、生命周期操作
// 迁自 pages/stacks.js（逐字保留），套件专属分支改为按槽位分发。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  // 源文件模块级助手（逗号列表 → 数组）：骨架统一提供在 StacksParts.splitList
  const splitList = P.splitList
  P.mixins.push({
    methods: {
    instCompTags(it) {
      if (!it) return []
      try {
        const arr = JSON.parse(it.components_json || '[]')
        return Array.isArray(arr) ? arr : []
      } catch (e) { return [] }
    },
    instDisplayComps(it) {
      return this.instCompTags(it).filter(c => c && c !== it.mode)
    },
    installedCompDefs(it) {
      if (!it) return []
      const defs = this.compsOf(it.stack_key)
      return this.instDisplayComps(it).map(k => defs.find(d => d.key === k) || { key: k, label: k, desc: '' })
    },
    async removeOneComp(it, def) {
      try {
        await ElMessageBox.confirm('将在全部成员上停止并卸载 ' + def.label + '，集群记录保留。', '卸载组件 · ' + def.label, {
          type: 'warning', confirmButtonText: '确认卸载', cancelButtonText: '取消'
        })
      } catch (e) { return }
      try {
        const hostIds = (it.hosts || []).filter(h => h.status !== 'removed').map(h => h.host_id)
        const r = await api.post('/stacks/instances/' + it.id + '/remove-component', {
          stack_key: it.stack_key, mode: it.mode, name: it.name,
          host_ids: hostIds, master_host_id: 0,
          params: { components: def.key }, host_params: {}
        })
        if (r.code !== 0) { ElMessage.error(r.message || '卸载组件失败'); return }
        ElMessage.success('已开始卸载组件')
        await this.afterStackOp({ ...r.data, op: 'remove_component' })
      } catch (e) { ElMessage.error(e.message || '卸载组件失败') }
    },
    compsOf(key) { return (window.StackForms && window.StackForms[key]?.comps) || [] },
    unusedCompsOf(it) {
      const comps = this.compsOf(it?.stack_key)
      if (!comps.length) return []
      const have = new Set(this.instCompTags(it))
      return comps.filter(c => !c.required && !have.has(c.key))
    },
    removableCompsOf(it) {
      const comps = this.compsOf(it?.stack_key)
      if (!comps.length) return []
      const locked = comps.filter(c => c.required).map(c => c.key)
      return this.instCompTags(it).filter(k => !locked.includes(k)).map(k => {
        const def = comps.find(c => c.key === k)
        return def || { key: k, label: k, desc: '已安装，可单独卸载' }
      })
    },
    busyInst(it) { return !it || it.status === 'deploying' },
    // 安装未成功（失败 / 部分成功），可重新安装或卸载
    instFailed(it) { return !!it && (it.status === 'failed' || it.status === 'partial') },
    instReady(it) { return !!it && it.status === 'ready' },
    instUninstalled(it) { return !!it && it.status === 'uninstalled' },
    canReinstall(it) { return !!(it && !this.busyInst(it) && it.status !== 'ready') },
    isPlatformStack(it) {
      if (!it) return false
      if (it.category === 'platform') return true
      return this.compsOf(it.stack_key).length > 0
    },
    activeInstHosts(it) { return (it?.hosts || []).filter(h => h.status !== 'removed') },
    isContainerStack(it) {
      if (!it) return true
      if (typeof it.requires_docker === 'boolean') return it.requires_docker
      const s = this.stacks.find(x => x.key === (it.stack_key || it.key))
      return !s || !!s.requires_docker
    },
    runtimeLabel(it) { return this.isContainerStack(it) ? '容器' : '主机安装' },
    stackAvatar(it) {
      const map = {
        redis: { t: 'R', c: 'stk-av-redis' },
        kafka: { t: 'K', c: 'stk-av-kafka' },
        elasticsearch: { t: 'ES', c: 'stk-av-es' },
        rabbitmq: { t: 'MQ', c: 'stk-av-rabbitmq' },
        rocketmq: { t: 'RM', c: 'stk-av-rocketmq' },
        nacos: { t: 'NA', c: 'stk-av-nacos' },
        powerjob: { t: 'PJ', c: 'stk-av-powerjob' },
        bigdata: { t: 'BD', c: 'stk-av-bigdata' }
      }
      const m = map[it && (it.stack_key || it.key)]
      if (m) return m
      const n = ((it && (it.stack_name || it.name)) || '?').trim()
      const ascii = n.match(/[A-Za-z]/)
      return { t: ascii ? ascii[0].toUpperCase() : n.slice(0, 1), c: this.isContainerStack(it) ? 'is-container' : 'is-host' }
    },
    compLabel(stackKey, k) {
      const def = this.compsOf(stackKey).find(c => c.key === k)
      if (def) return def.label
      // 运行期组件（不在组件目录里）给出可读名而不是裸键名
      return { metastore_db: 'Hive MetaDB', redis: 'Redis', sentinel: 'Sentinel' }[k] || k
    },
    instStatusType(s) { return { ready: 'success', deploying: 'warning', partial: 'danger', failed: 'danger', uninstalled: 'info' }[s] || 'info' },
    instStatusLabel(s) { return { ready: '就绪', deploying: '变更中', partial: '部分成功', failed: '失败', uninstalled: '已卸载' }[s] || s },
    applyInstanceContext(it) {
      this.targetInstance = it
      this.selectedKey = it.stack_key
      this.mode = it.mode
      this.memberHostIds = new Set((it.hosts || []).filter(h => h.status !== 'removed').map(h => h.host_id))
      try { this.sharedParams = JSON.parse(it.params_json || '{}') || {} } catch (e) { this.sharedParams = {} }
      const fe = window.StackForms && window.StackForms[it.stack_key]
      if (fe?.initSelection) fe.initSelection(this, it)
    },
    async prepareInstance(it, op) {
      this.resetWizard()
      this.wizardOp = op || 'create'
      let full = it
      try {
        const r = await api.get('/stacks/instances/' + it.id)
        if (r.code === 0) full = r.data
      } catch (e) { /* */ }
      this.applyInstanceContext(full)
      this.wizardVisible = true
      this.loadHosts()
      return full
    },
    openWizard() {
      this.resetWizard()
      this.wizardOp = 'create'
      this.wizardVisible = true
      this.loadStacks()
    },
    async openScaleOut(it) {
      await this.prepareInstance(it, 'scale_out')
      this.step = 2
    },
    async openScaleIn(it) {
      await this.prepareInstance(it, 'scale_in')
      // 落点角色保护（全部套件，docs/角色物化设计.md §3.3）：承载落点角色的成员置灰，仅数据/工作节点可缩容
      this.scaleInProtected = {}
      try {
        const r = await api.get('/stacks/instances/' + it.id + '/plan')
        if (r.code === 0 && r.data) {
          const m = {}
          ;(r.data.hosts || []).forEach(h => {
            const singles = (h.roles || []).filter(x => (x.scope || '') === '')
            if (singles.length) m[h.host_ip] = singles.map(x => x.label).join('+')
          })
          this.scaleInProtected = m
        }
      } catch (e) { /* 读取失败时由后端兜底拦截 */ }
      this.step = 2
    },
    async openAddComp(it) {
      const full = await this.prepareInstance(it, 'add_component')
      this.bdSel = []
      this.step = 1
      this.selectedHostIds = new Set(this.memberHostIds)
      const master = (full.hosts || []).find(h => h.role === 'master' && h.status !== 'removed')
      this.masterHostId = master ? master.host_id : 0
    },
    async openRemoveComp(it) {
      await this.prepareInstance(it, 'remove_component')
      this.bdSel = []
      this.step = 1
    },
    async openInstance(it) {
      this.instDrawerVisible = true
      this.instDetail = it
      this.instRuns = []
      try {
        const d = await api.get('/stacks/instances/' + it.id)
        if (d.code === 0) this.instDetail = d.data
      } catch (e) { /* */ }
      this.loadInstPlan(it.id)
      try {
        const r = await api.get('/stacks/instances/' + it.id + '/runs')
        if (r.code === 0) this.instRuns = r.data || []
      } catch (e) { /* */ }
    },
    async loadInstPlan(id) {
      this.instPlan = null
      try {
        const r = await api.get('/stacks/instances/' + id + '/plan')
        if (r.code === 0) this.instPlan = r.data
      } catch (e) { /* */ }
    },
    async renameInstance(it) {
      try {
        const { value } = await ElMessageBox.prompt('集群名称', '重命名', { inputValue: it.name, confirmButtonText: '保存', cancelButtonText: '取消' })
        const name = (value || '').trim()
        if (!name) return
        const r = await api.patch('/stacks/instances/' + it.id, { name })
        if (r.code !== 0) { ElMessage.error(r.message || '更新失败'); return }
        ElMessage.success('已改名')
        this.loadInstances()
        if (this.instDrawerVisible && this.instDetail && this.instDetail.id === it.id) this.openInstance(this.instDetail)
      } catch (e) { /* cancel */ }
    },
    async reinstallInstance(it) {
      try {
        await ElMessageBox.confirm('第一步会自动清理：停止并移除各节点残留容器、清空服务数据目录（避免上次失败污染），随后按原主机和参数全新部署。实例记录保留。', '重新安装 ' + it.name, {
          type: 'warning', confirmButtonText: '开始重装', cancelButtonText: '取消'
        })
      } catch (e) { return }
      try {
        const r = await api.post('/stacks/instances/' + it.id + '/reinstall')
        if (r.code !== 0) { ElMessage.error(r.message || '重装失败'); return }
        ElMessage.success('已开始重新安装')
        await this.afterStackOp({ ...r.data, op: 'reinstall' })
      } catch (e) { ElMessage.error(e.message || '重装失败') }
    },
    async uninstallInstance(it) {
      try {
        await ElMessageBox.confirm('将停止并移除全部节点上的套件容器；服务数据目录默认保留（重装会先自动清空），实例记录保留为「已卸载」。', '卸载 ' + it.name, {
          type: 'warning', confirmButtonText: '确认卸载', cancelButtonText: '取消'
        })
      } catch (e) { return }
      try {
        const r = await api.post('/stacks/instances/' + it.id + '/uninstall')
        if (r.code !== 0) { ElMessage.error(r.message || '卸载失败'); return }
        ElMessage.success('已开始卸载')
        await this.afterStackOp({ ...r.data, op: 'uninstall' })
      } catch (e) { ElMessage.error(e.message || '卸载失败') }
    },
    async purgeInstance(it) {
      try {
        await ElMessageBox.confirm(
          '⚠️ 将停止并删除全部节点上的套件容器、数据卷与服务目录（' + (it.home_hint || '含 Hive/HBase 等全部数据') + '），删除后不可恢复！' +
          '清理完成后集群转为「已卸载」，可重新安装。',
          '清理残留 · ' + it.name, {
            type: 'error', confirmButtonText: '确认清理', cancelButtonText: '取消'
          })
      } catch (e) { return }
      try {
        const r = await api.post('/stacks/instances/' + it.id + '/uninstall', { purge: true })
        if (r.code !== 0) { ElMessage.error(r.message || '清理失败'); return }
        ElMessage.success('已开始清理残留')
        await this.afterStackOp({ ...r.data, op: 'uninstall' })
      } catch (e) { ElMessage.error(e.message || '清理失败') }
    },
    async deleteInstance(it) {
      if (it.status !== 'uninstalled') { ElMessage.warning('请先卸载后再删除'); return }
      const msg = '将删除本地集群配置与全部流程记录，不可恢复。服务器上的服务已在卸载时停止。'
      try {
        await ElMessageBox.confirm(msg, '删除 ' + it.name, { type: 'warning', confirmButtonText: '删除记录', cancelButtonText: '取消' })
      } catch (e) { return }
      try {
        const r = await api.delete('/stacks/instances/' + it.id)
        if (r.code !== 0) { ElMessage.error(r.message || '删除失败'); return }
        ElMessage.success('已删除记录')
        this.instDrawerVisible = false
        this.loadInstances()
      } catch (e) { ElMessage.error(e.message || '删除失败') }
    },
    },
  })

  P.mixins.push({
    computed: {
    // 模板裸引用（v-if="caDownloadAvailable"）必须是 computed；放 methods 里求值为函数对象恒真
    // （曾导致所有套件的实例抽屉都显示「下载 CA 证书」，实际仅 ES 冷热温 + SSL 开启有证书）
    caDownloadAvailable() { const h = this.hook(this.instKey(), 'caDownloadAvailable'); return h ? !!h.call(this, this) : false },
    },
    methods: {
    // —— 套件专属展示的分发点 ——
    instKey() { const it = this.instDetail || this.verifyTarget; return (it && it.stack_key) || '' },
    instIsHa(it) { const h = this.hook(it && it.stack_key, 'instIsHa'); return h ? !!h.call(this, this, it) : false },
    memTags(h) { const hk = this.hook(this.instKey(), 'memTags'); return hk ? hk.call(this, this, h) : [] },
    instTierStyle(it) { const h = this.hook(it && it.stack_key, 'instTierStyle'); return h ? h.call(this, this, it) : null },
    async downloadCa() { const h = this.hook(this.instKey(), 'downloadCa'); if (h) return h.call(this, this) },
    }
  })
})()
