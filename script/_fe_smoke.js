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
// 轮询用的 setInterval：沙箱里不能真起定时器（会让断言进程挂住不退出），给个空实现。
// 「取一次进度」本身由业务断言直接调方法覆盖。
sandbox.setInterval = () => 0
sandbox.clearInterval = noop
sandbox.URL = { createObjectURL: () => 'blob:x', revokeObjectURL: noop }
sandbox.Blob = function () {}
sandbox.document = {
  createElement: () => ({ click: noop, style: {}, setAttribute: noop }),
  addEventListener: noop, querySelector: () => null, body: {},
  head: { appendChild: noop }
}
// location 必须模拟浏览器形态：wails-shim.js 用 protocol/hostname 判定是否桌面环境，
// 缺字段会让 isDesktop 误判为 true（undefined !== 'http:'），从而在无 DOM 沙箱里走桌面分支崩掉。
sandbox.location = { hash: '', protocol: 'http:', hostname: '127.0.0.1' }
sandbox.addEventListener = noop
sandbox.Vue = { createApp: () => ({ use: noop, component: noop, mount: () => ({}) }), ref: v => ({ value: v }), computed: f => f, watch: noop }
sandbox.ElementPlus = {}
sandbox.ElementPlusIconsVue = {}
sandbox.localStorage = { getItem: () => null, setItem: noop, removeItem: noop }
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
  check(Array.isArray(page.props) && page.props.join(',') === 'page,versionData', 'props 不一致')
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

// 12b. 数据视图时间升降序（用户口径「一个按钮解决：默认降序，点一下变升序」）：
//      排序做在查询侧（ES sort 的时间字段），切换即重查；每次查询恒带 sort_order。
{
  const discTpl = String((sandbox.EsDiscover && sandbox.EsDiscover.template) || '')
  check(/@click="toggleSort"/.test(discTpl) && discTpl.indexOf('时间 ↓ 降序') >= 0 && discTpl.indexOf('时间 ↑ 升序') >= 0,
    '数据视图时间排序要用一个按钮切换（默认「时间 ↓ 降序」，点击后「时间 ↑ 升序」）')
}

// 12c. 命中高亮（用户口径「只有查询之后才触发高亮；多个匹配项多个颜色」）：
//      命中项在查询时刻从查询串快照（打字不触发），客户端按项分色渲染。
{
  const discTpl = String((sandbox.EsDiscover && sandbox.EsDiscover.template) || '')
  check(/v-html="hlValue\(kv\.v\)"/.test(discTpl) && discTpl.indexOf('renderHl') < 0,
    '文档值要用 hlValue 渲染（查询时刻的快照命中项分色高亮），旧的 ES 片段 renderHl 应移除')
  const t = sandbox.EsDiscover && sandbox.EsDiscover.methods.extractHlTerms
  if (t) {
    const terms = sandbox.EsDiscover.methods.extractHlTerms.call(sandbox.EsDiscover,
      'env:prod and (level:error or "connection refused")')
    check(terms.length === 3 && terms[0] === 'prod' && terms[1] === 'error' && terms[2] === 'connection refused',
      'Discover：命中项提取（field:value 取取值、引号短语整体、and/or 不算）: ' + JSON.stringify(terms))
    const terms2 = sandbox.EsDiscover.methods.extractHlTerms.call(sandbox.EsDiscover, 'status:>=500 500 timeout')
    check(terms2.length === 2 && terms2.indexOf('500') >= 0 && terms2.indexOf('timeout') >= 0,
      'Discover：范围值取数字部分、重复项去重: ' + JSON.stringify(terms2))
  }
}

// 12d. 索引列表 / 总数异步化（用户口径「先展示前 100 个，总数异步转圈」「新建视图的右边索引列表
//      转圈异步加载、不阻抽屉框」）：列表与总数拆成两个请求，列表先渲染；抽屉即开即用。
{
  const idxTpl = String((sandbox.EsIndexTab && sandbox.EsIndexTab.template) || '')
  check(/v-if="!loadingTotal"/.test(idxTpl) && /<Loading \/>/.test(idxTpl),
    '索引页：左上角总数位要有异步转圈（列表先渲染，总数慢一步不挡表格）')
  const vTpl = String((sandbox.EsViewsTab && sandbox.EsViewsTab.template) || '')
  check(vTpl.indexOf('es-view-create" v-loading') < 0 && /class="es-view-match-list" v-loading="loadingIdx"/.test(vTpl),
    '新建视图：转圈只盖右侧索引列表，不阻塞整个抽屉')
}

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


// 15. MySQL 导入 / 导出对话框（第八轮新增，首次上锁）。
//     两个组件的模板都很大，标识符拼错只有在真渲染时才炸；9b 的口径只作用于套件骨架页，
//     故这里对组件对象复用同一套口径（data/computed/methods/props + 模板局部变量）。
//     另锁三件最容易悄悄漂移的事：脚本加载顺序（export 的 mounted 会直接调用 window.MySQLTransferUtil）、
//     「本地直连不设超时」（两个组件的请求都必须带 timeout: 0）、导入的两段式确认与安全默认值。
const T_UTIL = '/static/pages/mysql_transfer_util.js'
const T_EXP = '/static/pages/mysql_export.js'
const T_IMP = '/static/pages/mysql_import.js'
const tOrder = srcs.map(s => s.split('?')[0])
check([T_UTIL, T_EXP, T_IMP].every(p => tOrder.indexOf(p) >= 0), 'MySQL 导入导出三脚本未全部引入 index.html')
check(tOrder.indexOf(T_UTIL) >= 0 && tOrder.indexOf(T_UTIL) < tOrder.indexOf(T_EXP)
  && tOrder.indexOf(T_UTIL) < tOrder.indexOf(T_IMP),
  'mysql_transfer_util.js 必须排在 mysql_export.js / mysql_import.js 之前'
  + '（export 的 mounted 会直接调用 window.MySQLTransferUtil.guessTable）')

const tSrc = {}
for (const rel of [T_UTIL, T_EXP, T_IMP]) tSrc[rel] = ((sources.find(s => s[0] === rel)) || [])[1] || ''

for (const g of ['MySQLTransferUtil', 'MySQLExport', 'MySQLImport']) {
  check(!!sandbox[g], 'window.' + g + ' 缺失')
}

// 15a. 组件模板引用的标识符必须有定义（判定口径与 9b 一致：剥字符串字面量与对象键，
//      带参调用不算裸引用；v-for / 插槽解构 / 箭头参数算模板局部变量）。
const T_KNOWN = new Set(['true', 'false', 'null', 'undefined', 'typeof', 'in', 'new', 'this',
  'ElMessage', 'ElMessageBox', 'api', 'window', 'Math', 'Number', 'String', 'Boolean',
  'Object', 'Array', 'JSON', 'Date', 'RegExp', 'Set', 'Map',
  'encodeURIComponent', 'decodeURIComponent', 'parseInt', 'parseFloat', 'isNaN'])
function checkTransferTemplate(compName, comp) {
  const tpl = String((comp && comp.template) || '')
  check(tpl.length > 0, compName + ' 无 template')
  if (!tpl) return
  const bare = new Set()
  const collect = (expr) => {
    expr = String(expr)
      .replace(/'(?:[^'\\]|\\.)*'|"(?:[^"\\]|\\.)*"/g, "''")
      .replace(/([{,]\s*)[A-Za-z_$][\w$-]*(\s*:)/g, '$1$2')
    const re = /(?:^|[^A-Za-z0-9_$.])([A-Za-z_$][A-Za-z0-9_$]*)(\s*\()?/g
    let mm
    while ((mm = re.exec(expr))) { if (!mm[2]) bare.add(mm[1]) }
  }
  for (const mm of tpl.matchAll(/\{\{\s*([^}]+?)\s*\}\}/g)) collect(mm[1])
  for (const mm of tpl.matchAll(/\sv-(?:if|else-if)="([^"]*)"/g)) collect(mm[1])
  for (const mm of tpl.matchAll(/\sv-for="[^"]*?\sin\s+([^"]*)"/g)) collect(mm[1])
  for (const mm of tpl.matchAll(/\sv-model="([^"]*)"/g)) collect(mm[1])
  for (const mm of tpl.matchAll(/\s:(?:data|label|title|class|content|disabled|loading|key|value|placeholder)="([^"]*)"/g)) collect(mm[1])

  const locals = new Set()
  for (const mm of tpl.matchAll(/\sv-for="([^"]*)"/g)) {
    const left = mm[1].split(/\s+in\s+/)[0] || ''
    for (const name of left.matchAll(/[A-Za-z_$][A-Za-z0-9_$]*/g)) locals.add(name[0])
  }
  for (const mm of tpl.matchAll(/#default="\{([^}]*)\}"/g)) {
    for (const name of mm[1].split(',')) { const n = name.trim(); if (n) locals.add(n) }
  }
  for (const mm of tpl.matchAll(/([A-Za-z_$][\w$]*)\s*=>/g)) locals.add(mm[1])

  const dataKeys = new Set()
  try {
    if (typeof comp.data === 'function') {
      // data() 可能读根上下文（key 浏览器的默认库来自 this.conn.default_db）。
      // 用一个「取不到就给空串」的占位上下文调用，目的只是拿到键名清单。
      const stub = new Proxy({ conn: { id: 1, default_db: 0 }, connId: 1, db: 0, keyName: 'probe' }, {
        get: (t, k) => (typeof k === 'symbol' ? undefined : (k in t ? t[k] : ''))
      })
      Object.keys(comp.data.call(stub) || {}).forEach(k => dataKeys.add(k))
    }
  } catch (e) { /* data 依赖运行环境则跳过 */ }
  const members = new Set([].concat(
    Object.keys(comp.computed || {}), Object.keys(comp.methods || {}),
    Array.isArray(comp.props) ? comp.props : Object.keys(comp.props || {})))
  const undef = [...bare].filter(n => !n.startsWith('$') && !locals.has(n) && !dataKeys.has(n)
    && !members.has(n) && !T_KNOWN.has(n))
  check(undef.length === 0, compName + ' 模板引用了未定义成员: ' + undef.join(','))
}
checkTransferTemplate('MySQLExport', sandbox.MySQLExport)
checkTransferTemplate('MySQLImport', sandbox.MySQLImport)

// 15b. 默认值：导出按 INSERT 分批，导入默认「整批回滚并停止」且结构变更默认关闭。
//      这几个默认值是安全边界，不该被顺手改掉（skip 会静默跳过失败语句；几万条 dump 里
//      混进一条 DROP，肉眼是看不出来的）。
const expData = (sandbox.MySQLExport && sandbox.MySQLExport.data) ? sandbox.MySQLExport.data() : {}
const impData = (sandbox.MySQLImport && sandbox.MySQLImport.data) ? sandbox.MySQLImport.data() : {}
check(expData.format === 'sql', '导出默认格式应为 INSERT .sql，当前: ' + expData.format)
check(expData.batchRows === 200, '导出默认每批行数应为 200，当前: ' + expData.batchRows)
check(impData.batchSize === 500, '导入默认每批条数应为 500，当前: ' + impData.batchSize)
check(impData.onError === 'stop', '导入默认错误策略应为「遇错整批回滚并停止」，当前: ' + impData.onError)
check(impData.allowSchema === false, '导入的「允许结构变更」默认必须为关，当前: ' + impData.allowSchema)

// 15c. 请求契约：不设 HTTP 超时（本地直连）；导入两段式确认（先探后提交）。
for (const rel of [T_EXP, T_IMP]) {
  // 只认「请求配置对象里的那一处」：注释里写「timeout: 0 —— 本地直连…」也算的话，改真值时会假绿（已踩）
  check(/,\s*\{\s*timeout:\s*0\s*\}\s*\)/.test(tSrc[rel]),
    rel + ' 的请求必须带 { timeout: 0 } —— 本地直连不设 HTTP 超时，卡在 axios 默认的 15 秒没有意义')
  check(/window\.MySQLTransferUtil/.test(tSrc[rel]),
    rel + ' 未复用 window.MySQLTransferUtil（导出/导入各写一份格式化与推断逻辑必然漂移）')
}
check(/payload\(false,\s*''\)/.test(tSrc[T_IMP]), '导入必须先发一次未确认请求（payload(false, ...)）拿待确认信息与文件大小')
check(/payload\(true,\s*key\)/.test(tSrc[T_IMP]), '导入的正式请求必须带 confirmed=true（payload(true, key)）')
check(/need_confirm/.test(tSrc[T_IMP]), '导入必须处理服务端返回的 need_confirm，否则两段式确认形同虚设')

// 15d. SQL 转储的「去掉库名」与导入的目标库下拉（2026-10-14 用户需求）。
//      口径：查询照原样带库执行；strip 开了之后 INSERT 只写表名——dump 落到哪个库都能导，
//      目标库在导入时用下拉选（清单拉不到就手输，绝不拦导入）。
check(expData.stripSchema === false, '导出「去掉库名」默认必须为关（默认行为不变，改动要显式）')
check(/v-model="stripSchema"/.test(tSrc[T_EXP]) && tSrc[T_EXP].indexOf('去掉库名') >= 0,
  '导出：对话框要有「去掉库名」开关（只影响 INSERT 目标表名，不影响查询本身）')
check(/strip_schema:\s*this\.stripSchema/.test(tSrc[T_EXP]),
  '导出：开关要随请求带给后端（strip_schema）')
check(/\/mysql\/' \+ this\.connId \+ '\/databases'/.test(tSrc[T_IMP]),
  '导入：目标库下拉要打 /mysql/:id/databases 拉库清单')
check(tSrc[T_IMP].indexOf('allow-create') >= 0,
  '导入：库下拉要允许手输（清单拉不到时退化为手输，不能拦导入）')

// 16. Redis 工具（key 浏览器）：首次上锁。
//     三条是安全边界，不是外观偏好：
//       ① 匹配串不得为空——空串拼出来的 `*` 就是 KEYS *，会阻塞整个实例（后端也会拦，这里拦的是「按钮看起来可点」）；
//       ② 「展示全部」必须先有匹配，否则它只是一个换了名字的 KEYS *；
//       ③ 请求不设 HTTP 超时——「展示全部」要连续推进很多轮 SCAN，卡在 axios 默认的 15 秒没有意义。
//     后端侧的对应门禁是 api/tool/redis 的 TestOnlyReadCommands（只读命令白名单）。
const R_UTIL = '/static/pages/redis_util.js'
const R_VAL = '/static/pages/redis_value.js'
const R_EXP = '/static/pages/redis_explorer.js'
const R_PAGE = '/static/pages/redis.js'
for (const p of [R_UTIL, R_VAL, R_EXP, R_PAGE]) {
  check(tOrder.indexOf(p) >= 0, 'index.html 未引入 ' + p)
}
check(tOrder.indexOf(R_UTIL) < tOrder.indexOf(R_VAL) && tOrder.indexOf(R_VAL) < tOrder.indexOf(R_EXP)
  && tOrder.indexOf(R_EXP) < tOrder.indexOf(R_PAGE),
  'Redis 脚本顺序错误：必须 redis_util → redis_value → redis_explorer → redis.js'
  + '（explorer 的 components 在对象字面量求值时就要取到 window.RedisValue）')

