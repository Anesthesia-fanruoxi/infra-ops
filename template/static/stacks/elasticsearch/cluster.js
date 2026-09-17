// Elasticsearch 套件 · 第 2 步拦截提示（冷热温：先有 master 候选，再有数据层）。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  P.register('elasticsearch', {
    hooks: {
      step2BlockedHint() {
        if (this.mode !== 'cold_warm_hot') return ''
        const hasMaster = this.selectedHosts.some(h => {
          const hp = this.hostParams[h.id] || {}
          return String(hp.roles || '').split(',').map(s => s.trim()).includes('master')
        })
        if (!hasMaster) return '请在角色矩阵中至少勾选 1 个「master 候选」容器（生产建议 ≥3 台奇数；最少主机 ' + this.minHosts + ' 台）'
        return '请在角色矩阵中勾选数据层角色（hot/warm/cold 至少 1 个容器）'
      }
    }
  })
})()
