// 套件部署页模板片段 · instance（原样迁入；套件专属表达式已改为通用分发名，类名统一 .stk-*）。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  P.tpl.instance = `  <el-drawer v-model="instDrawerVisible" :title="instDetail?.name || '集群'" size="42%" class="deploy-drawer">
    <div v-if="instDetail" class="deploy-drawer-meta">
      <span class="deploy-drawer-meta-tpl">{{instDetail.stack_name}} · {{modeLabel(instDetail.mode, instDetail.stack_key)}}</span>
      <span class="stk-runtime" :class="isContainerStack(instDetail) ? 'is-container' : 'is-host'">{{runtimeLabel(instDetail)}}</span>
      <el-tag :type="instStatusType(instDetail.status)" size="small">{{instStatusLabel(instDetail.status)}}</el-tag>
    </div>
    <div class="stk-stk-inst-tags" style="margin:8px 0 14px">
      <el-tag v-for="c in instDisplayComps(instDetail)" :key="c" size="small">{{compLabel(instDetail.stack_key, c)}}</el-tag>
    </div>
    <div class="stk-stk-inst-ops stk-drawer-ops" style="margin-bottom:16px" v-if="instDetail">
      <el-button v-if="canReinstall(instDetail)" size="small" type="primary" @click="reinstallInstance(instDetail)">重新安装</el-button>
      <el-button size="small" type="primary" plain :loading="verifying" :disabled="instDetail.status==='uninstalled'" @click="openVerifyDialog(instDetail)">探活</el-button>
      <el-button v-if="caDownloadAvailable" size="small" type="primary" plain @click="downloadCa">下载 CA 证书</el-button>
      <el-button size="small" :disabled="busyInst(instDetail) || instDetail.status==='uninstalled'" @click="openScaleOut(instDetail)">扩容</el-button>
      <el-button size="small" :disabled="busyInst(instDetail) || instDetail.status==='uninstalled' || activeInstHosts(instDetail).length<2" @click="openScaleIn(instDetail)">缩容</el-button>
      <el-button v-if="isPlatformStack(instDetail)" size="small" :disabled="busyInst(instDetail) || instDetail.status==='uninstalled'" :title="unusedCompsOf(instDetail).length ? '加装尚未安装的组件' : '所有组件均已安装'" @click="openAddComp(instDetail)">加装组件</el-button>
      <el-button v-if="isPlatformStack(instDetail)" size="small" :disabled="busyInst(instDetail) || instDetail.status==='uninstalled'" :title="removableCompsOf(instDetail).length ? '按组件卸载' : '没有可单独卸载的组件'" @click="openRemoveComp(instDetail)">卸载组件</el-button>
      <el-button v-if="instDetail.status==='failed' || instDetail.status==='partial'" size="small" type="warning" plain :disabled="busyInst(instDetail)" @click="purgeInstance(instDetail)">清理残留</el-button>
    </div>
    <div class="drawer-sec-title"><span>成员节点</span><span class="stk-inst-mem-count">{{(instDetail.hosts||[]).filter(x => x.status!=='removed').length}} 台</span></div>
    <div class="stk-inst-mem-grid">
      <div class="stk-inst-mem-card" v-for="h in (instDetail.hosts||[]).filter(x => x.status!=='removed')" :key="h.id" :class="h.role === 'master' ? 'is-master' : ''">
        <div class="stk-inst-mem-top">
          <span class="stk-inst-mem-name" :title="h.host_name">{{h.host_name}}</span>
          <el-tag size="small" :type="h.role === 'master' ? 'primary' : 'info'" effect="light">{{roleLabel(h.role)}}</el-tag>
        </div>
        <div class="mono stk-inst-mem-ip">{{h.host_ip}}</div>
        <div class="stk-inst-mem-tags">
          <template v-for="tag in memTags(h)" :key="tag"><span class="stk-inst-mem-tag">{{tag}}</span></template>
          <span v-if="!memTags(h).length" class="stk-inst-mem-tag is-idle">工作正常</span>
        </div>
      </div>
      <div v-if="!(instDetail.hosts||[]).filter(x => x.status!=='removed').length" class="faint stk-inst-mem-empty">暂无成员</div>
    </div>
    <!-- 角色计划（全部套件）：部署前物化的落点契约，部署/探活/扩缩容共读（docs/角色物化设计.md） -->
    <template v-if="instDetail.id">
      <div class="drawer-sec-title stk-inst-runs-title">
        <span>角色计划</span>
        <span v-if="instPlan" class="stk-inst-plan-meta">rev {{instPlan.rev}} · {{instPlan.ha ? 'HA' : '单机'}} · <span class="mono">{{planTime(instPlan.generated_at)}}</span></span>
        <span class="stk-inst-mem-count">
          <el-tooltip v-if="instReady(instDetail)" content="部署已完成，角色计划已定型，不支持重新规划" placement="top">
            <span><el-button v-if="instPlan" size="small" text type="primary" disabled>重新规划</el-button></span>
          </el-tooltip>
          <el-button v-else-if="instPlan" size="small" text type="primary" @click="replanInst">重新规划</el-button>
          <span v-else class="faint">加载中…</span>
        </span>
      </div>
      <div v-if="instPlan" class="stk-inst-plan-box">
        <div v-for="h in instPlan.hosts" :key="h.host_ip" class="stk-inst-plan-row">
          <span class="mono stk-inst-plan-ip">{{h.host_ip}}</span>
          <div class="stk-inst-plan-chips">
            <span v-for="(r, i) in h.roles" :key="i" class="stk-inst-plan-chip" :class="[r.source==='manual' ? 'is-manual' : 'is-auto', roleTextClass(r)]" :title="r.source==='manual' ? '手动指定' : '自动分配'">{{r.label}}</span>
            <span v-if="!(h.roles||[]).length" class="faint">—</span>
          </div>
        </div>
        <div class="stk-legend" style="margin-top:2px"><span class="stk-legend-item"><i class="dot is-manual"></i>手动</span><span class="stk-legend-item"><i class="dot is-auto"></i>自动</span><span class="stk-legend-sep"></span><span class="stk-legend-item rt-primary">主/Active</span><span class="stk-legend-item rt-standby">备/Standby</span><span class="stk-legend-item rt-quorum">仲裁</span><span class="stk-legend-item rt-worker">工作/成员</span><span class="stk-legend-item rt-aux">辅助</span></div>
      </div>
    </template>
    <div class="drawer-sec-title stk-inst-runs-title"><span>流程记录</span><span class="stk-inst-mem-count">{{instRuns.length}} 条</span></div>
    <div class="stk-inst-runs-wrap">
      <el-table :data="instRuns" size="small" height="100%" class="stk-inst-runs-table" empty-text="暂无流程记录" @row-click="openDrawer">
        <el-table-column label="ID" width="72" align="center"><template #default="{row}"><span class="mono">#{{row.id}}</span></template></el-table-column>
        <el-table-column label="操作" width="76"><template #default="{row}"><span class="stk-inst-op">{{opLabel(row.op)}}</span></template></el-table-column>
        <el-table-column label="状态" width="120" align="center"><template #default="{row}"><span class="stk-inst-status" :class="row.status"><i class="dot"></i>{{taskStatusLabel(row.status)}}</span></template></el-table-column>
        <el-table-column label="时间"><template #default="{row}"><span class="mono faint">{{formatTime(row.created_at)}}</span></template></el-table-column>
      </el-table>
    </div>
  </el-drawer>
`
})()
