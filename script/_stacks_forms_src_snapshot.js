// 套件表单注册表（方案 C：公共骨架 + 套件表单插槽）。
// 公共向导骨架（stacks.js）按 selectedKey 在 window.StackForms 取差异化交互；
// 未注册的套件走骨架的通用渲染路径（GenericForm），零功能损失。
//
// 每个注册条目可包含：
//   comps           组件定义数组（平台类套件「组件多选」的数据源，实例卡片/加装/卸载也用它）
//   selectComp      第 1 步「选择套件」时替换通用模式卡的组件名（kebab-case，需在 app.js 注册）
//   opComp          加装/卸载组件（wizardOp=add/remove_component）第 1 步的组件名
//   step2Comp       第 2 步追加在主机列表上方的组件名
//   hints           第 1 步追加在底部的提示文案数组
//   onSelect(ctx)   点击套件卡片时初始化选中态
//   resetSelection(ctx)        向导重置时清空选中态
//   initSelection(ctx, it)     从集群实例回填选中态（扩容/缩容/加装等操作）
//   toggle(ctx, key, val)      组件勾选切换（含依赖联动）
//   minHosts(ctx)   返回数字则覆盖通用 min_hosts，返回 null/undefined 走通用
//   canStep3(ctx)   返回 false 则阻止进入第 3 步，返回 true 走通用
//   visibleSharedVars(ctx, list)  过滤第 3 步共享参数列表
//   extraParams(ctx)              提交时附加的隐藏参数（components/replicas 等）
//   roleTag(ctx, isMaster)        第 3 步主节点/成员角色标签文案，返回空走通用
//
// ctx 即向导骨架（stacks.js 页面实例）：bdSel/replicas 等选中态由骨架持有，钩子经 ctx 读写。
;(function () {
  const bigdataComps = [
    { key: 'hdfs', label: 'HDFS 存储', required: true, desc: '1 NameNode + N DataNode，集群存储基座，必选' },
    { key: 'zookeeper', label: 'ZooKeeper', required: false, desc: '协调服务，全体节点 ensemble（HBase 依赖）' },
    { key: 'yarn', label: 'YARN', required: false, desc: 'ResourceManager + NodeManager 资源调度' },
    { key: 'spark', label: 'Spark 计算', required: false, desc: 'standalone 1 Master + N Worker' },
    { key: 'flink', label: 'Flink 计算', required: false, desc: 'session 1 JobManager + N TaskManager' },
    { key: 'hive', label: 'Hive 数仓', required: false, desc: 'Metastore + HiveServer2（单实例，依赖 HDFS；Trino 依赖）' },
    { key: 'hbase', label: 'HBase', required: false, deps: ['zookeeper'], desc: 'NoSQL 列存数据库，依赖 ZooKeeper' },
    { key: 'trino', label: 'Trino', required: false, deps: ['hive'], desc: '交互式 SQL 查询引擎，依赖 Hive Metastore' }
  ]

  // bigdata 变量 → 组件归属（第 3 步参数随勾选联动）
  const bigdataVarComp = {
    image: 'hdfs', nn_rpc_port: 'hdfs', replication: 'hdfs',
    image_zookeeper: 'zookeeper',
    nm_mem: 'yarn', nm_vcores: 'yarn',
    image_spark: 'spark', master_port: 'spark', webui_port: 'spark', worker_cores: 'spark', worker_mem: 'spark',
    image_flink: 'flink', jm_rpc_port: 'flink', tm_slots: 'flink',
    image_hive: 'hive',
    image_hbase: 'hbase',
    image_trino: 'trino', trino_http_port: 'trino', trino_mem: 'trino'
  }

  window.StackForms = {
    bigdata: {
      comps: bigdataComps,
      selectComp: 'stack-form-bigdata-select',
      opComp: 'stack-form-bigdata-op',
      step2Comp: 'stack-form-bigdata-roles',
      // 可规划主角色的组件（zookeeper 为 ensemble 不在此列）
      masterComps: [
        { key: 'hdfs', label: 'HDFS NameNode' },
        { key: 'yarn', label: 'YARN ResourceManager' },
        { key: 'spark', label: 'Spark Master' },
        { key: 'flink', label: 'Flink JobManager' },
        { key: 'hive', label: 'Hive (Metastore/HS2)' },
        { key: 'hbase', label: 'HBase HMaster' },
        { key: 'trino', label: 'Trino Coordinator' }
      ],
      onSelect(ctx) { ctx.bdSel = ['hdfs'] },
      resetSelection(ctx) { ctx.bdSel = ['hdfs']; ctx.masters = {} },
      initSelection(ctx, it) {
        let tags = []
        try { tags = JSON.parse(it.components_json || '[]') || [] } catch (e) { tags = [] }
        ctx.bdSel = Array.isArray(tags) && tags.length ? tags.slice() : ['hdfs']
      },
      onHaToggle(ctx, val) {
        const sel = new Set(ctx.bdSel)
        if (val) {
          if (!sel.has('zookeeper')) {
            sel.add('zookeeper')
            ElMessage.success('HA 依赖 ZooKeeper，已自动勾选')
          }
        } else {
          sel.delete('metastore_db') // 关闭 HA 移除 Hive 元数据库组件
        }
        ctx.bdSel = [...sel]
      },
      minHosts(ctx) { return ctx.sharedParams?.ha === 'true' ? 3 : null },
      toggle(ctx, key, val) {
        const c = bigdataComps.find(x => x.key === key)
        if (c?.required && ctx.wizardOp === 'create') return
        const set = new Set(ctx.bdSel)
        // 卸载模式：勾选的是「要移除的组件」，不联动依赖（否则卸 Trino 会顺手把 Hive 也标上）
        if (ctx.wizardOp === 'remove_component') {
          if (val) set.add(key)
          else set.delete(key)
          ctx.bdSel = [...set]
          return
        }
        if (val) {
          set.add(key)
          // 依赖自动联动：勾 HBase 带上 ZooKeeper，勾 Trino 带上 Hive；HA+Hive 带上 metastore_db
          ;(c?.deps || []).forEach(d => set.add(d))
        } else {
          if (key === 'zookeeper' && ctx.sharedParams?.ha === 'true') {
            ElMessage.warning('HA 模式下 ZooKeeper 为必选，请先关闭高可用')
            return
          }
          // 被依赖的组件不能取消（先取消依赖它的组件）
          const needed = bigdataComps.some(x => ctx.bdSel.includes(x.key) && (x.deps || []).includes(key))
          if (needed) {
            ElMessage.warning(`组件 ${c.label} 正被其他已选组件依赖，请先取消对应组件`)
            return
          }
          set.delete(key)
        }
        // HA+Hive 时同步维护 metastore_db
        const haHive = ctx.sharedParams?.ha === 'true' && set.has('hive')
        if (haHive) set.add('metastore_db')
        else set.delete('metastore_db')
        if (ctx.wizardOp === 'remove_component' || ctx.wizardOp === 'add_component') {
          ctx.bdSel = [...set]
          return
        }
        ctx.bdSel = bigdataComps.map(x => x.key).filter(k => set.has(k))
        if (set.has('metastore_db')) ctx.bdSel.push('metastore_db')
      },
      canStep3(ctx) {
        // 新建时 NameNode 必须明确指定（角色规划的锚点）
        if (ctx.wizardOp === 'create' && ctx.bdSel.includes('hdfs')) {
          if (!ctx.masters.hdfs || !ctx.selectedHostIds.has(ctx.masters.hdfs)) return false
        }
        // HA：同组件主从角色同机会拦截角色矩阵冲突
        if (ctx.sharedParams?.ha === 'true' && haConflictList(ctx).length) return false
        return true
      },
      // HA 全角色矩阵定义（对应设计文档 §3.2）：group.comp 决定随勾选组件渲染
      haRoles: [
        { comp: 'hdfs', label: 'HDFS 存储', roles: [
          { key: 'hdfs', label: 'NN1 (Active)', required: true },
          { key: 'hdfs_nn2', label: 'NN2 (Standby)' },
          { key: 'hdfs_jns', label: 'JournalNode ×3', multi: true, min: 3 }
        ] },
        { comp: 'zookeeper', label: 'ZooKeeper', roles: [
          { key: 'zookeeper_ips', label: 'Ensemble 节点', multi: true, min: 3 }
        ] },
        { comp: 'yarn', label: 'YARN 调度', roles: [
          { key: 'yarn', label: 'RM1 (Active)' },
          { key: 'yarn_rm2', label: 'RM2 (Standby)' }
        ] },
        { comp: 'spark', label: 'Spark 计算', roles: [
          { key: 'spark', label: 'Master-1' },
          { key: 'spark_m2', label: 'Master-2' }
        ] },
        { comp: 'flink', label: 'Flink 计算', roles: [
          { key: 'flink', label: 'JobManager-1' },
          { key: 'flink_jm2', label: 'JobManager-2' }
        ] },
        { comp: 'hbase', label: 'HBase 存储', roles: [
          { key: 'hbase', label: 'HMaster-1' },
          { key: 'hbase_hm2', label: 'HMaster-2' }
        ] },
        { comp: 'hive', label: 'Hive 数仓', roles: [
          { key: 'hive', label: 'Metastore-1 / HS2-1' },
          { key: 'hive_ms2', label: 'Metastore-2' },
          { key: 'hive_hs2b', label: 'HiveServer2-2' },
          { key: 'hive_db', label: 'MetaDB (MySQL)' }
        ] }
      ],
      visibleSharedVars(ctx, list) {
        const haOnly = ['hdfs_nameservice', 'jn_http_port', 'jn_rpc_port', 'hive_db_image', 'hive_db_password']
        const haOn = ctx.sharedParams?.ha === 'true'
        return list.filter(v => v.name !== 'components')
          // 高可用已在第 1 步确定，填写参数页不再展示该开关
          .filter(v => v.name !== 'ha')
          .filter(v => !bigdataVarComp[v.name] || ctx.bdSel.includes(bigdataVarComp[v.name]))
          .filter(v => !haOnly.includes(v.name) || haOn)
      },
      extraParams(ctx) {
        const out = { components: ctx.bdSel.join(',') }
        const m = ctx.mastersForSubmit()
        if (m) out.masters = m
        return out
      }
    },

    redis: {
      step2Comp: 'stack-form-redis-topo',
      hints: ['单机 Redis 请使用「基础建设」中的部署 Redis 模板。'],
      minHosts(ctx) { return ctx.mode === 'cluster' ? (ctx.replicas === '1' ? 6 : 3) : null },
      canStep3(ctx) {
        if (ctx.mode === 'cluster' && ctx.replicas === '1' && ctx.selectedHosts.length % 2 !== 0) return false
        return true
      },
      visibleSharedVars(ctx, list) { return list.filter(v => v.name !== 'replicas') },
      extraParams(ctx) { return ctx.mode === 'cluster' ? { replicas: String(ctx.replicas) } : {} }
    },

    elasticsearch: {
      step2Comp: 'stack-form-elasticsearch-roles',
      roleTag(ctx, isMaster) { return isMaster ? '引导' : '加入' },
      minHosts(ctx) {
        return ctx.mode === 'cold_warm_hot' ? 4 : null
      },
      // 冷热温角色已由第 2 步勾选控制，参数区不再展示冗余的 roles 文本框
      filterHostVars(ctx, list) {
        return ctx.mode === 'cold_warm_hot' ? list.filter(v => v.name !== 'roles') : list
      },
      canStep3(ctx) {
        // 冷热温模式：缺 master 候选则不允许进入下一步（提示在角色分配区展示）
        if (ctx.mode !== 'cold_warm_hot') return true
        return ctx.selectedHosts.some(h => {
          const hp = ctx.hostParams[h.id] || {}
          const roles = (hp.roles || 'coordinator').split(',').map(s => s.trim()).filter(Boolean)
          return roles.includes('master')
        })
      }
    },
    elfk: {
      roleTag(ctx, isMaster) { return isMaster ? '引导（ES master + Kibana）' : 'ES 数据节点 + Filebeat' }
    },
    rabbitmq: {
      roleTag(ctx, isMaster) { return isMaster ? '引导' : '加入' }
    },
    rocketmq: {
      roleTag(ctx, isMaster) { return isMaster ? 'NameServer' : 'Broker' }
    }
  }
})()

