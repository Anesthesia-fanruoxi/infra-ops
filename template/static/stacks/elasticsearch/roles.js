// Elasticsearch 套件 · 冷热温节点角色勾选（原 StackFormElasticsearchRoles）。
// 角色互斥规则与后端 validateCWHRoles 一致，逻辑原样迁入。
;(function () {
  const ES_ROLES = [
    { key: 'master', label: 'master 候选', cls: 'stk-es-master', hint: '参与选举，隐式协调，不存数据' },
    { key: 'coordinator', label: '纯协调', cls: 'stk-es-coord', hint: '仅负载均衡与聚合（node.roles 为空）' },
    { key: 'data_hot', label: '数据-hot', cls: 'stk-es-hot', hint: '高 IOPS 热层' },
    { key: 'data_warm', label: '数据-warm', cls: 'stk-es-warm', hint: '中频访问层' },
    { key: 'data_cold', label: '数据-cold', cls: 'stk-es-cold', hint: '低频归档层' }
  ]
  const isData = k => k.startsWith('data_')

  window.StackFormElasticsearchRoles = {
    props: ['ctx'],
    watch: {
      // 已选主机变化时，为 create 流程中尚未指定角色的主机补齐默认角色：
      // 首台 master（canStep3 锚点，避免"选完主机无法下一步"），第 2 台纯协调，其余留空待勾数据层。
      selectedIds(val, old) {
        if (!this.useDefault) return
        const none = (!old || !old.size) ? val : new Set([...val].filter(id => !old.has(id)))
        if (!none.size) return
        let coord = 0
        let anyMaster = this.hosts.some(h => this.rolesOf(h.id).includes('master'))
        this.hosts.forEach(h => {
          if (!none.has(h.id)) return
          if (this.rolesOf(h.id).length) return
          if (!anyMaster) { this.setRoles(h.id, ['master']); anyMaster = true; return }
          if (coord >= 1) return
          this.setRoles(h.id, ['coordinator'])
          coord++
        })
      }
    },
    computed: {
      selectedIds() { return new Set(this.ctx.selectedHostIds || []) },
      visible() {
        const ctx = this.ctx
        return ctx.selectedKey === 'elasticsearch' && ctx.mode === 'cold_warm_hot' &&
          (ctx.wizardOp === 'create' || ctx.wizardOp === 'scale_out' || ctx.wizardOp === 'add_component')
      },
      hosts() { return this.visible ? this.ctx.selectedHosts : [] },
      defs() { return ES_ROLES },
      useDefault() { return this.ctx.wizardOp === 'create' },
      // 无 hostParams 且未设定时（create 首两台之外的存量主机）读取后端的 coordinator 兜底
      roleCounts() {
        const c = { master: 0, coordinator: 0, data_hot: 0, data_warm: 0, data_cold: 0 }
        this.hosts.forEach(h => this.rolesOf(h.id).forEach(k => { if (k in c) c[k]++ }))
        return c
      },
      // 缺内容提示：无 master 候选 / 无数据层节点
      warnings() {
        const out = []
        if (this.roleCounts.master === 0) out.push('尚未指定 master 候选主机（至少 1 台，生产建议 ≥3 台奇数），无法进入下一步')
        if (this.roleCounts.data_hot + this.roleCounts.data_warm + this.roleCounts.data_cold === 0) {
          out.push('尚未指定数据层节点（hot/warm/cold 至少 1 台），否则集群无法存储数据')
        }
        if (this.hosts.some(h => this.rolesOf(h.id).some(isData))) {
          out.push('多数据层叠加仅建议资源充足的大规格主机选用，规格一般时建议每台只承担单一数据层（避免 I/O 冲突）')
        }
        return out
      }
    },
    methods: {
      rolesOf(id) {
        const hp = this.ctx.hostParams && this.ctx.hostParams[id]
        if (!hp || !hp.roles) return []
        return String(hp.roles).split(',').map(s => s.trim()).filter(Boolean)
      },
      setRoles(id, roles) {
        this.ctx.onHostParam(id, 'roles', roles.join(','))
      },
      // master 与数据层 / 协调互斥：返回当前是否应禁用某角色的 checkbox
      disabledOf(h, def) {
        const rs = this.rolesOf(h.id)
        if (!rs.length) return false
        if (def.key === 'master') return rs.some(r => r === 'coordinator' || isData(r))
        if (def.key === 'coordinator') return rs.some(r => r === 'master' || isData(r))
        return rs.includes('master') || rs.includes('coordinator')
      },
      selectedOf(h, def) { return this.rolesOf(h.id).includes(def.key) },
      toggleRole(h, def, val) {
        const rs = this.rolesOf(h.id).slice()
        let next
        if (!val) {
          next = rs.filter(r => r !== def.key)
        } else if (def.key === 'master') {
          next = ['master']
        } else if (def.key === 'coordinator') {
          next = ['coordinator']
        } else {
          // 数据层：清掉 master/协调，叠加多种数据 tier
          next = rs.filter(isData)
          if (!next.includes(def.key)) next.push(def.key)
        }
        this.setRoles(h.id, next)
      }
    },
    template: `
<div v-if="visible" class="stk-es-role-plan">
  <div class="deploy-host-vars-title">节点角色 <span class="deploy-var-hint">master / 纯协调 / 数据层三者其一，数据层可叠加 hot+warm+cold</span></div>
  <div class="stk-es-role-counts">
    <span>master 候选 <b>{{roleCounts.master}}</b></span>
    <span>纯协调 <b>{{roleCounts.coordinator}}</b></span>
    <span>数据-hot <b>{{roleCounts.data_hot}}</b></span>
    <span>数据-warm <b>{{roleCounts.data_warm}}</b></span>
    <span>数据-cold <b>{{roleCounts.data_cold}}</b></span>
  </div>
  <div class="stk-es-role-rows" v-if="hosts.length">
    <div class="stk-es-role-row" v-for="h in hosts" :key="h.id">
      <div class="stk-es-role-host">
        <span class="stk-es-role-hostname">{{h.name}}</span>
        <span class="mono stk-es-role-hostip">{{h.ip}}</span>
      </div>
      <div class="stk-es-role-checks">
        <el-checkbox
          v-for="d in defs" :key="d.key" size="small"
          :model-value="selectedOf(h, d)"
          :disabled="disabledOf(h, d)"
          @change="val => toggleRole(h, d, val)"
        ><span :class="'stk-es-role-chip ' + d.cls">{{d.label}}</span></el-checkbox>
      </div>
      <span v-if="rolesOf(h.id).filter(isData).length > 1" class="stk-es-role-io-warn">I/O 叠加</span>
    </div>
  </div>
  <div v-else class="faint stk-es-role-empty">请先在主机列表中勾选参与节点</div>
  <div v-if="warnings.length" class="stk-es-role-warnings">
    <div v-for="(w, i) in warnings" :key="i" class="stk-es-role-warn">{{w}}</div>
  </div>
</div>`
  }
})()

// 组件自注册（StacksParts.register 合并语义）：套件内 <script> 顺序不影响装配。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  P.register('elasticsearch', { components: { 'stack-form-elasticsearch-roles': window.StackFormElasticsearchRoles } })
})()