for (const g of ['RedisUtil', 'RedisValue', 'RedisExplorer', 'RedisPage']) {
  check(!!sandbox[g], 'window.' + g + ' 缺失')
}
checkTransferTemplate('RedisExplorer', sandbox.RedisExplorer)
checkTransferTemplate('RedisValue', sandbox.RedisValue)
checkTransferTemplate('RedisPage', sandbox.RedisPage)

const rSrc = {}
for (const rel of [R_UTIL, R_VAL, R_EXP, R_PAGE]) rSrc[rel] = ((sources.find(s => s[0] === rel)) || [])[1] || ''

// 16a. 空匹配串必须被拦（前端挡在按钮与入口，后端再挡一次）
check(/ElMessage\.warning\([^)]*要匹配的开头内容/.test(rSrc[R_EXP]),
  'redis_explorer.js 的 search 必须显式拒绝空匹配串（留空等价于 KEYS *）')
check(/:disabled="!prefix\.trim\(\)"/.test(rSrc[R_EXP]),
  '「查询」按钮须以 prefix.trim() 为可用条件，空串时不得可点')
check(/\bprefix\.trim\(\)/.test(rSrc[R_EXP]),
  'redis_explorer.js 必须对匹配串做 trim 判定（只有空白字符也算空）')
// 匹配语义是「前缀」：输入 ops: 要下发 ops:*，请求字段名也必须是 prefix
check(/前缀匹配/.test(rSrc[R_EXP]) && !/结尾匹配/.test(rSrc[R_EXP]),
  'redis_explorer.js 的匹配语义应为前缀匹配（输入开头内容，下发 前缀*），不得残留结尾匹配')
check(/\bprefix:\s*s\b/.test(rSrc[R_EXP]),
  'redis_explorer.js 的请求体必须用 prefix 字段（后端 scanReq.Prefix 按前缀拼 MATCH）')

// 16b. 「展示全部」必须先有匹配结果。
//      行为断言，不是文本断言：只读 canAll 的源码文本会假绿——
//      同一文件里 `if (this.found > 0) return '本页没有内容'` 就能满足 `/found > 0/`
//      （已踩），删掉 canAll 里的条件门禁依然全绿。真正要锁的是「无命中时按钮不可用」。
const canAllFn = sandbox.RedisExplorer && sandbox.RedisExplorer.computed
  ? sandbox.RedisExplorer.computed.canAll : null
check(typeof canAllFn === 'function', 'RedisExplorer.computed.canAll 缺失')
if (typeof canAllFn === 'function') {
  const ctx = (o) => Object.assign({
    scanId: 'sc_1', hasMore: true, found: 1, loading: false,
  }, o || {})
  check(canAllFn.call(ctx({ found: 1 })) === true, '有匹配且仍有下一页时「展示全部」应可用')
  check(canAllFn.call(ctx({ found: 0 })) === false,
    'found = 0 时「展示全部」必须不可用 —— 没有命中时它只是一个换了名字的 KEYS *')
  check(canAllFn.call(ctx({ scanId: '' })) === false, '没有会话时「展示全部」必须不可用')
  check(canAllFn.call(ctx({ loading: true })) === false, '扫描进行中「展示全部」必须不可用')
  check(canAllFn.call(ctx({ hasMore: false, done: true })) === false,
    '扫完之后「展示全部」必须不可用（后端此时给 limit_hit，前端应提示把匹配串写得更具体）')
}

// 16c. 请求契约
check(/,\s*\{\s*timeout:\s*0\s*\}\s*\)/.test(rSrc[R_EXP]),
  'redis_explorer.js 的请求必须带 { timeout: 0 } —— 「展示全部」要连续推进多轮 SCAN，'
  + '卡在 axios 默认的 15 秒没有意义')
check(/window\.RedisUtil/.test(rSrc[R_EXP]) && /window\.RedisUtil/.test(rSrc[R_VAL]),
  'key 浏览器与值预览都必须复用 window.RedisUtil（TTL 文案与类型徽标各写一套必然漂移）')

// 16d. 只读：前端页面不得出现任何写命令字样（后端命令白名单见 TestOnlyReadCommands）
for (const rel of [R_VAL, R_EXP, R_PAGE]) {
  check(!/'(?:DEL|SET|LPUSH|RPUSH|SADD|HSET|ZADD|FLUSHDB|FLUSHALL|EXPIRE|RENAME|CONFIG)'/.test(rSrc[rel]),
    rel + ' 出现写命令字样：本工具承诺只读')
}

// 17. 前端 API 路径 vs 后端路由注册（首次上锁）。
//     起因是真实 404：AI 接入配置上提为平台设置后，router.go 里 /api/mysql/ai/config 已摘，
//     但 pages/mysql_ai.js 还在打它 —— 面板永远停在「未启用」，看起来像开关打不开。
//     静态交叉核对是能挡住这类漂移的最便宜手段（跑一次 404 不必等真机点一遍）。
//
//     口径说明（都是刻意选的宽松边界，宁可漏报不可误报）：
//     · 只取字面量路径的**前两段**（如 /mysql/5/ping → /mysql）作为匹配键，
//       因为 :id 是运行期值，无法静态展开；
//     · 路由分组名 → 前缀的映射只列工具类几组（router.go 里 `protected.Group("/xxx")` 形式固定）；
//     · 路径含模板插值（`/mysql/' + id + '/mode`）时只取首段静态部分。
const ROUTER = path.join(ROOT, 'router', 'router.go')
check(fs.existsSync(ROUTER), 'router/router.go 不存在，无法做 API 路径交叉核对')
if (fs.existsSync(ROUTER)) {
  const rsrc = fs.readFileSync(ROUTER, 'utf8')
  // 分组变量 → 路径前缀：**从 router.go 里解析 Group 定义本身**，不手工维护映射表
  // （手工表会漂：加一组路由时忘了同步映射，这条门禁就静默漏报，比没有更坏）。
  const GROUP_PREFIX = {}
  for (const m of rsrc.matchAll(/(\w+)\s*:?=\s*(?:protected|r)\.Group\("([^"]*)"\)/g)) {
    GROUP_PREFIX[m[1]] = m[2]
  }
  // protected = r.Group("/api")：它的「前缀」是 baseURL 本身，前端字面量里不会出现 /api，
  // 故映射成空串，让 protected.X("/a/b") 产出 "/a/b"（与第二段直挂那条同基准）。
  if (GROUP_PREFIX.protected === '/api') GROUP_PREFIX.protected = ''
  check(Object.keys(GROUP_PREFIX).length >= 8,
    '未能从 router.go 解析出路由分组（regex 可能已失效）——交叉核对形同虚设')

  const registered = {}     // verb -> Set(path)：axios 基准（两边都不含 /api）
  const registeredFull = {} // verb -> Set(path)：桥直连基准（两边都含 /api）
  const addRoute = (o, verb, p) => {
    if (!o[verb]) o[verb] = new Set()
    o[verb].add(p)
  }
  for (const m of rsrc.matchAll(/(\w+)\.(GET|POST|PUT|DELETE)\("([^"]*)"/g)) {
    const prefix = GROUP_PREFIX[m[1]]
    if (!prefix) continue
    addRoute(registered, m[2], prefix + m[3])
    addRoute(registeredFull, m[2], '/api' + prefix + m[3])
  }
  // 直接挂在 protected 上的（无分组）：前缀就是 /api。
  // 存进 base 集时**去掉 /api** —— 前端 api 字面量是相对 axios baseURL('/api') 的，
  // '/deploy/run' 对应的就是 protected.GET("/api/deploy/run")，两边要在同一基准上比；
  // 桥直连集则相反：__INFRA_SSE__.post 的 rawURL 直接派发进 Gin 引擎（无 baseURL），必须自带 /api。
  for (const m of rsrc.matchAll(/protected\.(GET|POST|PUT|DELETE)\("([^"]*)"/g)) {
    addRoute(registered, m[1], m[2])
    addRoute(registeredFull, m[1], '/api' + m[2])
  }
  check(Object.keys(registered).length >= 4,
    '未能从 router.go 解析出任何路由（分组解析可能已失效）——交叉核对形同虚设')

  // 扫全部前端脚本里的 api.<verb>('<path>')
  // 注意：api 的 baseURL 是 '/api'，所以这里的字面量路径本身就是相对 /api 的，
  //       后端直挂 protected 的 /api/deploy/run 同样对应前端字面量 '/deploy/run'。
  const apiCall = /\bapi\.(get|post|put|delete)\(\s*'([^']+)'/g
  let scanned = 0
  const miss = []
  // ⛔ 匹配口径（base 集与桥直连集共用）：只认两种情形 ——
  //   ① 完整字面量逐段相等（末段可为 :param，对应前端拼了运行期 id 的写法）；
  //   ② 字面量整体是某条注册路径的**末段截断**，且注册路径尾部全是 :param。
  // 绝不做「逐段回退 + 前缀相等」的宽松匹配：那样 /mysql/ai/config 会被同前缀的
  // /mysql/ai/logs 兜住，明明后端没这条却报全绿（已踩，假绿两轮才发现）。
  const matchRoute = (segs, cands) => {
    const eqPath = (p) => {
      const ps = p.split('/').filter(Boolean)
      if (ps.length !== segs.length) return false
      for (let i = 0; i < ps.length; i++) {
        if (i === ps.length - 1 && ps[i].startsWith(':')) continue
        if (ps[i] !== segs[i]) return false
      }
      return true
    }
    if ([...cands].some(eqPath)) return true
    return [...cands].some(p => {
      const ps = p.split('/').filter(Boolean)
      if (ps.length <= segs.length) return false
      for (let i = 0; i < segs.length; i++) if (ps[i] !== segs[i]) return false
      return ps.slice(segs.length).every(s => s.startsWith(':'))
    })
  }
  for (const [rel, code] of sources) {
    for (const m of code.matchAll(apiCall)) {
      const verb = m[1].toUpperCase()
      const raw = m[2]
      if (!raw.startsWith('/')) continue
      // 尾斜杠归一：前端 '/deploy/tasks/' 与后端 '/api/deploy/tasks' 是同一条
      const path = raw.replace(/\/+$/, '') || '/'
      const segs = path.split('/').filter(Boolean)
      if (!segs.length) continue
      const cands = registered[verb] || new Set()
      let hit = matchRoute(segs, cands)
      // 已知的静态核对盲区：路径在下一段才拼运行期值（'/stacks/instances/' + id + '/scale-out'），
      // 正则只能抓到静态那一段，深度必然不够。这类一律放行，否则误报会淹没真问题。
      const DYNAMIC_TAIL = ['/stacks/instances/', '/stacks/instances']
      if (!hit && DYNAMIC_TAIL.includes(path)) hit = true
      scanned++
      if (!hit) miss.push(rel + ' → api.' + m[1] + "('" + raw + "')")
    }
    // this.base() + '/xxx'（es 工具页风格，base 运行期才是 '/es/:id'）：
    // 上面的 apiCall 正则抓不到（首参不是字面量），曾整个从核对里漏掉。
    // 口径：字符串字面量必须是某条**同动词**已注册路径的连续末段
    // （base 贡献的 '/es/:id' 无法定态展开，只能按末段对账）。
    for (const m of code.matchAll(/\bapi\.(get|post|put|delete)\(\s*this\.base\(\)\s*\+\s*'([^']+)'/g)) {
      const verb = m[1].toUpperCase()
      const raw = m[2]
      if (!raw.startsWith('/')) continue
      const segs = raw.replace(/\/+$/, '').split('/').filter(Boolean)
      if (!segs.length) continue
      scanned++
      const cands = registered[verb] || new Set()
      const suffixHit = [...cands].some(p => {
        const ps = p.split('/').filter(Boolean)
        if (ps.length <= segs.length) return false
        const tail = ps.slice(ps.length - segs.length)
        return tail.every((s, i) => s === segs[i])
      })
      if (!suffixHit) miss.push(rel + " → api." + verb + "(this.base() + '" + raw + "')（后端未注册该末段路径）")
    }
    // 桥直连调用（流式）：__INFRA_SSE__.post / sse.post 的 rawURL 直接派发进 Gin 引擎，
    // 没有 axios baseURL('/api') 兜底 ⇒ 字面量必须自带 /api 前缀，且按全前缀集比对。
    // 真实事故（2026-10-13）：漏前缀 →「接口不存在: POST /metrics/conns/2/ai/promql」弹到 AI 面板。
    for (const m of code.matchAll(/(?:\bsse|__INFRA_SSE__)\.post\(\s*'([^']+)'/g)) {
      const raw = m[1]
      scanned++
      if (!raw.startsWith('/api/')) {
        miss.push(rel + " → sse.post('" + raw + "')（桥直连无 baseURL，路径必须自带 /api 前缀）")
        continue
      }
      const segs = raw.replace(/\/+$/, '').split('/').filter(Boolean)
      if (!matchRoute(segs, registeredFull['POST'] || new Set())) {
        miss.push(rel + " → sse.post('" + raw + "')（后端未注册）")
      }
    }
  }
  check(miss.length === 0,
    '前端调用了后端未注册的路径（真机表现为 404）：\n      ' + miss.join('\n      '))
  if (miss.length === 0) console.log(`  API 路径交叉核对：${scanned} 处调用全部命中后端注册`)
}