// 大数据底座 · 第 1 步组件多选（新建）
window.StackFormBigdataSelect = {
  props: ['ctx'],
  template: `
<div class="stack-comp-select">
  <div class="deploy-host-vars-title">部署组件</div>
  <div class="stack-comp-grid">
    <div v-for="c in comps" :key="c.key" class="stack-comp-card" :class="{'is-on': ctx.bdSel.includes(c.key), 'is-req': compRequired(c), 'is-locked': compRequired(c)}" @click="toggle(c)">
      <el-checkbox :model-value="ctx.bdSel.includes(c.key)" :disabled="compRequired(c)" @click.stop @change="v => ctx.formToggle(c.key, v)" />
      <div class="stack-comp-body">
        <div class="stack-comp-name">{{c.label}}<el-tag v-if="compRequired(c)" size="small" type="danger" style="margin-left:6px">必选</el-tag></div>
        <div class="stack-comp-desc">{{c.desc}}</div>
      </div>
    </div>
  </div>
</div>`,
  computed: { comps() { return window.StackForms.bigdata.comps } },
  methods: {
    // 开启 HA 后 ZooKeeper 一并成为必选（协调服务，事后不可取消）
    compRequired(c) { return c.required || (c.key === 'zookeeper' && this.ctx.sharedParams?.ha === 'true') },
    toggle(c) { if (this.compRequired(c)) return; this.ctx.formToggle(c.key, !this.ctx.bdSel.includes(c.key)) }
  }
}

