// Kafka 套件 · 前端入口。
//
// 套件差异全部经 forms / hooks 插槽表达，通用骨架零改动：
//   - forms.selectComp     第 1 步「部署模式」区连同 KafkaUI 开关（select.js）
//   - forms.visibleSharedVars  第 3 步不重复展示已在第 1 步确定的开关
//   - hooks.*              探活组件页签与接入端点合成（hooks.js）
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  P.register('kafka', {
    forms: {
      selectComp: 'stack-form-kafka-select',
      hints: ['扩缩容复用节点脚本：扩容以 broker-only 身份加入集群，缩容必须保留 3 台及以上（controller 仲裁多数派）。'],
      visibleSharedVars(ctx, list) {
        const uiOn = ctx.sharedParams?.enable_ui === 'true'
        return list
          // KafkaUI 开关已在第 1 步卡片区确定，参数页不再重复展示
          .filter(v => v.name !== 'enable_ui')
          // 未启用 KafkaUI 时隐藏它的镜像/端口，避免填了却不生效
          .filter(v => uiOn || (v.name !== 'ui_image' && v.name !== 'ui_port'))
      }
    },
    components: {}
  })
})()
