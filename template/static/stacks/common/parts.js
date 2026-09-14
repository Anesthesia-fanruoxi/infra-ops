// 套件前端装配容器（设计文档 §4.2 common/）。
//
// 分工：每个 common/*.js 只把自己那部分 computed / methods push 进 window.StacksParts.mixins，
// 模板片段登记到 window.StacksParts.tpl；挂载壳 pages/stacks.js 负责拼装成 window.StacksPage。
//
// 套件差异一律经 window.StackDrivers 注入，骨架不认识任何套件：
//   hooks       骨架扩展点（按槽位名分发，未实现的套件走通用默认）
//   components  该套件独有的 Vue 组件（同时挂 window.StackFormXxx 供 app.js 全局注册）
//   hints       第 1 步底部提示文案
//   forms       表单向导插槽（原 window.StackForms[key]，语义不变）
;(function () {
  window.StacksParts = {
    mixins: [],
    tpl: {},
    tplOrder: ['hero', 'wizard', 'preflight', 'instance', 'verify', 'run'],

    // 逗号列表 → 数组（探活表格的省略号 + tooltip 展示用）
    splitList: (s) => String(s == null ? '' : s).split(/,\s*/).map(x => x.trim()).filter(Boolean),

    // 套件注册：可多次调用（index.js 登记表单/组件，select.js / verify.js 等追加 hooks）
    register(key, driver) {
      driver = driver || {}
      const D = (window.StackDrivers = window.StackDrivers || {})
      const F = (window.StackForms = window.StackForms || {})
      const prev = D[key] || {}
      const forms = Object.assign({}, prev.forms || {}, driver.forms || {})
      D[key] = {
        key: key,
        forms: forms,
        hooks: Object.assign({}, prev.hooks || {}, driver.hooks || {}),
        components: Object.assign({}, prev.components || {}, driver.components || {}),
        hints: driver.hints !== undefined ? driver.hints : (prev.hints || forms.hints || [])
      }
      F[key] = forms
      return D[key]
    },

    // 汇总各套件的独有组件为页面局部组件表
    componentsOf() {
      const out = {}
      Object.keys(window.StackDrivers || {}).forEach(k => {
        const cs = (window.StackDrivers[k] || {}).components || {}
        Object.keys(cs).forEach(n => { if (cs[n]) out[n] = cs[n] })
      })
      return out
    },

    // 静态引入清单（设计文档 §5.4 方案 A；同时留给后续按需加载评估用）
    assetsOf() {
      return Object.keys(window.StackDrivers || {}).sort().map(k => ({
        key: k,
        script: '/static/stacks/' + k + '/index.js',
        style: '/static/stacks/' + k + '/style.css'
      }))
    },

    // 模板片段按渲染顺序拼装，与拆分前 pages/stacks.js 的单串逐字节一致
    template() {
      return '\n' + this.tplOrder.map(k => this.tpl[k] || '').join('\n')
    }
  }
})()
