// Kafka 套件 · 扩展点：探活接入端点合成，以及多组件主机的组件页签。
//
// 组件归属由后端 probe.go 在端点与检查项上打 component；骨架 common/verify-dialog.js
// 的 probeAttributed 会自动剔除探活结果里无归属的空壳页签。
// 后端检查项命名约定：每台主机每个组件一条「实例」（明细以容器名开头），
// 故组件页签的容器列直接取自该检查项。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})

  const COMP_LABELS = { kafka: 'Kafka Broker', zookeeper: 'ZooKeeper', ui: 'KafkaUI' }
  const yes = (v) => ['yes', 'true', '1', 'on'].includes(String(v == null ? '' : v).trim().toLowerCase())

  // 需要分页签的两种情况：zk 模式一台主机跑 Kafka + ZooKeeper；任意模式开了 KafkaUI
  // 后首台多一个 UI 容器。两者都静态可判，页签不会在探活返回后才「跳出来」。
  const useTabs = (ctx) => ctx.verifyTarget?.stack_key === 'kafka' &&
    (ctx.verifyTarget?.mode === 'zk' || yes(ctx.verifyParams?.enable_ui))

  function verifyComponents(ctx) {
    if (!useTabs(ctx)) return []
    return ['kafka', 'zookeeper', 'ui'].map(k => ({ key: k, label: COMP_LABELS[k] }))
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

  // 组件视角的节点表：只统计本组件的检查项（ZooKeeper 页签不受 Kafka 容器状态影响，反之亦然）
  function verifyCompHostRows(ctx) {
    const comp = ctx.verifyActiveTab
    if (!comp || comp === 'all') return []
    const fullMap = Object.fromEntries(ctx.verifyHostRows.map(r => [r.ip, r]))
    const out = []
    for (const h of (ctx.verifyResult?.hosts || [])) {
      const compChecks = (h.checks || []).filter(c => c.component === comp)
      if (!compChecks.length) continue
      const f = fullMap[h.host_ip] || {}
      // 容器名取「实例」检查项明细的首段（后端格式：<容器名> <状态>）
      const containers = compChecks.filter(c => c.ok && c.name === '实例')
        .map(c => String(c.detail || '').trim().split(/\s+/)[0]).filter(Boolean)
      const uptime = f.uptime || '-'
      out.push({
        ip: h.host_ip, host_name: h.host_name,
        ok: compChecks.every(c => c.ok),
        role: f.role || '-',
        instances: containers.length ? containers.join(', ') : '无',
        instList: containers.length > 1 ? containers : [],
        version: f.version || '-',
        uptime: uptime,
        uptimeList: uptime !== '-' ? (f.uptimeList || []) : []
      })
    }
    return out
  }

  P.register('kafka', {
    hooks: {
      // 探活接入地址兜底（后端已实现 EndpointProvider，正常路径走 verifyResult.endpoints）：
      // 两模式都给 kafka://，zk 模式追加 zookeeper://，开启 KafkaUI 时首台追加 http://
      endpointFor(h, hp, p, pt) {
        const it = this.verifyTarget
        const base = { role: h.role, host_ip: h.host_ip, host_name: h.host_name }
        const out = [{ name: 'Kafka Broker', component: 'kafka', url: 'kafka://' + h.host_ip + ':' + pt, ...base }]
        if (it.mode === 'zk') {
          out.push({ name: 'ZooKeeper', component: 'zookeeper', url: 'zookeeper://' + h.host_ip + ':' + (hp.zk_port || p.zk_port || '2181'), ...base })
        }
        if (yes(p.enable_ui)) {
          const hosts = (it.hosts || []).filter(x => x.status !== 'removed')
          if (hosts.length && hosts[0].id === h.id) {
            out.push({ name: 'KafkaUI', component: 'ui', url: 'http://' + h.host_ip + ':' + (hp.ui_port || p.ui_port || '8080'), ...base })
          }
        }
        return out
      },
      // —— 探活组件页签 ——
      verifyTabbed() { return useTabs(this) },
      verifyComponents() { return verifyComponents(this) },
      currentVerifyCompLabel() {
        const c = verifyComponents(this).find(c => c.key === this.verifyActiveTab)
        return c ? c.label : ''
      },
      verifyCompEndpoints() { return verifyCompEndpoints(this) },
      verifyCompEndpointsByHost() { return verifyCompEndpointsByHost(this) },
      verifyCompHostRows() { return verifyCompHostRows(this) },
      // 组件视角该列是本组件的容器（Kafka / ZooKeeper / UI 各一个），不是骨架默认的「实例」
      verifyCompCountLabel() { return '容器' }
    }
  })
})()
