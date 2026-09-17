// Elasticsearch 套件 · 前端入口（设计文档 §4.2 目录树）。
// forms 原样迁自 pages/stacks-forms.js；hooks 在 verify.js / cluster.js；
// 组件（stack-form-elasticsearch-roles）在 roles.js 内自注册 —— 该组件在第 2 步同时承担
// 「规格档位选择（两模式）」与「冷热温节点角色矩阵」。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  P.register('elasticsearch', {
    forms: {
      step2Comp: 'stack-form-elasticsearch-roles',
      // 第 2 步「倒品字」两栏：上左主机勾选、上右角色矩阵，下方通栏角色计划预览
      step2Split: true,
      roleTag(ctx, isMaster) { return isMaster ? '引导' : '加入' },
      minHosts(ctx) {
        return ctx.mode === 'cold_warm_hot' ? 4 : null
      },
      // 冷热温角色已由第 2 步矩阵勾选控制，参数区不再展示冗余的 roles 文本框
      filterHostVars(ctx, list) {
        return ctx.mode === 'cold_warm_hot' ? list.filter(v => v.name !== 'roles') : list
      },
      // 规格档位已在第 2 步的档位卡上选定（两模式通用），参数页不重复展示
      visibleSharedVars(ctx, list) {
        return list.filter(v => v.name !== 'sizing')
      },
      canStep3(ctx) {
        // 冷热温模式：缺 master 候选容器则不允许进入下一步（提示在角色矩阵区展示）
        if (ctx.mode !== 'cold_warm_hot') return true
        return ctx.selectedHosts.some(h => {
          const hp = ctx.hostParams[h.id] || {}
          const roles = String(hp.roles || '').split(',').map(s => s.trim()).filter(Boolean)
          return roles.includes('master')
        })
      }
    },
    components: {}
  })
})()
