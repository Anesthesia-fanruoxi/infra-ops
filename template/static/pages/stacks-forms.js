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
      roleTag(ctx, isMaster) { return isMaster ? '引导' : '加入' }
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
    <label v-for="c in comps" :key="c.key" class="stack-comp-card" :class="{'is-on': ctx.bdSel.includes(c.key), 'is-req': c.required}">
      <el-checkbox :model-value="ctx.bdSel.includes(c.key)" :disabled="c.required" @change="v => ctx.formToggle(c.key, v)" />
      <div class="stack-comp-body">
        <div class="stack-comp-name">{{c.label}}<el-tag v-if="c.required" size="small" type="danger" style="margin-left:6px">必选</el-tag></div>
        <div class="stack-comp-desc">{{c.desc}}</div>
      </div>
    </label>
  </div>
</div>`,
  computed: { comps() { return window.StackForms.bigdata.comps } }
}

// 大数据底座 · 加装/卸载组件多选
window.StackFormBigdataOp = {
  props: ['ctx'],
  template: `
<div class="stack-comp-select">
  <div class="deploy-host-vars-title">{{ctx.wizardOp==='remove_component' ? '卸载组件' : '加装组件'}}</div>
  <div class="stack-comp-grid">
    <label v-for="c in comps" :key="c.key" class="stack-comp-card" :class="{'is-on': ctx.bdSel.includes(c.key)}">
      <el-checkbox :model-value="ctx.bdSel.includes(c.key)" @change="v => ctx.formToggle(c.key, v)" />
      <div class="stack-comp-body">
        <div class="stack-comp-name">{{c.label}}</div>
        <div class="stack-comp-desc">{{c.desc}}</div>
      </div>
    </label>
  </div>
</div>`,
  computed: {
    comps() {
      return this.ctx.unusedWizardComps || []
    }
  }
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
    }
  },
  methods: {
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
  <!-- HA：分组角色矩阵 -->
  <template v-if="isHa">
    <div v-for="grp in haRoleGroups" :key="grp.comp" class="stack-ha-card" :class="{'is-conflict': groupConflict(grp)}">
      <div class="stack-ha-head">
        <span class="stack-ha-name">{{grp.label}}</span>
        <span class="stack-ha-badge">HA</span>
        <span v-if="groupConflict(grp)" class="stack-ha-conflict-tag">角色同机冲突</span>
      </div>
      <div class="stack-ha-rows">
        <div v-for="r in grp.roles" :key="r.key" class="stack-ha-row" :class="{'role-conflict': rowConflict(grp, r)}">
          <span class="stack-ha-role">{{r.label}}<b v-if="r.required && !r.multi">*</b></span>
          <el-select v-if="r.multi" v-model="ctx.masters[r.key]" multiple collapse-tags :collapse-tags-tooltip="true" :placeholder="haPlaceholder(r)" size="small" style="flex:1">
            <el-option v-for="h in ctx.selectedHosts" :key="h.id" :label="h.name + '（' + h.ip + '）'" :value="h.id" />
          </el-select>
          <el-select v-else v-model="ctx.masters[r.key]" clearable :placeholder="haPlaceholder(r)" size="small" style="flex:1">
            <el-option v-for="h in ctx.selectedHosts" :key="h.id" :label="h.name + '（' + h.ip + '）'" :value="h.id" />
          </el-select>
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
