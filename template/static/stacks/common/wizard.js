// 套件部署页 · 通用骨架 · 向导骨架：步骤流转、套件/模式选择、参数装配与提交
// 迁自 pages/stacks.js（逐字保留），套件专属分支改为按槽位分发。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  // 源文件模块级助手（逗号列表 → 数组）：骨架统一提供在 StacksParts.splitList
  const splitList = P.splitList
  P.mixins.push({
  data() {
    return {
      step: 1, stacks: [], stacksLoading: false, selectedKey: '', mode: '',
      bdSel: ['hdfs'], replicas: '0', masterHostId: 0, masters: {},
      hosts: [], hostsLoading: false, hostsLoaded: false, selectedHostIds: new Set(),
      hostFilter: '', hostSort: { field: 'name', order: 'asc' },
      sharedParams: {}, hostParams: {},
      deploying: false, wizardVisible: false, wizardOp: 'create', clusterName: '',
      targetInstance: null, memberHostIds: new Set(),
      scaleInProtected: {}, instPlan: null, opPlan: null, opPlanErr: '', opPlanLoading: false,
      instances: [], instancesLoading: false,
      instDrawerVisible: false, instDetail: null, instRuns: [],
      verifyVisible: false, verifyTarget: null, verifyResult: null, verifying: false, verifyActiveTab: 'all',
      preflightVisible: false, preflight: {},
      drawerVisible: false, recordMeta: null, recordHosts: [], recordLogs: [], logFilter: '',
      recordPlan: null, recordSteps: [], runPoll: null,
      // 部署资产就绪状态（第二步套件表单消费，如大数据 HA 的 MySQL 驱动）
      assetCheck: [], assetCheckBusy: false,
      sseLog: null, setupSse: null, logAutoScroll: true
    }
  },
  mounted() { this.loadInstances(); this.loadStacks(); this.connectSetup() },
  watch: {
    // 主节点变更 / 角色规划联动：清理失效指向
    masterHostId() { this.syncMasters() }
  },
  beforeUnmount() { this.closeDrawer(); this.closeSetup() },
    computed: {
    selectedStack() { return this.stacks.find(s => s.key === this.selectedKey) || null },
    stackGroups() {
      const defs = [
        { key: 'service', label: '单服务集群', hint: '一个中间件做成主从 / 哨兵 / 集群；卡片标明容器或主机安装' },
        { key: 'platform', label: '组合套件', hint: '多个组件按层部署，例如 HDFS → Spark / Flink / Hive' }
      ]
      return defs.map(d => ({
        ...d,
        stacks: this.stacks.filter(s => (s.category || 'service') === d.key)
      })).filter(g => g.stacks.length)
    },
    selectedMode() { return (this.selectedStack?.modes || []).find(m => m.key === this.mode) || null },
    selfCtx() { return this },
    formEntry() { return (window.StackForms && window.StackForms[this.selectedKey]) || null },
    step1FormComp() { return this.formEntry?.selectComp || null },
    opFormComp() { return this.formEntry?.opComp || null },
    step2FormComp() { return this.formEntry?.step2Comp || null },
    step1Hints() { return this.formEntry?.hints || [] },
    wizardTitle() {
      const name = this.targetInstance?.name || ''
      return {
        create: '新建集群', scale_out: '扩容 · ' + name, scale_in: '缩容 · ' + name,
        add_component: '加装组件 · ' + name, remove_component: '卸载组件 · ' + name
      }[this.wizardOp] || '套件部署'
    },
    wizardFirstStepTitle() {
      return { create: '选择套件', add_component: '选择加装组件', remove_component: '选择卸载组件' }[this.wizardOp] || '选择套件'
    },
    wizardStepIndex() {
      if (this.wizardOp === 'create' || this.wizardOp === 'add_component' || this.wizardOp === 'remove_component') return this.step - 1
      if (this.wizardOp === 'scale_in') return 0
      return this.step === 3 ? 1 : 0
    },
    defaultClusterName() {
      const s = this.selectedStack
      const m = this.selectedMode
      if (!s) return ''
      return s.name + '-' + (m?.label || this.mode || '')
    },
    unusedWizardComps() {
      if (this.wizardOp === 'remove_component') return this.removableCompsOf(this.targetInstance)
      return this.unusedCompsOf(this.targetInstance)
    },
    canStep2() {
      if (this.wizardOp === 'add_component' || this.wizardOp === 'remove_component') return this.bdSel.length > 0
      return !!(this.selectedKey && this.mode)
    },
    canStep3() {
      if (this.selectedHosts.length < this.minHosts) return false
      if (this.wizardOp === 'scale_in' && this.selectedHosts.length >= this.memberHostIds.size) return false
      if (this.formEntry?.canStep3 && this.formEntry.canStep3(this) === false) return false
      if (this.needMaster && !this.selectedHosts.some(h => h.id === this.masterHostId)) return false
      return true
    },
    canRun() {
      if (!this.canStep3) return false
      return this.visibleSharedVars.every(v => {
        if (!v.required) return true
        return !!(this.sharedParams[v.name] || v.default || '').trim()
      })
    },
    wizardHint() {
      if (this.step === 2 && !this.canStep3) {
        // 套件专属拦截提示（冷热温缺 master / 大数据缺 NameNode）；未实现则给通用文案
        const hk = this.hook(this.selectedKey, 'step2BlockedHint')
        const msg = hk ? (hk.call(this, this) || '') : ''
        if (msg) return msg
        return '主机数量或角色规划不满足当前模式'
      }
      if (this.step === 3 && !this.canRun) {
        const miss = this.visibleSharedVars.find(v => v.required && !(this.sharedParams[v.name] || v.default || '').trim())
        return miss ? '请填写' + miss.label : '参数未填写完整'
      }
      return ''
    },
    runningCount() { return this.instances.filter(t => t.status === 'deploying').length },
    logDrawerLocked() {
      return !!(this.drawerVisible && this.recordMeta && this.recordMeta.op === 'add_component' && this.recordMeta.status === 'running')
    },
    preflightReady() {
      if (this.preflight.unreachable && this.preflight.unreachable.length) return false
      // 部署前角色预览（全部套件）：预览未生成或失败时不允许确认部署
      if (this.needOpPlan && !this.opPlan) return false
      return true
    },
    filteredLogs() {
      if (!this.logFilter) return this.recordLogs
      const kw = this.logFilter.toLowerCase()
      return this.recordLogs.filter(l => (l.ip||'').toLowerCase().includes(kw) || (l.text||'').toLowerCase().includes(kw) || (l.phase||'').toLowerCase().includes(kw))
    },
    },
    methods: {
    async loadStacks() {
      this.stacksLoading = true
      try { const r = await api.get('/stacks'); if (r.code === 0) this.stacks = r.data || [] } catch (e) { /* */ }
      finally { this.stacksLoading = false }
    },
    async loadHosts() {
      if (this.hostsLoading) return
      this.hostsLoading = true
      try {
        const r = await api.get('/hosts', { params: { page: 1, page_size: 200, status: 'online' } })
        if (r.code === 0) { this.hosts = r.data?.list || []; this.hostsLoaded = true }
      } catch (e) { /* */ } finally { this.hostsLoading = false }
    },
    async loadInstances() {
      this.instancesLoading = true
      try { const r = await api.get('/stacks/instances', { params: { page: 1, page_size: 50 } }); if (r.code === 0) this.instances = r.data?.list || [] } catch (e) { /* */ }
      finally { this.instancesLoading = false }
    },
    resetWizard() {
      this.step = 1; this.selectedKey = ''; this.mode = ''; this.replicas = '0'; this.masterHostId = 0; this.masters = {}
      Object.values(window.StackForms || {}).forEach(fe => { if (fe.resetSelection) fe.resetSelection(this) })
      this.selectedHostIds = new Set(); this.sharedParams = {}; this.hostParams = {}
      this.preflightVisible = false; this.preflight = {}; this.deploying = false
      this.wizardOp = 'create'; this.clusterName = ''; this.targetInstance = null; this.memberHostIds = new Set()
      this.opPlan = null; this.opPlanErr = ''; this.opPlanLoading = false
      this.assetCheck = []; this.assetCheckBusy = false
    },
    selectStack(s) {
      this.selectedKey = s.key
      this.masters = {}
      const fe = window.StackForms && window.StackForms[s.key]
      if (fe?.onSelect) fe.onSelect(this)
      if (s.modes && s.modes.length) this.onModeChange(s.modes[0].key)
    },
    formToggle(key, val) {
      const fe = this.formEntry
      if (fe?.toggle) fe.toggle(this, key, val)
      this.syncMasters()
    },
    onModeChange(key) {
      this.mode = key
      const m = (this.selectedStack?.modes || []).find(x => x.key === key)
      const home = m?.default_home_dir || (this.selectedStack?.host_vars || []).find(v => v.name === 'home_dir')?.default || '/data'
      const shared = {}
      ;(this.selectedStack?.shared_vars || []).forEach(v => {
        if (v.modes && v.modes.length && !v.modes.includes(key)) return
        shared[v.name] = this.sharedParams[v.name] || v.default || ''
      })
      this.sharedParams = shared
      this.hostVars.forEach(v => {
        if (v.name === 'home_dir') v._modeDefault = home
      })
    },
    onBoolVar(name, val) {
      this.$set(this.sharedParams, name, val ? 'true' : 'false')
    },
    onHaToggle(val) {
      this.sharedParams.ha = val ? 'true' : 'false'
      const fe = this.formEntry
      if (fe?.onHaToggle) fe.onHaToggle(this, val)
    },
    goStep(n) {
      this.step = n
      if (n >= 2) {
        Promise.resolve(this.loadHosts()).then(() => {
          if (this.needMaster && !this.masterHostId && this.selectedHosts.length) {
            this.masterHostId = this.selectedHosts[0].id
          }
          if (this.useRolePlan) this.ensureDefaultRoles()
          this.$nextTick(() => this.syncRoleHostTableSelection())
        })
      }
    },
    async startPreflight() {
      // 部署前角色预览（全部套件，docs/角色物化设计.md §二）：参数已就绪，与部署前置一同确认落点
      if (this.needOpPlan) {
        await this.refreshOpPlan()
        if (!this.opPlan) { ElMessage.error(this.opPlanErr || '角色计划预览未生成，无法部署'); return }
      }
      if (!this.selectedStack?.requires_docker) {
        await this.confirmRun()
        return
      }
      this.deploying = true
      try {
        const r = await api.post('/stacks/preflight', { host_ids: this.selectedHosts.map(h => h.id) })
        if (r.code !== 0) { ElMessage.error(r.message || '预检失败'); return }
        this.preflight = r.data || {}
        this.preflightVisible = true
      } catch (e) { ElMessage.error(e.message || '预检失败') }
      finally { this.deploying = false }
    },
    async confirmRun() {
      this.deploying = true
      try {
        const r = await this.submitWizard()
        if (!r) return
        if (r.code !== 0) { ElMessage.error(r.message || '创建失败'); return }
        ElMessage.success('已开始执行')
        this.preflightVisible = false
        const startedOp = this.wizardOp
        this.wizardVisible = false
        await this.afterStackOp({ ...r.data, op: startedOp })
      } catch (e) { ElMessage.error(e.message || '创建失败') }
      finally { this.deploying = false }
    },
    async confirmScaleIn() {
      try {
        await ElMessageBox.confirm('将停止并移除选中节点上的套件服务，集群记录会保留。', '确认缩容', { type: 'warning' })
      } catch (e) { return }
      this.deploying = true
      try {
        const r = await this.submitWizard()
        if (!r) return
        if (r.code !== 0) { ElMessage.error(r.message || '缩容失败'); return }
        ElMessage.success('已开始缩容')
        const startedOp = this.wizardOp
        this.wizardVisible = false
        await this.afterStackOp({ ...r.data, op: startedOp })
      } catch (e) { ElMessage.error(e.message || '缩容失败') }
      finally { this.deploying = false }
    },
    async confirmRemoveComp() {
      try {
        await ElMessageBox.confirm('将在全部成员上卸载所选组件。HDFS 不能单独卸载，需整集群卸载。', '确认卸载组件', { type: 'warning' })
      } catch (e) { return }
      this.deploying = true
      try {
        const r = await this.submitWizard()
        if (!r) return
        if (r.code !== 0) { ElMessage.error(r.message || '卸载组件失败'); return }
        ElMessage.success('已开始卸载组件')
        const startedOp = this.wizardOp
        this.wizardVisible = false
        await this.afterStackOp({ ...r.data, op: startedOp })
      } catch (e) { ElMessage.error(e.message || '卸载组件失败') }
      finally { this.deploying = false }
    },
    async submitWizard() {
      const host_params = {}
      this.selectedHosts.forEach(h => { if (this.hostParams[h.id]) host_params[String(h.id)] = this.hostParams[h.id] })
      const extra = (this.formEntry?.extraParams && this.formEntry.extraParams(this)) || {}
      const params = { ...this.sharedParams, ...extra }
      const body = {
        stack_key: this.selectedKey, mode: this.mode,
        name: this.clusterName.trim(),
        host_ids: this.selectedHosts.map(h => h.id),
        master_host_id: this.resolveMasterHostId(),
        params, host_params
      }
      if (this.wizardOp === 'create') return api.post('/stacks/run', body)
      const id = this.targetInstance?.id
      if (!id) { ElMessage.error('缺少集群实例'); return null }
      const path = { scale_out: 'scale-out', scale_in: 'scale-in', add_component: 'add-component', remove_component: 'remove-component' }[this.wizardOp]
      return api.post('/stacks/instances/' + id + '/' + path, body)
    },
    async afterStackOp(data) {
      await this.loadInstances()
      if (this.instDrawerVisible && this.instDetail?.id) {
        await this.openInstance(this.instDetail)
      }
      const runId = data && (data.run_id || data.id)
      if (runId) {
        this.instDrawerVisible = false
        await this.openDrawer({ id: runId, status: 'running', op: data.op || this.wizardOp || 'create' })
      }
    },
    },
  })

  P.mixins.push({
    methods: {
    // —— 套件能力分发（骨架不认识任何套件）——
    drv(key) { return (window.StackDrivers && window.StackDrivers[key]) || null },
    hook(key, name) {
      const d = this.drv(key)
      const h = d && d.hooks && d.hooks[name]
      return typeof h === 'function' ? h : null
    },
    }
  })
})()