// 大数据底座 · 加装/卸载组件面板
// 展示「全部组件」并明确区分已安装 / 未安装：
//   加装：已安装的置灰回填（勾选 + 禁用），未安装的可勾选；
//   卸载：未安装的置灰（禁用），已安装的可勾选（底座组件与 HA 基座锁定并给出原因）。
window.StackFormBigdataOp = {
  props: ['ctx'],
  computed: {
    isRemove() { return this.ctx.wizardOp === 'remove_component' },
    catalog() { return ((window.StackForms.bigdata || {}).comps || []) },
    installedKeys() { return this.ctx.instCompTags(this.ctx.targetInstance) },
    haOn() { return this.ctx.sharedParams?.ha === 'true' },
    // 全集 = 组件目录 + 已安装但不在目录中的运行期组件（如 metastore_db）
    board() {
      const out = this.catalog.map(c => ({ ...c, installed: this.installedKeys.includes(c.key) }))
      this.installedKeys.forEach(k => {
        if (!this.catalog.some(c => c.key === k)) {
          out.push({ key: k, label: compDisplayLabel(k), desc: '集群运行期组件', installed: true })
        }
      })
      return out
    },
    addableCount() { return this.board.filter(c => this.selectable(c)).length },
    installedCount() { return this.board.filter(c => c.installed).length },
    summary() {
      if (this.isRemove) return `已安装 ${this.installedCount} 个组件 · 可卸载 ${this.addableCount} 个`
      return `已安装 ${this.installedCount} 个组件 · 可加装 ${this.addableCount} 个`
    },
    emptyHint() {
      if (this.isRemove) return '当前没有可单独卸载的组件（底座组件请使用「卸载集群」）'
      return '所有组件均已安装，无需加装'
    }
  },
  methods: {
    // 仍会留在集群里、且依赖 c 的组件（与后端 validateBigdataRemove 的「剩余集」规则一致）。
    // 本次一并勾选卸载的依赖方不算阻塞，因此被依赖方随之解锁。
    blockingDependents(c) {
      const removing = new Set(this.ctx.bdSel)
      return this.catalog.filter(x =>
        (x.deps || []).includes(c.key) &&
        this.installedKeys.includes(x.key) &&
        !removing.has(x.key))
    },
    lockReason(c) {
      if (this.isRemove) {
        if (c.required) return '底座组件'
        if (!c.installed) return '未安装'
        if (this.haOn && c.key === 'zookeeper') {
          const deps = this.installedKeys.filter(k => ['yarn', 'spark', 'flink', 'hbase', 'hive'].includes(k))
          if (deps.length) return 'HA 选主基座'
        }
        if (this.haOn && c.key === 'metastore_db' && this.installedKeys.includes('hive')) return 'Hive 依赖'
        const blockers = this.blockingDependents(c)
        if (blockers.length) return '被 ' + blockers.map(b => b.label).join('、') + ' 依赖，请一并勾选'
        return ''
      }
      if (c.required) return '底座必选'
      if (c.installed) return '已安装'
      return ''
    },
    selectable(c) { return this.lockReason(c) === '' },
    checked(c) {
      if (this.ctx.bdSel.includes(c.key)) return true
      return !this.isRemove && c.installed
    },
    onToggle(c, v) {
      if (!this.selectable(c)) return
      this.ctx.formToggle(c.key, v)
    },
    tagText(c) { return c.installed ? '已安装' : '未安装' }
  },
  template: `
<div class="stack-comp-select">
  <div class="stack-comp-ophead">
    <span class="deploy-host-vars-title">{{isRemove ? '选择要卸载的组件' : '选择要加装的组件'}}</span>
    <span class="stack-comp-opsum">{{summary}}</span>
  </div>
  <div class="stack-comp-grid">
    <div
      v-for="c in board"
      :key="c.key"
      class="stack-comp-card"
      :class="{
        'is-on': checked(c),
        'is-req': c.key === 'hdfs',
        'is-installed': c.installed,
        'is-locked': !selectable(c),
        'is-missing': !c.installed
      }"
      @click="onToggle(c, !checked(c))"
    >
      <el-checkbox :model-value="checked(c)" :disabled="!selectable(c)" @click.stop @change="v => onToggle(c, v)" />
      <div class="stack-comp-body">
        <div class="stack-comp-name">
          {{c.label}}
          <el-tag size="small" :type="c.installed ? 'success' : 'info'" effect="plain" style="margin-left:6px">{{tagText(c)}}</el-tag>
          <span v-if="lockReason(c) && lockReason(c) !== tagText(c)" class="stack-comp-lock">{{lockReason(c)}}</span>
        </div>
        <div class="stack-comp-desc">{{c.desc}}</div>
      </div>
    </div>
  </div>
  <div v-if="!addableCount" class="stack-comp-empty">{{emptyHint}}</div>
  <p class="stack-hint">{{isRemove
    ? '勾选后在全部成员上停止并卸载该组件（集群记录保留）。灰色项不可勾选。'
    : '已安装组件以置灰回填展示，勾选未安装组件后进入下一步指定主角色。'}}</p>
</div>`
}

