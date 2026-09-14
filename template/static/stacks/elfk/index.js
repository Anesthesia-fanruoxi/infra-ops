// ELFK 编排 套件 · 前端入口。
// 该套件没有独有的表单向导组件与展示分支：全流程走通用骨架，
// 差异仅由 forms 的插槽（roleTag / hints 等）与后端蓝图驱动。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  P.register('elfk', {
    forms: {
      roleTag(ctx, isMaster) { return isMaster ? '引导（ES master + Kibana）' : 'ES 数据节点 + Filebeat' }
    },
    components: {}
  })
})()