// 18. 统一错误出口（error_notice.js）：桌面模式打不开 F12（Windows 上 Wails 关掉浏览器加速键，
//     见 desktop/devtools.go），console 与 DevTools 都不是可靠通道 ⇒ 错误必须自己跳到界面上。
//     真实踩过的两个洞：
//       ① 404 时前端只能显示 axios 造的 `Request failed with status code 404`，
//          看不出是哪条路径对不上——后端 NoRoute 也没给 body，同理什么信息都没有；
//       ② 3 秒 toast 会错过，且未捕获的 JS 异常 / Promise 拒绝原先完全无出口，
//          界面表现是「点了没反应」。
//     故锁三件事：error_notice.js 存在且早于 app.js 装载、app.js 拦截器真的走它、
//     describeApiError 对四类错误都给出可定位的描述。
const E_NOTICE = '/static/error_notice.js'
check(tOrder.indexOf(E_NOTICE) >= 0, 'index.html 未引入 ' + E_NOTICE)
check(tOrder.indexOf('/static/app.js') > tOrder.indexOf(E_NOTICE),
  E_NOTICE + ' 必须早于 app.js 装载（拦截器要用 window.ErrorNotice）')
const EN = sandbox.ErrorNotice
check(!!EN, 'window.ErrorNotice 缺失（错误通知出口未装载，桌面模式将无任何报错可见）')
check(/notifyApiError\(err\)/.test(fs.readFileSync(path.join(ROOT, 'template/static/app.js'), 'utf8')),
  'app.js 的 axios 拦截器必须调用 window.ErrorNotice.notifyApiError(err)'
  + '——退回 3 秒 ElMessage 等于桌面模式下什么都没发生')
if (EN) {
  const D = EN.describeApiError
  check(typeof D === 'function', 'ErrorNotice.describeApiError 缺失（纯函数，门禁要直接调它）')
  check(typeof EN.notifyApiError === 'function' && typeof EN.attachGlobalHooks === 'function',
    'ErrorNotice 需提供 notifyApiError 与 attachGlobalHooks')

  // 行为断言：构造四类典型错误，直接调 describeApiError 看输出
  if (typeof D === 'function') {
    const withResp = (status, data, url, method) => D({
      config: { url: url || '/redis', method: method || 'get' },
      response: { status: status, data: data }
    })
    // ① 后端有 message：原样带上，并附方法与路径
    const r1 = withResp(409, { code: 409, message: '同名连接已存在' }, '/redis', 'post')
    check(r1.kind === 'http' && r1.status === 409, 'describeApiError: HTTP 错误的 kind/status 不对: ' + JSON.stringify(r1))
    check(/同名连接已存在/.test(r1.text), 'describeApiError: 必须保留后端 message')
    check(/POST \/redis/.test(r1.text), 'describeApiError: 必须给出「方法 + 路径」，否则 404 看不出是哪条接口')
    // ② 404 且后端无 body：给出的是「后端自己的话」而不是 axios 那句 status code
    const r2 = withResp(404, '', '/mysql/ai/config', 'get')
    check(/接口不存在/.test(r2.lines.join('\n')),
      'describeApiError: 404 无 body 时必须给出可读原因（当前: ' + r2.lines.join('|') + '）')
    check(!/^Request failed/.test(r2.title),
      'describeApiError: 404 标题不应只显示 axios 的 status code（当前: ' + r2.title + '）')
    // ③ 超时与网络不通要能被区分开，二者提示语不同（前者建议查 timeout: 0）
    const r3 = D({ config: { url: '/redis/1/keys', method: 'post' }, code: 'ECONNABORTED', message: 'timeout of 15000ms exceeded' })
    check(r3.kind === 'timeout' && /timeout:\s*0/.test(r3.lines.join('\n')),
      'describeApiError: 超时须判为 timeout 并提示「请求需带 timeout: 0」（导出/导入类接口的真实坑）')
    const r4 = D({ config: { url: '/redis', method: 'get' }, message: 'Network Error' })
    check(r4.kind === 'network', 'describeApiError: 无 response 且非超时应判为 network，当前: ' + r4.kind)
    // ④ 空输入不崩（全局钩子会把任意 reason 丢进来）
    check(!!D(null) && !!D({}).text, 'describeApiError: 传 null/空对象不得抛异常')
  }

  // 通知行为：必须 duration:0（不自动消失）且超过上限时关掉最早的一条。
  // 沙箱里 ElementPlus 是空对象，故临时挂一个记录用的假 ElNotification。
  const fakeInsts = []
  sandbox.ElementPlus.ElNotification = (opts) => {
    const inst = { opts: opts, closed: false, close() { this.closed = true } }
    fakeInsts.push(inst)
    return inst
  }
  EN.notify({ title: 'T', lines: ['L'] })
  check(fakeInsts.length === 1 && fakeInsts[0].opts.duration === 0,
    'ErrorNotice.notify 的通知必须 duration: 0（桌面无 DevTools，自动消失等于没报错）')
  check(fakeInsts[0].opts.showClose === true, 'ErrorNotice.notify 的通知必须可手动关闭（showClose）')
  const fake = D({ config: { url: '/x', method: 'get' }, response: { status: 500, data: { message: 'boom' } } })
  for (let i = 0; i < EN.MAX_LIVE + 3; i++) EN.notifyApiError(fake)
  check(fakeInsts.length === EN.MAX_LIVE + 4, 'notify 每次都应新建一条通知实例')
  check(fakeInsts[1].closed === true,
    '超过 MAX_LIVE 后应关闭最早的一条（批量请求全失败时不能糊满整屏）')
  check(fakeInsts[fakeInsts.length - 1].closed === false, '最新一条通知不应被立即关闭')

  // 浏览器噪音过滤：ResizeObserver 循环报错是 Chrome 一帧内未送达的布局通知（下一帧自动补送），
  // 无实际影响也修不了——sftp 页实测被全局钩子弹成「前端脚本异常」，只会吓到用户。
  // 钩子级行为断言：先打桩捕获真实监听器，再分别喂噪音与真异常，看 liveCount 账本。
  check(typeof EN.isBenignNoise === 'function', 'ErrorNotice.isBenignNoise 缺失（浏览器布局噪音过滤）')
  check(EN.isBenignNoise('ResizeObserver loop completed with undelivered notifications.')
    && EN.isBenignNoise('ResizeObserver loop limit exceeded'),
    'isBenignNoise 必须识别 ResizeObserver loop 的新旧两版措辞')
  check(!EN.isBenignNoise("Cannot read properties of undefined (reading 'id')")
    && !EN.isBenignNoise('ResizeObserver is not defined'),
    'isBenignNoise 不得误伤真实异常（如 ResizeObserver 本身缺失）')
  const hooks = {}
  sandbox.addEventListener = (type, fn) => { hooks[type] = fn }
  EN.attachGlobalHooks()
  check(typeof hooks.error === 'function' && typeof hooks.unhandledrejection === 'function',
    'attachGlobalHooks 必须注册 window error 与 unhandledrejection 两个钩子')
  // 注意：liveCount 是滚动窗口（恒 ≤ MAX_LIVE），弹没弹要看实例创建数 fakeInsts.length 的增量
  const baseN = fakeInsts.length
  hooks.error({ message: 'ResizeObserver loop completed with undelivered notifications.',
    filename: '/static/pages/sftp.js', lineno: 1 })
  check(fakeInsts.length === baseN, 'window error 收到 ResizeObserver 噪音不得弹通知')
  hooks.error({ message: 'Uncaught TypeError: x is not a function',
    filename: '/static/pages/sftp.js', lineno: 9 })
  check(fakeInsts.length === baseN + 1, 'window error 收到真实异常必须弹通知（过滤器不得扩大化）')
  hooks.unhandledrejection({ reason: new Error('ResizeObserver loop completed with undelivered notifications.') })
  check(fakeInsts.length === baseN + 1, 'unhandledrejection 收到 ResizeObserver 噪音同样不得弹')
  delete sandbox.ElementPlus.ElNotification
}

// 19. AI 输出高亮（ai_highlight.js）：AI 解读的结论要能一眼看到重点（数值 / 异常 / 正常 / 标识符）。
//     它是唯一一处「把模型返回的文本拼成 HTML 再 v-html」的地方，所以转义必须由它自己做——
//     模型原样带 < > & 时不能变成标签。断言全部走行为（分类 / 否定词优先 / 原文不丢字），
//     不比对字面量字符串（字面量断言在改词表时会假红，在改逻辑时会假绿）。
const AI_HL = '/static/ai_highlight.js'
check(tOrder.indexOf(AI_HL) >= 0, 'index.html 未引入 ' + AI_HL)
const HL = sandbox.AiHl
check(!!HL && typeof HL.html === 'function', 'window.AiHl.html 缺失（AI 面板的着色入口）')
if (HL && typeof HL.html === 'function') {
  const strip = (h) => String(h).replace(/<[^>]+>/g, '')
    .replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&quot;/g, '"').replace(/&amp;/g, '&')
  const tags = (h) => [...String(h).matchAll(/class="ai-hl ai-hl-([a-z]+)"/g)].map(m => m[1])

  // ① 转义优先：原文里的标签必须变实体，数值识别照旧
  const xss = HL.html('<img src=x onerror=alert(1)> 占比 5%')
  check(xss.indexOf('<img') < 0, 'AiHl.html 未转义输入里的 HTML 标签（v-html 注入面）: ' + xss)
  check(tags(xss).indexOf('num') >= 0, 'AiHl.html 未识别带 % 的数值: ' + xss)

  // ② 三类关键词分类：红线词 / 注意词 / 正常词各归各位
  check(tags(HL.html('内存持续升高，已出现 OOM')).indexOf('bad') >= 0, 'AI 高亮未把 OOM 这类风险词标为 bad')
  check(tags(HL.html('该节点负载偏高，注意观察')).indexOf('warn') >= 0, 'AI 高亮未把「偏高」标为 warn')
  check(tags(HL.html('全集群运行平稳')).indexOf('good') >= 0, 'AI 高亮未把「平稳」标为 good')
  // ③ 否定词优先：不能把「无异常」里的「异常」标成红（这是字面匹配最容易出的假红）
  check(tags(HL.html('各序列均无异常')).join(',') === 'good',
    'AiHl 必须让「无异常」整体命中 good，而不是把其中的「异常」标成 bad: ' + HL.html('各序列均无异常'))
  check(tags(HL.html('两者无不一致项')).every(t => t === 'good'),
    'AiHl 必须让「无不一致」整体命中 good: ' + HL.html('两者无不一致项'))
  check(tags(HL.html('两者不一致')).join(',') === 'warn',
    'AiHl 必须把单独出现的「不一致」标为 warn: ' + HL.html('两者不一致'))

  // ④ 标识符与标签选择器（PromQL 原文在结论里必须一眼可辨）
  const codeHtml = HL.html('建议执行 sum by (container) (container_memory_usage{namespace="scfq-service"})')
  check(tags(codeHtml).indexOf('code') >= 0, 'AiHl 未把 PromQL 标识符标为 code: ' + codeHtml)
  check(strip(codeHtml).indexOf('namespace="scfq-service"') >= 0, 'AiHl 丢了标签选择器原文: ' + codeHtml)

  // ④b 实体名不能被数字规则切碎：mall-order-7f9c 必须整体是一个片段。
  //     这类名字在监控解读里到处都是（容器 / 服务 / 主机名），切碎后满屏碎片数字比不染色还难读。
  const nameHtml = HL.html('mall-order-7f9c 峰值 1.86 GB')
  check(tags(nameHtml).indexOf('name') >= 0, 'AiHl 未把容器名这类实体名标为 name: ' + nameHtml)
  check(nameHtml.indexOf('mall-order-7f9c') >= 0,
    'AiHl 把实体名切碎了（名字里的数字被当成独立数值）: ' + nameHtml)
  // ④c 时间点整体识别，不能拆成「14」+「20」
  check(/<span class="ai-hl ai-hl-num">14:20<\/span>/.test(HL.html('峰值出现在 14:20 左右')),
    'AiHl 应把时间点整体当作一个数值片段: ' + HL.html('峰值出现在 14:20 左右'))

  // ⑤ 原文一字不丢：剥掉标签后必须完全等于输入（tokenizer 最容易在这里吞字符）
  const samples = [
    '过去 1 小时 3 条序列全部无异常，峰值 1.2 GB，均值 340 MB',
    'instance="10.0.0.1:9100" 的 CPU 使用率偏高（92.5%），建议关注',
    '磁盘写入速率 4.5 MB/s，较昨日增长 30%，未出现 OOM',
    '`rate(http_requests_total[5m])` 全为 0'
  ]
  for (const s of samples) {
    check(strip(HL.html(s)) === s, 'AiHl 改变了原文（剥标签后不一致）: ' + JSON.stringify(strip(HL.html(s))) + ' != ' + JSON.stringify(s))
  }
  // ⑥ 空输入不崩（字段可能缺失）
  check(HL.html('') === '' && HL.html(null) === '' && HL.html(undefined) === '',
    'AiHl.html 对空输入必须返回空串且不抛异常')
}

// ⑦ 解读面板接线：三段结论走 hl 渲染，且不再有已下线的「详细分析」
const ex = sandbox.MetricsAIExplain
const exTpl = String((ex && ex.template) || '')
check(!!ex && typeof (ex.methods || {}).hl === 'function', 'MetricsAIExplain 必须有 hl 方法（走 AiHl.html）')
check(/v-html="hl\(f\)"/.test(exTpl) && /v-html="hl\(s\)"/.test(exTpl),
  '解读面板的观察发现 / 排查建议必须经 hl 着色渲染')
check(exTpl.indexOf('result.detail') < 0, '解读面板不应再渲染 detail（详细分析已下线）')

// ⑦b 解读改弹框（用户口径）：结果区按钮两态、工作台不再有内联卡片。
//     「首次执行自动弹出 / 关闭后仍能重看 / 查看不重跑 / 重新分析覆盖」四条行为在 ⑫ 里实测。
const wbTpl = String((sandbox.MetricsWorkbench && sandbox.MetricsWorkbench.template) || '')
check(exTpl.indexOf('el-dialog') >= 0 && exTpl.indexOf('>关闭<') >= 0 && exTpl.indexOf('>重新分析<') >= 0,
  '解读结论必须以弹框呈现，且自带「关闭 / 重新分析」两个动作')
check(wbTpl.indexOf('查看解读') >= 0 && wbTpl.indexOf('重新分析') >= 0 && wbTpl.indexOf('AI 解读') >= 0,
  '结果区必须给出「AI 解读 / 查看解读 / 重新分析」三个入口（关闭后能重看、能重生成）')
check(wbTpl.indexOf('aiPanel') < 0,
  '工作台不应再有内联解读卡片（aiPanel 引用）——解读已全部搬进弹框')
check(/@state="onExplainState"/.test(wbTpl),
  '工作台必须监听解读组件的 state 事件（按钮两态靠它同步）')
check(/@click="viewExplain"/.test(wbTpl),
  '「查看解读」必须走 viewExplain（只重开弹框）——接成 explain 就变成每次查看都重跑一遍')
