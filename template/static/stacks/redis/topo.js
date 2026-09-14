// 该套件独有的表单向导组件；app.js 按 window.StackFormRedisTopo 全局注册，名称保持不变。
window.StackFormRedisTopo = {
  props: ['ctx'],
  template: `
<div v-if="ctx.mode==='cluster'" class="stk-redis-replicas">
  <span class="deploy-host-vars-title">集群拓扑</span>
  <el-radio-group v-model="ctx.replicas" size="small">
    <el-radio-button label="0">三主（至少 3 台）</el-radio-button>
    <el-radio-button label="1">三主三从（至少 6 台偶数）</el-radio-button>
  </el-radio-group>
</div>`
}

// 组件自注册（StacksParts.register 合并语义）：套件内 <script> 顺序不影响装配，
// 也不再依赖 index.js 的加载时机。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  P.register('redis', { components: { 'stack-form-redis-topo': window.StackFormRedisTopo } })
})()
