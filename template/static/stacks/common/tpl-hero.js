// 套件部署页模板片段 · hero（原样迁入；套件专属表达式已改为通用分发名，类名统一 .stk-*）。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  P.tpl.hero = `<div class="deploy-page stk-page">
  <section class="deploy-hero">
    <div class="deploy-hero-grid"></div>
    <div class="deploy-hero-glow"></div>
    <div class="deploy-hero-content">
      <div class="deploy-hero-row">
        <div>
          <span class="deploy-eyebrow">STACK DEPLOY</span>
          <h1>套件部署</h1>
          <p>集群实例长期保留 · 扩容、缩容、加装、卸载都记在实例里</p>
        </div>
        <div class="deploy-hero-actions">
          <el-tag v-if="runningCount" type="warning" size="large" class="deploy-running-tag" style="margin-right:10px">
            <span class="dot running"></span>{{runningCount}} 个集群变更中
          </el-tag>
          <el-button size="large" @click="loadInstances"><el-icon style="margin-right:6px"><Refresh /></el-icon>刷新</el-button>
          <el-button type="primary" size="large" class="deploy-new-btn" @click="openWizard">
            <el-icon style="margin-right:6px"><Plus /></el-icon>新建集群
          </el-button>
        </div>
      </div>
    </div>
  </section>

  <div class="stk-stk-inst-grid" v-loading="instancesLoading">
    <div v-for="it in instances" :key="it.id" class="stk-stk-inst-card" :class="'stk-stk-inst-card--'+it.status" @click="openInstance(it)">
      <div class="stk-stk-inst-top">
        <div class="stk-stk-inst-avatar" :class="stackAvatar(it).c">{{stackAvatar(it).t}}</div>
        <div class="stk-stk-inst-title">
          <div class="stk-stk-inst-name">{{it.name}}<span v-if="instIsHa(it)" class="stk-ha-badge" style="margin-left:6px;position:relative;top:-1px">HA</span></div>
          <div class="stk-stk-inst-meta">{{it.stack_name}} · {{modeLabel(it.mode, it.stack_key)}}</div>
        </div>
        <div class="stk-stk-inst-side">
          <span class="stk-runtime" :class="isContainerStack(it) ? 'is-container' : 'is-host'">{{runtimeLabel(it)}}</span>
          <span class="stk-stk-inst-status" :class="'is-'+it.status"><i class="dot"></i>{{instStatusLabel(it.status)}}</span>
        </div>
      </div>
      <div class="stk-stk-inst-tags">
        <template v-if="instTierStyle(it)">
          <span v-for="t in instTierStyle(it).items" :key="t" :class="instTierStyle(it).cls">{{t}}</span>
        </template>
        <el-tag v-else v-for="c in instDisplayComps(it)" :key="c" size="small" effect="plain" round>{{compLabel(it.stack_key, c)}}</el-tag>
      </div>
      <div class="stk-stk-inst-foot">
        <span class="stk-stk-inst-hosts"><el-icon :size="14"><Monitor /></el-icon>{{it.host_count || 0}} 台主机</span>
        <div class="stk-stk-inst-ops" @click.stop>
          <!-- 安装中：按钮全部置灰，禁止任何操作 -->
          <template v-if="busyInst(it)">
            <el-button size="small" type="primary" disabled>重新安装</el-button>
            <el-button size="small" type="danger" plain disabled>卸载</el-button>
            <el-button size="small" text disabled>重命名</el-button>
          </template>
          <template v-else>
            <!-- 安装失败 / 已卸载：可重新安装 -->
            <el-button v-if="instFailed(it) || instUninstalled(it)" size="small" type="primary" @click="reinstallInstance(it)">重新安装</el-button>
            <!-- 安装失败 / 安装成功：可卸载 -->
            <el-button v-if="instFailed(it) || instReady(it)" size="small" type="danger" plain @click="uninstallInstance(it)">卸载</el-button>
            <el-button size="small" text @click="renameInstance(it)">重命名</el-button>
            <!-- 仅已卸载可删除 -->
            <el-button v-if="instUninstalled(it)" size="small" text type="danger" @click="deleteInstance(it)">删除</el-button>
          </template>
        </div>
      </div>
    </div>
    <div v-if="!instancesLoading && !instances.length" class="stk-stk-inst-empty">
      <el-icon :size="30" style="opacity:.45;margin-bottom:8px"><Box /></el-icon>
      <div>还没有集群实例，点击右上角新建</div>
    </div>
  </div>
`
})()