// 「查看解读」在**生成期间也必须可点**：弹框关掉后还能点开看流式进度。
// 早前一版它跟着 busy 一起禁用 —— 生成中一旦关了弹框就只能干等（用户口径：解析中也要支持点击查看弹框）。
// 锚点选在 viewExplain 这个绑定所在的那一个标签上（该绑定唯一），不是裸文本。
const viewBtn = (wbTpl.match(/<el-button[^>]*@click="viewExplain"[^>]*>/) || [''])[0]
check(!!viewBtn && viewBtn.indexOf(':disabled') < 0,
  '「查看解读」不能在生成期间被禁用（否则生成中关掉弹框就只能干等）: ' + JSON.stringify(viewBtn))

// ⑧ 追加生成只给一次（用户口径）：默认只有一个生成按钮，生成过一轮才出现「追加生成」，
//    追加成功即丢弃上下文让按钮消失；因此界面上不该再有「重新开始」这种清理入口。
const genTpl = String((sandbox.MetricsAIGen && sandbox.MetricsAIGen.template) || '')
check(genTpl.indexOf('重新开始') < 0, '生成面板不应再有「重新开始」按钮（追加用完自然回到初始态）')
check(/v-if="prev"[^>]*@click="generate\(true\)"/.test(genTpl),
  '「追加生成」必须只在 prev 存在（即已生成过一轮）时出现')
const myTpl = String((sandbox.MySQLAI && sandbox.MySQLAI.template) || '')
check(myTpl.indexOf('重新开始') < 0, 'MySQL 生成面板不应再有「重新开始」按钮')
check(/v-if="prev"[^>]*@click="generate\(true\)"/.test(myTpl),
  'MySQL「追加生成」必须只在 prev 存在时出现')
// 主按钮文案必须具体到产物（生成 PromQL / 生成 SQL），与面板标题的 kind 对齐——
// 标题已经写了「AI 生成 X」，按钮再泛化成「AI 生成」就看不出产出什么（用户口径：两侧统一）。
const mainLabel = (tpl) => {
  const m = /:loading="loading && !loadingFollowup"[^>]*>([^<]+)</.exec(tpl)
  return m ? m[1].trim() : ''
}
check(mainLabel(genTpl) === '生成 PromQL',
  '监控生成面板主按钮文案必须是「生成 PromQL」，实际: ' + mainLabel(genTpl))
check(mainLabel(myTpl) === '生成 SQL',
  'MySQL 生成面板主按钮文案必须是「生成 SQL」，实际: ' + mainLabel(myTpl))

// ⑩ ES · Grok 解析面板：页签注册 + 面板接线。tabs / components 直接取行为结果（调 computed），
//    不是扫文本 —— 扫文本挡不住「页签加了但组件没注册」这种半拉子接线。
const esTabs = (sandbox.ESPage && sandbox.ESPage.computed && sandbox.ESPage.computed.tabs)
  ? sandbox.ESPage.computed.tabs.call({}) : []
check(esTabs.some(t => t.name === 'grok'), 'ES 主页面的页签里必须有 grok（Grok 解析）')
check(!!((sandbox.ESPage || {}).components || {})['es-grok-tab'],
  'ES 主页面必须注册 es-grok-tab 组件（只加页签不注册组件 = 白屏）')
const grokTpl = String((sandbox.EsGrokTab && sandbox.EsGrokTab.template) || '')
check(!!sandbox.EsGrokTab && /@click="parse"/.test(grokTpl) && /@click="aiGenerate\(false\)"/.test(grokTpl),
  'Grok 面板必须有「解析」与「AI 生成」两个动作')
check(/v-if="aiPrev"[^>]*@click="aiGenerate\(true\)"/.test(grokTpl),
  'Grok「追加生成」必须只在 aiPrev 存在（已生成过一轮）时出现')
check(/v-for="f in l\.fields"/.test(grokTpl),
  'Grok 结果必须逐字段列出（字段名 + 值），否则解析出来也看不出结构')

// ⑩b Logstash 配置区块：独立子组件。装载顺序必须锁住 —— 子组件在**定义时**取
//     window.EsGrokLogstash，es_grok.js 先执行就是组件注册成 undefined（点开页签白屏），
//     与 es_template_quick 那条是同一种坑（那里靠注释约定，这里直接进断言）。
const iLs = htmlSrc.indexOf('/static/pages/es_grok_logstash.js')
const iGrok = htmlSrc.indexOf('/static/pages/es_grok.js')
check(iLs >= 0 && iGrok >= 0 && iLs < iGrok,
  'index.html 必须先引入 es_grok_logstash.js，再引入 es_grok.js（子组件定义时就要求 window.EsGrokLogstash 已存在）')
check(!!sandbox.EsGrokLogstash, 'es_grok_logstash.js 必须定义 window.EsGrokLogstash')
check(!!((sandbox.EsGrokTab || {}).components || {})['es-grok-logstash'],
  'Grok 面板必须注册 es-grok-logstash 组件（只写标签不注册 = 白屏）')
check(/<es-grok-logstash\s/.test(grokTpl), 'Grok 面板必须挂载 <es-grok-logstash>（否则这个功能根本没入口）')
const lsTpl = String((sandbox.EsGrokLogstash && sandbox.EsGrokLogstash.template) || '')
check(/v-model="tag"/.test(lsTpl) && /@click="generate"/.test(lsTpl),
  'Logstash 配置区块必须有「匹配 tag」输入框与「生成配置」动作')
check(/@click="copy"/.test(lsTpl) && /{{ config }}/.test(lsTpl),
  'Logstash 配置区块必须把配置摊开展示并提供复制（不然生成出来拿不走）')

// ⑩c 数据导入：面板只剩「文件 + 索引 + 检查 + 预览 + 日志」；规则区已整块删除
//     （pattern 由「检查」自动生成、留在内存里），别再引回来。
const iImp = htmlSrc.indexOf('/static/pages/es_import.js')
check(iImp >= 0, 'index.html 必须引入 es_import.js')
check(htmlSrc.indexOf('es_import_rule.js') < 0,
  '导入规则区已删除：index.html 不该再引用 es_import_rule.js（pattern 由「检查」自动生成）')
const impTpl = String((sandbox.EsImportTab && sandbox.EsImportTab.template) || '')
check(/@click="pickFile"/.test(impTpl) && /@click="doAnalyze"/.test(impTpl),
  '导入面板必须有「选择文件」与「检查」两个动作（检查 = 手动触发分析）')
check(/v-if="analyzed"/.test(impTpl),
  '「预览」必须等「检查」出结果（analyzed）才出现 —— 没结果就没有可核实的东西')
check(/top="10vh"/.test(impTpl), '预览弹框上下各留 10%（top="10vh"）')
check(impTpl.indexOf('扫了') < 0 && !/es-imp-pv-rule/.test(impTpl) && impTpl.indexOf('被跳过的记录（最多显示') < 0,
  '预览弹框只留「左字段 + 右前 20 行」两栏：扫描统计 / 生效规则 / 跳过明细都不该再出现')
check(/es-imp-log-time/.test(impTpl) && /es-imp-log-mark/.test(impTpl),
  '检查日志要有「时间 + 状态标记」的日志样式（按步骤输出处理过程）')
check(/data-file-drop-target/.test(impTpl) && /id="es-imp-drop"/.test(impTpl),
  '导入面板要支持文件拖放（面板即投放目标 data-file-drop-target / id="es-imp-drop"）')
check(/日志格式/.test(impTpl) && /v-model="hint"/.test(impTpl) && /type="textarea"/.test(impTpl),
  '导入面板要有「日志格式」补充框（textarea，可留空）——不贴格式定义时 AI 只能凭空猜字段名')
check(impTpl.indexOf('用跳过样本重新生成 pattern') >= 0 && impTpl.indexOf('重新导入（删除索引重建）') >= 0,
  '导入：结果区要有补救入口（用跳过样本重新生成 / 删除索引重建）')
check(impTpl.indexOf('被跳过的记录示例') >= 0 && /skip_samples/.test(impTpl),
  '导入：进度弹框要展示被跳过的记录示例（供重新生成 pattern）')
check(impTpl.indexOf('删除索引重建导入') >= 0, '预览弹框要有「删除索引重建导入」入口（索引已存在时）')
check(impTpl.indexOf('索引计数核实') >= 0, '导入完成后要显示索引计数核实进度（每 5 秒刷新，最长 1 分钟）')
check(/label="系统生成字段"/.test(impTpl) && /label="映射字段"/.test(impTpl) && /v-model="renames\[row\.name\]"/.test(impTpl),
  '预览字段表要三列：系统生成字段 / 映射字段（空=不改，有值替换第一列）/ 类型')
check(impTpl.indexOf('跳过数量超过 20 条') >= 0 && /@click="pauseDecision\('continue'\)"/.test(impTpl)
  && /@click="pauseDecision\('refine'\)"/.test(impTpl),
  '跳过超阈值的暂停弹框要有「继续导入」与「重新生成并删除重建」两个决策')
check(/width="65%" top="15vh"/.test(impTpl),
  '暂停弹框要 65% 宽、上下各留 15%（20 条样例在框内滚动，不能撑出屏幕）')
check(/包内过滤/.test(impTpl) && /v-if="isArchive"/.test(impTpl) && /v-model="filter"/.test(impTpl),
  '「包内过滤」输入行只在选中压缩包时出现（v-if="isArchive" + v-model="filter"）')
check(impTpl.indexOf('importing || !sources.length') >= 0,
  '导入：「检查」按钮的可用性要跟 sources 走（写 !source 会永远禁用）')
check(impTpl.indexOf('pickFile') < impTpl.indexOf('es-imp-path'),
  '「选择文件」按钮要排在「数据文件」标签后面（路径显示之前）')
check(String((sandbox.EsImportTab.methods || {}).pickFile).indexOf('PickImportFiles') >= 0,
  '导入：选择文件要走多选对话框 PickImportFiles（一次可选多份）')
const shimSrc = ((sources.find(s => s[0].endsWith('static/wails-shim.js'))) || [])[1] || ''
check(shimSrc.indexOf('__INFRA_DROP__') >= 0 && shimSrc.indexOf('infra:files-dropped') >= 0,
  'wails-shim.js 要暴露 __INFRA_DROP__（订阅 infra:files-dropped 拿拖入文件的真实路径）')
check(!/<es-import-rule/.test(impTpl)
  && !('es-import-rule' in ((sandbox.EsImportTab || {}).components || {})),
  '导入面板不该再挂载/注册 es-import-rule（规则区已删除）')
check(/@click="doPreview"/.test(impTpl),
  '导入面板必须能先「预览」（字段类型要先看一眼再导，不然只能等报错）')
check(/@click="importFromPreview"/.test(impTpl),
  '「开始导入」要放在预览弹框里（先看抽样与字段类型，确认再导）')
check(/@click="cancel"/.test(impTpl), '导入面板在导入过程中要能给「取消」')
// 页签挂载：只定义组件、不挂进 ESPage 的 tabs 里 = 功能没有入口
const esPageTpl = String((sandbox.ESPage && sandbox.ESPage.template) || '')
check(/<es-import-tab/.test(esPageTpl),
  'es.js 的表单里必须挂载 <es-import-tab>（否则这个功能没有入口）')
// _id 字段已砍掉：文档 id 由 ES 自动生成，别再加回界面或请求体
check(impTpl.indexOf('idField') < 0 && impTpl.indexOf('id_field') < 0,
  '导入：_id 字段已去掉（文档 id 由 ES 自动生成），模板里不该再出现 idField/id_field')
// 预览是左右布局弹框：左原始数据 / 右切分结果，对照着看才知道规则切得对不对
check(/v-model="showPreview"/.test(impTpl) && /es-imp-pv-col/.test(impTpl),
  '导入：预览要做成左右布局弹框（左原始数据 / 右切分结果），不是塞在面板里')
// 进度放弹框、关掉不中断：后台静默导入的两个必要条件 —— 有「查看导入进度」入口，
// 且关闭动作绝不挂 cancel（挂了就是一关窗就取消导入）
check(/v-model="showProgress"/.test(impTpl) && impTpl.indexOf('查看导入进度') >= 0,
  '导入：进度要放抽屉展示，关掉后能从「查看导入进度」回到后台导入')
check(/<el-drawer v-model="showProgress"/.test(impTpl) && /es-imp-filelist/.test(impTpl) && /<Loading \/>/.test(impTpl),
  '导入进度要改成抽屉 + 逐文件清单（完成打勾 / 当前转圈）')
check(impTpl.indexOf('@close="cancel"') < 0 && impTpl.indexOf('@closed="cancel"') < 0,
  '导入：关闭进度弹框绝不能触发取消（那是后台静默导入的前提）')
check(String((sandbox.EsImportTab.methods || {}).confirmImport).indexOf('es-imp-confirm') >= 0,
  '导入：确认框要加宽（默认 420px 装不下长路径，用户已经吐槽过）')
// 输入目标索引的即时校验：存在与否决定「追加还是新建」，等真跑才发现 already exists 太晚
check(impTpl.indexOf('索引已存在，数据将追加') >= 0,
  '导入：输入索引后要有「已存在，数据将追加」的即时提示位')
check(String((sandbox.EsImportTab.methods || {}).checkExists).indexOf('/import/exists') >= 0,
  '导入：即时校验要打 /import/exists 接口')
// 工作台把 el-tabs 自带页头 display:none，屏幕上的页签按钮全部来自 tabs 计算属性 ——
// 挂了 pane 漏挂导航项 = 用户永远点不到（2026-10-14「数据导入」实测踩过：pane 在、按钮不在）。
// 两个集合必须一一对应，少哪头都是不可达（导航多出来的 = 死按钮）。
const paneNames = []
const paneRe = /<el-tab-pane[^>]*\bname="([^"]+)"/g
let pm
while ((pm = paneRe.exec(esPageTpl))) paneNames.push(pm[1])
const navNames = sandbox.ESPage.computed.tabs.call({}).map(t => t.name)
check(paneNames.length > 0, 'ESPage 模板里必须存在 el-tab-pane（结构断了？）')
check(navNames.length === paneNames.length && paneNames.every(n => navNames.indexOf(n) >= 0)
  && navNames.every(n => paneNames.indexOf(n) >= 0),
  'es.js 的 tabs 计算属性必须与 el-tab-pane 一一对应（工作台藏了 el-tabs 页头，漏导航项 = 页签点不到）'
  + ': panes=[' + paneNames.join(',') + '] nav=[' + navNames.join(',') + ']')

