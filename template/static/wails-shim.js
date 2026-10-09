/*
 * wails-shim.js：桌面模式（Wails v3）前端适配层。
 *
 * 职责：
 *  1. 环境探测：设置 window.__INFRA_DESKTOP__（桌面窗口为 true，浏览器调试为 false）；
 *     仅桌面环境接管 window.EventSource，浏览器访问保留原生（便于调试）。
 *  2. 运行时引导：桌面环境动态引入资产服务器内置的 Wails 运行时（/wails/runtime.js）。
 *  3. EventSource overlay：以同名 API 覆盖 window.EventSource，底层经 Wails 服务
 *     SSEBridge（desktop/sse_bridge.go）消费事件流，绕开资产服务器对长连接的
 *     转发限制（wailsapp/wails#2847）。现有页面 new EventSource(...) 调用零修改。
 *
 * 语义与浏览器 EventSource 对齐：命名事件分发、readyState、onerror 属性、
 * addEventListener/removeEventListener/close、断流 3 秒自动重连。
 * 终止条件对齐浏览器对非 text/event-stream 响应的处理：HTTP 200 但内容类型
 * 非 SSE（如后端 resp.Fail 的业务错误 JSON）或其他状态码时不重连。
 */
(function () {
  'use strict'

  const isDesktop = location.hostname === 'wails.localhost' ||
    (location.protocol !== 'http:' && location.protocol !== 'https:')
  window.__INFRA_DESKTOP__ = isDesktop
  if (!isDesktop) return

  // ---- Wails 运行时：动态引入（浏览器模式下避免 404 噪音） ----
  const runtimeScript = document.createElement('script')
  runtimeScript.type = 'module'
  runtimeScript.src = '/wails/runtime.js'
  document.head.appendChild(runtimeScript)

  const READY_TIMEOUT_MS = 30000
  const RECONNECT_DELAY_MS = 3000

  // Content-Type 判定：区分「SSE 流式响应」与「HTTP 200 + 业务错误 JSON」
  // （项目后端 resp.Fail 为 200 + 业务码，仅凭状态码无法判断）。
  function isSSEContentType(ct) {
    return String(ct || '').toLowerCase().indexOf('text/event-stream') >= 0
  }

  let runtimePromise = null
  function ensureRuntime() {
    if (runtimePromise) return runtimePromise
    const p = new Promise((resolve, reject) => {
      const deadline = Date.now() + READY_TIMEOUT_MS
      const poll = () => {
        const wails = window.wails
        if (wails && wails.Call && wails.Events) { resolve(wails); return }
        if (Date.now() > deadline) { reject(new Error('Wails runtime 未就绪')); return }
        setTimeout(poll, 50)
      }
      poll()
    })
    runtimePromise = p.catch((err) => { runtimePromise = null; throw err })
    return runtimePromise
  }

  // ---- SSE 帧解析：event: / data:（多行）/ id: / retry: / ": 注释" ----
  class SSEParser {
    constructor(onFrame) {
      this._onFrame = onFrame
      this._buffer = ''
      this._data = ''
      this._event = ''
      this._lastEventId = ''
      this._hasData = false
    }

    feed(chunk) {
      this._buffer += chunk
      let idx
      while ((idx = this._buffer.indexOf('\n')) >= 0) {
        let line = this._buffer.slice(0, idx)
        this._buffer = this._buffer.slice(idx + 1)
        if (line.endsWith('\r')) line = line.slice(0, -1)
        this._processLine(line)
      }
    }

    _processLine(line) {
      if (line === '') { this._dispatch(); return }
      if (line.startsWith(':')) return // 心跳注释（": ping"）
      const colon = line.indexOf(':')
      let field, value
      if (colon < 0) {
        field = line; value = ''
      } else {
        field = line.slice(0, colon)
        value = line.slice(colon + 1)
        if (value.startsWith(' ')) value = value.slice(1)
      }
      if (field === 'event') this._event = value
      else if (field === 'data') {
        this._data += (this._hasData ? '\n' : '') + value
        this._hasData = true
      } else if (field === 'id') this._lastEventId = value
      // retry 字段忽略（重连节拍固定 3s）
    }

    _dispatch() {
      if (!this._hasData) { this._event = ''; return }
      const type = this._event || 'message'
      const data = this._data
      const id = this._lastEventId
      this._data = ''; this._event = ''; this._hasData = false
      this._onFrame(type, data, id)
    }
  }

  // ---- 桥接版 EventSource（API 与浏览器 EventSource 对齐） ----
  class BridgeEventSource {
    constructor(url, options) {
      this.url = String(url)
      this.withCredentials = !!(options && options.withCredentials)
      this.readyState = BridgeEventSource.CONNECTING
      this.onopen = null
      this.onmessage = null
      this.onerror = null

      this._listeners = {}
      this._clientID = null
      this._unsubChunk = null
      this._unsubEnd = null
      this._closed = false
      this._retryTimer = null
      this._ended = false
      this._parser = new SSEParser((type, data, id) => this._emit(type, data, id))
      this._connect()
    }

    addEventListener(type, listener) {
      if (!listener) return
      ;(this._listeners[type] = this._listeners[type] || []).push(listener)
    }

    removeEventListener(type, listener) {
      const list = this._listeners[type]
      if (!list) return
      const i = list.indexOf(listener)
      if (i >= 0) list.splice(i, 1)
    }

    dispatchEvent(ev) {
      this._emit((ev && ev.type) || '', (ev && ev.data) || '')
      return true
    }

    close() {
      this._closed = true
      if (this._retryTimer) { clearTimeout(this._retryTimer); this._retryTimer = null }
      const clientID = this._clientID
      this._cleanup()
      if (clientID) this._callClose(clientID)
      this.readyState = BridgeEventSource.CLOSED
    }

    // ---- 内部 ----
    _connect() {
      this.readyState = BridgeEventSource.CONNECTING
      this._ended = false
      // clientID 由前端生成：先注册监听器再发起订阅，避免连接快照帧丢失
      const clientID = 'c' + Date.now().toString(36) + Math.random().toString(36).slice(2, 10)
      this._clientID = clientID
      ensureRuntime().then((wails) => {
        if (this._closed) return null
        this._unsubChunk = wails.Events.On('sse:' + clientID, (ev) => {
          this._parser.feed((ev && ev.data) || '')
        })
        this._unsubEnd = wails.Events.On('sse:' + clientID + ':end', (ev) => {
          const d = (ev && ev.data) || {}
          const status = typeof d.status === 'number' ? d.status : 0
          this._handleEnd(status, String(d.contentType || ''), false)
        })
        return wails.Call.ByName('infra-ops/desktop.SSEBridge.Open', clientID, this.url)
      }).then(() => {
        if (this._closed) { this._callClose(clientID); return }
        // end 事件先于 Open 响应到达（短流）或已重连为新 clientID：不复活为 OPEN
        if (this._ended || this._clientID !== clientID) return
        this.readyState = BridgeEventSource.OPEN
        this._emit('open', '')
      }).catch(() => {
        // 运行时未就绪 / 订阅调用失败：按本地失败处理（触发 error 后重试）
        if (this._closed || this._clientID !== clientID) return
        this._handleEnd(0, '', true)
      })
    }

    // 流结束：依据状态码与响应 Content-Type 决策是否重连。
    // 后端 resp.Fail 为「HTTP 200 + 业务错误 JSON」，仅凭状态码无法区分，
    // 故 200 时以 Content-Type 是否为 text/event-stream 判定（对齐浏览器 MIME 校验）：
    //   本地失败 / 状态未知        → 重连
    //   200 + SSE 流              → 服务端结束流，重连（浏览器断流语义）
    //   200 + 非 SSE（错误 JSON）  → 业务性失败，终止不重连
    //   其余状态码（401/404 等）   → 终止不重连
    _handleEnd(status, contentType, isLocalFailure) {
      this._ended = true
      this._cleanup()
      if (this._closed) return
      let reconnect
      if (isLocalFailure || status === 0) reconnect = true
      else if (status !== 200) reconnect = false
      else reconnect = isSSEContentType(contentType)
      if (reconnect) {
        this.readyState = BridgeEventSource.CONNECTING
        this._emit('error', '')
        this._retryTimer = setTimeout(() => {
          this._retryTimer = null
          this._connect()
        }, RECONNECT_DELAY_MS)
      } else {
        this.readyState = BridgeEventSource.CLOSED
        this._emit('error', '')
      }
    }

    _cleanup() {
      if (this._unsubChunk) { try { this._unsubChunk() } catch (e) { /* */ } this._unsubChunk = null }
      if (this._unsubEnd) { try { this._unsubEnd() } catch (e) { /* */ } this._unsubEnd = null }
    }

    _callClose(clientID) {
      try { window.wails.Call.ByName('infra-ops/desktop.SSEBridge.Close', clientID).catch(() => {}) } catch (e) { /* */ }
    }

    _emit(type, data, lastEventId) {
      const ev = {
        type: type,
        data: data || '',
        lastEventId: lastEventId || '',
        origin: location.origin,
        target: this,
        currentTarget: this,
      }
      let handlers = []
      if (type === 'open' && typeof this.onopen === 'function') handlers.push(this.onopen)
      if (type === 'message' && typeof this.onmessage === 'function') handlers.push(this.onmessage)
      if (type === 'error' && typeof this.onerror === 'function') handlers.push(this.onerror)
      const list = this._listeners[type]
      if (list && list.length) handlers = handlers.concat(list.slice())
      for (const fn of handlers) {
        try { fn.call(this, ev) } catch (e) { console.error('[wails-shim] SSE 事件处理异常:', e) }
      }
    }
  }

  BridgeEventSource.CONNECTING = 0
  BridgeEventSource.OPEN = 1
  BridgeEventSource.CLOSED = 2

  window.EventSource = BridgeEventSource

  // ---- F12 打开 DevTools ----
  // 为什么必须自己做（Windows 上 Wails 的三重限制，详见 desktop/devtools.go）：
  //  1. Wails 硬编码 PutAreBrowserAcceleratorKeysEnabled(false)，浏览器加速键被整体关掉，
  //     WebView2 自带的 F12 → DevTools 失效，按键被原生窗口吃掉；
  //  2. RegisterKeyBinding("f12") 在 Windows 上无效（windowKeyEvents 只有 darwin/linux 投递）；
  //  3. window.OpenDevTools() 在 production && !devtools 标签下是空函数。
  // 故唯一可行路径就是这里监听 keydown，经 Call.ByName 打到 Go 侧。
  // ⛔ 只能打开、不能切换：Wails 没暴露 CloseDevTools，重复调用只是把焦点挪过去。
  //    要关就点 DevTools 窗口的 ×，或用 Ctrl+Shift+I。
  function bindDevToolsKey() {
    window.addEventListener('keydown', function (ev) {
      if (ev.key !== 'F12' && ev.key !== 'F12'.toLowerCase && ev.code !== 'F12') return
      ev.preventDefault()
      ensureRuntime().then(function (wails) {
        return wails.Call.ByName('infra-ops/desktop.DevToolsService.Open')
      }).then(function (ok) {
        // Open 返回 false = 本构建未带 devtools 标签（release 常见），如实告知而不是让人干等
        if (ok === false) {
          console.warn('[wails] 本构建不含 DevTools 能力（未打 devtools 标签）；'
            + '需 DevTools 请用 script/build-desktop.ps1 重建，或用 -cdp 配合远程调试端口')
        }
      }).catch(function (err) {
        console.error('[wails] 打开 DevTools 失败:', err)
      })
    })
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', bindDevToolsKey)
  } else {
    bindDevToolsKey()
  }

  // ---- 无边框窗口控制（主窗口 Frameless，见 main.go） ----
  // 原生标题栏（含标题文字）已整体移除：窗口拖动由页面带 --wails-draggable: drag
  // 的区域承担（运行时注入的 drag 模块 → 原生 HTCAPTION 拖动；双击顶栏最大化在
  // app.js 里用 dblclick 调 ToggleMaximise）。这里把最小化 / 最大化(还原) / 关闭
  // 桥接为 window.__INFRA_WIN__（浏览器模式为 undefined，app.js 据此隐藏按钮），
  // 并把最大化状态同步到 <html data-win-maximised> —— CSS 据此切换「最大化/还原」
  // 图标（见 style.css），避开 Vue 侧订阅时序问题。
  function setupWindowControls() {
    ensureRuntime().then(function (wails) {
      const W = wails.Window
      if (!W) return
      let last = null
      function apply(v) {
        if (v === last) return
        last = v
        if (v) document.documentElement.setAttribute('data-win-maximised', '')
        else document.documentElement.removeAttribute('data-win-maximised')
      }
      function sync() { W.IsMaximised().then(apply).catch(function () {}) }
      // 最大化/还原（含系统层面：标题栏双击、Win+方向键、贴靠、任务栏菜单）都会经
      // Windows 侧 WM_SIZE 派生事件回到这里，触发重查询真实状态（事件只作触发器，
      // 状态以 IsMaximised 读数为准）
      ;['common:WindowMaximise', 'common:WindowUnMaximise', 'common:WindowRestore'].forEach(function (name) {
        try { wails.Events.On(name, sync) } catch (e) { /* 事件缺失时忽略 */ }
      })
      window.__INFRA_WIN__ = {
        minimise: function () { W.Minimise().catch(function () {}) },
        toggleMaximise: function () { W.ToggleMaximise().then(sync).catch(function () {}) },
        close: function () { W.Close().catch(function () {}) }
      }
      sync()
    }).catch(function () { /* 运行时未就绪：按钮保持无操作，不弹错 */ })
  }

  setupWindowControls()
})()
