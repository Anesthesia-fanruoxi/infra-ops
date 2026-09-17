// Elasticsearch 套件 · 探活与角色标签扩展点。
// 冷热温（cold_warm_hot）一机多容器：按角色分层做探活页签（master 候选 / 纯协调 / 数据层），
// 探活端口按角色固定偏移（与后端 vars.go cwhRolePort / 脚本 http_port 同表）；
// 后端对外仅登记协调/master 入口，数据节点容器由前端 endpointFor 逐容器合成检查；
// 空角色兜底冷热温全数据层（与后端 cwhDataRoles 一致）；cluster 模式交还骨架通用视图。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})

  const ES_ROLE_LABEL = {
    master: 'master 候选', coordinator: '纯协调',
    data_hot: '数据-hot', data_warm: '数据-warm', data_cold: '数据-cold'
  }
  // 角色固定偏移端口（与后端 vars.go cwhRolePort / 脚本 http_port 同表，改动需多处同步）
  const ES_ROLE_PORTS = {
    master: 9200, coordinator: 9210,
    data_hot: 9220, data_warm: 9230, data_cold: 9240
  }
  const esRoleLabel = (r) => ES_ROLE_LABEL[r] || r
  const isEsCwh = (it) => !!it && it.stack_key === 'elasticsearch' && it.mode === 'cold_warm_hot'
  const roleKeysOf = (rolesStr) => {
    const roles = String(rolesStr || '').split(',').map(s => s.trim()).filter(Boolean)
    return roles.length ? roles : ['data_hot', 'data_warm', 'data_cold']
  }
  const hostRoleKeys = (h) => {
    let hp = {}
    try { hp = JSON.parse((h && h.params_json) || '{}') || {} } catch (e) { hp = {} }
    return roleKeysOf(hp.roles)
  }
  const hostRolesTags = (h) => hostRoleKeys(h).map(r => esRoleLabel(r))
  // 实例卡片/抽屉：冷热温实例展示分层分布（数据-hot/warm/cold 聚合；含 master/协调）
  const instTierTags = (it) => {
    if (!isEsCwh(it)) return []
    const seen = new Set()
    ;(it.hosts || []).forEach(h => hostRolesTags(h).forEach(t => seen.add(t)))
    const order = ['数据-hot', '数据-warm', '数据-cold', 'master 候选', '纯协调']
    return order.filter(t => seen.has(t))
  }

  // —— 探活页签（仅冷热温）：按角色分层声明清单，顺序固定 ——
  const ROLE_ORDER = ['master', 'coordinator', 'data_hot', 'data_warm', 'data_cold']
  function verifyComponents(ctx) {
    if (!isEsCwh(ctx.verifyTarget)) return []
    const seen = new Set(), keys = []
    ;(ctx.verifyTarget.hosts || []).filter(h => h.status !== 'removed').forEach(h =>
      hostRoleKeys(h).forEach(k => { if (!seen.has(k)) { seen.add(k); keys.push(k) } }))
    keys.sort((a, b) => ROLE_ORDER.indexOf(a) - ROLE_ORDER.indexOf(b))
    return keys.map(k => ({ key: k, label: esRoleLabel(k) }))
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
    const map = new Map()
    verifyCompEndpoints(ctx).forEach(ep => {
      const key = ep.host_ip || ep.url
      if (!map.has(key)) {
        map.set(key, { host_ip: key, host_name: ep.host_name || key, role: ep.role || '', endpoints: [] })
      }
      map.get(key).endpoints.push(ep)
    })
    return Array.from(map.values())
  }
  // ES 无 ProbePlugin，检查项无 component 归属；页签节点表仅在探活结果带归属时呈现
  function verifyCompHostRows(ctx) {
    const comp = ctx.verifyActiveTab
    if (!comp || comp === 'all') return []
    const fullMap = Object.fromEntries((ctx.verifyHostRows || []).map(r => [r.ip, r]))
    const out = []
    for (const h of (ctx.verifyResult?.hosts || [])) {
      const compChecks = (h.checks || []).filter(c => c.component === comp)
      if (!compChecks.length) continue
      const f = fullMap[h.host_ip] || {}
      out.push({
        ip: h.host_ip, host_name: h.host_name,
        ok: compChecks.every(c => c.ok),
        role: f.role || '-', instances: '-', instList: [],
        version: f.version || '-', uptime: f.uptime || '-', uptimeList: []
      })
    }
    return out
  }

  P.register('elasticsearch', {
    hooks: {
      // 实例卡片标签：冷热温改用分层 chip（类名由套件给出，骨架不认识 ES）
      instTierStyle(it) {
        return isEsCwh(it) ? { cls: 'stk-es-tier-chip', items: instTierTags(it) } : null
      },
      // 抽屉成员标签：冷热温取角色分层
      memTags(h) {
        return isEsCwh(this.instDetail) ? hostRolesTags(h) : []
      },
      // —— 探活页签（冷热温按角色分层）——
      verifyTabbed() { return isEsCwh(this.verifyTarget) },
      verifyComponents() { return verifyComponents(this) },
      currentVerifyCompLabel() { return currentVerifyCompLabel(this) },
      // 探活前端点合成（一机多容器：每角色容器一条，端口按角色固定偏移，
      // 与后端 vars.go cwhRolePort / 脚本 http_port 同表；component 决定页签归属）
      endpointFor(h, hp, p, pt) {
        return roleKeysOf(hp && hp.roles).map(r => ({
          name: 'Elasticsearch-' + esRoleLabel(r), component: r,
          url: 'http://' + h.host_ip + ':' + (ES_ROLE_PORTS[r] || pt || 9200), role: r,
          host_ip: h.host_ip, host_name: h.host_name
        }))
      },
      verifyCompEndpoints() { return verifyCompEndpoints(this) },
      verifyCompEndpointsByHost() { return verifyCompEndpointsByHost(this) },
      verifyCompHostRows() { return verifyCompHostRows(this) },
      verifyCompCountLabel() { return '容器' },
      // 探活接入地址主机头：冷热温按实际分层角色展示（其余套件交还骨架的主/从）
      verifyHostRole(hg) {
        if (!isEsCwh(this.verifyTarget)) return ''
        const parts = []
        const seen = new Set()
        ;(hg.endpoints || []).forEach(ep => {
          const r = (ep.role || '').trim()
          if (r && !seen.has(r)) { seen.add(r); parts.push(esRoleLabel(r)) }
        })
        return parts.join(' / ')
      }
    }
  })
})()
