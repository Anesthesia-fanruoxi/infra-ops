// Elasticsearch 套件 · 前端入口（设计文档 §4.2 目录树）。
// forms 原样迁自 pages/stacks-forms.js；hooks 在 verify.js / cluster.js；
// 组件（stack-form-elasticsearch-roles）在 roles.js 内自注册。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  P.register('elasticsearch', {
    forms: {
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
    components: {}
  })
})()