// metastore_db 等运行期组件的中文名
function compDisplayLabel(key) {
  return { metastore_db: 'Hive MetaDB' }[key] || key
}

// 大数据底座 · 第 2 步角色规划（各组件主角色所在主机）
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
<div v-if="(ctx.wizardOp==='create' || ctx.wizardOp==='add_component') && (isHa ? haRoleGroups.length : roleComps.length)" class="stack-role-plan">
  <div class="deploy-host-vars-title">角色规划 <span class="deploy-var-hint">{{isHa ? 'HA 全角色矩阵·留空自动分配' : roleHint}}</span></div>
  <!-- HA：分组角色矩阵，卡片小方块（一行多组件） -->
  <template v-if="isHa">
    <div class="stack-ha-grid">
      <div v-for="grp in haRoleGroups" :key="grp.comp" class="stack-ha-card" :class="{'is-conflict': groupConflict(grp)}">
        <div class="stack-ha-head">
          <span class="stack-ha-name">{{grp.label}}</span>
          <span class="stack-ha-badge">HA</span>
          <span v-if="groupConflict(grp)" class="stack-ha-conflict-tag">角色冲突</span>
        </div>
        <div class="stack-ha-rows">
          <div v-for="r in grp.roles" :key="r.key" class="stack-ha-row" :class="{'role-conflict': rowConflict(grp, r)}">
            <span class="stack-ha-role">{{r.label}}<b v-if="r.required && !r.multi">*</b></span>
            <el-select v-if="r.multi" v-model="ctx.masters[r.key]" multiple collapse-tags :collapse-tags-tooltip="true" :placeholder="haPlaceholder(r)" size="small" class="stack-ha-select">
              <el-option v-for="h in ctx.selectedHosts" :key="h.id" :label="h.name + '（' + h.ip + '）'" :value="h.id" />
            </el-select>
            <el-select v-else v-model="ctx.masters[r.key]" clearable :placeholder="haPlaceholder(r)" size="small" class="stack-ha-select">
              <el-option v-for="h in ctx.selectedHosts" :key="h.id" :label="h.name + '（' + h.ip + '）'" :value="h.id" />
            </el-select>
          </div>
        </div>
      </div>
    </div>
    <div v-if="haConflictText" class="stack-ha-conflict">{{haConflictText}}</div>
  </template>
  <!-- 非 HA：保持现状 -->
  <div v-else class="stack-role-grid">
    <div v-for="c in roleComps" :key="c.key" class="stack-role-item">
      <span class="stack-role-label">{{c.label}}<b v-if="c.key==='hdfs' && ctx.wizardOp==='create'">*</b></span>
      <el-select v-model="ctx.masters[c.key]" :clearable="!(c.key==='hdfs' && ctx.wizardOp==='create')" :placeholder="rolePlaceholder(c)" size="small" style="flex:1">
        <el-option v-for="h in ctx.selectedHosts" :key="h.id" :label="h.name + '（' + h.ip + '）'" :value="h.id" />
      </el-select>
    </div>
  </div>
  <!-- 角色计划预览：部署前物化，冲突在提交前暴露（§5.1/§5.2） -->
  <div v-if="ctx.wizardOp==='create' || ctx.wizardOp==='add_component'" class="stack-plan-preview-bar">
    <el-button size="small" :loading="!!ctx.rolePreviewBusy" @click="previewPlan">{{ctx.rolePreview ? '刷新角色计划' : '预览角色计划'}}</el-button>
    <span class="deploy-var-hint">提交前物化校验：主备冲突、IP 越界在此暴露；「手」= 手动指定，「自」= 自动分配</span>
  </div>
  <div v-if="ctx.rolePreview" class="stack-plan-preview">
    <div class="stack-plan-preview-head">
      <span>角色计划 · rev {{ctx.rolePreview.rev}} · {{ctx.rolePreview.ha ? 'HA' : '单机'}}</span>
      <span class="deploy-var-hint">{{previewSummary}}</span>
    </div>
    <div class="stack-plan-preview-grid">
      <div v-for="h in ctx.rolePreview.hosts" :key="h.host_ip" class="stack-plan-preview-row">
        <span class="mono stack-plan-preview-ip">{{h.host_ip}}</span>
        <span class="stack-plan-preview-host">{{h.host_name}}</span>
        <div class="stack-plan-preview-chips">
          <span v-for="(r, i) in h.roles" :key="i" class="stack-plan-chip" :class="{'is-manual': r.source==='manual', 'is-all': r.scope==='all'}" :title="(r.source==='manual' ? '手动指定' : '自动分配') + ' · ' + (r.scope==='all' ? '全员角色' : '落点角色（部署后冻结）')">{{r.label}}<i>{{r.source==='manual' ? '手' : '自'}}</i></span>
          <span v-if="!(h.roles||[]).length" class="deploy-var-hint">无角色</span>
        </div>
      </div>
    </div>
    <div v-if="ctx.rolePreview.warnings && ctx.rolePreview.warnings.length" class="stack-plan-preview-warn">
      <div v-for="(w, i) in ctx.rolePreview.warnings" :key="i">{{w}}</div>
    </div>
  </div>
