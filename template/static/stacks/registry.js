// 套件前端注册表（设计文档 §4.2 / §5.4）。
//
// window.StackDrivers 由各套件目录的 index.js 注册；本文件提供容器与启动冒烟断言。
// 兼容：window.StackForms 仍是「表单向导插槽」视图（骨架与 app.js 均按它取用），
// 由 StacksParts.register 同步写入，故 app.js 的全局组件注册无需改动。
;(function () {
  window.StackDrivers = window.StackDrivers || {}
  window.StackForms = window.StackForms || {}

  // 9 个内置套件必须全部注册：漏引某个 <script> 时立刻在控制台暴露，
  // 而不是等用户点开该套件向导才发现交互缺失。
  window.StacksRegistryReady = function () {
    const want = ['redis', 'bigdata', 'kafka', 'elasticsearch', 'rabbitmq',
                  'rocketmq', 'nacos', 'powerjob', 'elfk']
    const miss = want.filter(k => !window.StackDrivers[k])
    if (miss.length) console.error('[stacks] 套件前端未注册：' + miss.join('、'))
    return miss
  }
})()
