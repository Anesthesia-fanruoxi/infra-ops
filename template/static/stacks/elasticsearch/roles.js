// Elasticsearch 套件 · 第 2 步套件表单（规格档位 + 冷热温节点角色矩阵）。
//
// 两块内容：
//   1) 规格档位（两模式通用）：三档内部预设（小额尝鲜 / 标准使用 / 大力出奇迹）。
//      档位定义「每个角色的 JVM 堆 + 内存建议 + 基本数量」与单 hot 节点性能基准
//      （日增/写入/QPS 为单节点量级参考，集群能力 ≈ n × 该值，n 由用户定，界面不做推算）。
//      数字**不是**前端硬编码，
//      而是读蓝图 extras.sizing（后端 sizing.go 是唯一事实源，与部署期注入同源），
//      所以界面显示多少、容器就按多少定堆，不会漂移。选档位写 ctx.sharedParams.sizing；
//      第 3 步经 index.js 的 visibleSharedVars 过滤，不重复展示。
//   2) 节点角色矩阵（仅冷热温）：每个勾选格 = 该主机一个独立 ES 容器（每容器单一角色）：
//      - master 与纯协调可同机叠加（两容器，master 候选本身隐式协调）；
//      - 数据层 hot/warm/cold 可叠加（各自独立端口/目录，端口按角色固定偏移）；
//      - 数据层与 master/协调互斥（不混部，双向置灰）；
//      - 行不勾选 = 自动模式：矩阵保持为空，部署时自动归为数据节点（后端 cwhHostRoles 兜底），
//        角色计划预览中显示为灰色「自动分配（数据节点）」；
//      - 数据三列列头带全选（跳过 master/协调行，取消会把非占用行的该列一并去掉）；
//      - create 首台自动补 master 锚点，其余新勾选主机默认不勾（自动模式）。
// 互斥规则与后端 validateCWHRoles 同源，兜底口径与 vars.go cwhDataRoles 同源。
;(function () {
  const ES_ROLES = [
    { key: 'master', label: 'master 候选', cls: 'stk-es-master', hint: '参与选举，隐式协调，不存数据' },
    { key: 'coordinator', label: '纯协调', cls: 'stk-es-coord', hint: '仅负载均衡与聚合（node.roles 为空）' },
    { key: 'data_hot', label: '数据-hot', cls: 'stk-es-hot', hint: '高 IOPS 热层' },
    { key: 'data_warm', label: '数据-warm', cls: 'stk-es-warm', hint: '中频访问层' },
    { key: 'data_cold', label: '数据-cold', cls: 'stk-es-cold', hint: '低频归档层' }
  ]
  const isData = k => k.startsWith('data_')
  // 角色 chip 样式（规格表复用矩阵同一套配色，避免第二套词表）
  const clsOf = k => (ES_ROLES.find(r => r.key === k) || {}).cls || ''

  window.StackFormElasticsearchRoles = {
    props: ['ctx'],
    watch: {
      // 新勾选主机的默认落位：create 首台补 master 锚点（canStep3 依赖），
      // 其余一律不勾 —— 空行 = 自动模式（部署时自动归数据节点，预览显示灰色「自动分配」）；
      // 扩容 / 加装的新增主机同样默认不勾，需要手动指定时再勾选。
      // 集群模式不写 roles 主机参数（该参数是冷热温专属），故先做 visible 守卫。
      selectedIds(val, old) {
        if (!this.isCwh || !this.visible) return
        const none = (!old || !old.size) ? val : new Set([...val].filter(id => !old.has(id)))
        if (!none.size) return
        let anyMaster = this.hosts.some(h => this.rolesOf(h.id).includes('master'))
        this.hosts.forEach(h => {
          if (!none.has(h.id) || this.rolesOf(h.id).length) return
          if (this.useDefault && !anyMaster) { this.setRoles(h.id, ['master']); anyMaster = true }
        })
      }
    },
    computed: {
      selectedIds() { return new Set(this.ctx.selectedHostIds || []) },
      mode() { return this.ctx.mode },
      isCwh() { return this.ctx.mode === 'cold_warm_hot' },
      // 两模式都要选规格档位（集群模式只有档位卡，没有角色矩阵）
      visible() {
        const ctx = this.ctx
        return ctx.selectedKey === 'elasticsearch' && (this.isCwh || ctx.mode === 'cluster') &&
          (ctx.wizardOp === 'create' || ctx.wizardOp === 'scale_out' || ctx.wizardOp === 'add_component')
      },
      hosts() { return this.visible ? this.ctx.selectedHosts : [] },
      defs() { return ES_ROLES },
      useDefault() { return this.ctx.wizardOp === 'create' },
      // —— 规格档位（数据源：蓝图 extras.sizing，后端 sizing.go 唯一事实源）——
      sizingProfiles() {
        const ex = this.ctx.selectedStack?.extras || {}
        return Array.isArray(ex.sizing) ? ex.sizing : []
      },
      sizing() { return this.ctx.sharedParams?.sizing || 'standard' },
      curProfile() { return this.sizingProfiles.find(p => p.key === this.sizing) || this.sizingProfiles[1] || null },
      // 集群模式节点同时承担 master + data，按数据节点档取值，故只展示数据-hot 一行
      sizingRoles() {
        const roles = this.curProfile?.roles || []
        return this.isCwh ? roles : roles.filter(r => r.role === 'data_hot')
      },
      containerCount() { return this.hosts.reduce((n, h) => n + this.rolesOf(h.id).length, 0) },
      // 未勾选任何角色的主机（自动模式）：将按数据节点部署，预览中显示为灰色「自动分配」
      autoRows() { return this.hosts.filter(h => !this.rolesOf(h.id).length).length },
      roleCounts() {
        const c = { master: 0, coordinator: 0, data_hot: 0, data_warm: 0, data_cold: 0 }
        this.hosts.forEach(h => this.rolesOf(h.id).forEach(k => { if (k in c) c[k]++ }))
        return c
      },
      warnings() {
        const out = []
        if (!this.isCwh) return out // 集群模式的落点校验由通用层负责
        if (this.roleCounts.master === 0) out.push('尚未指定 master 候选容器（至少 1 台，生产建议 ≥3 台奇数），无法进入下一步')
        const dataN = this.roleCounts.data_hot + this.roleCounts.data_warm + this.roleCounts.data_cold
        if (dataN === 0 && this.autoRows === 0) out.push('尚未指定数据层容器（hot/warm/cold 至少 1 个），否则集群无法存储数据')
        const layered = this.hosts.filter(h => this.rolesOf(h.id).filter(isData).length > 1)
        if (layered.length) {
          out.push(layered.length + ' 台主机叠加多个数据层容器（' + layered.map(h => h.name).join('、') + '）：仅建议资源充足的大规格主机选用，避免 I/O 冲突')
        }
        // 一机多容器时最容易踩的坑：同机容器的堆加起来超过整机内存（脚本侧也会再校验一次）
        const stacked = this.hosts.filter(h => this.rolesOf(h.id).length > 1)
        if (stacked.length && this.curProfile) {
          out.push(stacked.length + ' 台主机承载多个容器（' + stacked.map(h => h.name).join('、') + '）：请确认这些主机的内存覆盖各容器之和（' + this.curProfile.label + '档见下方规格表「内存」列，已按角色给出口径）')
        }
        return out
      }
    },
    methods: {
      isData,
      chipCls: clsOf,
      pickSizing(key) { this.ctx.sharedParams.sizing = key },
      rolesOf(id) {
        const hp = this.ctx.hostParams && this.ctx.hostParams[id]
        if (!hp || !hp.roles) return []
        return String(hp.roles).split(',').map(s => s.trim()).filter(Boolean)
      },
      setRoles(id, roles) {
        this.ctx.onHostParam(id, 'roles', roles.join(','))
      },
      // 双向互斥：本行已占 master/协调 → 数据列置灰；已占数据层 → master/协调置灰
      disabledOf(h, def) {
        const rs = this.rolesOf(h.id)
        if (!rs.length) return false
        return isData(def.key) ? rs.some(r => !isData(r)) : rs.some(isData)
      },
      selectedOf(h, def) { return this.rolesOf(h.id).includes(def.key) },
      toggleRole(h, def, val) {
        const rs = this.rolesOf(h.id).slice()
        let next
        if (!val) {
          next = rs.filter(r => r !== def.key)        // 允许清空：该行回到自动模式（不加兜底）
        } else if (isData(def.key)) {
          next = rs.filter(isData)                     // 勾数据层：清掉 master/协调（互斥）
          if (!next.includes(def.key)) next.push(def.key)
        } else {
          next = rs.filter(r => !isData(r))            // 勾 master/协调：清掉数据层（互斥），二者可叠加
          if (!next.includes(def.key)) next.push(def.key)
        }
        this.setRoles(h.id, next)
      },
      // 数据列全选：勾选所有非 master/协调行（自动跳过占用行）；
      // 取消（val=false）去掉非占用行该列，行被清空则回到自动模式。
      colChecked(def) {
        if (!this.hosts.length) return false
        return this.hosts.every(h => {
          const rs = this.rolesOf(h.id)
          return rs.some(r => !isData(r)) || rs.includes(def.key)
        })
      },
      colIndeterminate(def) {
        const some = this.hosts.some(h => {
          const rs = this.rolesOf(h.id)
          return !rs.some(r => !isData(r)) && rs.includes(def.key)
        })
        return some && !this.colChecked(def)
      },
      toggleCol(def, val) {
        this.hosts.forEach(h => {
          const rs = this.rolesOf(h.id)
          if (rs.some(r => !isData(r))) return
          if (val) {
            if (!rs.includes(def.key)) this.setRoles(h.id, rs.concat(def.key))
          } else if (rs.includes(def.key)) {
            this.setRoles(h.id, rs.filter(r => r !== def.key))
          }
        })
      }
    },
    template: `
<div v-if="visible" class="stk-es-role-plan">
  <div class="stk-es-sizing">
    <div class="deploy-host-vars-title">规格档位 <span class="deploy-var-hint">档位定义每个角色的 JVM 参数、内存建议与基本数量，性能基准为单 hot 节点量级——机器数量与硬件规格由你按实际资源自定</span></div>
    <div v-if="sizingProfiles.length" class="stk-es-sizing-cards">
      <label v-for="p in sizingProfiles" :key="p.key" class="stk-es-sizing-card" :class="{'stk-es-sizing-card--sel': sizing===p.key}">
        <el-radio :model-value="sizing" :label="p.key" @change="pickSizing(p.key)">{{p.label}}</el-radio>
        <p>{{p.summary}}</p>
      </label>
    </div>
    <div v-else class="faint" style="font-size:12px">规格档位表未下发（请刷新页面重取套件清单）</div>
    <div v-if="curProfile" class="stk-es-sizing-body">
      <div class="stk-es-sizing-line"><b>适用</b>{{curProfile.fit}}</div>
      <div class="stk-es-sizing-caps">
        <span>单 hot 日增 <b>{{curProfile.hot_daily_gb}}</b></span>
        <span>单 hot 写入 <b>{{curProfile.hot_write_mbps}}</b></span>
        <span>查询 <b>{{curProfile.query_qps}}</b></span>
      </div>
      <table class="stk-es-sizing-table">
        <thead>
          <tr><th>角色</th><th>JVM 堆</th><th>内存建议</th><th>基本数量</th></tr>
        </thead>
        <tbody>
          <tr v-for="r in sizingRoles" :key="r.role">
            <td><span :class="'stk-es-role-chip ' + chipCls(r.role)">{{r.label}}</span></td>
            <td class="stk-es-sizing-heap">{{r.heap_gb}}g</td>
            <td class="stk-es-sizing-mem">{{r.mem_gb}} GB<span v-if="r.mem_rule" class="faint">（{{r.mem_rule}}）</span></td>
            <td class="faint">{{r.ref}}</td>
          </tr>
        </tbody>
      </table>
      <div v-if="curProfile.mem_note" class="stk-es-sizing-note">{{curProfile.mem_note}}</div>
      <div class="stk-es-sizing-note">{{curProfile.note}}</div>
      <div v-if="!isCwh" class="stk-es-sizing-note">集群模式的每个节点同时承担 master 与 data，按上表数据节点档取值。</div>
    </div>
  </div>

  <template v-if="isCwh">
    <div class="deploy-host-vars-title">节点角色矩阵 <span class="deploy-var-hint">每格勾选 = 部署一个对应角色的 ES 容器；master 与纯协调可同机叠加，数据层与两者互斥；行不勾选 = 自动模式（按数据节点部署）</span></div>
    <div class="stk-es-role-counts">
      <span>容器 <b>{{containerCount}}</b></span>
      <span v-if="autoRows">自动分配 <b>{{autoRows}}</b> 台</span>
      <span>master 候选 <b>{{roleCounts.master}}</b></span>
      <span>纯协调 <b>{{roleCounts.coordinator}}</b></span>
      <span>数据-hot <b>{{roleCounts.data_hot}}</b></span>
      <span>数据-warm <b>{{roleCounts.data_warm}}</b></span>
      <span>数据-cold <b>{{roleCounts.data_cold}}</b></span>
    </div>
    <table class="stk-es-matrix" v-if="hosts.length">
      <thead>
        <tr>
          <th class="stk-es-mx-host">主机</th>
          <th v-for="d in defs" :key="d.key" class="stk-es-mx-col">
            <el-checkbox v-if="isData(d.key)" size="small"
              :model-value="colChecked(d)" :indeterminate="colIndeterminate(d)"
              @change="val => toggleCol(d, val)"><span :class="'stk-es-role-chip ' + d.cls">{{d.label}}</span></el-checkbox>
            <span v-else :class="'stk-es-role-chip ' + d.cls">{{d.label}}</span>
          </th>
          <th class="stk-es-mx-cnt">容器</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="h in hosts" :key="h.id">
          <td class="stk-es-mx-host">
            <div class="stk-es-role-host">
              <span class="stk-es-role-hostname">{{h.name}}</span>
              <span class="mono stk-es-role-hostip">{{h.ip}}</span>
            </div>
          </td>
          <td v-for="d in defs" :key="d.key" class="stk-es-mx-cell">
            <el-tooltip :content="d.hint" placement="top" :disabled="!d.hint">
              <el-checkbox size="small"
                :model-value="selectedOf(h, d)" :disabled="disabledOf(h, d)"
                @change="val => toggleRole(h, d, val)" />
            </el-tooltip>
          </td>
          <td class="stk-es-mx-cnt">
            <template v-if="rolesOf(h.id).length">
              <span class="stk-es-mx-cnt-n">{{rolesOf(h.id).length}}</span>
              <span v-if="rolesOf(h.id).filter(isData).length > 1" class="stk-es-role-io-warn">I/O</span>
            </template>
            <span v-else class="stk-es-mx-auto" title="未勾选：按数据节点自动部署，可在角色计划预览中确认">自动</span>
          </td>
        </tr>
      </tbody>
    </table>
    <div v-else class="faint stk-es-role-empty">请先在主机列表中勾选参与节点</div>
    <div v-if="warnings.length" class="stk-es-role-warnings">
      <div v-for="(w, i) in warnings" :key="i" class="stk-es-role-warn">{{w}}</div>
    </div>
  </template>
</div>`
  }
})()

// 组件自注册（StacksParts.register 合并语义）：套件内 <script> 顺序不影响装配。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  P.register('elasticsearch', { components: { 'stack-form-elasticsearch-roles': window.StackFormElasticsearchRoles } })
})()