</div>`
}

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

// Redis · 第 2 步集群拓扑（三主 / 三主三从）
window.StackFormRedisTopo = {
  props: ['ctx'],
  template: `
<div v-if="ctx.mode==='cluster'" class="stack-replicas">
  <span class="deploy-host-vars-title">集群拓扑</span>
  <el-radio-group v-model="ctx.replicas" size="small">
    <el-radio-button label="0">三主（至少 3 台）</el-radio-button>
    <el-radio-button label="1">三主三从（至少 6 台偶数）</el-radio-button>
  </el-radio-group>
</div>`
}

// Elasticsearch 冷热温 · 第 2 步节点角色分配
// 角色互斥（master / 纯协调 / 数据层三者其一，数据层可叠加 hot/warm/cold）：
//   选 master 清掉协调与数据；选纯协调清掉 master 与数据；选数据层清掉 master 与协调但保留多重数据。
// 与后端 validateCWHRoles 一致；缺 master 候选时 canStep3 拦截进入下一步。
;(function () {
  const ES_ROLES = [
    { key: 'master', label: 'master 候选', cls: 'es-master', hint: '参与选举，隐式协调，不存数据' },
    { key: 'coordinator', label: '纯协调', cls: 'es-coord', hint: '仅负载均衡与聚合（node.roles 为空）' },
    { key: 'data_hot', label: '数据-hot', cls: 'es-hot', hint: '高 IOPS 热层' },
    { key: 'data_warm', label: '数据-warm', cls: 'es-warm', hint: '中频访问层' },
    { key: 'data_cold', label: '数据-cold', cls: 'es-cold', hint: '低频归档层' }
  ]
  const isData = k => k.startsWith('data_')

  window.StackFormElasticsearchRoles = {
    props: ['ctx'],
    watch: {
      // 已选主机变化时，为 create 流程中尚未指定角色的主机补齐默认角色：
      // 首台 master（canStep3 锚点，避免"选完主机无法下一步"），第 2 台纯协调，其余留空待勾数据层。
      selectedIds(val, old) {
        if (!this.useDefault) return
        const none = (!old || !old.size) ? val : new Set([...val].filter(id => !old.has(id)))
        if (!none.size) return
        let coord = 0
        let anyMaster = this.hosts.some(h => this.rolesOf(h.id).includes('master'))
        this.hosts.forEach(h => {
          if (!none.has(h.id)) return
          if (this.rolesOf(h.id).length) return
          if (!anyMaster) { this.setRoles(h.id, ['master']); anyMaster = true; return }
          if (coord >= 1) return
          this.setRoles(h.id, ['coordinator'])
          coord++
        })
      }
    },
    computed: {
      selectedIds() { return new Set(this.ctx.selectedHostIds || []) },
      visible() {
        const ctx = this.ctx
        return ctx.selectedKey === 'elasticsearch' && ctx.mode === 'cold_warm_hot' &&
          (ctx.wizardOp === 'create' || ctx.wizardOp === 'scale_out' || ctx.wizardOp === 'add_component')
      },
      hosts() { return this.visible ? this.ctx.selectedHosts : [] },
      defs() { return ES_ROLES },
      useDefault() { return this.ctx.wizardOp === 'create' },
      // 无 hostParams 且未设定时（create 首两台之外的存量主机）读取后端的 coordinator 兜底
      roleCounts() {
        const c = { master: 0, coordinator: 0, data_hot: 0, data_warm: 0, data_cold: 0 }
        this.hosts.forEach(h => this.rolesOf(h.id).forEach(k => { if (k in c) c[k]++ }))
        return c
      },
      // 缺内容提示：无 master 候选 / 无数据层节点
      warnings() {
        const out = []
        if (this.roleCounts.master === 0) out.push('尚未指定 master 候选主机（至少 1 台，生产建议 ≥3 台奇数），无法进入下一步')
        if (this.roleCounts.data_hot + this.roleCounts.data_warm + this.roleCounts.data_cold === 0) {
          out.push('尚未指定数据层节点（hot/warm/cold 至少 1 台），否则集群无法存储数据')
        }
        if (this.hosts.some(h => this.rolesOf(h.id).some(isData))) {
          out.push('多数据层叠加仅建议资源充足的大规格主机选用，规格一般时建议每台只承担单一数据层（避免 I/O 冲突）')
        }
        return out
      }
    },
    methods: {
      rolesOf(id) {
        const hp = this.ctx.hostParams && this.ctx.hostParams[id]
        if (!hp || !hp.roles) return []
        return String(hp.roles).split(',').map(s => s.trim()).filter(Boolean)
      },
      setRoles(id, roles) {
        this.ctx.onHostParam(id, 'roles', roles.join(','))
      },
      // master 与数据层 / 协调互斥：返回当前是否应禁用某角色的 checkbox
      disabledOf(h, def) {
        const rs = this.rolesOf(h.id)
        if (!rs.length) return false
        if (def.key === 'master') return rs.some(r => r === 'coordinator' || isData(r))
        if (def.key === 'coordinator') return rs.some(r => r === 'master' || isData(r))
        return rs.includes('master') || rs.includes('coordinator')
      },
      selectedOf(h, def) { return this.rolesOf(h.id).includes(def.key) },
      toggleRole(h, def, val) {
        const rs = this.rolesOf(h.id).slice()
        let next
        if (!val) {
          next = rs.filter(r => r !== def.key)
        } else if (def.key === 'master') {
          next = ['master']
        } else if (def.key === 'coordinator') {
          next = ['coordinator']
        } else {
          // 数据层：清掉 master/协调，叠加多种数据 tier
          next = rs.filter(isData)
          if (!next.includes(def.key)) next.push(def.key)
        }
        this.setRoles(h.id, next)
      }
    },
    template: `
