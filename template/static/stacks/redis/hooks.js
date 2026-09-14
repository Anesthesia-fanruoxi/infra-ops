// Redis 套件 · 扩展点：成员角色文案、探活端点合成、主从「复制」列名。
// 逻辑自 pages/stacks.js 的 redis 分支原样迁入。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  P.register('redis', {
    hooks: {
      // 主从 / 哨兵模式下非主节点为「从」；集群模式交还骨架的「工作节点」
      memberRoleTag(isMaster) {
        if (isMaster) return ''
        return (this.mode === 'replication' || this.mode === 'sentinel') ? '从' : ''
      },
      // 探活接入地址：redis:// + 哨兵模式的 redis-sentinel://
      endpointFor(h, hp, p, pt) {
        const it = this.verifyTarget
        const isMaster = h.role === 'master'
        const name = (it.mode === 'replication' || it.mode === 'sentinel')
          ? (isMaster ? 'Redis 主' : 'Redis 从')
          : 'Redis'
        const out = [{ name, url: 'redis://' + h.host_ip + ':' + pt, role: h.role, host_ip: h.host_ip, host_name: h.host_name }]
        if (it.mode === 'sentinel') {
          out.push({ name: 'Sentinel', url: 'redis-sentinel://' + h.host_ip + ':' + (hp.sentinel_port || p.sentinel_port || '26379'), role: h.role, host_ip: h.host_ip, host_name: h.host_name })
        }
        return out
      },
      // 节点结果表的实例列：redis 下语义为「复制」
      verifyCountLabel() { return '复制' }
    }
  })
})()
