#!/usr/bin/env node
// 前端装载冒烟（不做真实浏览器渲染）：
//   按 template/index.html 的 script 顺序在沙箱中求值全部前端脚本，
//   断言 window.StackDrivers / StackForms / StacksPage 的装配结果与预期一致。
// 用法：node script/_fe_smoke.js
const fs = require('fs')
const path = require('path')
const vm = require('vm')

const ROOT = path.resolve(__dirname, '..')
const INDEX = path.join(ROOT, 'template', 'index.html')
const EXPECT = path.join(ROOT, 'script', '_expected_template.txt')

const html = fs.readFileSync(INDEX, 'utf8')
const srcs = []
const re = /<script\s+src="([^"]+)"\s*><\/script>/g
let m
while ((m = re.exec(html))) srcs.push(m[1])

// 第三方库与 app.js 不适合在无 DOM 沙箱中求值（它们只做挂载，不参与套件装配）；
// 套件侧脚本全部求值，app.js 依赖的 window.StackFormXxx 由下方断言直接覆盖。
const SKIP = /^\/static\/(vendor\/|app\.js$)/

// 最小浏览器环境
const noop = () => {}
const sandbox = {}
sandbox.window = sandbox
sandbox.console = console
sandbox.setTimeout = setTimeout
sandbox.clearTimeout = clearTimeout
sandbox.URL = { createObjectURL: () => 'blob:x', revokeObjectURL: noop }
sandbox.Blob = function () {}
sandbox.document = {
  createElement: () => ({ click: noop, style: {}, setAttribute: noop }),
  addEventListener: noop, querySelector: () => null, body: {}
}
sandbox.location = { hash: '' }
sandbox.addEventListener = noop
sandbox.Vue = { createApp: () => ({ use: noop, component: noop, mount: () => ({}) }), ref: v => ({ value: v }), computed: f => f, watch: noop }
sandbox.ElementPlus = {}
sandbox.ElementPlusIconsVue = {}
sandbox.ElMessage = Object.assign(noop, { error: noop, success: noop, warning: noop })
sandbox.ElMessageBox = Object.assign(() => Promise.resolve(), { confirm: () => Promise.resolve() })
sandbox.axios = { create: () => ({ get: () => Promise.resolve({}), post: () => Promise.resolve({}), interceptors: { response: { use: noop } } }) }

const ctx = vm.createContext(sandbox)
let loaded = 0
let expected = 0
const errs = []
for (const src of srcs) {
  if (SKIP.test(src)) continue
  expected++
  const fp = path.join(ROOT, 'template', src.replace(/^\//, ''))
  if (!fs.existsSync(fp)) { errs.push('MISSING FILE: ' + src); continue }
  try {
    vm.runInContext(fs.readFileSync(fp, 'utf8'), ctx, { filename: src })
    loaded++
  } catch (e) {
    errs.push('EVAL FAIL ' + src + ': ' + e.message)
  }
}

function check(cond, msg) { if (!cond) errs.push(msg) }

// 1. script 全部存在且求值成功
check(loaded === expected, `脚本求值 ${loaded}/${expected}`)

// 2. 套件注册表：9 个 key
const WANT = ['redis', 'bigdata', 'kafka', 'elasticsearch', 'rabbitmq', 'rocketmq', 'nacos', 'powerjob', 'elfk']
const drivers = sandbox.StackDrivers || {}
const miss = WANT.filter(k => !drivers[k])
check(miss.length === 0, 'StackDrivers 缺 key: ' + miss.join(','))

// 3. 启动冒烟函数应返回空数组（无缺失）
if (typeof sandbox.StacksRegistryReady === 'function') {
  const r = sandbox.StacksRegistryReady()
  check(Array.isArray(r) && r.length === 0, 'StacksRegistryReady 返回非空: ' + JSON.stringify(r))
} else {
  errs.push('缺少 window.StacksRegistryReady')
}

// 4. StackForms 兼容视图：redis 仍带 hints（原 stacks-forms.js 的 hints 不得丢失）
const forms = sandbox.StackForms || {}
check(!!forms.redis, 'StackForms.redis 缺失')
check(!!(forms.redis && forms.redis.hints && forms.redis.hints.length),
  'StackForms.redis.hints 丢失（register 的 hints 回退失效）')
check(!!forms.bigdata && Array.isArray(forms.bigdata.comps) && forms.bigdata.comps.length === 8,
  'StackForms.bigdata.comps 不是 8 个组件')
check(!!(forms.bigdata && forms.bigdata.masterComps && forms.bigdata.masterComps.length === 7),
  'StackForms.bigdata.masterComps 不是 7 项')
check(!!(forms.bigdata && forms.bigdata.haRoles && forms.bigdata.haRoles.length === 7),
  'StackForms.bigdata.haRoles 不是 7 组')

// 5. 页面装配
const page = sandbox.StacksPage
check(!!page, 'window.StacksPage 缺失')
if (page) {
  check(Array.isArray(page.mixins) && page.mixins.length >= 7, 'mixins 数量异常: ' + (page.mixins || []).length)
  check(Array.isArray(page.props) && page.props.join(',') === 'page,user,versionData', 'props 不一致')
  const comps = Object.keys(page.components || {})
  for (const c of ['stack-form-bigdata-select', 'stack-form-bigdata-op', 'stack-form-bigdata-roles',
                   'stack-form-redis-topo', 'stack-form-elasticsearch-roles']) {
    check(comps.includes(c), '页面局部组件缺失: ' + c)
  }
  // 组件定义本身必须可解析（app.js 按 window.Xxx 全局注册）
  for (const g of ['StackFormBigdataSelect', 'StackFormBigdataOp', 'StackFormBigdataRoles',
                   'StackFormRedisTopo', 'StackFormElasticsearchRoles']) {
    check(!!sandbox[g] && typeof sandbox[g] === 'object', '全局组件缺失: window.' + g)
  }
  // 6. 模板逐字节等于拆分前（生成器产出的预期串）
  const expect = fs.readFileSync(EXPECT, 'utf8')
  check(page.template === expect, `template 与拆分前不一致: got ${page.template.length} 字符, exp ${expect.length} 字符`)
  // 7. 骨架不含套件分支
  const hooks = Object.keys(page.components || {}).length
  check(hooks >= 5, 'componentsOf 汇总数异常')
  // 8. 不存在遗留的套件专属方法名（应已改为通用分发名）
  const probeSrc = page.mixins.map(mx => JSON.stringify(Object.keys(mx.computed || {})) + JSON.stringify(Object.keys(mx.methods || {}))).join('')
  for (const bad of ['isEsCwh', 'isBigdataVerify', 'esVerifyHostRole', 'esRoleLabel', 'haDrawnRoles']) {
    check(!probeSrc.includes('"' + bad + '"'), '骨架仍残留套件专属成员: ' + bad)
  }
  for (const good of ['verifyTabbed', 'verifyHostRole', 'instTierStyle', 'memTags', 'hook']) {
    check(probeSrc.includes('"' + good + '"'), '骨架缺少通用分发成员: ' + good)
  }
}

if (errs.length) {
  console.log('前端装载冒烟 不通过：')
  errs.forEach(e => console.log('  - ' + e))
  process.exit(1)
}
console.log(`前端装载冒烟 通过：脚本 ${loaded}/${expected} 个，9 个套件注册齐备，`
  + `页面局部组件 ${Object.keys(page.components).length} 个，模板 ${page.template.length} 字符逐字节一致。`)
