// 设置页：平台级配置的唯一入口——AI 接入、系统信息、数据表、备份恢复四个标签页。
//
// 各页签独立加载：AI 配置随时改即生效，系统信息与数据表是只读快照，
// 备份恢复是独立的数据出口；一个页签加载失败不该拖垮另外三个。
// 只读与重操作页签懒加载，切到对应页签时才发请求。
window.SettingsPage = {
  props: ['page', 'versionData'],
  components: {
    'settings-ai': window.SettingsAI,
    'settings-system': window.SettingsSystem,
    'settings-tables': window.SettingsTables,
    'settings-backup': window.SettingsBackup
  },
  data() {
    return { tab: 'ai' }
  },
  watch: {
    // 页签内容层是独立滚动区（style.css 设的 .el-tabs__content overflow），
    // 切页签时回到顶部，避免停留在上一个页签的滚动位置
    tab() {
      this.$nextTick(() => {
        const box = this.$el && this.$el.querySelector('.el-tabs__content')
        if (box) box.scrollTop = 0
      })
    }
  },
  template: `
<div class="tool-page tool-page--settings">
  <section class="tool-intro">
    <div class="tool-intro-copy">
      <span class="tool-eyebrow">SETTINGS</span>
      <h1>设置</h1>
      <p>AI 接入 · 系统信息 · 数据表 · 备份恢复</p>
    </div>
  </section>
  <el-tabs v-model="tab" class="set-tabs">
    <el-tab-pane label="AI 接入" name="ai">
      <settings-ai />
    </el-tab-pane>
    <el-tab-pane label="系统信息" name="system" lazy>
      <settings-system />
    </el-tab-pane>
    <el-tab-pane label="数据表" name="tables" lazy>
      <settings-tables />
    </el-tab-pane>
    <el-tab-pane label="备份恢复" name="backup" lazy>
      <settings-backup />
    </el-tab-pane>
  </el-tabs>
</div>`
}