// ⑪ 模板串健康检查：反引号模板串里的 \n 会在 JS 解析模板串时还原成**真实换行**。
//    换行本身不致命（stacks 表单里就有合法的多行表达式），致命的是它落进**字符串字面量内部**
//    —— 即文件里写 '...\n...'：还原后字符串被换行截断，Vue 编译报 Invalid or unexpected token
//    → 整页空白（实测踩过：ES 工作台一处 join('\n') 全白，而 node --check 与脚本求值都发现不了）。
//    判定必须在**文件文本**上做：解析后的字符串里分不清「作者写的多行」与「\n 还原出的换行」，
//    而文件里「单反斜杠 + n/t/r」只有一个来源 —— 作者想在字符串里换行（正确写法是 \\n）。
{
  const files = []
  const walk = (dir) => {
    for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
      const p = path.join(dir, e.name)
      if (e.isDirectory()) walk(p)
      else if (e.name.endsWith('.js')) files.push(p)
    }
  }
  walk(path.join(ROOT, 'template', 'static'))
  let tplCount = 0
  for (const fp of files) {
    const src = fs.readFileSync(fp, 'utf8')
    const rel = path.relative(ROOT, fp).split(path.sep).join('/')
    const re = /template:\s*`([\s\S]*?)`\s*,/g
    let m
    while ((m = re.exec(src))) {
      tplCount++
      const tpl = m[1]
      const line0 = src.slice(0, m.index).split('\n').length
      const parts = []
      for (const it of tpl.match(/{{[\s\S]*?}}/g) || []) parts.push(['插值', it])
      for (const at of tpl.match(/[A-Za-z0-9@:.-]+="[^"]*"/g) || []) parts.push(['属性值', at])
      for (const [kind, text] of parts) {
        // 单反斜杠 + n/t/r；\\n（正确写法）被 lookbehind 排除
        if (/(?<!\\)\\[ntr]/.test(text)) {
          check(false, `模板${kind}里写了 \\n 转义 ${rel}(模板块起于 L${line0}): ${JSON.stringify(text.slice(0, 70))}`
            + ' —— JS 会把它还原成真实换行并截断表达式（Vue 编译失败 = 页面空白），字符串换行请写 \\\\n')
        }
      }
    }
  }
  // 防假绿：抽取量骤降说明路径或正则失效，上面的循环会「零命中全通过」
  check(tplCount >= 20, `模板串抽取数量异常（仅 ${tplCount} 个，预期几十个）—— 本条检查已形同虚设`)
}

// ⑨ 行为断言（不是文本断言）：直接拿假上下文调组件的 generate，
//    锁死「首轮成功 → 出现可追加上下文；追加成功 → 立刻清空（按钮消失）；追加失败 → 保留（可重试）」。
//    这三条是用户口径的核心，读模板 regex 锁不住（清空动作在方法体里，写错了模板照样通过）。
//    组件方法走全局 api，沙箱本没有它（app.js 被 SKIP），这里临时打桩，断言结束恢复。
const realApi = sandbox.api
// AI 生成类接口已流式化（window.__INFRA_SSE__.post，桌面桥的 POST + SSE）：打桩同步投递
// final / error 帧后收尾——面板状态在 generate 返回前完成切换，断言保持原来的线性写法。
const sseStub = (frames) => ({
  post: (url, body, handlers) => {
    for (const [type, data] of frames) handlers.onEvent && handlers.onEvent(type, data)
    handlers.onEnd && handlers.onEnd({ status: 200, contentType: 'text/event-stream' })
    return { close: () => {} }
  }
})
const sseOK = (payload) => sseStub([['final', JSON.stringify(payload)]])
const sseErr = (message) => sseStub([['error', JSON.stringify({ message: message })]])
const restoreStream = () => { sandbox.api = realApi; delete sandbox.__INFRA_SSE__ }
async function followupFlow(comp, name, ctxInit, okPayload) {
  const mk = () => Object.assign({}, comp.data(), ctxInit, { $emit: () => {} })
  const first = mk()
  sandbox.__INFRA_SSE__ = sseOK(okPayload)
  await comp.methods.generate.call(first, false)
  check(!!first.prev, name + '：首轮生成成功后必须留下可追加的上下文（否则「追加生成」按钮永不出现）')
  sandbox.__INFRA_SSE__ = sseOK(okPayload)
  await comp.methods.generate.call(first, true)
  check(first.prev === null && first.base === '',
    name + '：追加成功后必须清空上下文——追加只给一次，用完按钮要消失、回到只有一个生成按钮')
  const retry = mk()
  sandbox.__INFRA_SSE__ = sseOK(okPayload)
  await comp.methods.generate.call(retry, false)
  sandbox.__INFRA_SSE__ = sseErr('boom')
  await comp.methods.generate.call(retry, true)
  check(!!retry.prev, name + '：追加失败时必须保留上下文（按钮留着可重试，不能白丢一次机会）')
}

// Grok 面板的行为断言：自定义 pattern 解析、编译错误的去处、AI 生成后是否复跑。
async function grokFlow(comp) {
  const msg = { warning: () => {}, success: () => {}, error: () => {} }
  // 假上下文不是 Vue 实例，computed 不会自动求值 —— patterns / definitions 必须手动接上，
  // 否则组件方法里读 this.patterns 就是 undefined（这正是「读 length 炸掉」的原因）。
  const mk = () => {
    const ctx = Object.assign({}, comp.data(), { conn: { id: 7 }, $message: msg })
    Object.defineProperty(ctx, 'patterns', { get: () => comp.computed.patterns.call(ctx) })
    Object.defineProperty(ctx, 'definitions', { get: () => comp.computed.definitions.call(ctx) })
    // 组件内部会 this.parse() 复用自身（解析按钮 / AI 生成后自动复跑），假上下文同样要接上；
    // AI 生成里还会用到 mergeDefs（把模型给的定义并进用户自定义框）
    ctx.parse = () => comp.methods.parse.call(ctx)
    ctx.mergeDefs = (d) => comp.methods.mergeDefs.call(ctx, d)
    ctx.defsToText = (d) => comp.methods.defsToText.call(ctx, d)
    return ctx
  }

  // ① 自定义 pattern 文本 → 定义表：注释行、没有正则的行都要跳过（否则会把注释当成 pattern 送后端）
  const dctx = mk()
  dctx.defText = '# 注释\nMY_ID [A-Z]{2}-\\d{4}\nNOPE\n\nOTHER \\d+'
  const defs = comp.computed.definitions.call(dctx)
  check(defs.MY_ID === '[A-Z]{2}-\\d{4}' && defs.OTHER === '\\d+' && !('NOPE' in defs) && !('#' in defs),
    '自定义 pattern 要按「NAME 正则」解析，注释行与没有正则的行必须跳过: ' + JSON.stringify(defs))

  // ② 编译失败：错误留在面板上且清空旧结果 —— 用户是照着这个错误改 pattern 的
  const bad = mk()
  bad.sample = 'a b c'
  bad.patternText = '%{NOPE:x}'
  bad.result = { total: 1, lines: [] }
  sandbox.api = { post: () => Promise.resolve({ code: 4016, message: '%{NOPE:x} 未定义的 pattern' }) }
  await comp.methods.parse.call(bad)
  check(bad.parseError.indexOf('未定义') >= 0 && bad.result === null,
    'pattern 编译失败必须把错误留在面板并清空上一次结果（否则用户对着过时结果改 pattern）')

  // ③ AI 生成：终稿填进 pattern 框 → **自动复跑一次解析**（右栏立刻反映新 pattern 的结果）。
  //    「自动复跑」是这一版的口径：模型判断不了自己的 pattern 能不能匹配样例（实测会把日志格式定义里
  //    的变量顺序当成字面分隔符），服务端 final 前已用本地引擎实跑校验、必要时回喂修正一次，
  //    前端再复跑一次是为了让使用者马上看到结果，而不是对着上一版 pattern 的结果发呆。
  //    追加只用一轮：请求带上上一版 pattern 与**首轮**补充要求原文，成功即清空上下文（按钮消失），
  //    失败保留（可重试）——三条都在方法体里，读模板 regex 锁不住。
  const ai = mk()
  ai.sample = 'a b'
  ai.aiHint = '初始要求'
  const posts = []
  const sseStub2 = (frames) => ({
    post: (url, body, handlers) => {
      posts.push({ url: url, body: body })
      for (const [type, data] of frames) handlers.onEvent(type, data)
      handlers.onEnd({ status: 200, contentType: 'text/event-stream' })
      return { close: () => {} }
    }
  })
  const parseOK = { code: 0, data: {
    total: 1, matched: 1, partial: 0, missed: 0, truncated: false,
    lines: [{ no: 1, raw: 'a b', matched: true, partial: false, pattern_index: 0, fields: [{ name: 'a', value: 'a' }] }] } }
  const okPayload = { patterns: ['%{WORD:a} %{WORD:b}'], definitions: {}, explain: '两个词', fields: [],
    verify: { ok: true, matched: 1, partial: 0, missed: 0, total: 1, attempts: 1, adopted_retry: false, error: '' } }
  // 解析走普通 api.post（不是 SSE），所以这里也要记一笔，才能断言「生成后确实复跑了」
  sandbox.api = { post: (url) => { posts.push({ url: url, body: null }); return Promise.resolve(parseOK) } }
  sandbox.__INFRA_SSE__ = sseStub2([['final', JSON.stringify(okPayload)]])
  await comp.methods.aiGenerate.call(ai, false)
  await new Promise(r => setTimeout(r, 0))
  check(ai.patternText === '%{WORD:a} %{WORD:b}',
    'AI 生成的 pattern 必须填进 pattern 输入框: ' + JSON.stringify(ai.patternText))
  check(posts.filter(p => p.url.indexOf('/grok/parse') >= 0).length === 1,
    'AI 生成后必须自动复跑一次解析（否则右栏显示的还是上一版 pattern 的结果）: ' + JSON.stringify(posts.map(p => p.url)))
  check(!!ai.result && ai.result.total === 1 && ai.result.lines.length === 1,
    'AI 生成后右栏应铺上新 pattern 的解析结果')
  check(!!ai.aiVerify && ai.aiVerify.ok === true && ai.aiVerify.attempts === 1,
    'final 里的 verify 结果要记下来（界面据此说明生成质量）: ' + JSON.stringify(ai.aiVerify))
  check(comp.computed.verifyText.call(ai).indexOf('1/1') >= 0,
    '校验说明要写明命中行数: ' + comp.computed.verifyText.call(ai))
  check(!!ai.aiPrev && ai.aiPrev.patterns.length === 1,
    'AI 生成成功后必须留下可追加上下文（否则「追加生成」按钮永不出现）')
  check(!('prev' in posts[0].body), '首轮生成不应带 prev 上下文（此时还没有任何一版 pattern）')

  // ③b phase 帧（首稿未整行命中、服务端正在按失败样例修正）：草稿必须清空重来，
  //     否则修正稿会和首稿拼在一起，用户看到的是一段根本没验证过的混合串。
  const ph = mk()
  ph.sample = 'a b'
  let atPhase = null
  sandbox.__INFRA_SSE__ = {
    post: (url, body, handlers) => {
      handlers.onEvent('delta', JSON.stringify({ field: 'patterns', index: 0, text: '%{WORD:a}' }))
      const before = ph.aiDraftPatterns.slice()
      handlers.onEvent('phase', JSON.stringify({ stage: 'retry', message: '未整行命中', matched: 0, total: 1 }))
      atPhase = { before: before, after: ph.aiDraftPatterns.slice(), phase: ph.aiPhase }
      handlers.onEvent('delta', JSON.stringify({ field: 'patterns', index: 0, text: '%{WORD:a} %{WORD:b}' }))
      handlers.onEvent('final', JSON.stringify(okPayload))
      handlers.onEnd({ status: 200, contentType: 'text/event-stream' })
      return { close: () => {} }
    }
  }
  await comp.methods.aiGenerate.call(ph, false)
  await new Promise(r => setTimeout(r, 0))
  check(atPhase.before.length === 1 && atPhase.after.length === 0,
    'phase 帧必须清空草稿（修正稿重新起草，不能与首稿混在一起）: ' + JSON.stringify(atPhase))
  check(atPhase.phase.indexOf('修正') >= 0 && atPhase.phase.indexOf('0/1') >= 0,
    'phase 帧要给出「命中几行 / 正在修正」的说明: ' + atPhase.phase)
  check(ph.aiPhase === '' && ph.patternText === '%{WORD:a} %{WORD:b}',
    'final 之后要收起修正提示并落终稿: phase=' + JSON.stringify(ph.aiPhase))

  // ③c 模型给的自定义 pattern 只能**追加**到用户已有的定义里：整体覆盖会让用户写过的定义消失，
  //     下一次「解析」直接报「未定义的 pattern」（用户会以为是自己写错了）。
  const dm = mk()
  dm.sample = 'a b'
  dm.defText = '# 我的注释\nMY_ID [A-Z]{2}'
  sandbox.__INFRA_SSE__ = sseStub2([['final', JSON.stringify({
    patterns: ['%{MY_ID:id}'], definitions: { EXTRA: '\\d+' }, explain: '', fields: [], usage: {} })]])
  await comp.methods.aiGenerate.call(dm, false)
  await new Promise(r => setTimeout(r, 0))
  check(dm.defText.indexOf('# 我的注释') >= 0 && dm.defText.indexOf('MY_ID [A-Z]{2}') >= 0 &&
    dm.defText.indexOf('EXTRA \\d+') >= 0,
    '模型定义只能追加，用户原有的行（含注释）必须保留: ' + JSON.stringify(dm.defText))
  check(dm.showDefs === true, '追加了模型定义后「自定义 pattern」区要自动展开，否则等于没生效')

  // ④ 追加生成：请求带上一版 pattern 与首轮补充要求原文，成功即清空上下文（按钮消失）

  // 追加：body 带上一版 pattern + 首轮补充要求（aiHint 已改成「再细化」也不漂移），成功后清空上下文
  ai.aiHint = '再细化'
  sandbox.__INFRA_SSE__ = sseStub2([['final', JSON.stringify({
    patterns: ['%{WORD:a} %{WORD:b} %{DATA:rest}'], definitions: {}, explain: '更细', fields: [], usage: {} })]])
  await comp.methods.aiGenerate.call(ai, true)
  await new Promise(r => setTimeout(r, 0))
  // 追加轮的请求：从 posts 里按 URL 找（生成后紧跟的自动复跑也会进 posts，不能直接取最后一条）
  const follow = posts.filter(p => p.url.indexOf('/grok/ai') >= 0).pop()
  check(follow.body.prev && follow.body.prev.patterns[0] === '%{WORD:a} %{WORD:b}' && follow.body.prev.hint === '初始要求',
    '追加生成必须带上上一版 pattern 与「首轮」补充要求原文: ' + JSON.stringify(follow.body.prev))
  check(follow.body.hint === '再细化', '追加轮的 hint 应是本轮文本框内容（补充要求以本轮为准）')
  check(ai.aiPrev === null && ai.aiBase === '',
    '追加成功后必须清空上下文——追加只给一次，用完按钮要消失: aiPrev=' + JSON.stringify(ai.aiPrev))

  // 追加失败：上下文与错误都保留（按钮留着可重试）
  const ai2 = mk()
  ai2.sample = 'a b'
  sandbox.__INFRA_SSE__ = sseStub2([['final', JSON.stringify(okPayload)]])
  await comp.methods.aiGenerate.call(ai2, false)
  sandbox.__INFRA_SSE__ = sseStub2([['error', JSON.stringify({ message: 'boom' })]])
  await comp.methods.aiGenerate.call(ai2, true)
  check(!!ai2.aiPrev && ai2.aiError === 'boom',
    '追加失败时必须保留上下文（按钮留着可重试，不能白丢一次机会）: ' + JSON.stringify(ai2.aiPrev))
}

