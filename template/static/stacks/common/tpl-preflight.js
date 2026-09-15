// 套件部署页模板片段 · preflight（原样迁入；套件专属表达式已改为通用分发名，类名统一 .stk-*）。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  P.tpl.preflight = `
  <el-dialog v-model="preflightVisible" title="Docker 前置检查" width="560px" :close-on-click-modal="false">
    <p class="stk-preflight-lead">已安装 Docker 的主机将<strong>跳过安装</strong>，即使版本不一致也不改动配置、安装目录或 daemon.json。未安装的主机会调用「安装 Docker」模板。</p>
    <div v-if="preflight.unreachable && preflight.unreachable.length" class="stk-preflight-block stk-preflight-block--fail">
      <div class="stk-preflight-title">无法探测（请检查 SSH）</div>
      <div v-for="h in preflight.unreachable" :key="h.host_id">{{h.name}} {{h.ip}} · {{h.error}}</div>
    </div>
    <div v-if="preflight.will_install && preflight.will_install.length" class="stk-preflight-block">
      <div class="stk-preflight-title">将安装 Docker（{{preflight.will_install.length}}）</div>
      <div v-for="h in preflight.will_install" :key="h.host_id">{{h.name}} <span class="mono">{{h.ip}}</span></div>
    </div>
    <div v-if="preflight.will_skip && preflight.will_skip.length" class="stk-preflight-block stk-preflight-block--ok">
      <div class="stk-preflight-title">已有 Docker，跳过且不改动（{{preflight.will_skip.length}}）</div>
      <div v-for="h in preflight.will_skip" :key="h.host_id">{{h.name}} <span class="mono">{{h.ip}}</span></div>
    </div>
    <!-- 角色计划预览已前移到向导第二步（tpl-wizard.js 通用分支 / bigdata roles.js）：
         这里只做 Docker 前置检查，不再同屏展示落点信息 -->
    <template #footer>
      <el-button @click="preflightVisible=false">取消</el-button>
      <el-button type="primary" :loading="deploying" :disabled="!preflightReady" @click="confirmRun">确认部署</el-button>
    </template>
  </el-dialog>
`
})()
