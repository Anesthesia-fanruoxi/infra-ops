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
    <!-- 部署前角色预览（全部套件）：与部署前置一同确认落点，未通过不能确认部署 -->
    <div v-if="needOpPlan" class="stk-plan-preview" style="margin-top:12px">
      <div class="stk-plan-preview-head">
        <span>角色计划预览 · 确认落点后开始部署</span>
        <span v-if="opPlan" class="faint">{{opPlan.generated_by === 'manual' ? '含手动指定' : '自动分配'}}</span>
        <el-button v-if="opPlanErr" size="small" text type="primary" @click="refreshOpPlan">重试</el-button>
      </div>
      <div v-if="opPlanErr" class="stk-plan-preview-warn"><div>{{opPlanErr}}</div></div>
      <div v-else-if="opPlan" class="stk-plan-preview-grid">
        <div v-for="h in opPlan.hosts" :key="h.host_ip" class="stk-plan-preview-row">
          <span class="mono stk-plan-preview-ip">{{h.host_ip}}</span>
          <div class="stk-plan-preview-chips">
            <span v-for="(r, i) in h.roles" :key="i" class="stk-plan-chip" :class="[r.source==='manual' ? 'is-manual' : 'is-auto', roleTextClass(r)]" :title="r.source==='manual' ? '手动指定' : '自动分配'">{{r.label}}</span>
            <span v-if="!(h.roles||[]).length" class="faint">—</span>
          </div>
        </div>
      </div>
      <div v-else class="faint" style="font-size:12px;padding-top:6px">正在生成角色计划预览…</div>
      <div class="stk-legend"><span class="stk-legend-item"><i class="dot is-manual"></i>手动</span><span class="stk-legend-item"><i class="dot is-auto"></i>自动</span><span class="stk-legend-sep"></span><span class="stk-legend-item rt-primary">主/Active</span><span class="stk-legend-item rt-standby">备/Standby</span><span class="stk-legend-item rt-quorum">仲裁</span><span class="stk-legend-item rt-worker">工作/成员</span><span class="stk-legend-item rt-aux">辅助</span></div>
      <div v-if="opPlan && (opPlan.warnings||[]).length" class="stk-plan-preview-warn">
        <div v-for="(w, i) in opPlan.warnings" :key="i">{{w}}</div>
      </div>
    </div>
    <template #footer>
      <el-button @click="preflightVisible=false">取消</el-button>
      <el-button type="primary" :loading="deploying" :disabled="!preflightReady" @click="confirmRun">确认部署</el-button>
    </template>
  </el-dialog>
`
})()
