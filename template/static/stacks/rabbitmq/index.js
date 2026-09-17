// RabbitMQ 套件 · 前端入口。
// 全流程走通用骨架，差异仅由 forms 的插槽（roleTag / step3Comp 等）与后端蓝图驱动。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  P.register('rabbitmq', {
    forms: {
      roleTag(ctx, isMaster) { return isMaster ? '引导' : '加入' },
      // 第三步插槽：延迟队列插件 .ez 资产条。必须挂第 3 步——它的开关 delayed_plugin
      // 就是第 3 步的共享变量，挂第 2 步会在用户打开开关时显示在上一屏（见 assets.js）
      step3Comp: 'stack-form-rabbitmq-assets'
    },
    components: {}
  })
})()
