// 大数据底座套件 · 前端入口。
// 组件多选 / 加装卸载 / HA 全角色矩阵是本套件独有形态：
//   select.js 组件多选、op.js 加装卸载、roles.js 角色与 HA 矩阵、verify.js 组件化探活（均自注册）。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
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
  P.register('bigdata', {
    forms: {
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
        // 元数据库密码：HA+Hive 新建时前端标必填（星号 + 提交前拦截），避免留空导致 MySQL
        // 拒绝初始化；只在新建拦截，加装/扩缩容等操作沿用实例已存参数，不阻塞
        // 仅前端提示，后端不做校验（空值仍由 hive-site JDO 与 ha.sh 兜底 HiveDb@123）
        const pwRequired = ctx.wizardOp === 'create' && haOn && (ctx.bdSel || []).includes('hive')
        return list.filter(v => v.name !== 'components')
          // 高可用已在第 1 步确定，填写参数页不再展示该开关
          .filter(v => v.name !== 'ha')
          .filter(v => !bigdataVarComp[v.name] || ctx.bdSel.includes(bigdataVarComp[v.name]))
          .filter(v => !haOnly.includes(v.name) || haOn)
          .map(v => (pwRequired && v.name === 'hive_db_password' ? { ...v, required: true } : v))
      },
      extraParams(ctx) {
        const out = { components: ctx.bdSel.join(',') }
        const m = ctx.mastersForSubmit()
        if (m) out.masters = m
        return out
      }
    },
    components: {}
  })
})()
