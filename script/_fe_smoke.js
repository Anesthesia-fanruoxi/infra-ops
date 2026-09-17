#!/usr/bin/env node
// 前端装载冒烟（不做真实浏览器渲染）：
//   按 template/index.html 的 script 顺序在沙箱中求值全部前端脚本，
//   断言 window.StackDrivers / StackForms / StacksPage 的装配结果与预期一致。
// 用法：node script/_fe_smoke.js            校验（模板逐字节比对 _expected_template.txt）
//      node script/_fe_smoke.js --record     有意改动模板后重录基线（_expected_template.txt）
const fs = require('fs')
const path = require('path')
const vm = require('vm')

const RECORD = process.argv.includes('--record')
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
const sources = []
for (const src of srcs) {
  // 引用可带 ?v= 缓存击穿查询串（如 templates.js?v=20260916）：文件路径与 SKIP 一律按纯净路径判定
  const clean = src.split('?')[0]
  if (SKIP.test(clean)) continue
  expected++
  const fp = path.join(ROOT, 'template', clean.replace(/^\//, ''))
  if (!fs.existsSync(fp)) { errs.push('MISSING FILE: ' + src); continue }
  try {
    const code = fs.readFileSync(fp, 'utf8')
    vm.runInContext(code, ctx, { filename: clean })
    sources.push([clean, code])
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
                   'stack-form-redis-topo', 'stack-form-elasticsearch-roles', 'stack-form-kafka-select',
                   'stack-form-rabbitmq-assets']) {
    check(comps.includes(c), '页面局部组件缺失: ' + c)
  }
  // 组件定义本身必须可解析（app.js 按 window.Xxx 全局注册）
  for (const g of ['StackFormBigdataSelect', 'StackFormBigdataOp', 'StackFormBigdataRoles',
                   'StackFormRedisTopo', 'StackFormElasticsearchRoles', 'StackFormKafkaSelect',
                   'StackFormRabbitmqAssets']) {
    check(!!sandbox[g] && typeof sandbox[g] === 'object', '全局组件缺失: window.' + g)
  }
  // 6. 模板逐字节等于拆分前（生成器产出的预期串）；--record 时以当前拼装结果重录基线
  if (RECORD) {
    fs.writeFileSync(EXPECT, page.template)
  } else {
    const expect = fs.readFileSync(EXPECT, 'utf8')
    check(page.template === expect, `template 与拆分前不一致: got ${page.template.length} 字符, exp ${expect.length} 字符（有意改动请加 --record 重录）`)
  }
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
  // 9. 模板「裸引用」的成员不得落在 methods（Vue 把 methods 绑定为 bound function，
  //    裸引用拿到的是函数对象：v-for/:data 渲染为空、{{}}/:label 打印 function () { [native code] }；
  //    带参调用（foo(x)）不受影响，故仍可留在 methods）。同类缺陷已出现三次（CA 按钮 / 探活页签 / 组件页签）。
  const bare = new Set()
  // 只收「后面不跟 (」的顶层标识符：foo(x) 是调用（methods 合法），foo 才是裸引用
  const collect = (expr) => {
    // 先剥掉字符串字面量与对象键（:class="{ active: ... }" 的 active 是类名，不是标识符），
    // 再收「后面不跟 (」的顶层标识符：foo(x) 是调用（methods 合法），foo 才是裸引用
    expr = String(expr)
      .replace(/'(?:[^'\\]|\\.)*'|"(?:[^"\\]|\\.)*"/g, "''")
      .replace(/([{,]\s*)[A-Za-z_$][\w$-]*(\s*:)/g, '$1$2')
    const re = /(?:^|[^A-Za-z0-9_$.])([A-Za-z_$][A-Za-z0-9_$]*)(\s*\()?/g
    let m
    while ((m = re.exec(expr))) { if (!m[2]) bare.add(m[1]) }
  }
  for (const m of page.template.matchAll(/\{\{\s*([^}]+?)\s*\}\}/g)) collect(m[1])
  for (const m of page.template.matchAll(/\sv-(?:if|else-if)="([^"]*)"/g)) collect(m[1])
  for (const m of page.template.matchAll(/\sv-for="[^"]*?\sin\s+([^"]*)"/g)) collect(m[1])
  for (const m of page.template.matchAll(/\s:(?:data|label|title|class|content|disabled|loading)="([^"]*)"/g)) collect(m[1])
  const allMethods = Object.assign({}, ...page.mixins.map(mx => mx.methods || {}))
  const allComputed = Object.assign({}, ...page.mixins.map(mx => mx.computed || {}))
  const bareMethods = [...bare].filter(n => allMethods[n] && !allComputed[n])
  check(bareMethods.length === 0, '模板裸引用了 methods 成员（须改为 computed）: ' + bareMethods.join(','))
  // 9b. 模板引用的标识符必须有定义（data/computed/methods/props 或模板局部变量）。
  //     同类缺陷第 4 次踩（tpl-wizard 引用 stackTabs/catFilter/activeCatTab/visibleStacks/
  //     stackCatLabel，但 wizard.js 的成员定义丢失）——渲染时直接 TypeError，冒烟却全绿。
  const locals = new Set()
  for (const m of page.template.matchAll(/\sv-for="([^"]*)"/g)) {
    const left = m[1].split(/\s+in\s+/)[0] || ''
    for (const name of left.matchAll(/[A-Za-z_$][A-Za-z0-9_$]*/g)) locals.add(name[0])
  }
  for (const m of page.template.matchAll(/#default="\{([^}]*)\}"/g)) {
    for (const name of m[1].split(',')) { const n = name.trim(); if (n) locals.add(n) }
  }
  // 模板内联箭头函数的参数（filter(x => x.status...) 的 x）也是局部变量
  for (const m of page.template.matchAll(/([A-Za-z_$][\w$]*)\s*=>/g)) locals.add(m[1])
  for (const m of page.template.matchAll(/\(([^()]*)\)\s*=>/g)) {
    for (const name of m[1].split(',')) { const n = name.trim(); if (/^[A-Za-z_$][\w$]*$/.test(n)) locals.add(n) }
  }
  let dataKeys = new Set()
  for (const mx of page.mixins) {
    if (typeof mx.data === 'function') {
      try { Object.keys(mx.data() || {}).forEach(k => dataKeys.add(k)) } catch (e) { /* data 依赖运行环境则跳过 */ }
    }
  }
  const propsKeys = new Set([].concat(...page.mixins.map(mx => mx.props || []), page.props || []))
  const KNOWN = new Set(['true', 'false', 'null', 'undefined', 'typeof', 'in', 'new', 'this',
    'ElMessage', 'ElMessageBox', 'api', 'window', 'Math', 'Number', 'String', 'Boolean',
    'Object', 'Array', 'JSON', 'Date', 'RegExp', 'Set', 'Map',
    'encodeURIComponent', 'decodeURIComponent', 'parseInt', 'parseFloat', 'isNaN'])
  const undef = [...bare].filter(n => !n.startsWith('$') && !locals.has(n) && !dataKeys.has(n)
    && !propsKeys.has(n) && !allMethods[n] && !allComputed[n] && !KNOWN.has(n))
  check(undef.length === 0, '模板引用了未定义成员（脚本侧缺失或拼写不一致）: ' + undef.join(','))
  // 10. 跨套件切换必须清空共享参数：selectStack 换 key 时若不清空，onModeChange 会按变量名
  //     回填旧值（this.sharedParams[name] || v.default），同名变量（如各套件的 image）串线——
  //     真实案例：先打开 rocketmq 再切 rabbitmq，实例 #16 部署拉成 apache/rocketmq:4.9.7。
  //     mock 以通用方法表构造 this；hostVars 是 computed，置空数组即可（onModeChange 仅遍历）。
  const bpRabbit = {
    key: 'rabbitmq', name: 'RabbitMQ', modes: [{ key: 'cluster', label: '集群' }],
    shared_vars: [{ name: 'image', default: 'rabbitmq:3.13-management' }, { name: 'cluster_name', default: '' }],
    host_vars: [{ name: 'data_dir', default: '/data' }]
  }
  const t1 = Object.assign({}, allMethods, {
    selectedKey: 'rocketmq',
    sharedParams: { image: 'apache/rocketmq:4.9.7', cluster_name: 'rmq-x' },
    hostParams: { 1: { java_opts: '-Xmx4g' } },
    selectedStack: bpRabbit, hostVars: []
  })
  t1.selectStack(bpRabbit)
  check(t1.sharedParams.image === 'rabbitmq:3.13-management',
    '跨套件切换后 image 串线（应为新套件默认，实为 ' + t1.sharedParams.image + '）')
  check(!t1.sharedParams.cluster_name, '跨套件切换未清空共享参数: cluster_name 残留')
  check(Object.keys(t1.hostParams).length === 0, '跨套件切换未清空逐主机参数 hostParams')
  // 重复点击当前套件不得清空（保留已填参数）：与清空分支互为正反例
  const t2 = Object.assign({}, allMethods, {
    selectedKey: 'rabbitmq',
    sharedParams: { image: 'custom/rabbit:9' },
    hostParams: { 2: { data_dir: '/x' } },
    selectedStack: bpRabbit, hostVars: []
  })
  t2.selectStack(bpRabbit)
  check(t2.sharedParams.image === 'custom/rabbit:9' && Object.keys(t2.hostParams).length === 1,
    '重复点击当前套件不应清空已填参数')
  // 10b. 自建仓库选择器（共享变量 Type=registry）：**镜像源只在第 3 步表达**——由套件声明的
  //      registry 变量渲染成「自建仓库」下拉，选中后映射为 hub_host_id 走平台链路
  //      （镜像预热 + insecure 预检），并把仓库地址回写变量。第 2 步不得再有
  //      「直连拉取 / hub 镜像主机」栏：同一件事两处表达只会互相打架（骨架第 2 步那栏已删）。
  //      变量名不参与判断（骨架按 type 识别），但全部 Docker 套件统一声明为 image_registry
  //      ——它就是引擎注入与消费的键（后端门禁见 store/builtin_stacks_test.go 的同名契约测试）。
  const tplSrc10b = ((sources.find(s => /\/common\/tpl-wizard\.js$/.test(s[0]))) || [])[1] || ''
  const wizSrc10b = ((sources.find(s => /\/common\/wizard\.js$/.test(s[0]))) || [])[1] || ''
  check(!!tplSrc10b && !!wizSrc10b, '未加载 common/wizard.js 或 common/tpl-wizard.js')
  check(!/deploy-hub-bar/.test(tplSrc10b),
    '骨架第 2 步不得再有镜像源栏：镜像源只在第 3 步由 registry 变量表达')
  check(!/hubBarVisible|onHubModeChange/.test(wizSrc10b),
    '镜像源旧入口残留：hubBarVisible / onHubModeChange 应随第 2 步那栏一并移除')
  const regVar = Object.assign({}, allComputed).registryVars
  const regPick = Object.assign({}, allComputed).registryPick
  const regAddr = Object.assign({}, allComputed).hubAddr
  const bpReg = {
    key: 'rabbitmq', requires_docker: true,
    shared_vars: [{ name: 'image', default: 'rabbitmq:3.13-management' },
      { name: 'image_registry', type: 'registry', default: '' }]
  }
  check(regVar.call({ selectedStack: bpReg }).length === 1
    && regVar.call({ selectedStack: bpReg })[0].name === 'image_registry',
  'registryVars 未按 type=registry 识别套件声明的仓库变量')
  const t3 = {
    hubMode: 'direct', hubHostId: null, hubAutoInsecure: false,
    registries: [{ host_id: 7, host_name: 'registry-1', host_ip: '192.168.7.13', url: 'http://192.168.7.13:5000', status: 'online' }],
    registriesLoaded: true, selectedStack: bpReg, loadRegistries: noop
  }
  allMethods.onRegistryPick.call(t3, 7)
  check(t3.hubMode === 'hub' && t3.hubHostId === 7, '自建仓库选中后未切到 hub 镜像源')
  check(regPick.call(t3) === 7, '自建仓库选中值未与 hub 状态保持一致')
  check(regAddr.call(t3) === '192.168.7.13:5000',
    '仓库地址解析与部署中心口径不一致（应为 ip:port）: ' + regAddr.call(t3))
  allMethods.onRegistryPick.call(t3, 0)
  check(t3.hubMode === 'direct' && t3.hubHostId === null, '自建仓库取消后未回到直连拉取')
  check(regPick.call(t3) === 0, '自建仓库取消后下拉选中值应为 0（不使用）')
}

// 10c. 延迟插件资产条：打开开关只做「本地是否已存在」的检测，不得自动代下——
//      下载会写服务端目录、消耗外网带宽，必须由用户点「本地下载」显式触发；
//      本地不存在时须给「本地下载 / 上传文件」两个按钮供二选一（离线内网走上传）。
const rbAssetsSrc = ((sources.find(s => s[0].endsWith('stacks/rabbitmq/assets.js'))) || [])[1] || ''
check(!!rbAssetsSrc, '未加载 rabbitmq/assets.js（延迟插件资产条脚本缺失）')
if (rbAssetsSrc) {
  const i0 = rbAssetsSrc.indexOf('async checkAssets()')
  check(i0 >= 0, 'assets.js 缺少 checkAssets（打开开关后的本地检测入口）')
  let body = i0 < 0 ? '' : rbAssetsSrc.slice(i0)
  const iPick = body.indexOf('pickAsset')
  if (iPick >= 0) body = body.slice(0, iPick)
  check(!/fetchAsset\s*\(/.test(body), 'checkAssets 不得自动代下：应由用户点「本地下载」显式触发')
  check(!/FormData|assets\/upload/.test(body), 'checkAssets 不得自动上传：应由用户点「上传文件」显式触发')
  check(!/_autoTried/.test(rbAssetsSrc), 'assets.js 残留自动预置痕迹 _autoTried')
  check(/>本地下载</.test(rbAssetsSrc) && />上传文件</.test(rbAssetsSrc),
    '本地不存在时必须提供「本地下载」「上传文件」两个按钮')
  check(/v-if="pluginOn"/.test(rbAssetsSrc), '资产条应以插件开关为渲染条件（打开开关即触发检测）')
}

// 10d. 套件 UI 的挂载步骤必须与触发它的参数同屏，否则因果链永远不可见——
//      rabbitmq 的资产条曾挂第 2 步（forms.step2Comp），而它的开关 delayed_plugin 是第 3 步
//      共享变量：第 2 步渲染时开关还没勾（pluginOn 恒假，组件不出现），等用户走到第 3 步
//      打开开关，资产条停在上一屏。表现就是「打开了开关，什么提示都没有」。
//      bigdata 之所以正常，是它的触发条件（HA/组件勾选）与资产条同在 roles 组件的第 2 步里。
const tplWizSrc = ((sources.find(s => /\/common\/tpl-wizard\.js$/.test(s[0]))) || [])[1] || ''
const wizCoreSrc = ((sources.find(s => /\/common\/wizard\.js$/.test(s[0]))) || [])[1] || ''
check(!!tplWizSrc && !!wizCoreSrc, '未加载 common/wizard.js 或 common/tpl-wizard.js')
check(/step===3 && step3FormComp/.test(tplWizSrc),
  '骨架第 3 步缺少套件插槽 step3FormComp（共享参数区的套件 UI 无处可挂）')
check(/step3FormComp\(\)\s*\{\s*return this\.formEntry\?\.step3Comp/.test(wizCoreSrc),
  'wizard.js 未按 forms.step3Comp 暴露第 3 步套件组件')
const rbForms = (forms && forms.rabbitmq) || {}
check(rbForms.step3Comp === 'stack-form-rabbitmq-assets',
  'rabbitmq 资产条必须注册为 forms.step3Comp（与 delayed_plugin 开关同一步），当前: ' + rbForms.step3Comp)
check(!rbForms.step2Comp,
  'rabbitmq 资产条不得挂 forms.step2Comp：开关在第 3 步，资产条会显示在上一屏、用户看不到')

// 11. 禁用 Vue 2 专有 API：本项目跑的是 Vue 3（vendor/vue.global.prod.js），
//     这些方法在 Vue 3 里**不存在**，调用处会抛 TypeError 并静默失效——
//     表现形式是「界面上的开关点了没反应」。$set 已经真实踩过一次（wizard.js 的 onBoolVar，
//     该路径当时是死代码，直到 kafka 第一步开关接上才暴露）。
const VUE2_ONLY = [
  ['this.$set(', /this\.\$set\s*\(/],
  ['this.$delete(', /this\.\$delete\s*\(/],
  ['this.$on(', /this\.\$on\s*\(/],
  ['this.$off(', /this\.\$off\s*\(/],
  ['Vue.set(', /\bVue\.set\s*\(/],
  ['Vue.delete(', /\bVue\.delete\s*\(/],
  ['Vue.prototype', /Vue\.prototype\b/],
  ['beforeDestroy', /beforeDestroy\b/]
]
for (const [label, rx] of VUE2_ONLY) {
  for (const [src, code] of sources) {
    const lines = code.split(/\r?\n/)
    for (let i = 0; i < lines.length; i++) {
      if (rx.test(lines[i])) errs.push(`Vue 3 不存在的 API ${label}（${src}:${i + 1}）：${lines[i].trim().slice(0, 90)}`)
    }
  }
}

// 12. ES Discover（日志搜索）文档流：用户口径「不用卡片，用虚线分割」（2026-09-17）。
//     背景：文档条原本是圆角卡片（border + radius + 时间列灰底 + 卡片间 10px 间隙），
//     改为纯列表——行与行之间用虚线分隔，时间列只留虚线竖分隔、不留灰底。
//     这几条纯属视觉口径，没有别的机制会拦住回退，所以在这里上锁。
const cssSrc = fs.readFileSync(path.join(ROOT, 'template', 'static', 'style.css'), 'utf8')
function cssRule(sel) {
  const i = cssSrc.indexOf('\n' + sel + ' {')
  if (i < 0) return ''
  const j = cssSrc.indexOf('}', i)
  return j < 0 ? '' : cssSrc.slice(i, j)
}
const ruleDocs = cssRule('.es-docs')
const ruleDoc = cssRule('.es-doc')
const ruleDocTime = cssRule('.es-doc-time')
const ruleDocsHead = cssRule('.es-docs-head')
check(!!ruleDocs && !!ruleDoc && !!ruleDocTime && !!ruleDocsHead,
  'style.css 缺少 ES 文档流规则（.es-docs / .es-doc / .es-doc-time / .es-docs-head）')
check(/border-bottom:\s*1px dashed/.test(ruleDoc),
  'ES 文档行须以虚线分隔（.es-doc 需 border-bottom: 1px dashed），当前: ' + ruleDoc.trim())
check(!/border-radius/.test(ruleDoc) && !/border:\s*1px solid/.test(ruleDoc) && !/background/.test(ruleDoc),
  'ES 文档行不得再做成卡片（.es-doc 不得有圆角 / 实线边框 / 底色），当前: ' + ruleDoc.trim())
check(/flex-shrink:\s*0/.test(ruleDoc),
  'ES 文档行必须保留 flex-shrink:0（滚动容器直接子项铁律，去掉会被挤成细线），当前: ' + ruleDoc.trim())
check(!/\bgap:/.test(ruleDocs),
  'ES 文档流改用虚线分隔后不应保留卡片间隙（.es-docs 不得有 gap），当前: ' + ruleDocs.trim())
check(/border-right:\s*1px dashed/.test(ruleDocTime) && !/background/.test(ruleDocTime),
  'ES 文档时间列应只留虚线竖分隔、不保留卡片灰底，当前: ' + ruleDocTime.trim())
check(/border-bottom:\s*1px dashed/.test(ruleDocsHead) && !/border-radius/.test(ruleDocsHead) && !/border:\s*1px solid/.test(ruleDocsHead),
  'ES 文档表头不应做成卡片（去圆角 / 实线边框，改虚线底边），当前: ' + ruleDocsHead.trim())

// 13. 索引模板「可视化创建」：请求体由 es_template_build.js 的纯函数拼装，
//     这里**直接调用**它们做真值断言（纯逻辑用源码文本匹配是测不出错的）。
//     断言的核心是「与线上既有四个索引模板等价」：公共层与索引模板的切分口径、
//     两条请求的顺序、模拟体的内联口径、ILM 阶段链、pattern 重叠、命名白名单、回显转义。
const TB = sandbox.EsTplBuild
check(!!TB, '未加载 es_template_build.js（window.EsTplBuild 缺失）')
if (TB) {
  const base = { name: 'big-es-default-index', patterns: ['xafq_credit_api_access_log*'], priority: 21,
    lifecycleOn: true, policyName: 'hot-warm-cold-180days', shards: 3, replicas: 0,
    refreshInterval: '10s', translogFlush: '512m', translogDurability: 'async',
    tier: 'data_hot', timeField: 'timestamp', timeFormat: 'epoch_millis' }

  // 13a. 不抽公共层：一份请求体应完整体现线上模板的全部设置
  const solo = TB.buildIndexTemplate(Object.assign({}, base, { useBase: false }))
  const si = solo.template.settings.index
  const wantKeys = ['refresh_interval', 'translog', 'routing', 'lifecycle', 'number_of_shards', 'number_of_replicas']
  const missKeys = wantKeys.filter(k => si[k] === undefined)
  check(missKeys.length === 0, '不用公共层时索引模板缺少设置项: ' + missKeys.join(','))
  check(si.lifecycle.name === 'hot-warm-cold-180days'
    && si.routing.allocation.include._tier_preference === 'data_hot'
    && si.number_of_shards === '3',
  '不用公共层时设置项与线上模板口径不一致: ' + JSON.stringify(si))
  check(solo.template.mappings.properties.timestamp.format === 'epoch_millis',
    '时间字段 mapping 未按 epoch_millis 生成')
  check(Array.isArray(solo.composed_of) && solo.composed_of.length === 0,
    '不用公共层时 composed_of 必须为空数组（与线上模板保持一致）')

  // 13b. 抽公共层：必须是两条请求，且组件模板在前——
  //      索引模板的 composed_of 引用它，顺序反了 ES 直接拒绝。
  const withBase = Object.assign({}, base, { useBase: true, baseMode: 'new', baseName: 'xafq-logs-base' })
  const reqs = TB.buildRequests(withBase)
  check(reqs.length === 2, '抽公共层时应发两条请求（组件模板 + 索引模板），当前 ' + reqs.length)
  // 条数不对时只报条数，不进下面的逐条断言——否则 reqs[1] 会是 undefined，
  // TypeError 会把脚本直接打断，连「为什么红」都来不及打印出来。
  if (reqs.length === 2) {
    check(reqs[0].path === '/_component_template/xafq-logs-base',
      '组件模板必须是第一条请求，当前第一条: ' + reqs[0].path)
    check(reqs[1].path === '/_index_template/big-es-default-index', '第二条应为索引模板，当前: ' + reqs[1].path)
    const bset = reqs[0].body.template.settings.index
    check(!!(bset.lifecycle && bset.refresh_interval && bset.translog && bset.routing),
      '公共层未收齐横切设置项（lifecycle / refresh_interval / translog / routing）: ' + JSON.stringify(bset))
    check(bset.number_of_shards === undefined,
      '分片数是本模板差异项，不得进公共层（否则所有共享者被同一个分片数锁死）')
    const iset = reqs[1].body.template.settings.index
    check(JSON.stringify(reqs[1].body.composed_of) === JSON.stringify(['xafq-logs-base']),
      '索引模板未通过 composed_of 引用公共层: ' + JSON.stringify(reqs[1].body.composed_of))
    check(iset.lifecycle === undefined && iset.refresh_interval === undefined && iset.translog === undefined,
      '索引模板不得重复声明已进公共层的项（重复声明会覆盖组件模板，把「改一处处处生效」废掉）: ' + JSON.stringify(iset))
    check(iset.number_of_shards === '3', '索引模板应保留自己的分片数')
  }

  // 13c. 保存前模拟：组件模板尚未创建时 composed_of 在 ES 侧解析不到（_simulate 报 not found），
  //      必须内联展开公共层——模拟的才是「保存后」的合并结果。
  const sim = TB.simulateBody(withBase)
  check(sim.composed_of.length === 0, '公共层未创建时模拟体必须清空 composed_of，否则 _simulate 报 not found')
  const ms = sim.template.settings.index
  check(!!(ms.lifecycle && ms.refresh_interval && ms.routing) && ms.number_of_shards === '3',
    '模拟体应内联公共层 + 本模板差异项，当前: ' + JSON.stringify(ms))

  // 13d. 复用已有组件模板时不得再发组件模板请求
  const reuse = Object.assign({}, base, { useBase: true, baseMode: 'existing', baseName: 'xafq-logs-base' })
  check(TB.buildRequests(reuse).length === 1, '复用已有组件模板时只应发索引模板一条请求')

  // 13e. ILM 阶段链与「会不会自动清理」（用线上 hot-warm-cold-180days 的真实结构）
  const pol = { policy: { phases: {
    hot: { min_age: '0ms' },
    warm: { min_age: '7d', actions: { allocate: { include: { _tier_preference: 'data_warm' } } } },
    cold: { min_age: '30d', actions: { allocate: { include: { _tier_preference: 'data_cold' } } } },
    delete: { min_age: '180d', actions: { delete: {} } } } } }
  const chain = TB.phaseChain(pol).map(p => p.label)
  check(JSON.stringify(chain) === JSON.stringify(['hot', 'warm 7d', 'cold 30d', 'delete 180d']),
    'ILM 阶段链顺序/标签不对: ' + JSON.stringify(chain))
  check(JSON.stringify(TB.phaseTiers(pol)) === JSON.stringify(['data_warm', 'data_cold']),
    '未能从策略里解析出迁移目标层: ' + JSON.stringify(TB.phaseTiers(pol)))
  check(TB.autoDeletes(pol) === true && TB.autoDeletes({ policy: { phases: { hot: {} } } }) === false,
    '「策略是否有 delete 阶段」判定错误')
  check(TB.phaseChain({ policy: { phases: { hot: { min_age: '0ms' } } } })[0].label === 'hot',
    'min_age 为 0ms 时不应显示成 hot 0ms')

  // 13f. pattern 重叠：线上 es-default-index(xafq_* · 20) 与具体业务模板(21) 构成一张覆盖网
  const existing = [
    { name: 'es-default-index', index_patterns: ['xafq_*'], priority: 20, composed_of: [] },
    { name: 'big-es-default-index', index_patterns: ['xafq_credit_api_access_log*'], priority: 21, composed_of: [] }
  ]
  const ovHit = TB.overlaps({ name: 'sendtimestamp-index', patterns: ['xafq_sms_sending_record*'], priority: 21 }, existing)
    .filter(o => o.name === 'es-default-index')[0]
  check(!!ovHit, '未检出与通配模板 es-default-index 的 pattern 重叠')
  check(!!ovHit && ovHit.wins === true, 'priority 21 应压过 20：重叠处本模板生效')
  const ovLow = TB.overlaps({ name: 'tmp', patterns: ['xafq_sms_sending_record*'], priority: 10 }, existing)
    .filter(o => o.name === 'es-default-index')[0]
  check(!!ovLow && ovLow.wins === false, 'priority 10 应被 20 覆盖：重叠处对方生效')
  check(TB.overlaps({ name: 'tmp', patterns: ['other_app_*'], priority: 21 }, existing).length === 0,
    '不相干的 pattern 不应报重叠')

  // 13g. 命名白名单与必填（与后端 validateResourceName 同口径；前端只做早拦，后端才是门）
  check(TB.nameError('.es-x') !== '' && TB.nameError('a*b') !== '' && TB.nameError('a,b') !== ''
    && TB.nameError('_x') !== '' && TB.nameError('') !== '' && TB.nameError('ok-name') === '',
  '名称白名单判定与后端 validateResourceName 不一致')
  const verr = TB.validate({ name: 'ok', patterns: [], lifecycleOn: true, policyName: '', useBase: false }, [])
  check(verr.length === 2, '校验应同时报出「无 pattern」与「未选策略」，当前: ' + JSON.stringify(verr))

  // 13h. 二次确认的请求回显（§3.3 硬约束）走 dangerouslyUseHTMLString，不转义就是自造 XSS
  const echo = TB.echoHTML('head', [{ method: 'PUT', path: '/_index_template/<img>', body: { a: '<b>' } }])
  check(echo.indexOf('<img>') < 0 && echo.indexOf('<b>') < 0,
    '请求回显未转义用户输入（模板名 / pattern 会原样进 HTML）')
  check(echo.indexOf('&lt;img&gt;') >= 0, '请求回显转义后应保留可见的尖括号文本')
  check(/<pre[^>]*white-space:pre-wrap/.test(echo),
    '请求回显必须放在 white-space:pre-wrap 的 pre 里（ElMessageBox 纯文本消息不保留换行，两份 JSON 会糊成一团）')
}

// 13i. 装配顺序：两个脚本必须排在 es_templates.js **之前**。
//      es_templates.js 的 components 是在对象字面量求值时构造的，那一刻 window.EsTemplateQuickDialog
//      必须已存在；顺序写反的表现是「按钮点了报组件未注册」。
const htmlSrc = fs.readFileSync(INDEX, 'utf8')
const iBuild = htmlSrc.indexOf('/static/pages/es_template_build.js')
const iQuick = htmlSrc.indexOf('/static/pages/es_template_quick.js')
const iTplPage = htmlSrc.indexOf('/static/pages/es_templates.js')
check(iBuild >= 0 && iQuick >= 0, 'index.html 未引入 es_template_build.js / es_template_quick.js')
check(iBuild >= 0 && iBuild < iQuick && iQuick < iTplPage,
  'index.html 脚本顺序错误：必须 es_template_build.js → es_template_quick.js → es_templates.js')
const tplTabSrc = ((sources.find(s => s[0].endsWith('pages/es_templates.js'))) || [])[1] || ''
check(!!tplTabSrc, '未加载 pages/es_templates.js')
check(/'es-template-quick':\s*window\.EsTemplateQuickDialog/.test(tplTabSrc),
  'es_templates.js 未把可视化创建对话框注册为局部组件')
check(/openQuick\(\)\s*\{\s*this\.\$refs\.quick\.open\(\)/.test(tplTabSrc),
  'es_templates.js 的「可视化创建」按钮无处可去（缺少 openQuick 调用 ref.open()）')
check(/<es-template-quick ref="quick"/.test(tplTabSrc), 'es_templates.js 未挂载 <es-template-quick>')
// 断言必须锚定在**按钮**上：本文件上方那段说明文字里也写着「JSON 直接提交」，
// 只写 />JSON 直接提交</ 会被说明文字满足，把按钮删掉照样通过（实测踩过——假绿）。
check(/@click="newIdx">JSON 直接提交</.test(tplTabSrc), '两个入口必须并存：JSON 直接提交按钮不得被可视化创建取代')

// 13j. 对话框不得高过视口，且右列 JSON 预览必须铺满（两条都是渲染台量出来的，不是估算）。
//      ① 改前：左侧八段表单 + 右侧固定 380px 预览，849px 视口下整框曾达 1448px，底部三个按钮被顶出屏幕。
//      ② 改后仍不够：预览框固定 380px，而右列被撑到 1232px（左侧表单有多长它就有多长），可见区下方空出 690px（渲染台 ?fixedprev=true 回退复现，与改前逐项一致）。
//      口径：整框 flex 列 → body 只当容器（不再自己滚）→ 两列吃满剩余高度并各自滚 → 预览框吃掉右列剩余高度。
//      纯版式契约，没有别的机制会拦住回退，故上锁。
const ruleDlg = cssRule('.es-tqc-dialog')
const ruleDlgBody = cssRule('.es-tqc-dialog > .el-dialog__body')
const ruleDlgHead = cssRule('.es-tqc-dialog > .el-dialog__header')
const ruleDlgFoot = cssRule('.es-tqc-dialog > .el-dialog__footer')
const ruleQCLeft = cssRule('.es-tqc-dialog .es-qc-left')
const ruleQCPrev = cssRule('.es-tqc-dialog .es-qc-right > .es-qc-preview')
check(!!ruleDlg && !!ruleDlgBody && !!ruleDlgHead && !!ruleDlgFoot && !!ruleQCLeft && !!ruleQCPrev,
  'style.css 缺少 .es-tqc-dialog 的视口自适应 / 右列铺满规则（整框 flex 列 + body 容器化 + 左列自滚 + 预览框生长）')
check(/display:\s*flex/.test(ruleDlg) && /flex-direction:\s*column/.test(ruleDlg)
  && /max-height:\s*calc\(100vh/.test(ruleDlg),
  '索引模板对话框须为高度受限的 flex 列（display:flex + flex-direction:column + max-height:calc(100vh…），当前: ' + ruleDlg.trim())
// body 必须 min-height:0（能缩到内容以下）且是 flex 列容器；不得再自己滚——body 一滚，
// 右列就被撑成整段表单那么高，预览框再怎么生长也只是长条，铺不满可见区（实测踩过）。
check(/min-height:\s*0/.test(ruleDlgBody) && /display:\s*flex/.test(ruleDlgBody)
  && /flex-direction:\s*column/.test(ruleDlgBody),
  '对话框 body 须能收缩并只当 flex 列容器（min-height:0 + display:flex + flex-direction:column）——'
  + '缺 min-height:0 时 flex 项缩不到内容以下，整框照样顶出视口，当前: ' + ruleDlgBody.trim())
check(!/overflow-y:\s*auto/.test(ruleDlgBody),
  '对话框 body 不得自滚：改由左右两列各自滚，否则右列被撑长、预览框铺不满，当前: ' + ruleDlgBody.trim())
check(/flex-shrink:\s*0/.test(ruleDlgHead) && /flex-shrink:\s*0/.test(ruleDlgFoot),
  '对话框头尾须 flex-shrink:0（高度受限 flex 列的直接子项铁律，否则被压扁），当前 header: '
  + ruleDlgHead.trim() + ' / footer: ' + ruleDlgFoot.trim())
check(/overflow-y:\s*auto/.test(ruleQCLeft),
  '左表单列须自己滚（overflow-y:auto），否则长表单会把 body 顶长、右列跟着被撑高，当前: ' + ruleQCLeft.trim())
// 预览框生长：flex:1 1 0 吃掉右列剩余高度 + 显式 min-height 才允许缩到内容以下（flex 自动最小尺寸陷阱）。
// 只写 flex-grow 不写 min-height 时，pre 的自动最小尺寸等于整段 JSON，照样缩不下来。
check(/flex:\s*1\s+1\s+0/.test(ruleQCPrev) && /min-height:\s*\d+px/.test(ruleQCPrev)
  && /height:\s*auto/.test(ruleQCPrev) && /max-height:\s*none/.test(ruleQCPrev),
  'JSON 预览框须吃掉右列剩余高度（flex:1 1 0 + height:auto + max-height:none + 显式 min-height，'
  + '以覆盖 .es-qc-preview 的固定 380px 与 max-height:420px），当前: ' + ruleQCPrev.trim())


// 14. 原始 JSON 抽屉（索引 mapping、模板 _simulate 结果）：JSON 面板必须向下铺满。
//     背景：面板吃的是全局 .es-source { max-height: 220px }，抽屉 1294px 高时面板仍只有 220px，
//     下方空出 959px；长 mapping（实测内容 2456px）挤在 220px 小框里滚——用户口径是「只显示一部分、没向下铺满」。
//     口径与 13j / 生命周期详情一致：body 当高度受限 flex 列 → 说明行不压缩 → 面板吃满剩余高度。
//     附带两条：面板要描边（抽屉与面板同为白底，不描边看不出到底铺没铺满）；
//     面板要 position:relative（v-loading 遮罩是绝对定位的，改前实测遮罩 0→1294 盖满整个抽屉、连标题一起盖）。
//     纯版式契约，没有别的机制会拦住回退，故上锁。
const rawBody = cssRule('.es-raw-drawer .el-drawer__body')
const rawChild = cssRule('.es-raw-drawer .el-drawer__body > *')
const rawPre = cssRule('.es-raw-drawer .el-drawer__body > .es-source')
check(!!rawBody && !!rawChild && !!rawPre,
  'style.css 缺少 .es-raw-drawer 的铺满规则（body 容器化 / 直接子项不压缩 / 面板吃满剩余高度）')
check(/display:\s*flex/.test(rawBody) && /flex-direction:\s*column/.test(rawBody) && /overflow:\s*hidden/.test(rawBody),
  '原始 JSON 抽屉的 body 须为不自滚的 flex 列容器（display:flex + flex-direction:column + overflow:hidden），'
  + '当前: ' + rawBody.trim())
check(/flex-shrink:\s*0/.test(rawChild),
  '抽屉 body 的直接子项须 flex-shrink:0（高度受限 flex 列铁律，否则说明行被压扁），当前: ' + rawChild.trim())
check(/flex:\s*1\s+1\s+0/.test(rawPre) && /min-height:\s*0/.test(rawPre) && /max-height:\s*none/.test(rawPre),
  'JSON 面板须吃满抽屉剩余高度（flex:1 1 0 + min-height:0 + max-height:none，'
  + '以覆盖 .es-source 全局的 max-height:220px）——缺 min-height:0 时 <pre> 的自动最小尺寸等于整段 JSON，'
  + '缩不下来会顶长容器，当前: ' + rawPre.trim())
check(/position:\s*relative/.test(rawPre),
  'JSON 面板须 position:relative：v-loading 遮罩是绝对定位的，面板不定位它会盖满整个抽屉（实测 0→1294），'
  + '当前: ' + rawPre.trim())
check(/border:\s*1px solid/.test(rawPre),
  'JSON 面板须描边（与 .es-lc-detail .es-source / .es-qc-preview 一致）：抽屉与面板同为白底，'
  + '不描边就看不出是否铺满，当前: ' + rawPre.trim())

// 14b. 三个原始 JSON 抽屉都要挂上该类，且类必须在**承载面板的那个** el-drawer 上。
//      类挂错位置或漏挂时 CSS 静默不生效（面板照样封顶 220px），不会抛错，只能在这里拦。
const RAW_DRAWERS = ['pages/es_index.js', 'pages/es_templates.js', 'pages/es_template_quick.js']
for (const rel of RAW_DRAWERS) {
  const code = ((sources.find(s => s[0].endsWith(rel))) || [])[1] || ''
  check(!!code, '未加载 ' + rel)
  if (!code) continue
  const iDrawer = code.search(/<el-drawer[^>]*class="es-raw-drawer"/)
  const iPre = code.search(/<pre class="es-source" v-loading=/)
  check(iDrawer >= 0, rel + ' 的原始 JSON 抽屉未挂 class="es-raw-drawer"（[^>]* 限定：必须写在 el-drawer 标签上）')
  check(iPre >= 0, rel + ' 缺少带 v-loading 的 .es-source 面板')
  check(iDrawer >= 0 && iPre >= 0 && iDrawer < iPre,
    rel + ' 的 es-raw-drawer 没有挂在承载 JSON 面板的那个抽屉上（类必须落在面板之前出现的 el-drawer 标签上）')
}

// 14c. 反向覆盖：凡是用了「带加载态的 .es-source 面板」的文件，同文件抽屉就必须挂 es-raw-drawer。
//      将来新增一处同类面板却忘了挂类时，这里要红，而不是等用户再报一次「没铺满」。
for (const [rel, code] of sources) {
  if (code.indexOf('<pre class="es-source" v-loading=') < 0) continue
  check(/<el-drawer[^>]*class="es-raw-drawer"/.test(code),
    rel + ' 有带 v-loading 的 .es-source 面板，但同文件的 el-drawer 未挂 class="es-raw-drawer"（面板会封顶 220px）')
}


if (errs.length) {
  console.log('前端装载冒烟 不通过：')
  errs.forEach(e => console.log('  - ' + e))
  process.exit(1)
}
console.log(`前端装载冒烟 通过：脚本 ${loaded}/${expected} 个，9 个套件注册齐备，`
  + `页面局部组件 ${Object.keys(page.components).length} 个，模板 ${page.template.length} 字符`
  + (RECORD ? '已重录为基线。' : '逐字节一致。'))
