/*
 * error_notice.js：统一错误出口（桌面模式的核心补偿手段）。
 *
 * 为什么需要这个文件：
 *   桌面应用里 F12 打不开（Windows 上 Wails 关掉了浏览器加速键，见 desktop/devtools.go），
 *   于是 console 与 DevTools 都不是可靠的排障通道——出错的唯一去处就是界面本身。
 *   之前只有 app.js 拦截器里一句 `ElMessage.error(msg)`，两个问题：
 *     ① 3 秒后自动消失，操作慢一点就错过；
 *     ② 拿不到「哪个接口、什么状态码」——404 时前端只能显示
 *        `Request failed with status code 404`（axios 造句的话），看不出是哪条路径对不上。
 *   另外页面里大量 catch 是有意静默的（无主机时下拉为空之类），
 *   但**未被捕获**的 JS 异常与 Promise 拒绝原先完全没有出口，界面表现为「点了没反应」。
 *
 * 职责：
 *   1. describeApiError(err)：把 axios 错误规整成 { kind, status, method, url, title, lines, text }；
 *   2. notifyApiError(err)：以「不自动消失 + 可手动关闭」的通知呈现；
 *   3. attachGlobalHooks()：兜住 window error / unhandledrejection（已被拦截器报过的 axios 错误不重复报）。
 *
 * 约束：不依赖 Vue 的 h()（渲染走 ElNotification 的 message + CSS white-space），
 * 通知条目上限 MAX_LIVE，超出关闭最早的一条，避免批量请求失败时糊满屏幕。
 */
(function () {
  'use strict'

  const MAX_LIVE = 5

  // 各 HTTP 状态在「后端没给 message」时的兜底解释——
  // 404 是本项目真实踩过的坑（前后端路径漂移），单独说清楚。
  const STATUS_HINT = {
    400: '请求被后端拒绝，通常是参数不合法',
    401: '未认证',
    403: '被后端拒绝',
    404: '接口不存在：前后端路径不一致，或该接口已下线',
    405: '方法不允许：该路径不接受这个 HTTP 方法',
    409: '状态冲突（同名已存在等）',
    500: '后端内部错误，详情见 data/app.log',
    502: '网关错误',
    503: '服务不可用',
    504: '网关超时'
  }

  const live = []

  function closeOldest() {
    while (live.length > MAX_LIVE) {
      const inst = live.shift()
      try { inst.close() } catch (e) { /* 实例可能已被用户关掉 */ }
    }
  }

  function fmtErr(err) {
    if (!err) return '未知错误'
    if (typeof err === 'string') return err
    if (err.stack) return String(err.message || '') + '\n' + String(err.stack)
    return String(err.message || JSON.stringify(err))
  }

  // describeApiError：纯函数，不碰 DOM，门禁直接对它做行为断言。
  function describeApiError(err) {
    err = err || {}
    const cfg = err.config || {}
    const method = String(cfg.method || 'get').toUpperCase()
    const url = cfg.url || '(未知地址)'
    const head = method + ' ' + url
    const resp = err.response

    if (resp) {
      const status = Number(resp.status || 0)
      const data = resp.data
      let serverMsg = ''
      if (data && typeof data === 'object') serverMsg = String(data.message || data.error || '')
      else if (typeof data === 'string') serverMsg = data.slice(0, 200)
      if (!serverMsg) serverMsg = STATUS_HINT[status] || ('HTTP ' + status + '（后端未返回说明）')
      return {
        kind: 'http',
        status,
        method,
        url,
        title: 'HTTP ' + status + ' · ' + head,
        lines: [serverMsg],
        text: 'HTTP ' + status + '\n' + head + '\n' + serverMsg
      }
    }

    // 没有 response = 请求根本没回来：超时的报错文案也归到这里。
    const code = String(err.code || '')
    if (code === 'ECONNABORTED' || /timeout/i.test(String(err.message || ''))) {
      return {
        kind: 'timeout',
        status: 0,
        method,
        url,
        title: '请求超时 · ' + head,
        lines: ['后端在超时时间内没有返回。若是结果集很大或导出/导入类操作，'
          + '该接口需要显式关闭 HTTP 超时（请求时传 { timeout: 0 }）。'],
        text: 'TIMEOUT\n' + head + '\n' + String(err.message || '')
      }
    }

    return {
      kind: 'network',
      status: 0,
      method,
      url,
      title: '无法连接后端 · ' + head,
      lines: ['请求没有到达后端进程。若应用刚启动、或后端已崩溃，界面上只会出现这一条。'],
      text: 'NETWORK\n' + head + '\n' + String(err.message || '')
    }
  }

  function notify(info) {
    if (typeof ElementPlus === 'undefined' || !ElementPlus.ElNotification) {
      // 兜底：通知组件都拿不到时，至少别把错误吞干净
      console.error('[error_notice] ' + info.title + '\n' + info.lines.join('\n'))
      return null
    }
    const inst = ElementPlus.ElNotification({
      title: info.title,
      message: info.lines.join('\n'),
      type: 'error',
      position: 'top-right',
      duration: 0,          // 不自动消失：没有 F12 时，错过这条就等于没报错
      showClose: true,
      customClass: 'infra-error-notice'
    })
    live.push(inst)
    closeOldest()
    return inst
  }

  function notifyApiError(err) {
    return notify(describeApiError(err))
  }

  function notifyPlain(title, lines) {
    return notify({ kind: 'js', status: 0, title: title, lines: lines || [] })
  }

  // attachGlobalHooks：未捕获异常的兜底出口。
  // window error 与 unhandledrejection 在页面里都不该被静默——原先它们只进 console，
  // 而桌面模式没有 console 可看，表现就是「界面毫无反应，不知道哪一步炸了」。
  function attachGlobalHooks() {
    if (typeof window.addEventListener !== 'function' || window.__infraErrorHooks) return
    window.__infraErrorHooks = true

    window.addEventListener('error', function (ev) {
      const file = String(ev.filename || '').split('/').pop()
      const where = file ? file + (ev.lineno ? ':' + ev.lineno : '') : ''
      notifyPlain('前端脚本异常' + (where ? ' · ' + where : ''),
        [String(ev.message || '未知错误')])
    })

    window.addEventListener('unhandledrejection', function (ev) {
      const r = ev.reason
      // 走 api 出来的错误已由拦截器报过，别重复弹两条
      if (r && (r.config || r.response)) return
      notifyPlain('未处理的异步错误', [fmtErr(r)])
    })
  }

  window.ErrorNotice = {
    MAX_LIVE: MAX_LIVE,
    STATUS_HINT: STATUS_HINT,
    describeApiError: describeApiError,
    notify: notify,
    notifyApiError: notifyApiError,
    notifyPlain: notifyPlain,
    attachGlobalHooks: attachGlobalHooks,
    // liveCount：当前在显示的通知条数。DOM 节点数不等于它（关闭后要等过渡结束才摘节点），
    // 排查「通知堆了几条」时以这个为准。
    liveCount: () => live.length
  }
})()
