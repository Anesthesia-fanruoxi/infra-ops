// 套件部署页 · 通用骨架 · 探活对话框：端点聚合与节点结果表（套件差异经 hooks 注入）
// 迁自 pages/stacks.js（逐字保留），套件专属分支改为按槽位分发。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  // 源文件模块级助手（逗号列表 → 数组）：骨架统一提供在 StacksParts.splitList
  const splitList = P.splitList
  // 组件页签过滤：探活结果里既无端点、也无检查项的组件会渲染成空壳页签
  // （如 bigdata 的 metastore_db 是运行期组件，其容器 hive-metastore-db 归属 hive）。
  // 仅当结果中确实带有组件归属信息时才过滤；无归属信息（整体探活失败/套件未打归属）时
  // 原样返回声明清单，保证任何套件都不会因探活中断而丢掉全部页签。
  function probeAttributed(ctx, list) {
    const keys = new Set()
    ;(ctx.verifyResult?.endpoints || []).forEach(e => e.component && keys.add(e.component))
    ;(ctx.verifyResult?.hosts || []).forEach(h => (h.checks || []).forEach(c => c.component && keys.add(c.component)))
    return keys.size ? list.filter(c => keys.has(c.key)) : list
  }
  P.mixins.push({
    computed: {
    verifyParams() {
      try { return JSON.parse(this.verifyTarget?.params_json || '{}') || {} } catch (e) { return {} }
    },
    verifyPassword() { return (this.verifyParams.password || '').trim() },
    verifyEndpoints() {
      const it = this.verifyTarget
      const extractIP = (url) => { const m = url && url.match(/(\d+\.\d+\.\d+\.\d+)/); return m ? m[1] : url }
      const inferRole = (name) => { if (/主\b|master/i.test(name)) return 'master'; if (/从\b|replica|slave/i.test(name)) return 'replica'; return '' }
      if (!it) return (this.verifyResult?.endpoints || []).map(ep => ({ name: ep.name, component: ep.component, url: ep.url, role: ep.role || inferRole(ep.name), host_ip: extractIP(ep.url) }))
      if (this.verifyResult?.endpoints?.length) {
        return this.verifyResult.endpoints.map(ep => ({ name: ep.name, component: ep.component, url: ep.url, role: ep.role || inferRole(ep.name), host_ip: extractIP(ep.url) }))
      }
      const hosts = (it.hosts || []).filter(h => h.status !== 'removed')
      const p = this.verifyParams
      const port = p.port || '6379'
      const out = []
      hosts.forEach(h => {
        let hp = {}
        try { hp = JSON.parse(h.params_json || '{}') || {} } catch (e) { hp = {} }
        const pt = hp.port || port
        // 套件专属端点合成（redis 双协议 / bigdata 按组件拆分）；未实现则走通用 tcp
        const hk = this.hook(it.stack_key, 'endpointFor')
        const eps = hk ? hk.call(this, this, h, hp, p, pt) : null
        if (eps) { eps.forEach(e => out.push(e)); return }
        out.push({ name: (this.roleLabel(h.role) || '节点'), url: 'tcp://' + h.host_ip, role: h.role, host_ip: h.host_ip, host_name: h.host_name })
      })
      return out
    },
    verifyEndpointsByHost() {
      const eps = this.verifyEndpoints
      const map = new Map()
      eps.forEach(ep => {
        const key = ep.host_ip || ep.url
        if (!map.has(key)) {
          map.set(key, { host_ip: key, host_name: ep.host_name || key, role: ep.role || '', endpoints: [] })
        }
        map.get(key).endpoints.push(ep)
      })
      return Array.from(map.values())
    },
    verifyHostRows() {
      return (this.verifyResult?.hosts || []).map(h => {
        const check = (name) => (h.checks || []).find(c => c.name === name)
        const roleRaw = (h.live_role || check('角色')?.detail || h.role || '').toString()
        const isMaster = /^master\b/i.test(roleRaw) || roleRaw === 'master'
        const isReplica = /^(slave|replica|worker)\b/i.test(roleRaw) || roleRaw === 'replica' || roleRaw === 'slave' || roleRaw === 'worker'
        let role = roleRaw.split('·')[0].trim() || this.roleLabel(h.role) || '-'
        if (isMaster) role = 'master'
        else if (isReplica) role = roleRaw === 'worker' || h.role === 'worker' ? 'worker' : 'replica'
        const replCheck = check('复制')
        const instCheck = check('实例')
        const roleDetail = check('角色')?.detail || ''
        let replication = ''
        if (instCheck?.detail) {
          replication = instCheck.detail
        } else if (isReplica || role === 'replica') {
          const m = roleDetail.match(/跟随\s+(\S+)/)
          if (m) replication = m[1]
          else if (replCheck?.detail) {
            const link = replCheck.detail.match(/master_link_status=(\S+)/)
            replication = link ? link[1] : replCheck.detail
          }
        } else if (replCheck?.detail) {
          replication = replCheck.detail
        }
        const verDetail = check('版本')?.detail || ''
        let version = verDetail, uptime = ''
        if (verDetail.includes('·')) {
          const parts = verDetail.split('·').map(s => s.trim())
          version = parts[0] || ''
          uptime = parts.slice(1).join(' · ')
        }
        return {
          host_id: h.host_id,
          ip: h.host_ip,
          host_name: h.host_name,
          ok: !!h.ok,
          role,
          replication,
          instList: replication.includes(',') ? splitList(replication) : [],
          version: version || '-',
          uptime: uptime || '-',
          uptimeList: uptime ? splitList(uptime) : []
        }
      })
    },
    },
    methods: {
    async openVerifyDialog(it) {
      this.verifyVisible = true
      this.verifyResult = null
      this.verifyTarget = it
      try {
        const d = await api.get('/stacks/instances/' + it.id)
        if (d.code === 0) this.verifyTarget = d.data
      } catch (e) { /* */ }
      await this.runVerify()
    },
    async runVerify() {
      const it = this.verifyTarget
      if (!it || it.status === 'uninstalled') return
      this.verifying = true
      try {
        const r = await api.post('/stacks/instances/' + it.id + '/verify', {}, { timeout: 120000 })
        if (r.code !== 0) { ElMessage.error(r.message || '探活失败'); return }
        this.verifyResult = r.data
        if (!r.data?.ok) ElMessage.warning(r.data?.summary || '探活未通过')
      } catch (e) {
        ElMessage.error(e.message || '探活失败')
      } finally { this.verifying = false }
    },
    },
  })

  P.mixins.push({
    // 模板「裸引用」（v-if / v-for / :data / :label / {{ }}）的挂点必须是 computed：
    // Vue 把 methods 绑定成 bound function，裸引用拿到的是函数对象——v-for/:data 渲染为空、
    // {{}}/:label 直接打印 function () { [native code] }。无参挂点一律放 computed，
    // 只有需要在模板里显式传参的（verifyHostRole(hg)）才留 methods。门禁见 script/_fe_smoke.js 第 9 项。
    computed: {
    verifyTabbed() { const h = this.hook(this.verifyTarget?.stack_key, 'verifyTabbed'); return h ? !!h.call(this, this) : false },
    // —— 套件专属展示的分发点（探活）——
    verifyComponents() {
      const h = this.hook(this.verifyTarget?.stack_key, 'verifyComponents')
      return probeAttributed(this, h ? (h.call(this, this) || []) : [])
    },
    currentVerifyCompLabel() { const h = this.hook(this.verifyTarget?.stack_key, 'currentVerifyCompLabel'); return h ? h.call(this, this) : '' },
    verifyCompEndpoints() { const h = this.hook(this.verifyTarget?.stack_key, 'verifyCompEndpoints'); return h ? h.call(this, this) : [] },
    verifyCompEndpointsByHost() { const h = this.hook(this.verifyTarget?.stack_key, 'verifyCompEndpointsByHost'); return h ? h.call(this, this) : [] },
    verifyCompHostRows() { const h = this.hook(this.verifyTarget?.stack_key, 'verifyCompHostRows'); return h ? h.call(this, this) : [] },
    verifyCountLabel() { const h = this.hook(this.verifyTarget?.stack_key, 'verifyCountLabel'); return (h && h.call(this, this)) || '实例' },
    verifyCompCountLabel() { const h = this.hook(this.verifyTarget?.stack_key, 'verifyCompCountLabel'); return (h && h.call(this, this)) || '实例' },
    },
    watch: {
      // 切实例或重新探活后，活动页签若已被过滤掉（组件在本实例不存在 / 无归属），回落到「查看所有」
      verifyComponents(list) {
        if (this.verifyActiveTab !== 'all' && !(list || []).some(c => c.key === this.verifyActiveTab)) this.verifyActiveTab = 'all'
      },
    },
    methods: {
    verifyHostRole(hg) { const h = this.hook(this.verifyTarget?.stack_key, 'verifyHostRole'); return h ? (h.call(this, this, hg) || '') : '' },
    }
  })
})()