// Logstash 配置区块的行为断言：锁的是「AI 只是加分项」这条设计——配置文本由后端本地拼装，
// 前端只负责落位，所以 enhance 三态（ok / skipped / failed）下配置都必须在；
// 把 failed 当失败、连配置一起吞掉，用户就白点了一次生成（这正是本功能最该防的退化）。
async function logstashFlow(comp) {
  const msg = { warning: () => {}, success: () => {}, error: () => {} }
  const calls = []
  const mk = () => Object.assign({}, comp.data(), {
    conn: { id: 7 }, $message: msg,
    patterns: ['%{WORD:a}'], definitions: {}, sample: 'a b', hint: ''
  })
  // 前端所有 AI 请求都只走流式通道，但这个通道有两种收尾：
  //   - SSE 帧（有增强时的 phase / final；服务端进了流式阶段再失败是 error 帧）；
  //   - 非 SSE 业务 JSON（pattern 编译不过这类前置校验，服务端还没发流式头就返回了 4016），
  //     通道会取出 body.message 转成 onError({message}) —— 见 wails-shim 里的 rawBuf 分支。
  const sseStub = (frames) => {
    sandbox.__INFRA_SSE__ = {
      post: (url, body, handlers) => {
        calls.push({ url: url, body: body })
        for (const [type, data] of frames) handlers.onEvent && handlers.onEvent(type, data)
        handlers.onEnd && handlers.onEnd({ status: 200, contentType: 'text/event-stream' })
        return { close: () => {} }
      }
    }
  }
  const finalFrame = (data) => sseStub([['final', JSON.stringify(data)]])
  const failStub = (message) => {
    sandbox.__INFRA_SSE__ = {
      post: (url, body, handlers) => {
        calls.push({ url: url, body: body })
        handlers.onError && handlers.onError({ message: message, status: 400 })
        return { close: () => {} }
      }
    }
  }

  // ① 正常：配置与状态记账都要落位，请求打到 /grok/logstash
  const c = mk()
  finalFrame({ config: 'filter {}', fields: ['a'], gsub: [], remove: [], notes: '', enhance: 'ok', dropped_gsub: [], dropped_remove: [] })
  await comp.methods.generate.call(c)
  check(c.config === 'filter {}' && c.enhance === 'ok' && c.fields.length === 1,
    'Logstash：生成后配置与状态必须落位: ' + JSON.stringify({ config: c.config, enhance: c.enhance }))
  check(calls.length === 1 && calls[0].url.indexOf('/grok/logstash') >= 0 &&
    calls[0].body.tag === '' && calls[0].body.source === '' && calls[0].body.patterns.length === 1,
    'Logstash：请求要打到 /grok/logstash 并带上 pattern / tag / source: ' + JSON.stringify(calls[0]))
  check(calls[0].body.drop_defaults === true,
    'Logstash：默认要删采集器字段（drop_defaults 默认 true，与开关初值一致）: ' + JSON.stringify(calls[0].body))
  check(String(comp.computed.statusText.call(c)).indexOf('AI 已按样例补充') >= 0,
    'Logstash：有增强时要说明补了什么: ' + comp.computed.statusText.call(c))

  // ①b phase 帧：等待期间要把「在干什么」显示出来（一次模型调用可能十几秒，干转会被当成卡死），
  //     final 到达后还要收掉，不能留在界面上
  const ph = mk()
  let atPhase = null
  sandbox.__INFRA_SSE__ = {
    post: (url, body, handlers) => {
      calls.push({ url: url, body: body })
      handlers.onEvent('phase', JSON.stringify({ message: '已交给模型判断 mutate 处理…' }))
      atPhase = { phase: ph.phase, loading: ph.loading }
      handlers.onEvent('final', JSON.stringify({ config: 'filter {}', fields: [], enhance: 'ok' }))
      handlers.onEnd({ status: 200, contentType: 'text/event-stream' })
      return { close: () => {} }
    }
  }
  await comp.methods.generate.call(ph)
  check(atPhase.phase.indexOf('模型') >= 0 && atPhase.loading === true,
    'Logstash：phase 帧要显示成进度文案（否则等待期间界面毫无反应）: ' + JSON.stringify(atPhase))
  check(ph.phase === '' && ph.config === 'filter {}',
    'Logstash：final 之后要收起进度文案并落配置: ' + JSON.stringify({ phase: ph.phase, config: ph.config }))

  // ② AI 调用失败：后端已降级为纯解析配置，前端必须照常展示 + 说清原因
  const f = mk()
  finalFrame({ config: 'filter {}', fields: [], enhance: 'failed', enhance_error: 'connection refused' })
  await comp.methods.generate.call(f)
  check(f.config === 'filter {}' && f.enhance === 'failed',
    'Logstash：AI 增强失败时配置仍要展示（AI 只是加分项，不能把配置吞掉）: ' + JSON.stringify(f.config))
  check(String(comp.computed.statusText.call(f)).indexOf('connection refused') >= 0,
    'Logstash：AI 失败原因要摊开说: ' + comp.computed.statusText.call(f))

  // ②b error 帧：流式阶段失败（如模型超时）也要落到面板上并收掉 loading
  const ef = mk()
  sseStub([['error', JSON.stringify({ message: '模型调用超时' })]])
  await comp.methods.generate.call(ef)
  check(ef.error.indexOf('模型调用超时') >= 0 && ef.loading === false,
    'Logstash：error 帧要落到面板并收掉 loading: ' + JSON.stringify({ e: ef.error, l: ef.loading }))

  // ③ pattern 编译失败：SSE 之前的前置校验（服务端连流式响应头都还没发），
  //     通道把它转成 onError —— 错误要留在面板 + 清掉旧配置
  const b = mk()
  b.config = 'stale-config'
  failStub('%{NOPE:x} 未定义的 pattern')
  await comp.methods.generate.call(b)
  check(b.error.indexOf('未定义') >= 0 && b.config === '',
    'Logstash：pattern 编译失败要把错误留在面板并清空上一次配置: ' + JSON.stringify({ e: b.error, c: b.config }))

  // ④ 没有 pattern 时不该发请求（省一次网络往返与 token）
  const n = mk()
  n.patterns = []
  const before = calls.length
  finalFrame({ config: '' })
  await comp.methods.generate.call(n)
  check(calls.length === before, 'Logstash：没有 pattern 时不该发请求，实际多了 ' + (calls.length - before) + ' 次')

  // ⑤ 流式通道不可用（非桌面形态）时必须明确报错，不能悄悄回退到普通 POST ——
  //     那条路会被 axios 的 15 秒默认超时打断，表现就从「功能不可用」变成「每次生成都超时」
  const noSse = mk()
  delete sandbox.__INFRA_SSE__
  sandbox.api = { post: () => { calls.push({ url: 'api-fallback', body: null }); return Promise.resolve({ code: 0, data: { config: 'fallback' } }) } }
  await comp.methods.generate.call(noSse)
  check(noSse.config === '' && !calls.some(x => x.url === 'api-fallback'),
    'Logstash：流式通道不可用时必须明确报错，不能悄悄回退到普通 POST')
}

// ⑫ 解读弹框的行为断言（用户口径）：首次执行自动弹出 / 关闭只是收起、结论仍留一份 /
//    「查看解读」只重开不重跑 / 「重新分析」重跑并整体覆盖（永远只有一份）。
//    这四条都发生在方法体里（visible 置位、result 是否被替换、有没有真发请求），
//    读模板 regex 一条也锁不住 —— 所以这里直接驱动方法。
async function explainFlow(comp) {
  const calls = []
  // 每轮首个 delta 到达**之前**的 result 快照：这才是「重新分析从空白重来」的证据。
  // （只看 run 结束后的 result 是不够的：final 帧无论如何都会整体替换，
  //  把「跨轮不清空」这种缺陷蒙过去 —— 反向验证时实测漏判过一次。）
  let beforeDelta = null
  let cur = null
  const mk = () => {
    const ctx = Object.assign({}, comp.data(), {
      connId: 1, cfgLoaded: true, cfg: { enabled: true, model: 'm', base_url: 'u' }, $emit: () => {}
    })
    // 假上下文不是 Vue 实例：方法内部互相调用（this.run / this.hasResult / this.emitState）
    // 以及组件自己的入口方法都要手动接上，否则一调用就是「is not a function」
    ctx.run = () => comp.methods.run.call(ctx)
    ctx.analyze = (p) => comp.methods.analyze.call(ctx, p)
    ctx.open = () => comp.methods.open.call(ctx)
    ctx.rerun = () => comp.methods.rerun.call(ctx)
    ctx.hasResult = () => comp.methods.hasResult.call(ctx)
    ctx.emitState = () => comp.methods.emitState.call(ctx)
    return ctx
  }
  // 流式桩：delta 一帧 + final 一帧 + onEnd（同步投递，断言可线性书写）
  const stub = () => {
    sandbox.__INFRA_SSE__ = {
      post: (url, body, handlers) => {
        calls.push({ url: url, body: body })
        beforeDelta = cur && cur.result ? JSON.parse(JSON.stringify(cur.result)) : null
        handlers.onEvent('delta', JSON.stringify({ field: 'summary', text: '半' }))
        handlers.onEvent('final', JSON.stringify({
          summary: '结论', findings: ['发现'], suggestions: ['建议'],
          digest: { series_count: 1, truncated: false, lines: ['l'] }, usage: {}
        }))
        handlers.onEnd({ status: 200, contentType: 'text/event-stream' })
        return { close: () => {} }
      }
    }
  }

  const c = mk()
  cur = c
  check(c.visible === false, '解读：组件初始不该弹出弹框')
  stub()
  c.analyze({ expr: 'up', mode: 'instant', start: 1, end: 2, step: 0 })
  check(c.visible === true, '解读：首次执行必须自动弹出弹框')
  check(calls.length === 1, '解读：首次执行应真的发起一次解读请求，实际 ' + calls.length + ' 次')
  check(c.hasResult() && c.result.findings.length === 1, '解读：首次执行后应落一份结论')
  const first = c.result

  // 关闭只是收起：结论必须留着（这是「关闭之后支持重新查看」的前提）
  c.visible = false
  check(c.hasResult(), '解读：关闭弹框不该把结论一起丢掉（否则无从「重新查看」）')
  const n = calls.length
  c.open()
  check(c.visible === true && calls.length === n,
    '解读：「查看解读」必须只重开、不重跑（否则白白再花一次 token）')
  c.rerun()
  check(calls.length === n + 1, '解读：「重新分析」必须重跑一次接口，实际增了 ' + (calls.length - n) + ' 次')
  check(beforeDelta !== null && beforeDelta.summary === '' && beforeDelta.findings.length === 0,
    '解读：重新分析必须从空白重来（永远只保留一份，不能叠在上一版结论上）'
    + ' 实测 delta 前快照 = ' + JSON.stringify(beforeDelta))
  check(c.result !== first && c.result.findings.length === 1,
    '解读：重新分析后结论仍是单份（不能把两次结果叠起来）')

  // 重来要跟着新参数走（弹框顶部那行「解读对象」用的就是它）
  c.analyze({ expr: 'rate(x[5m])', mode: 'range', start: 1000, end: 2000, step: 15 })
  check(c.lastParams.expr === 'rate(x[5m])', '解读：从结果区再次进入时必须更新解读对象参数')
  check(String(comp.computed.rangeText.call(c)).indexOf('范围') === 0,
    '解读：「解读对象」的时间范围文案应能正常生成')

  // 配置未读完就点解读：不能直接停在引导态，要记下来等配置到位再补跑
  const late = mk()
  cur = late
  late.cfgLoaded = false; late.cfg = {}
  late.pendingRun = false
  late.analyze({ expr: 'up', mode: 'instant', start: 1, end: 2, step: 0 })
  check(late.visible === true && late.pendingRun === true,
    '解读：配置还没读完时点解读应记下待跑（否则会误停在「还没启用」引导态）')
}

// ⑬ 数据导入的行为断言（用户口径）：两阶段确认 / 预览与类型可改 / 真跑关超时 /
//     进度与取消靠 progress_key / 「检查」SSE 出结果后「预览」才可用。
// 索引页异步化行为：列表与总数分两个请求；总数由 /indices/total 独立落位；搜索不动总数
async function indexTabFlow(comp) {
  if (!comp) return
  const gets = []
  sandbox.api = {
    get: (url) => {
      gets.push(url)
      if (url.indexOf('/indices/total') >= 0) return Promise.resolve({ code: 0, data: { total: 123 } })
      return Promise.resolve({ code: 0, data: { list: [{ index: 'a' }], total: 1, truncated: false } })
    },
    post: () => Promise.resolve({ code: 0 })
  }
  const c = Object.assign({}, comp.methods, comp.data(), { conn: { id: 5 } })
  await comp.methods.loadIndices.call(c)
  await new Promise(r => setTimeout(r, 0))
  check(gets.some(u => u.indexOf('/indices/total') >= 0),
    '索引页：总数要独立异步拉取（不挡列表渲染）: ' + JSON.stringify(gets))
  check(c.allTotal === 123 && c.loadingTotal === false,
    '索引页：总数由 /indices/total 落位并收起转圈: ' + c.allTotal)
  check(c.indices.length === 1 && c.idxTotal === 1 && c.loadingIndices === false,
    '索引页：列表先到先渲染（保留搜索匹配数）')
  c.idxKw = 'a'
  await comp.methods.searchIndices.call(c)
  await new Promise(r => setTimeout(r, 0))
  check(c.allTotal === 123, '索引页：搜索不应改动左上角总数')
}

