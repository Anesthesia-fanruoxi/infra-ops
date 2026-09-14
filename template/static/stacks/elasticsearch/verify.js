// Elasticsearch 套件 · 探活与角色标签扩展点。
// 冷热温（cold_warm_hot）的分层角色是 ES 独有语义；其余展示一律交还骨架的通用主/从。
// 逻辑自 pages/stacks.js 的 ES 分支原样迁入。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})

  const ES_ROLE_LABEL = {
    master: 'master 候选', coordinator: '纯协调',
    data_hot: '数据-hot', data_warm: '数据-warm', data_cold: '数据-cold'
  }
  const esRoleLabel = (r) => ES_ROLE_LABEL[r] || r
  const isEsCwh = (it) => !!it && it.stack_key === 'elasticsearch' && it.mode === 'cold_warm_hot'
  const hostRolesTags = (h) => {
    let hp = {}
    try { hp = JSON.parse((h && h.params_json) || '{}') || {} } catch (e) { hp = {} }
    const roles = String(hp.roles || '').split(',').map(s => s.trim()).filter(Boolean)
    return roles.length ? roles.map(r => esRoleLabel(r)) : []
  }
  // 实例卡片/抽屉：冷热温实例展示分层分布（数据-hot/warm/cold 聚合；含 master/协调）
  const instTierTags = (it) => {
    if (!isEsCwh(it)) return []
    const seen = new Set()
    ;(it.hosts || []).forEach(h => hostRolesTags(h).forEach(t => seen.add(t)))
    const order = ['数据-hot', '数据-warm', '数据-cold', 'master 候选', '纯协调']
    return order.filter(t => seen.has(t))
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
      // 冷热温 + SSL 开启时可下载 CA 证书
      caDownloadAvailable() {
        const it = this.instDetail || this.verifyTarget
        if (!isEsCwh(it)) return false
        try { return JSON.parse(it.params_json || '{}').ssl_enabled === 'true' } catch (e) { return false }
      },
      async downloadCa() {
        const it = this.instDetail || this.verifyTarget
        if (!it) return
        try {
          const text = await window.api.get('/stacks/instances/' + it.id + '/ca', { responseType: 'text', timeout: 30000 })
          const str = String(text || '')
          if (!str.includes('BEGIN CERTIFICATE')) { ElMessage.error('未获取到 CA 证书'); return }
          const blob = new Blob([str], { type: 'application/x-pem-file' })
          const url = URL.createObjectURL(blob)
          const a = document.createElement('a'); a.href = url; a.download = (it.name || 'es') + '-ca.crt'; a.click()
          URL.revokeObjectURL(url)
          ElMessage.success('已下载 CA 证书')
        } catch (e) { ElMessage.error(e.message || '下载 CA 失败') }
      },
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
