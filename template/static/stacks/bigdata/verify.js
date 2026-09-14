// 大数据底座套件 · 探活（多组件标签式）与实例角色展示扩展点。
// 逻辑自 pages/stacks.js 的 bigdata 分支原样迁入。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  const splitList = P.splitList

  const COMP_LABELS = { hdfs: 'HDFS', zookeeper: 'ZooKeeper', yarn: 'YARN', spark: 'Spark', flink: 'Flink', hive: 'Hive', hbase: 'HBase', trino: 'Trino' }
  const isBigdata = (ctx) => ctx.verifyTarget?.stack_key === 'bigdata'

  function verifyComponents(ctx) {
    if (!isBigdata(ctx)) return []
    const order = (ctx.verifyParams.components || '').split(',').map(s => s.trim()).filter(Boolean)
    const seen = new Set(), keys = []
    ;[...order, ...(ctx.verifyResult?.endpoints || []).map(e => e.component)]
      .forEach(k => { if (k && !seen.has(k)) { seen.add(k); keys.push(k) } })
    const L = COMP_LABELS
    return keys.map(k => ({ key: k, label: L[k] || k.toUpperCase() }))
  }
  function currentVerifyCompLabel(ctx) {
    const c = verifyComponents(ctx).find(c => c.key === ctx.verifyActiveTab)
    return c ? c.label : ''
  }
  function verifyCompEndpoints(ctx) {
    const comp = ctx.verifyActiveTab
    if (!comp || comp === 'all') return []
    return ctx.verifyEndpoints.filter(ep => ep.component === comp)
  }
  function verifyCompEndpointsByHost(ctx) {
    const eps = verifyCompEndpoints(ctx)
    const map = new Map()
    eps.forEach(ep => {
      const key = ep.host_ip || ep.url
      if (!map.has(key)) {
        map.set(key, { host_ip: key, host_name: ep.host_name || key, endpoints: [] })
      }
      map.get(key).endpoints.push(ep)
    })
    return Array.from(map.values())
  }
  function verifyCompHostRows(ctx) {
    const comp = ctx.verifyActiveTab
    if (!comp || comp === 'all') return []
    const fullMap = Object.fromEntries(ctx.verifyHostRows.map(r => [r.ip, r]))
    const out = []
    for (const h of (ctx.verifyResult?.hosts || [])) {
      const compChecks = (h.checks || []).filter(c => c.component === comp)
      if (!compChecks.length) continue
      const f = fullMap[h.host_ip] || {}
      const instances = compChecks.filter(c => c.ok).map(c => c.name.replace(/^容器\s*/, ''))
      const instStr = instances.length ? instances.join(', ') : '无'
      const uptimeStr = f.uptime || '-'
      out.push({
        ip: h.host_ip, host_name: h.host_name,
        ok: compChecks.every(c => c.ok),
        role: f.role || '-',
        instances: instStr,
        instList: instances.length > 1 ? instances : [],
        version: f.version || '-',
        uptime: uptimeStr,
        uptimeList: uptimeStr !== '-' ? splitList(uptimeStr) : []
      })
    }
    return out
  }
  // 集群实例是否以 HA 模式创建
  function instIsHa(ctx, it) {
    if (!it || it.stack_key !== 'bigdata') return false
    try { return JSON.parse(it.params_json || '{}').ha === 'true' } catch (e) { return false }
  }
  // 实例抽屉：按主机展示 HA 全角色标签（镜像后端确定性分配，仅用于展示）
  function haDrawnRoles(ctx, ip) {
    const it = ctx.instDetail
    if (!it || it.stack_key !== 'bigdata' || !instIsHa(ctx, it)) return []
    let params, masters
    try { params = JSON.parse(it.params_json || '{}'); masters = JSON.parse(params.masters || '{}') || {} } catch (e) { return [] }
    const comps = (params.components || '').split(',').map(x => x.trim()).filter(Boolean)
    const hosts = (it.hosts || []).filter(x => x.status !== 'removed').slice().sort((a, b) => (a.seq || 0) - (b.seq || 0))
    const ips = hosts.map(h => h.host_ip)
    const primary = (hosts.find(h => h.role === 'master') || {}).host_ip || ips[0] || ''
    const primaryOf = comp => masters[comp] || primary
    const secondaryOf = (comp, key) => masters[key] || (ips.find(i => i !== primaryOf(comp)) || '')
    const inComps = c => comps.includes(c)
    const out = []
    if (inComps('hdfs')) {
      if (ip === primaryOf('hdfs')) out.push('NN1')
      else if (ip === secondaryOf('hdfs', 'hdfs_nn2')) out.push('NN2·Standby')
      else if ((masters.hdfs_jns ? String(masters.hdfs_jns).split(',') : ips.slice(0, 3)).includes(ip)) out.push('JN')
    }
    if (inComps('yarn') && ip === primaryOf('yarn')) out.push('RM1')
    if (inComps('yarn') && ip === secondaryOf('yarn', 'yarn_rm2')) out.push('RM2·Standby')
    if (inComps('spark') && ip === primaryOf('spark')) out.push('Master-1')
    if (inComps('spark') && ip === secondaryOf('spark', 'spark_m2')) out.push('Master-2')
    if (inComps('flink') && ip === primaryOf('flink')) out.push('JM1')
    if (inComps('flink') && ip === secondaryOf('flink', 'flink_jm2')) out.push('JM2·Standby')
    if (inComps('hbase') && ip === primaryOf('hbase')) out.push('HMaster-1')
    if (inComps('hbase') && ip === secondaryOf('hbase', 'hbase_hm2')) out.push('HMaster-2')
    if (inComps('hive')) {
      const ms = [primaryOf('hive')]; if (String(masters.hive_ms2 || '')) ms.push(masters.hive_ms2)
      const hs = [primaryOf('hive')]; if (String(masters.hive_hs2b || '')) hs.push(masters.hive_hs2b)
      if (ms.includes(ip)) out.push(ms[1] === ip ? 'MS2·Standby' : 'Metastore')
      if (hs.includes(ip)) out.push(hs[1] === ip ? 'HS2·Standby' : 'HiveServer2')
      if (ip === (masters.hive_db || primaryOf('hive'))) out.push('MetaDB')
    }
    if (inComps('zookeeper')) {
      const zk = masters.zookeeper_ips ? String(masters.zookeeper_ips).split(',') : ips.slice(0, 3)
      const idx = zk.indexOf(ip)
      if (idx >= 0) out.push('ZK-' + (idx + 1))
    }
    return out
  }

  P.register('bigdata', {
    hooks: {
      verifyTabbed() { return isBigdata(this) },
      verifyComponents() { return verifyComponents(this) },
      currentVerifyCompLabel() { return currentVerifyCompLabel(this) },
      verifyCompEndpoints() { return verifyCompEndpoints(this) },
      verifyCompEndpointsByHost() { return verifyCompEndpointsByHost(this) },
      verifyCompHostRows() { return verifyCompHostRows(this) },
      // 组件视角的「容器实例」列名（整体视角沿用骨架的「实例」）
      verifyCompCountLabel() { return '容器实例' },
      instIsHa(it) { return instIsHa(this, it) },
      memTags(h) { return haDrawnRoles(this, h.host_ip) },
      // 新建时 NameNode 必须明确指定（角色规划的锚点）
      step2BlockedHint() {
        if (this.useRolePlan && this.wizardOp === 'create' && this.bdSel.includes('hdfs') &&
          (!this.masters.hdfs || !this.selectedHostIds.has(this.masters.hdfs))) {
          return '请勾选主机并指定 HDFS NameNode'
        }
        return ''
      }
    }
  })
})()