// Discover 时间升降序的行为断言：默认降序；切到 asc 后重查请求必须带 sort_order=asc
async function discoverSortFlow(comp) {
  if (!comp) return
  const calls = []
  sandbox.api = {
    post: (url, body) => { calls.push({ url: url, body: body }); return Promise.resolve({ code: 0, data: { result_id: 'x', hits: [], total: 0, window: 1000 } }) },
    get: () => Promise.resolve({ code: 0, data: {} })
  }
  const mk = (extra) => Object.assign({}, comp.methods, comp.data(), {
    conn: { id: 3 }, view: { id: 9, time_field: '@timestamp' }, timeField: '@timestamp', fields: [],
    start: '2026-10-14 00:00:00', end: '2026-10-14 01:00:00',
    $message: { warning: () => {}, error: () => {} }, $refs: {}, $nextTick: (f) => { if (f) f() }
  }, extra || {})
  const ctx = mk()
  await comp.methods.runSearch.call(ctx)
  check(calls.length === 1 && calls[0].body.sort_order === 'desc',
    'Discover：默认时间降序（最新优先）: ' + JSON.stringify(calls[0] && calls[0].body && calls[0].body.sort_order))
  const ctx2 = mk({ sortOrder: 'asc' })
  await comp.methods.runSearch.call(ctx2)
  check(calls.length === 2 && calls[1].url.indexOf('/search') >= 0 && calls[1].body.sort_order === 'asc',
    'Discover：升序查询要带 sort_order=asc')

  // 一个按钮切换：点一下 升序（重查带 asc），再点切回 降序（重查带 desc）
  const ctx3 = mk()
  comp.methods.toggleSort.call(ctx3)
  await new Promise(r => setTimeout(r, 0))
  check(ctx3.sortOrder === 'asc' && calls.length === 3 && calls[2].body.sort_order === 'asc',
    'Discover：点按钮应切到升序并立即重查')
  comp.methods.toggleSort.call(ctx3)
  await new Promise(r => setTimeout(r, 0))
  check(ctx3.sortOrder === 'desc' && calls.length === 4 && calls[3].body.sort_order === 'desc',
    'Discover：再点一下应切回降序并重查')

  // 命中高亮：多项分色、同项同色（大小写不敏感）；高亮项只在「查询」时刻刷新，打字不改动
  const ctx4 = mk()
  ctx4.hlTerms = ['foo', 'timeout']
  const html = comp.methods.hlValue.call(ctx4, 'FOO then timeout then foo')
  check(html.indexOf('es-hlchip c0') >= 0 && html.indexOf('es-hlchip c1') >= 0
    && (html.match(/es-hlchip c0/g) || []).length === 2,
    'Discover：多个命中项分色（同项同色、大小写不敏感）: ' + html)
  const ctx5 = mk()
  ctx5.hlTerms = ['old']
  ctx5.kql = 'warning'
  await comp.methods.runSearch.call(ctx5)
  check(JSON.stringify(ctx5.hlTerms) === '["warning"]', 'Discover：高亮项在查询时刻才刷新: ' + JSON.stringify(ctx5.hlTerms))
  ctx5.kql = 'typing-after-query'
  check(JSON.stringify(ctx5.hlTerms) === '["warning"]', 'Discover：查询后继续打字不改动已渲染的高亮')
}

