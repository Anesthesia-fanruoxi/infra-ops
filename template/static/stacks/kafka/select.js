// 该套件独有的表单向导组件；装配为页面局部组件 'stack-form-kafka-select'。
//
// 为什么由套件接管第一步：KafkaUI 是「本次部署的可选附加物」，与套件/模式同属第 1 步的
// 决策，放到第 3 步参数页会让用户以为它是集群参数。骨架在第一步「部署模式」区留了
// selectComp 插槽（tpl-wizard.js 的 <component :is="step1FormComp">），由套件渲染整段内容，
// 因此这里连同一贯由骨架渲染的模式网格一起接管，保持该区域只有一处定义。
//
// 版式：KafkaUI 与两个模式卡片**同排并列**（复用骨架的 .stk-mode-grid / .stk-mode，仅加
// .stk-kafka-ui-card 修饰），开=选中态（.stk-mode--sel）——用户口径「放到部署模式的卡片里面」。
// 整张卡片可点，开关自身用 @click.stop 拦截冒泡，两条路径都走 onBoolVar，不会双重翻转。
window.StackFormKafkaSelect = {
  props: ['ctx'],
  template: `
<div class="stk-kafka-select">
  <div class="deploy-host-vars-title">部署模式</div>
  <div class="stk-mode-grid">
    <label v-for="m in modes" :key="m.key" class="stk-mode" :class="{'stk-mode--sel': ctx.mode===m.key}">
      <el-radio :model-value="ctx.mode" :label="m.key" @change="ctx.onModeChange(m.key)">{{m.label}}</el-radio>
      <p>{{m.description}}</p>
      <span class="faint">{{m.host_hint}}</span>
    </label>
    <div class="stk-mode stk-kafka-ui-card" :class="{'stk-mode--sel': uiOn}" @click="toggleUI">
      <div class="stk-kafka-ui-head">
        <el-switch :model-value="uiOn" @click.stop @change="onUI"></el-switch>
        <span class="stk-kafka-ui-name">KafkaUI 控制台</span>
      </div>
      <p>可选组件，固定部署在第 1 台主机；开启后随集群一并安装。</p>
      <span class="faint">浏览器访问 {{uiUrl}}</span>
    </div>
  </div>
</div>`,
  computed: {
    modes() { return this.ctx.selectedStack?.modes || [] },
    uiOn() { return this.ctx.sharedParams?.enable_ui === 'true' },
    uiUrl() {
      // 第 1 步尚未选主机：此时只给语义说明，选完主机后自动带上第 1 台 IP
      const ip = (this.ctx.selectedHosts || [])[0]?.ip
      return (ip || '第 1 台主机') + ':' + (this.ctx.sharedParams?.ui_port || '8080')
    }
  },
  methods: {
    onUI(v) { this.ctx.onBoolVar('enable_ui', v) },
    toggleUI() { this.ctx.onBoolVar('enable_ui', !this.uiOn) }
  }
}

// 组件自注册（StacksParts.register 合并语义）：套件内 <script> 顺序不影响装配。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  P.register('kafka', { components: { 'stack-form-kafka-select': window.StackFormKafkaSelect } })
})()
