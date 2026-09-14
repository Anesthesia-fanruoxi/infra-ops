// 前端资源登记（设计文档 §4.2 assets.js）。
//
// 页面对套件资源一律**静态引入**（index.html 按套件分组），无运行时注入；
// 这里登记资源清单，供后续评估「按需加载」方案（§5.4 方案 B）时直接取用。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  window.StacksAssets = {
    common: [
      '/static/stacks/registry.js',
      '/static/stacks/common/parts.js',
      '/static/stacks/common/common.css'
    ],
    stacks() { return P.assetsOf() }
  }
})()
