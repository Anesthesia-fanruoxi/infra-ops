// 套件部署页 · 通用骨架 · 通用文案与格式化：标签映射、日志时间、探活协议徽标、复制
// 迁自 pages/stacks.js（逐字保留），套件专属分支改为按槽位分发。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  // 源文件模块级助手（逗号列表 → 数组）：骨架统一提供在 StacksParts.splitList
  const splitList = P.splitList
  P.mixins.push({
    methods: {
    escHtml(s) { return String(s == null ? '' : s).replace(/[&<>"]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c])) },
    tipHtml(list) {
      const arr = (list || []).map(x => String(x || '').trim()).filter(Boolean)
      if (!arr.length) return ''
      return '<div class="stk-vf-tip-list">' + arr.map(x => this.escHtml(x)).join('<br>') + '</div>'
    },
    opLabel(op) { return { create: '创建', reinstall: '重装', scale_out: '扩容', scale_in: '缩容', add_component: '加装', uninstall: '卸载', remove_component: '卸组件' }[op] || op || '创建' },
    modeLabel(m, stackKey) {
      const key = stackKey || this.recordMeta?.stack_key || this.selectedKey
      const s = this.stacks.find(x => x.key === key)
      const md = s && s.modes ? s.modes.find(x => x.key === m) : null
      if (md && md.label) return md.label
      return { replication: '主从', sentinel: '哨兵', cluster: '集群', standalone: '单机', single: '单机', ha: '高可用', kraft: 'KRaft 集群', zk: 'ZK 集群' }[m] || m
    },
    roleLabel(r) { return { master: '主', replica: '从', node: '节点', worker: '工作节点' }[r] || r },
    phaseName(p) { return { prereq: 'Docker', node: '节点', bootstrap: '初始化' }[p] || p || '' },
    phaseText(s) { return { pending: '等待', running: '进行', success: '成功', failed: '失败', skipped: '跳过' }[s] || s },
    phaseTag(s) { if (s === 'success' || s === 'skipped') return 'success'; if (s === 'failed') return 'danger'; if (s === 'running') return 'warning'; return 'info' },
    // 流水线步骤条 tooltip：状态 + 执行范围
    runStepTitle(s) {
      const scope = s.target === 'leader' ? '仅主节点' : '全部主机'
      return (s.label || s.key) + ' · ' + this.hostStatusText(s.status) + ' · ' + scope
    },
    // 主机视图组件芯片的状态文案（与运行步骤状态同词汇）
    compStatusText(s) { return { pending: '等待', running: '进行中', success: '完成', failed: '失败', skipped: '跳过' }[s] || s },
    // 角色计划生成时间：RFC3339（2026-09-11T14:39:21+08:00）→ 界面统一口径 2026-09-11 14:39:21
    planTime(t) { return t ? String(t).replace('T', ' ').replace(/(Z|[+-]\d{2}:\d{2})$/, '') : '' },
    hostStatusCls(s) { if (s === 'success') return 'online'; if (s === 'failed') return 'offline'; if (s === 'running') return 'running'; return 'unverified' },
    hostStatusText(s) { return { pending: '等待中', running: '执行中', success: '成功', failed: '失败', skipped: '跳过' }[s] || s },
    logTime(ts) { return ts ? (ts.length >= 19 ? ts.slice(11, 19) : ts) : '' },
    taskTagType(s) { if (s === 'success') return 'success'; if (s === 'failed' || s === 'partial') return 'danger'; if (s === 'running') return 'warning'; return 'info' },
    taskStatusLabel(s) { return { running: '执行中', success: '已完成', partial: '部分成功', failed: '失败' }[s] || s },
    formatTime(t) {
      if (!t) return '-'
      const d = new Date(t.replace(' ', 'T'))
      return d.getFullYear() + '-' + String(d.getMonth()+1).padStart(2,'0') + '-' + String(d.getDate()).padStart(2,'0') + ' ' + String(d.getHours()).padStart(2,'0') + ':' + String(d.getMinutes()).padStart(2,'0')
    },
    epProtocolLabel(url) {
      if (!url) return 'TCP'
      if (url.startsWith('redis-sentinel://')) return 'SENTINEL'
      if (url.startsWith('redis://')) return 'REDIS'
      if (url.startsWith('http://') || url.startsWith('https://')) return 'HTTP'
      if (url.startsWith('zk://')) return 'ZK'
      if (url.startsWith('mongodb://')) return 'MONGO'
      if (url.startsWith('kafka://')) return 'KAFKA'
      if (url.startsWith('rocketmq://')) return 'MQ'
      return 'TCP'
    },
    epProtocolClass(url) {
      const p = this.epProtocolLabel(url).toLowerCase()
      return 'proto-' + p
    },
    copyText(text) {
      if (!text) return
      if (navigator.clipboard) {
        navigator.clipboard.writeText(text).then(() => ElMessage.success('已复制')).catch(() => {})
      } else {
        const ta = document.createElement('textarea'); ta.value = text; document.body.appendChild(ta); ta.select()
        try { document.execCommand('copy'); ElMessage.success('已复制') } catch (e) { /* */ }
        document.body.removeChild(ta)
      }
    },
    },
  })
})()
