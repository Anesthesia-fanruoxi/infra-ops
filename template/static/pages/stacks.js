// 套件部署页 · 挂载壳（设计文档 §4.2 的终态形态：只保留挂载与路由，<= 120 行）。
//
// 页面本体 = 通用骨架（/static/stacks/common/ 的 mixins + 模板片段）
//          + 各套件差异（/static/stacks/<key>/，经 window.StackDrivers 的 hooks / components 注入）。
// 本文件只做装配与启动冒烟，不含任何套件分支。必须在全部 /static/stacks/** 之后加载。
;(function () {
  const P = window.StacksParts
  if (!P) {
    console.error('[stacks] 通用骨架未加载：请检查 /static/stacks/common/ 的 script 顺序')
    return
  }

  // 启动冒烟：9 个套件 key 必须齐全（缺少时控制台报错，便于发现漏引文件）
  if (window.StacksRegistryReady) window.StacksRegistryReady()

  window.StacksPage = {
    props: ['page', 'user', 'versionData'],
    components: P.componentsOf(),
    template: P.template(),
    mixins: P.mixins
  }
})()
