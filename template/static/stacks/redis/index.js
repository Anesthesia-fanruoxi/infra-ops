// Redis 套件 · 前端入口。
// 三模式（replication / sentinel / cluster）的差异落在 forms 插槽与本目录 hooks.js / topo.js。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  P.register('redis', {
    forms: {
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
    components: {}
  })
})()