<div v-if="visible" class="es-role-plan">
  <div class="deploy-host-vars-title">节点角色 <span class="deploy-var-hint">master / 纯协调 / 数据层三者其一，数据层可叠加 hot+warm+cold</span></div>
  <div class="es-role-counts">
    <span>master 候选 <b>{{roleCounts.master}}</b></span>
    <span>纯协调 <b>{{roleCounts.coordinator}}</b></span>
    <span>数据-hot <b>{{roleCounts.data_hot}}</b></span>
    <span>数据-warm <b>{{roleCounts.data_warm}}</b></span>
    <span>数据-cold <b>{{roleCounts.data_cold}}</b></span>
  </div>
  <div class="es-role-rows" v-if="hosts.length">
    <div class="es-role-row" v-for="h in hosts" :key="h.id">
      <div class="es-role-host">
        <span class="es-role-hostname">{{h.name}}</span>
        <span class="mono es-role-hostip">{{h.ip}}</span>
      </div>
      <div class="es-role-checks">
        <el-checkbox
          v-for="d in defs" :key="d.key" size="small"
          :model-value="selectedOf(h, d)"
          :disabled="disabledOf(h, d)"
          @change="val => toggleRole(h, d, val)"
        ><span :class="'es-role-chip ' + d.cls">{{d.label}}</span></el-checkbox>
      </div>
      <span v-if="rolesOf(h.id).filter(isData).length > 1" class="es-role-io-warn">I/O 叠加</span>
    </div>
  </div>
  <div v-else class="faint es-role-empty">请先在主机列表中勾选参与节点</div>
  <div v-if="warnings.length" class="es-role-warnings">
    <div v-for="(w, i) in warnings" :key="i" class="es-role-warn">{{w}}</div>
  </div>
</div>`
  }
})()
