// 该套件独有的表单向导组件；app.js 按 window.StackFormBigdataSelect 全局注册，名称保持不变。
window.StackFormBigdataSelect = {
  props: ['ctx'],
  template: `
<div class="stk-bd-comp-select">
  <div class="deploy-host-vars-title">部署组件</div>
  <div class="stk-bd-comp-grid">
    <div v-for="c in comps" :key="c.key" class="stk-bd-comp-card" :class="{'is-on': ctx.bdSel.includes(c.key), 'is-req': compRequired(c), 'is-locked': compRequired(c)}" @click="toggle(c)">
      <el-checkbox :model-value="ctx.bdSel.includes(c.key)" :disabled="compRequired(c)" @click.stop @change="v => ctx.formToggle(c.key, v)" />
      <div class="stk-bd-comp-body">
        <div class="stk-bd-comp-name">{{c.label}}<el-tag v-if="compRequired(c)" size="small" type="danger" style="margin-left:6px">必选</el-tag></div>
        <div class="stk-bd-comp-desc">{{c.desc}}</div>
      </div>
    </div>
  </div>
</div>`,
  computed: { comps() { return window.StackForms.bigdata.comps } },
  methods: {
    // 开启 HA 后 ZooKeeper 一并成为必选（协调服务，事后不可取消）
    compRequired(c) { return c.required || (c.key === 'zookeeper' && this.ctx.sharedParams?.ha === 'true') },
    toggle(c) { if (this.compRequired(c)) return; this.ctx.formToggle(c.key, !this.ctx.bdSel.includes(c.key)) }
  }
}

// 组件自注册（StacksParts.register 合并语义）：套件内 <script> 顺序不影响装配，
// 也不再依赖 index.js 的加载时机。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  P.register('bigdata', { components: { 'stack-form-bigdata-select': window.StackFormBigdataSelect } })
})()