async function importFlow(comp) {
  const msg = { warning: () => {}, success: () => {}, error: () => {}, info: () => {} }
  const calls = []
  const mk = (extra) => Object.assign({}, comp.methods, comp.data(), {
    conn: { id: 7 }, $message: msg, sources: ['C:/tmp/a.jsonl'], index: 'logs'
  }, extra || {})
  const stub = (fn) => {
    sandbox.api = {
      post: (url, body, cfg) => {
        calls.push({ url: url, body: body, cfg: cfg })
        return Promise.resolve(fn(url, body) || { code: 0 })
      },
      get: () => Promise.resolve({ code: 0, data: { found: false } })
    }
  }

  // ① 两阶段：先预检（confirmed=false），弹确认后再真跑（confirmed=true，且关掉超时）
  {
    const c = mk()
    c.types = { time_local: 'date', status: 'long' }
    c.filter = '*access*.log'
    c.renames = { time_local: 'ts' }
    let confirms = 0
    let confirmMsg = ''
    c.$confirm = (msg) => { confirms++; confirmMsg = msg; return Promise.resolve() }
    stub((url, body) => body.confirmed
      ? { code: 0, data: { read: 10, indexed: 9, failed: 1, skipped: 2, partial: 1, elapsed_ms: 120, failures: [], dropped_fields: [], files: [] } }
      : { code: 0, data: { need_confirm: true, sources: body.sources, size: 2048, index: 'logs' } })

    await comp.methods.start.call(c)
    check(confirms === 1, '导入：真跑之前必须让用户确认一次（文件 + 目标索引）')
    check(confirmMsg.indexOf('时间字段') >= 0 && confirmMsg.indexOf('time_local') >= 0,
      '导入：确认框要单独显示识别到的时间字段（date 类型）: ' + confirmMsg)
    check(calls[0].body.filter === '*access*.log', '导入：包内过滤规则要随请求发送')
    check(calls[1].body.renames && calls[1].body.renames.time_local === 'ts',
      '导入：字段映射（第二列）要随请求发送')
    check(calls[0].body.recreate_index === false && calls[1].body.recreate_index === false,
      '导入：普通导入不带 recreate_index（只有重建入口才为 true）')
    check(calls.length === 2, '导入：应是「预检 + 真跑」两次请求，实际 ' + calls.length + ' 次')
    check(Array.isArray(calls[0].body.sources) && calls[0].body.sources.length === 1,
      '导入：请求体要用 sources 数组携带所选文件（支持多选）')
    check(calls[0].body.confirmed === false && calls[1].body.confirmed === true,
      '导入：第一次是预检，第二次才真跑')
    check(calls[1].cfg && calls[1].cfg.timeout === 0,
      '导入：真跑必须关掉 HTTP 超时（耗时由数据量决定，15 秒默认值必定不够）')
    check(!!calls[1].body.progress_key, '导入：真跑要带 progress_key（进度与取消都靠它）')
    check(!('id_field' in calls[1].body), '导入：请求体不该再带 id_field（文档 id 由 ES 自动生成）')
    check(!('field' in calls[1].body) && !('split_pattern' in calls[1].body),
      '导入：请求体不该再带 field/split_pattern（规则区已删除，pattern 由「检查」生成）')
    check(c.showProgress === true, '导入：开始导入要自动弹出进度框（用户要求的过程可见）')
    check(c.result && c.result.indexed === 9, '导入：结果要落位: ' + JSON.stringify(c.result))
    check(c.importing === false, '导入：收尾后 loading 必须复位')
  }

  // ② 确认框里取消：不能再发真跑请求（用户没同意就别动数据）
  {
    const c = mk()
    c.$confirm = () => Promise.reject(new Error('cancel'))
    calls.length = 0
    stub(() => ({ code: 0, data: { need_confirm: true, size: 1, index: 'logs' } }))
    await comp.methods.start.call(c)
    check(calls.length === 1, '导入：确认框取消后不该再发真跑请求，实际 ' + calls.length + ' 次')
    check(!c.result, '导入：取消后不该有结果')
  }

  // ③ 预览：样本/统计/类型要落位；索引已存在时自动切到「不新建」
  {
    const c = mk({ createIndex: true })
    calls.length = 0
    stub(() => ({
      code: 0, data: {
        kind: 'json', scanned: 30, matched: 28, partial: 1, skipped: 1,
        types: { a: 'long', b: 'keyword' },
        type_options: ['keyword', 'long'], index_exists: true,
        skip_reasons: ['第 2 行未匹配 pattern'], files: [],
        rows: [
          { no: 1, raw: '{"a":1}', status: 'matched', fields: [{ name: 'a', value: '1' }] },
          { no: 2, raw: '这条不匹配', status: 'skipped', reason: '第 2 行未匹配 pattern', fields: [] }
        ]
      }
    }))
    await comp.methods.doPreview.call(c)
    check(calls.length === 1 && calls[0].url.indexOf('/import/preview') >= 0,
      '导入：预览要打到 /import/preview')
    check(c.preview && c.preview.scanned === 30, '导入：预览统计要落位')
    check(c.showPreview === true, '导入：预览要弹框展示（左右布局，不是塞在面板里）')
    check(c.types.a === 'long' && c.types.b === 'keyword',
      '导入：推断出的字段类型要落位')
    // 改副本不能动到服务端回的对象：共享的话重跑预览看不出差异，用户到底改过哪几个字段就说不清
    c.types.a = 'keyword'
    check(c.preview.types.a === 'long', '导入：类型必须存副本，改它不能污染 preview.types')
    check(c.createIndex === false,
      '导入：索引已存在时应自动切到「不新建」（否则 PUT 必报 already exists）')
    check(comp.computed.typeRows.call(c).length === 2, '导入：字段定义表要按字段展开')
    // 逐行预览：未命中的行也要在（带原因），三态映射成 grok 卡片样式 —— 用户对照着找问题
    const pvRows = c.preview.rows
    check(pvRows.length === 2 && pvRows[1].status === 'skipped'
      && String(pvRows[1].reason).indexOf('第 2 行') >= 0 && pvRows[0].fields.length === 1,
      '导入：逐行预览必须含未命中行与原因、命中行带字段')
    check(comp.methods.rowClass.call(c, pvRows[0]) === 'is-ok'
      && comp.methods.rowClass.call(c, pvRows[1]) === 'is-miss',
      '导入：逐行预览的命中/未命中要映射成 grok 卡片的三态样式')
  }

  // ④ 取消：要带上本次任务的 key，否则取消不到自己那一次
  {
    const c = mk({ key: 'imp-abc' })
    let sent = null
    sandbox.api = {
      post: (url, body) => { sent = { url: url, body: body }; return Promise.resolve({ code: 0, data: { cancelled: true } }) },
      get: () => Promise.resolve({ code: 0, data: { found: false } })
    }
    await comp.methods.cancel.call(c)
    check(sent && sent.url.indexOf('/import/cancel') >= 0 && sent.body.key === 'imp-abc',
      '导入：取消要带上本次任务的 key')
  }

  // ⑤ 本地选择文件只在桌面端可用（浏览器里没有原生对话框，静默失败会让人以为按钮坏了）
  {
    const c = mk()
    const warned = []
    c.$message = { warning: (m) => warned.push(m) }
    sandbox.__INFRA_DESKTOP__ = false
    await comp.methods.pickFile.call(c)
    check(warned.length === 1, '导入：非桌面端点「选择文件」要明确提示，实际 ' + JSON.stringify(warned))
  }

  // ⑥ 输入目标索引即时校验存在性：打 /import/exists，已存在要自动切「不新建」
  {
    const c = mk({ createIndex: true })
    const gets = []
    sandbox.api = {
      post: () => Promise.resolve({ code: 0 }),
      get: (url, cfg) => { gets.push({ url: url, cfg: cfg }); return Promise.resolve({ code: 0, data: { exists: true, valid: true } }) }
    }
    await comp.methods.checkExists.call(c)
    check(gets.length === 1 && gets[0].url.indexOf('/import/exists') >= 0
      && gets[0].cfg && gets[0].cfg.params.index === 'logs',
      '导入：输入索引要打 /import/exists 即时校验，并把索引名带给后端')
    check(c.indexExists === true && c.createIndex === false,
      '导入：即时校验发现索引已存在要自动切「不新建」（否则真跑 PUT 必报 already exists）')

    const c2 = mk({ createIndex: true })
    sandbox.api.get = () => Promise.resolve({ code: 0, data: { exists: false, valid: true } })
    await comp.methods.checkExists.call(c2)
    check(c2.indexExists === false && c2.createIndex === true,
      '导入：索引不存在时保持「不存在时创建」勾选，不该误改用户的选择')
  }

  // ⑦ 「检查」SSE 流水线：日志按步骤落面板（时间 + 状态标记）；done(ready) 决定「预览」出不出
  {
    const c = mk({ hint: 'logback: %d{yyyy-MM-dd HH:mm:ss} [%thread] %-5level %logger{36} - %msg' })
    let analyzeBody = null
    sandbox.__INFRA_SSE__ = {
      post: (url, body, handlers) => {
        analyzeBody = body
        for (const [t, d] of [
          ['log', JSON.stringify({ text: '当前上传文件类型为：普通文本（按行解析）', kind: 'info' })],
          ['final', JSON.stringify({
            patterns: ['%{WORD:m}'], definitions: { X: '\\d+' },
            verify: { ok: true, matched: 20, partial: 0, missed: 0, total: 20, attempts: 1, adopted_retry: false }
          })],
          ['done', JSON.stringify({ ready: true })]
        ]) handlers.onEvent(t, d)
        handlers.onEnd({ status: 200, contentType: 'text/event-stream' })
        return { close: () => {} }
      }
    }
    await comp.methods.doAnalyze.call(c)
    check(analyzeBody && analyzeBody.hint === c.hint,
      '导入：日志格式说明要随检查请求发送（否则 AI 生成字段名全凭想象）')
    check(c.analyzed === true, '导入：检查跑完（done ready）后「预览」才出现')
    check((c.patterns || []).length === 1 && c.patterns[0] === '%{WORD:m}' && c.definitions.X === '\\d+',
      '导入：final 帧的 pattern/definitions 要收进内存（预览/导入随请求带上）: ' + JSON.stringify(c.patterns))
    check(c.logs.length >= 3 && typeof c.logs[0] === 'object' && !!c.logs[0].time && c.logs[0].kind === 'info',
      '导入：日志行要带时间与状态标记（步骤式输出）: ' + JSON.stringify(c.logs))
    check(c.logs.some(l => l.kind === 'ok' && l.text.indexOf('本次抽样全部通过') >= 0)
      && c.logs.some(l => l.kind === 'ok' && l.text.indexOf('分析完成') >= 0),
      '导入：抽样全通过 / 分析完成要用成功样式逐条落日志: ' + JSON.stringify(c.logs.map(l => l.text)))
    check(!c.logs.some(l => l.text.indexOf('%{WORD:m}') >= 0),
      '导入：日志不再输出生成的正则（用户口径：去掉输出的正则）: ' + JSON.stringify(c.logs.map(l => l.text)))

    const c2 = mk()
    sandbox.__INFRA_SSE__ = sseStub([['error', JSON.stringify({ message: '抽样失败' })]])
    await comp.methods.doAnalyze.call(c2)
    check(c2.analyzed === false, '导入：检查失败（error 帧）时不该出现「预览」入口')

    const c3 = mk()
    sandbox.__INFRA_SSE__ = sseStub([['done', JSON.stringify({ ready: false })]])
    await comp.methods.doAnalyze.call(c3)
    check(c3.analyzed === false, '导入：done(ready:false)（未生成 pattern）时不该出现「预览」入口')
    check(c3.logs.some(l => l.kind === 'err'), '导入：未生成 pattern 要用错误样式说明')
    delete sandbox.__INFRA_SSE__
  }

  // ⑧ 多文件与拖拽：acceptFiles 收下选择并复位分析态；投放只认导入面板本身
  {
    const c = mk()
    c.desktop = true
    comp.methods.acceptFiles.call(c, ['C:/logs/a.log', 'C:\\logs\\b.log', ' ', null])
    check(c.sources.length === 2, '导入：acceptFiles 要收下多文件并过滤空串: ' + JSON.stringify(c.sources))
    check(c.analyzed === false && c.preview === null && c.patterns.length === 0,
      '导入：换文件后要复位分析/预览/pattern 状态')
    check(comp.computed.fileLabel.call(c).indexOf('共 2 个文件') >= 0
      && comp.computed.fileLabel.call(c).indexOf('a.log') >= 0,
      '导入：多文件要显示「N 个文件：名字」: ' + comp.computed.fileLabel.call(c))

    const c2 = mk({ sources: [] })
    c2.desktop = true
    comp.methods.onFilesDropped.call(c2, { elementId: 'other-area', files: ['C:/x.log'] })
    check(c2.sources.length === 0, '导入：投放到其它区域的文件不该改选择')
    comp.methods.onFilesDropped.call(c2, { elementId: 'es-imp-drop', files: ['C:/x.log', 'C:/y.log'] })
    check(c2.sources.length === 2, '导入：投放到导入面板要接住拖入的多文件')

    const c3 = mk({ sources: [], importing: true })
    c3.desktop = true
    comp.methods.onFilesDropped.call(c3, { elementId: 'es-imp-drop', files: ['C:/x.log'] })
    check(c3.sources.length === 0, '导入：导入进行中不接收新的拖放')

    // 包内过滤：只对压缩包生效的行 + 改动即复位分析态
    const cz = mk({ sources: ['C:/logs/a.zip'] })
    check(comp.computed.isArchive.call(cz) === true, '导入：选中压缩包时要出现「包内过滤」行')
    const cz2 = mk({ sources: ['C:/logs/a.tgz', 'C:/logs/b.log'] })
    check(comp.computed.isArchive.call(cz2) === true, '导入：多选里含压缩包也算（tgz 认）')
    const cz3 = mk({ sources: ['C:/logs/b.log', 'C:/logs/c.jsonl'] })
    check(comp.computed.isArchive.call(cz3) === false, '导入：普通日志/JSON 不显示包内过滤行')

    const cf = mk()
    cf.analyzed = true
    cf.preview = { rows: [] }
    cf.patterns = ['%{WORD:m}']
    comp.watch.filter.call(cf)
    check(cf.analyzed === false && cf.preview === null && cf.patterns.length === 0,
      '导入：改包内过滤要复位分析态（旧的检查/预览结果作废）')
    const ch = mk()
    ch.analyzed = true
    comp.watch.hint.call(ch)
    check(ch.analyzed === false, '导入：改日志格式说明要复位分析态')
  }

  // ⑨ 补救闭环：跳过样例回喂重生成 / 删除重建导入 / 索引按文件名自动取名 / 写入计数核实
  {
    // 索引自动取名：去扩展名（含 .tar.gz 双重后缀）、非法字符替换、全非法则返回空
    const cd = mk({ index: '' })
    check(comp.methods.deriveIndex.call(cd, 'C:/logs/2026-10-13/yshapi_access_2026-10-13.log') === 'yshapi_access_2026-10-13',
      '导入：索引名要能按文件名自动生成')
    check(comp.methods.deriveIndex.call(cd, 'C:/x/logs.tar.gz') === 'logs', '导入：.tar.gz 双重后缀要去干净')
    check(comp.methods.deriveIndex.call(cd, 'C:/x/应用 日志 2026.log') === '2026', '导入：非法字符要替换并修剪')
    check(comp.methods.deriveIndex.call(cd, 'C:/x/应用.log') === '', '导入：生成不出合法名时返回空（交给手填）')

    const cd2 = mk({ index: '' })
    comp.methods.acceptFiles.call(cd2, ['C:/logs/2026-10-13/yshapi_access_2026-10-13.log'])
    check(cd2.index === 'yshapi_access_2026-10-13', '导入：选文件后目标索引自动按文件名填: ' + cd2.index)
    const cd3 = mk({ index: 'my-custom' })
    comp.methods.acceptFiles.call(cd3, ['C:/logs/other_2026.log'])
    check(cd3.index === 'my-custom', '导入：用户手输过索引就不再被自动覆盖')

    // 用跳过样本重新生成：跳过样例随检查请求带上，且先收起进度弹框
    const cr = mk()
    cr.result = { index: 'logs', skipped: 2, skip_samples: ['bad one', 'bad two'] }
    cr.showProgress = true
    let refBody = null
    sandbox.__INFRA_SSE__ = {
      post: (url, body, handlers) => {
        refBody = body
        handlers.onEnd({ status: 200, contentType: 'text/event-stream' })
        return { close: () => {} }
      }
    }
    comp.methods.refineFromSkips.call(cr)
    await new Promise(r => setTimeout(r, 0))
    check(refBody && refBody.extra_samples && refBody.extra_samples.length === 2,
      '导入：用跳过样本重新生成要把跳过样例随检查请求带上: ' + JSON.stringify(refBody && refBody.extra_samples))
    check(cr.showProgress === false, '导入：重新生成时先收起进度弹框')
    delete sandbox.__INFRA_SSE__

    // 删除重建导入：弹不可撤销确认；两次请求都带 recreate_index；强制建索引并带字段类型
    const cri = mk({ createIndex: false })
    cri.index = 'logs'
    cri.types = { ts: 'date' }
    let confirms3 = 0
    cri.$confirm = () => { confirms3++; return Promise.resolve() }
    calls.length = 0
    stub((url, body) => body.confirmed
      ? { code: 0, data: { read: 1, indexed: 1, skipped: 0, failed: 0, elapsed_ms: 1, failures: [], dropped_fields: [], files: [], count_before: 0 } }
      : { code: 0, data: { need_confirm: true, sources: body.sources, size: 1, index: 'logs', index_exists: true } })
    await comp.methods.reimport.call(cri)
    await new Promise(r => setTimeout(r, 0))
    check(confirms3 === 1, '导入：重建导入要弹不可撤销确认')
    check(calls.length === 2 && calls[0].body.recreate_index === true && calls[1].body.recreate_index === true,
      '导入：重建导入两次请求都要带 recreate_index')
    check(calls[1].body.create_index === true && JSON.stringify(calls[1].body.types) === '{"ts":"date"}',
      '导入：重建时强制建索引并带上字段类型（「不存在时创建」勾没勾都要建）')
    comp.methods.stopVerifyCount.call(cri)

    // 计数核实：达到期望（before + indexed）即停
    const cv = mk()
    cv.result = { index: 'logs', indexed: 100, count_before: 0 }
    sandbox.api = {
      post: () => Promise.resolve({ code: 0 }),
      get: () => Promise.resolve({ code: 0, data: { count: 100 } })
    }
    comp.methods.startVerifyCount.call(cv)
    await new Promise(r => setTimeout(r, 0))
    check(cv.verify && cv.verify.done === true && cv.verify.current === 100,
      '导入：完成后核实索引计数（每 5 秒刷新、达到期望即停）: ' + JSON.stringify(cv.verify))
    comp.methods.stopVerifyCount.call(cv)

    const cv2 = mk()
    cv2.result = { index: 'logs', indexed: 100, count_before: 0 }
    sandbox.api.get = () => Promise.resolve({ code: 0, data: { count: 120 } })
    comp.methods.startVerifyCount.call(cv2)
    await new Promise(r => setTimeout(r, 0))
    check(cv2.verify && cv2.verify.done === true && cv2.verify.over === true,
      '导入：计数多于期望要如实标记（可能有历史数据）: ' + JSON.stringify(cv2.verify))
    comp.methods.stopVerifyCount.call(cv2)
  }

  // ⑩ 跳过超阈值暂停闭环：waiting 弹框 → continue / refine（中止 → 重新生成 → 自动删除重建）
  {
    const cp = mk()
    cp.key = 'imp-p'
    comp.methods.applyProgress.call(cp, { waiting: true, skipped: 25, skip_samples: ['s1', 's2'] })
    check(cp.pauseAsk === true && cp.pauseInfo && cp.pauseInfo.skipped === 25
      && cp.pauseInfo.skip_samples.length === 2 && cp.pauseInfo.key === 'imp-p',
      '导入：跳过超阈值挂起要弹决策框并带现场样例: ' + JSON.stringify(cp.pauseInfo))
    comp.methods.applyProgress.call(cp, { waiting: true, skipped: 26, skip_samples: [] })
    check(cp.pauseAsk === true && cp.pauseInfo.skipped === 25, '重复轮询不该叠加/覆盖决策框')

    const pc = mk()
    pc.key = 'imp-c'
    pc.pauseInfo = { key: 'imp-c', skipped: 25, skip_samples: ['x'] }
    pc.pauseAsk = true
    const resPosts = []
    sandbox.api = {
      post: (url, body) => { resPosts.push({ url: url, body: body }); return Promise.resolve({ code: 0, data: { ok: true } }) },
      get: () => Promise.resolve({ code: 0, data: { found: false } })
    }
    await comp.methods.pauseDecision.call(pc, 'continue')
    check(resPosts.length === 1 && resPosts[0].url.indexOf('/import/resume') >= 0
      && resPosts[0].body.action === 'continue' && pc.pauseAsk === false && pc._pauseDecided === true,
      '导入：暂停时选「继续」要打 resume(continue) 并收起弹框')

    const pr = mk()
    pr.key = 'imp-r'
    pr.pauseInfo = { key: 'imp-r', skipped: 25, skip_samples: ['r1', 'r2'] }
    pr.pauseAsk = true
    resPosts.length = 0
    await comp.methods.pauseDecision.call(pr, 'refine')
    check(resPosts.length === 1 && resPosts[0].body.action === 'abort' && pr._pendingRefine === true
      && pr._pauseSamples.length === 2,
      '导入：选「重新生成」要中止导入并记下现场样例')

    // 补救链：重新生成完成（done）后自动删除重建导入
    const cc = mk({ hint: 'fmt' })
    cc._chainReimport = true
    calls.length = 0
    stub((url, body) => body.confirmed
      ? { code: 0, data: { read: 1, indexed: 1, skipped: 0, failed: 0, elapsed_ms: 1, failures: [], dropped_fields: [], files: [], count_before: 0 } }
      : { code: 0, data: { need_confirm: true, sources: body.sources, size: 1, index: 'logs', index_exists: true } })
    sandbox.__INFRA_SSE__ = sseStub([['done', JSON.stringify({ ready: true })]])
    await comp.methods.doAnalyze.call(cc)
    await new Promise(r => setTimeout(r, 0))
    check(cc._chainReimport === false && calls.length === 2 && calls[1].body.recreate_index === true,
      '导入：重新生成完成后要自动删除重建导入: ' + JSON.stringify(calls.map(x => x.body.recreate_index)))
    comp.methods.stopVerifyCount.call(cc)
    delete sandbox.__INFRA_SSE__

    // 抽屉逐文件状态：已完成打勾 / 当前转圈 / 待处理；导入结束全部打勾
    const cfd = mk()
    cfd.progress = { files: ['a.log', 'b.log', 'c.log'], file_step: 2, phase: 'running' }
    check(comp.methods.fileState.call(cfd, 0) === 'done' && comp.methods.fileState.call(cfd, 1) === 'active'
      && comp.methods.fileState.call(cfd, 2) === 'pending',
      '导入：抽屉文件清单状态应为 done/active/pending')
    const cfd2 = mk()
    cfd2.progress = { files: ['a.log'], file_step: 1, phase: 'done' }
    check(comp.methods.fileState.call(cfd2, 0) === 'done', '导入结束后每个文件都要打勾')
  }
}
Promise.resolve()
  .then(() => followupFlow(sandbox.MetricsAIGen, '监控生成面板',
    { connId: 1, question: '看看各容器内存' },
    { promql: 'up', explain: '', range: null, used_metrics: 0, usage: {} }))
  .then(() => followupFlow(sandbox.MySQLAI, 'MySQL 生成面板',
    { conn: { id: 1 }, db: 'mall', question: '查订单' },
    { sql: 'SELECT 1', tables: ['mall.orders'], explain: '', sql_type: 'DQL', statement_count: 1, confirm_level: '', used_catalog: 0, usage: {} }))
  .then(() => grokFlow(sandbox.EsGrokTab))
  .then(() => logstashFlow(sandbox.EsGrokLogstash))
  .then(() => explainFlow(sandbox.MetricsAIExplain))
  .then(() => indexTabFlow(sandbox.EsIndexTab))
  .then(() => discoverSortFlow(sandbox.EsDiscover))
  .then(() => importFlow(sandbox.EsImportTab))
  .then(() => { restoreStream(); report() })
  .catch(e => { restoreStream(); errs.push('AI 面板行为断言执行异常: ' + e.message); report() })

// 收尾必须由 ⑨ 的异步断言回调触发（上面的 .then / .catch）：
// 那是本文件唯一异步项，若在这里直接判定，异步断言的结果会落在判定之后被丢掉（门禁假绿）。
function report() {
  if (errs.length) {
    console.log('前端装载冒烟 不通过：')
    errs.forEach(e => console.log('  - ' + e))
    process.exit(1)
  }
  console.log(`前端装载冒烟 通过：脚本 ${loaded}/${expected} 个，9 个套件注册齐备，`
    + `页面局部组件 ${Object.keys(page.components).length} 个，模板 ${page.template.length} 字符`
    + (RECORD ? '已重录为基线。' : '逐字节一致。'))
}
