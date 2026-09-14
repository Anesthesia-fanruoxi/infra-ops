// 套件部署页模板片段 · run（原样迁入；套件专属表达式已改为通用分发名，类名统一 .stk-*）。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  P.tpl.run = `  <el-drawer v-model="drawerVisible" :title="'套件运行 #' + (recordMeta?.id || '')" size="48%" class="deploy-drawer"
    :close-on-click-modal="!logDrawerLocked" :close-on-press-escape="!logDrawerLocked" :show-close="!logDrawerLocked"
    :before-close="onLogDrawerBeforeClose" @closed="closeDrawer">
    <div v-if="recordMeta" class="deploy-drawer-meta">
      <span class="deploy-drawer-meta-tpl">{{recordMeta.stack_name}} · {{modeLabel(recordMeta.mode)}}</span>
      <el-tag :type="taskTagType(recordMeta.status)" size="small">{{taskStatusLabel(recordMeta.status)}}</el-tag>
      <span v-if="recordMeta.created_at" class="mono faint deploy-drawer-meta-time">{{formatTime(recordMeta.created_at)}}</span>
    </div>
    <div v-if="recordHosts.length" class="deploy-drawer-hostlist">
      <div class="deploy-drawer-host" v-for="h in recordHosts" :key="h.id">
        <span class="status-badge" :class="hostStatusCls(h.status)"><span class="dot"></span>{{hostStatusText(h.status)}}</span>
        <span class="deploy-host-name">{{h.host_name}}</span>
        <span class="mono deploy-host-ip">{{h.host_ip}}</span>
        <el-tag size="small" type="info">{{roleLabel(h.role)}}</el-tag>
        <span class="stk-phase-pills">
          <el-tag v-if="showPrereqPill" size="small" :type="phaseTag(h.prereq_status)">Docker {{phaseText(h.prereq_status)}}</el-tag>
          <el-tag size="small" :type="phaseTag(h.node_status)">节点 {{phaseText(h.node_status)}}</el-tag>
          <el-tag v-if="showBootstrapPill" size="small" :type="phaseTag(h.bootstrap_status)">初始化 {{phaseText(h.bootstrap_status)}}</el-tag>
        </span>
      </div>
    </div>
    <div class="drawer-sec deploy-log-sec">
      <div class="drawer-sec-title">运行日志</div>
      <div class="orch-log-head">
        <span class="faint deploy-log-count">{{recordLogs.length}} 行</span>
        <el-input v-model="logFilter" placeholder="过滤：IP / 阶段 / 内容" clearable size="small" style="width:220px" />
      </div>
      <div class="deploy-log-box" ref="stackLogBox" @scroll="onLogScroll">
        <div v-if="!filteredLogs.length" class="deploy-log-empty">暂无日志</div>
        <div v-for="l in filteredLogs" :key="l.id" class="deploy-log-row">
          <span class="deploy-log-time">{{logTime(l.ts)}}</span>
          <span class="deploy-log-ip">{{l.ip || '-'}}</span>
          <span class="stk-log-phase">{{phaseName(l.phase)}}</span>
          <span class="deploy-log-text">{{l.text}}</span>
        </div>
      </div>
    </div>
  </el-drawer>
</div>`
})()
