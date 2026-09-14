// 该套件独有的表单向导组件；app.js 按 window.StackFormBigdataOp 全局注册，名称保持不变。
window.StackFormBigdataOp = {
  props: ['ctx'],
  computed: {
    isRemove() { return this.ctx.wizardOp === 'remove_component' },
    catalog() { return ((window.StackForms.bigdata || {}).comps || []) },
    installedKeys() { return this.ctx.instCompTags(this.ctx.targetInstance) },
    haOn() { return this.ctx.sharedParams?.ha === 'true' },
    // 全集 = 组件目录 + 已安装但不在目录中的运行期组件（如 metastore_db）
    board() {
      const out = this.catalog.map(c => ({ ...c, installed: this.installedKeys.includes(c.key) }))
      this.installedKeys.forEach(k => {
        if (!this.catalog.some(c => c.key === k)) {
          out.push({ key: k, label: compDisplayLabel(k), desc: '集群运行期组件', installed: true })
        }
      })
      return out
    },
    addableCount() { return this.board.filter(c => this.selectable(c)).length },
    installedCount() { return this.board.filter(c => c.installed).length },
    summary() {
      if (this.isRemove) return `已安装 ${this.installedCount} 个组件 · 可卸载 ${this.addableCount} 个`
      return `已安装 ${this.installedCount} 个组件 · 可加装 ${this.addableCount} 个`
    },
    emptyHint() {
      if (this.isRemove) return '当前没有可单独卸载的组件（底座组件请使用「卸载集群」）'
      return '所有组件均已安装，无需加装'
    }
  },
  methods: {
    // 仍会留在集群里、且依赖 c 的组件（与后端 validateBigdataRemove 的「剩余集」规则一致）。
    // 本次一并勾选卸载的依赖方不算阻塞，因此被依赖方随之解锁。
    blockingDependents(c) {
      const removing = new Set(this.ctx.bdSel)
      return this.catalog.filter(x =>
        (x.deps || []).includes(c.key) &&
        this.installedKeys.includes(x.key) &&
        !removing.has(x.key))
    },
    lockReason(c) {
      if (this.isRemove) {
        if (c.required) return '底座组件'
        if (!c.installed) return '未安装'
        if (this.haOn && c.key === 'zookeeper') {
          const deps = this.installedKeys.filter(k => ['yarn', 'spark', 'flink', 'hbase', 'hive'].includes(k))
          if (deps.length) return 'HA 选主基座'
        }
        if (this.haOn && c.key === 'metastore_db' && this.installedKeys.includes('hive')) return 'Hive 依赖'
        const blockers = this.blockingDependents(c)
        if (blockers.length) return '被 ' + blockers.map(b => b.label).join('、') + ' 依赖，请一并勾选'
        return ''
      }
      if (c.required) return '底座必选'
      if (c.installed) return '已安装'
      return ''
    },
    selectable(c) { return this.lockReason(c) === '' },
    checked(c) {
      if (this.ctx.bdSel.includes(c.key)) return true
      return !this.isRemove && c.installed
    },
    onToggle(c, v) {
      if (!this.selectable(c)) return
      this.ctx.formToggle(c.key, v)
    },
    tagText(c) { return c.installed ? '已安装' : '未安装' }
  },
  template: `
<div class="stk-bd-comp-select">
  <div class="stk-bd-comp-ophead">
    <span class="deploy-host-vars-title">{{isRemove ? '选择要卸载的组件' : '选择要加装的组件'}}</span>
    <span class="stk-bd-comp-opsum">{{summary}}</span>
  </div>
  <div class="stk-bd-comp-grid">
    <div
      v-for="c in board"
      :key="c.key"
      class="stk-bd-comp-card"
      :class="{
        'is-on': checked(c),
        'is-req': c.key === 'hdfs',
        'is-installed': c.installed,
        'is-locked': !selectable(c),
        'is-missing': !c.installed
      }"
      @click="onToggle(c, !checked(c))"
    >
      <el-checkbox :model-value="checked(c)" :disabled="!selectable(c)" @click.stop @change="v => onToggle(c, v)" />
      <div class="stk-bd-comp-body">
        <div class="stk-bd-comp-name">
          {{c.label}}
          <el-tag size="small" :type="c.installed ? 'success' : 'info'" effect="plain" style="margin-left:6px">{{tagText(c)}}</el-tag>
          <span v-if="lockReason(c) && lockReason(c) !== tagText(c)" class="stk-bd-comp-lock">{{lockReason(c)}}</span>
        </div>
        <div class="stk-bd-comp-desc">{{c.desc}}</div>
      </div>
    </div>
  </div>
  <div v-if="!addableCount" class="stk-bd-comp-empty">{{emptyHint}}</div>
  <p class="stk-hint">{{isRemove
    ? '勾选后在全部成员上停止并卸载该组件（集群记录保留）。灰色项不可勾选。'
    : '已安装组件以置灰回填展示，勾选未安装组件后进入下一步指定主角色。'}}</p>
</div>`
}

// 组件自注册（StacksParts.register 合并语义）：套件内 <script> 顺序不影响装配，
// 也不再依赖 index.js 的加载时机。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  P.register('bigdata', { components: { 'stack-form-bigdata-op': window.StackFormBigdataOp } })
})()


// metastore_db 等运行期组件的中文名
function compDisplayLabel(key) {
  return { metastore_db: 'Hive MetaDB' }[key] || key
}
