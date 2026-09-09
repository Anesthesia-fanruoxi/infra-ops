window.StacksPage = {
  props: ['page', 'user', 'versionData'],
  template: `
<div class="deploy-page stack-page">
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

  <div class="stack-inst-grid" v-loading="instancesLoading">
    <div v-for="it in instances" :key="it.id" class="stack-inst-card" :class="'stack-inst-card--'+it.status" @click="openInstance(it)">
      <div class="stack-inst-head">
        <div>
          <div class="stack-inst-name">{{it.name}}</div>
          <div class="stack-inst-meta">{{it.stack_name}} · {{modeLabel(it.mode, it.stack_key)}} · {{it.host_count || 0}} 台</div>
        </div>
        <el-tag :type="instStatusType(it.status)" size="small">{{instStatusLabel(it.status)}}</el-tag>
      </div>
      <div class="stack-inst-tags">
        <span class="stack-runtime" :class="isContainerStack(it) ? 'is-container' : 'is-host'">{{runtimeLabel(it)}}</span>
        <el-tag v-for="c in instCompTags(it)" :key="c" size="small" effect="plain">{{compLabel(c)}}</el-tag>
      </div>
      <div class="stack-inst-ops" @click.stop>
        <el-button size="small" :disabled="busyInst(it) || it.status==='uninstalled'" @click="openScaleOut(it)">扩容</el-button>
        <el-button size="small" :disabled="busyInst(it) || it.status==='uninstalled' || (it.host_count||0)<2" @click="openScaleIn(it)">缩容</el-button>
        <el-button v-if="it.category==='platform'" size="small" :disabled="busyInst(it) || it.status==='uninstalled' || !unusedCompsOf(it).length" @click="openAddComp(it)">加装</el-button>
        <el-button v-if="it.category==='platform'" size="small" :disabled="busyInst(it) || it.status==='uninstalled' || !removableCompsOf(it).length" @click="openRemoveComp(it)">卸载组件</el-button>
        <el-button size="small" type="danger" :disabled="busyInst(it) || it.status==='uninstalled'" @click="uninstallInstance(it)">卸载</el-button>
        <el-button size="small" text @click="renameInstance(it)">改名</el-button>
        <el-button size="small" text type="danger" :disabled="it.status==='deploying'" @click="deleteInstance(it)">删除</el-button>
      </div>
    </div>
    <div v-if="!instancesLoading && !instances.length" class="stack-inst-empty">还没有集群实例，点击右上角新建</div>
  </div>

  <el-dialog v-model="wizardVisible" :title="wizardTitle" width="85%" top="6vh" class="stack-wizard-dialog" :close-on-click-modal="false" @closed="resetWizard">
    <el-steps :active="wizardStepIndex" finish-status="success" align-center style="margin-bottom:20px">
      <el-step v-if="wizardOp==='create' || wizardOp==='add_component' || wizardOp==='remove_component'" :title="wizardFirstStepTitle" />
      <el-step v-if="wizardOp!=='remove_component'" :title="wizardOp==='scale_in' ? '选择移除主机' : '选择主机'" />
      <el-step v-if="wizardOp!=='scale_in' && wizardOp!=='remove_component'" title="填写参数" />
    </el-steps>

    <div v-show="step===1">
      <div v-if="wizardOp==='add_component' || wizardOp==='remove_component'">
        <div class="deploy-host-vars-title">{{wizardOp==='remove_component' ? '卸载组件' : '加装组件'}} <span class="deploy-var-hint">{{wizardOp==='remove_component' ? 'HDFS 需整集群卸载' : '已安装的不再列出'}}</span></div>
        <div class="stack-mode-grid">
          <label v-for="c in unusedWizardComps" :key="c.key" class="stack-mode" :class="{'stack-mode--sel': bdSel.includes(c.key)}">
            <el-checkbox :model-value="bdSel.includes(c.key)" @change="v => toggleBigdataComp(c.key, v)">{{c.label}}</el-checkbox>
            <p>{{c.desc}}</p>
          </label>
        </div>
        <p v-if="!unusedWizardComps.length" class="stack-hint">{{wizardOp==='remove_component' ? '没有可单独卸载的组件' : '没有可加装的组件'}}</p>
      </div>
      <div v-else>
      <div v-for="g in stackGroups" :key="g.key" class="stack-cat">
        <div class="stack-cat-head">
          <span class="stack-cat-title">{{g.label}}</span>
          <span class="stack-cat-hint">{{g.hint}}</span>
        </div>
        <div class="stack-card-grid">
          <button v-for="s in g.stacks" :key="s.key" type="button" class="stack-card" :class="{'stack-card--sel': selectedKey===s.key}" @click="selectStack(s)">
            <div class="stack-card-head">
              <div class="stack-card-name">{{s.name}}</div>
              <span class="stack-runtime" :class="s.requires_docker ? 'is-container' : 'is-host'">{{runtimeLabel(s)}}</span>
            </div>
            <div class="stack-card-modes">
              <el-tag v-for="m in s.modes" :key="m.key" size="small" :type="mode===m.key ? 'primary' : 'info'" effect="plain">{{m.label}}</el-tag>
            </div>
            <div class="stack-card-desc">{{s.description}}</div>
          </button>
        </div>
      </div>
      <div v-if="!stacks.length && !stacksLoading" class="deploy-placeholder">暂无可用套件</div>
      <div v-if="selectedStack" class="stack-mode-box">
        <template v-if="selectedKey==='bigdata'">
          <div class="deploy-host-vars-title">部署组件 <span class="deploy-var-hint">HDFS 必选打底，按需勾选计算 / 数仓组件</span></div>
          <div class="stack-mode-grid">
            <label v-for="c in bigdataComps" :key="c.key" class="stack-mode" :class="{'stack-mode--sel': bdSel.includes(c.key)}">
              <el-checkbox :model-value="bdSel.includes(c.key)" :disabled="c.required" @change="v => toggleBigdataComp(c.key, v)">{{c.label}}{{c.required ? ' · 必选' : ''}}</el-checkbox>
              <p>{{c.desc}}</p>
            </label>
          </div>
        </template>
        <template v-else>
          <div class="deploy-host-vars-title">部署模式</div>
          <div class="stack-mode-grid">
            <label v-for="m in selectedStack.modes" :key="m.key" class="stack-mode" :class="{'stack-mode--sel': mode===m.key}">
              <el-radio :model-value="mode" :label="m.key" @change="onModeChange(m.key)">{{m.label}}</el-radio>
              <p>{{m.description}}</p>
              <span class="faint">{{m.host_hint}}</span>
            </label>
          </div>
        </template>
        <p class="stack-hint" v-if="selectedStack">{{selectedStack.requires_docker ? '该套件以 Docker 容器运行，部署前会检查并按需安装 Docker。' : '该套件直接安装在主机上，不使用容器，也不会检查 Docker。'}}</p>
        <p class="stack-hint" v-if="selectedKey==='redis'">单机 Redis 请使用「基础建设」中的部署 Redis 模板。</p>
      </div>
      </div>
    </div>

    <div v-show="step===2">
      <div v-if="selectedKey==='redis' && mode==='cluster'" class="stack-replicas">
        <span class="deploy-host-vars-title">集群拓扑</span>
        <el-radio-group v-model="replicas" size="small">
          <el-radio-button label="0">三主（至少 3 台）</el-radio-button>
          <el-radio-button label="1">三主三从（至少 6 台偶数）</el-radio-button>
        </el-radio-group>
      </div>
      <div class="deploy-host-toolbar">
        <el-input v-model="hostFilter" placeholder="搜索主机名 / IP / 标签" clearable size="small" style="width:220px" />
        <el-select v-model="hostSort.field" size="small" style="width:100px" :teleported="false"><el-option label="按主机名" value="name" /><el-option label="按 IP" value="ip" /></el-select>
        <el-button size="small" @click="toggleHostSortOrder">{{hostSort.order==='asc' ? '↑ 升序' : '↓ 降序'}}</el-button>
        <el-button size="small" @click="toggleSelectAll">{{ selectedHosts.length === filteredHosts.length ? '取消全选' : '全选' }}</el-button>
        <span class="deploy-sel-count">已选 {{selectedHosts.length}} 台 · {{hostHint}}</span>
      </div>
      <div class="deploy-host-list deploy-host-list--dialog" v-loading="hostsLoading">
        <label v-for="h in filteredHosts" :key="h.id" class="deploy-host-item" :class="{'deploy-host-item--sel': selectedHostIds.has(h.id)}">
          <el-checkbox :model-value="selectedHostIds.has(h.id)" @change="toggleHost(h.id)" />
          <span class="deploy-host-name">{{h.name}}</span>
          <span class="mono deploy-host-ip">{{h.ip}}</span>
          <span class="tag-badge other" style="font-size:10px">{{h.tag || 'other'}}</span>
          <span class="status-badge" :class="h.status"><span class="dot"></span>{{h.status==='online'?'在线':h.status==='offline'?'离线':'未验证'}}</span>
          <el-radio v-if="needMaster && selectedHostIds.has(h.id)" :model-value="masterHostId" :label="h.id" @change="masterHostId=h.id" style="margin-left:auto">主节点</el-radio>
        </label>
        <div v-if="!filteredHosts.length && !hostsLoading" class="deploy-placeholder">无匹配主机</div>
      </div>
    </div>

    <div v-show="step===3">
      <div class="deploy-host-vars-head" v-if="wizardOp==='create'">
        <span class="deploy-host-vars-title">集群名称</span>
      </div>
      <div class="deploy-host-var-fields stack-shared-fields" v-if="wizardOp==='create'" style="margin-bottom:12px">
        <div class="deploy-host-var-field">
          <label>名称</label>
          <el-input size="small" v-model="clusterName" :placeholder="defaultClusterName" />
        </div>
      </div>
      <div class="deploy-host-vars-head">
        <span class="deploy-host-vars-title">集群共享参数</span>
      </div>
      <div class="deploy-host-var-fields stack-shared-fields">
        <div class="deploy-host-var-field" v-for="v in visibleSharedVars" :key="v.name">
          <label>{{v.label}}<b v-if="v.required">*</b></label>
          <el-input size="small" v-model="sharedParams[v.name]" :placeholder="v.default || ''" :show-password="v.name==='password'" />
        </div>
      </div>
      <div class="deploy-host-vars-head" style="margin:16px 0 10px">
        <span class="deploy-host-vars-title">每台主机 <span class="deploy-var-hint">留空则使用默认值</span></span>
        <el-button size="small" :disabled="selectedHosts.length < 2" @click="copyFirstToAll">复制首台到其他</el-button>
      </div>
      <div class="deploy-host-vars-list">
        <div class="deploy-host-var-row" v-for="h in selectedHosts" :key="h.id">
          <div class="deploy-host-var-rowhead">
            <span class="deploy-host-name">{{h.name}}</span>
            <span class="mono deploy-host-ip">{{h.ip}}</span>
            <el-tag v-if="needMaster" size="small" :type="h.id===masterHostId ? 'danger' : 'info'">{{hostRoleTag(h)}}</el-tag>
          </div>
          <div class="deploy-host-var-fields">
            <div class="deploy-host-var-field" v-for="v in hostVars" :key="v.name">
              <label>{{v.label}}</label>
              <el-input size="small" :model-value="hostParamValue(h.id, v)" @input="val => onHostParam(h.id, v.name, val)" :placeholder="hostVarPlaceholder(v)" />
            </div>
          </div>
        </div>
      </div>
    </div>

    <template #footer>
      <div class="deploy-wizard-footer">
        <span class="deploy-action-hint-inline">{{wizardHint}}</span>
        <div>
          <el-button @click="wizardVisible=false">取消</el-button>
          <el-button v-if="step>1 && wizardOp!=='scale_out' && wizardOp!=='scale_in'" @click="step--">上一步</el-button>
          <el-button v-if="step===1 && wizardOp!=='remove_component'" type="primary" :disabled="!canStep2" @click="goStep(2)">下一步</el-button>
          <el-button v-if="step===1 && wizardOp==='remove_component'" type="danger" :loading="deploying" :disabled="!canStep2" @click="confirmRemoveComp">确认卸载组件</el-button>
          <el-button v-if="step===2 && wizardOp!=='scale_in'" type="primary" :disabled="!canStep3" @click="goStep(3)">下一步</el-button>
          <el-button v-if="step===2 && wizardOp==='scale_in'" type="danger" :loading="deploying" :disabled="!canStep3" @click="confirmScaleIn">确认缩容</el-button>
          <el-button v-if="step===3" type="primary" :loading="deploying" :disabled="!canRun" @click="startPreflight">开始部署</el-button>
        </div>
      </div>
    </template>
  </el-dialog>

  <el-dialog v-model="preflightVisible" title="Docker 前置检查" width="560px" :close-on-click-modal="false">
    <p class="stack-preflight-lead">已安装 Docker 的主机将<strong>跳过安装</strong>，即使版本不一致也不改动配置、安装目录或 daemon.json。未安装的主机会调用「安装 Docker」模板。</p>
    <div v-if="preflight.unreachable && preflight.unreachable.length" class="stack-preflight-block stack-preflight-block--fail">
      <div class="stack-preflight-title">无法探测（请检查 SSH）</div>
      <div v-for="h in preflight.unreachable" :key="h.host_id">{{h.name}} {{h.ip}} · {{h.error}}</div>
    </div>
    <div v-if="preflight.will_install && preflight.will_install.length" class="stack-preflight-block">
      <div class="stack-preflight-title">将安装 Docker（{{preflight.will_install.length}}）</div>
      <div v-for="h in preflight.will_install" :key="h.host_id">{{h.name}} <span class="mono">{{h.ip}}</span></div>
    </div>
    <div v-if="preflight.will_skip && preflight.will_skip.length" class="stack-preflight-block stack-preflight-block--ok">
      <div class="stack-preflight-title">已有 Docker，跳过且不改动（{{preflight.will_skip.length}}）</div>
      <div v-for="h in preflight.will_skip" :key="h.host_id">{{h.name}} <span class="mono">{{h.ip}}</span></div>
    </div>
    <template #footer>
      <el-button @click="preflightVisible=false">取消</el-button>
      <el-button type="primary" :loading="deploying" :disabled="!preflightReady" @click="confirmRun">确认部署</el-button>
    </template>
  </el-dialog>

  <el-drawer v-model="instDrawerVisible" :title="instDetail?.name || '集群'" size="42%" class="deploy-drawer">
    <div v-if="instDetail" class="deploy-drawer-meta">
      <span class="deploy-drawer-meta-tpl">{{instDetail.stack_name}} · {{modeLabel(instDetail.mode, instDetail.stack_key)}}</span>
      <span class="stack-runtime" :class="isContainerStack(instDetail) ? 'is-container' : 'is-host'">{{runtimeLabel(instDetail)}}</span>
      <el-tag :type="instStatusType(instDetail.status)" size="small">{{instStatusLabel(instDetail.status)}}</el-tag>
    </div>
    <div class="stack-inst-tags" style="margin:8px 0 14px">
      <el-tag v-for="c in instCompTags(instDetail)" :key="c" size="small">{{compLabel(c)}}</el-tag>
    </div>
    <div class="stack-inst-ops" style="margin-bottom:16px" v-if="instDetail">
      <el-button size="small" :disabled="busyInst(instDetail) || instDetail.status==='uninstalled'" @click="openScaleOut(instDetail)">扩容</el-button>
      <el-button size="small" :disabled="busyInst(instDetail) || instDetail.status==='uninstalled' || activeInstHosts(instDetail).length<2" @click="openScaleIn(instDetail)">缩容</el-button>
      <el-button v-if="instDetail.category==='platform'" size="small" :disabled="busyInst(instDetail) || instDetail.status==='uninstalled' || !unusedCompsOf(instDetail).length" @click="openAddComp(instDetail)">加装</el-button>
      <el-button v-if="instDetail.category==='platform'" size="small" :disabled="busyInst(instDetail) || instDetail.status==='uninstalled' || !removableCompsOf(instDetail).length" @click="openRemoveComp(instDetail)">卸载组件</el-button>
      <el-button size="small" type="danger" :disabled="busyInst(instDetail) || instDetail.status==='uninstalled'" @click="uninstallInstance(instDetail)">卸载</el-button>
      <el-button size="small" text @click="renameInstance(instDetail)">改名</el-button>
      <el-button size="small" text type="danger" :disabled="instDetail.status==='deploying'" @click="deleteInstance(instDetail)">删除</el-button>
    </div>
    <div class="drawer-sec-title">成员</div>
    <div class="deploy-drawer-hostlist">
      <div class="deploy-drawer-host" v-for="h in (instDetail.hosts||[]).filter(x => x.status!=='removed')" :key="h.id">
        <span class="deploy-host-name">{{h.host_name}}</span>
        <span class="mono deploy-host-ip">{{h.host_ip}}</span>
        <el-tag size="small" type="info">{{roleLabel(h.role)}}</el-tag>
      </div>
      <div v-if="!(instDetail.hosts||[]).filter(x => x.status!=='removed').length" class="faint">暂无成员</div>
    </div>
    <div class="drawer-sec-title" style="margin-top:16px">流程记录</div>
    <el-table :data="instRuns" size="small" @row-click="openDrawer">
      <el-table-column label="ID" width="70"><template #default="{row}"><span class="mono">#{{row.id}}</span></template></el-table-column>
      <el-table-column label="操作" width="80"><template #default="{row}">{{opLabel(row.op)}}</template></el-table-column>
      <el-table-column label="状态" width="90"><template #default="{row}"><el-tag :type="taskTagType(row.status)" size="small">{{taskStatusLabel(row.status)}}</el-tag></template></el-table-column>
      <el-table-column label="时间"><template #default="{row}"><span class="mono faint">{{formatTime(row.created_at)}}</span></template></el-table-column>
    </el-table>
  </el-drawer>

  <el-drawer v-model="drawerVisible" :title="'套件运行 #' + (recordMeta?.id || '')" size="48%" class="deploy-drawer" @closed="closeDrawer">
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
        <span class="stack-phase-pills">
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
          <span class="stack-log-phase">{{phaseName(l.phase)}}</span>
          <span class="deploy-log-text">{{l.text}}</span>
        </div>
      </div>
    </div>
  </el-drawer>
</div>`,

  data() {
    return {
      step: 1, stacks: [], stacksLoading: false, selectedKey: '', mode: '',
      bdSel: ['hdfs'],
      bigdataComps: [
        { key: 'hdfs', label: 'HDFS 存储', required: true, desc: '1 NameNode + N DataNode，集群存储基座，必选' },
        { key: 'zookeeper', label: 'ZooKeeper', required: false, desc: '协调服务，全体节点 ensemble（HBase 依赖）' },
        { key: 'yarn', label: 'YARN', required: false, desc: 'ResourceManager + NodeManager 资源调度' },
        { key: 'spark', label: 'Spark 计算', required: false, desc: 'standalone 1 Master + N Worker' },
        { key: 'flink', label: 'Flink 计算', required: false, desc: 'session 1 JobManager + N TaskManager' },
        { key: 'hive', label: 'Hive 数仓', required: false, desc: 'Metastore + HiveServer2（单实例，依赖 HDFS；Trino 依赖）' },
        { key: 'hbase', label: 'HBase', required: false, deps: ['zookeeper'], desc: 'NoSQL 列存数据库，依赖 ZooKeeper' },
        { key: 'trino', label: 'Trino', required: false, deps: ['hive'], desc: '交互式 SQL 查询引擎，依赖 Hive Metastore' }
      ],
      replicas: '0', masterHostId: 0,
      hosts: [], hostsLoading: false, hostsLoaded: false, selectedHostIds: new Set(),
      hostFilter: '', hostSort: { field: 'name', order: 'asc' },
      sharedParams: {}, hostParams: {},
      deploying: false, wizardVisible: false, wizardOp: 'create', clusterName: '',
      targetInstance: null, memberHostIds: new Set(),
      instances: [], instancesLoading: false,
      instDrawerVisible: false, instDetail: null, instRuns: [],
      preflightVisible: false, preflight: {},
      drawerVisible: false, recordMeta: null, recordHosts: [], recordLogs: [], logFilter: '',
      sseLog: null, setupSse: null, logAutoScroll: true
    }
  },
  computed: {
    selectedStack() { return this.stacks.find(s => s.key === this.selectedKey) || null },
    stackGroups() {
      const defs = [
        { key: 'service', label: '单服务集群', hint: '一个中间件做成主从 / 哨兵 / 集群；卡片标明容器或主机安装' },
        { key: 'platform', label: '组合套件', hint: '多个组件按层部署，例如 HDFS → Spark / Flink / Hive' }
      ]
      return defs.map(d => ({
        ...d,
        stacks: this.stacks.filter(s => (s.category || 'service') === d.key)
      })).filter(g => g.stacks.length)
    },
    selectedMode() { return (this.selectedStack?.modes || []).find(m => m.key === this.mode) || null },
    wizardTitle() {
      const name = this.targetInstance?.name || ''
      return {
        create: '新建集群', scale_out: '扩容 · ' + name, scale_in: '缩容 · ' + name,
        add_component: '加装组件 · ' + name, remove_component: '卸载组件 · ' + name
      }[this.wizardOp] || '套件部署'
    },
    wizardFirstStepTitle() {
      return { create: '选择套件', add_component: '选择组件', remove_component: '选择卸载组件' }[this.wizardOp] || '选择套件'
    },
    wizardStepIndex() {
      if (this.wizardOp === 'create' || this.wizardOp === 'add_component' || this.wizardOp === 'remove_component') return this.step - 1
      if (this.wizardOp === 'scale_in') return 0
      return this.step === 3 ? 1 : 0
    },
    defaultClusterName() {
      const s = this.selectedStack
      const m = this.selectedMode
      if (!s) return ''
      return s.name + '-' + (m?.label || this.mode || '')
    },
    unusedWizardComps() {
      if (this.wizardOp === 'remove_component') return this.removableCompsOf(this.targetInstance)
      return this.unusedCompsOf(this.targetInstance)
    },
    needMaster() { return this.wizardOp !== 'scale_out' && this.wizardOp !== 'scale_in' && !!this.selectedMode?.assign_master },
    filteredHosts() {
      let list = this.hosts
      if (this.wizardOp === 'scale_out') list = list.filter(h => !this.memberHostIds.has(h.id))
      if (this.wizardOp === 'scale_in') list = list.filter(h => this.memberHostIds.has(h.id))
      if (this.hostFilter) {
        const kw = this.hostFilter.toLowerCase()
        list = list.filter(h => (h.name||'').toLowerCase().includes(kw) || (h.ip||'').toLowerCase().includes(kw) || (h.tag||'').toLowerCase().includes(kw))
      }
      return this.sortHostList(list, this.hostSort)
    },
    hostVars() {
      const list = this.selectedStack?.host_vars || []
      return list.filter(v => !v.modes || !v.modes.length || v.modes.includes(this.mode))
    },
    visibleSharedVars() {
      const list = this.selectedStack?.shared_vars || []
      const varComp = {
        image: 'hdfs', nn_rpc_port: 'hdfs', replication: 'hdfs',
        image_zookeeper: 'zookeeper',
        nm_mem: 'yarn', nm_vcores: 'yarn',
        image_spark: 'spark', master_port: 'spark', webui_port: 'spark', worker_cores: 'spark', worker_mem: 'spark',
        image_flink: 'flink', jm_rpc_port: 'flink', tm_slots: 'flink',
        image_hive: 'hive',
        image_hbase: 'hbase',
        image_trino: 'trino', trino_http_port: 'trino', trino_mem: 'trino'
      }
      return list.filter(v => !v.modes || !v.modes.length || v.modes.includes(this.mode))
        .filter(v => v.name !== 'replicas' && v.name !== 'components')
        .filter(v => this.selectedKey !== 'bigdata' || !varComp[v.name] || this.bdSel.includes(varComp[v.name]))
    },
    selectedHosts() { return this.hosts.filter(h => this.selectedHostIds.has(h.id)) },
    minHosts() {
      if (this.wizardOp === 'scale_out' || this.wizardOp === 'scale_in' || this.wizardOp === 'add_component') return 1
      if (this.selectedKey === 'redis' && this.mode === 'cluster') return this.replicas === '1' ? 6 : 3
      return this.selectedMode?.min_hosts || 2
    },
    showBootstrapPill() { return this.recordHosts.some(h => h.bootstrap_status && h.bootstrap_status !== 'skipped') },
    showPrereqPill() { return this.isContainerStack(this.recordMeta) },
    hostHint() {
      if (this.wizardOp === 'scale_out') return '选择要加入的新主机'
      if (this.wizardOp === 'scale_in') return '选择要移除的成员（不能全删，请用卸载）'
      return this.selectedMode?.host_hint || ''
    },
    canStep2() {
      if (this.wizardOp === 'add_component' || this.wizardOp === 'remove_component') return this.bdSel.length > 0
      return !!(this.selectedKey && this.mode)
    },
    canStep3() {
      if (this.selectedHosts.length < this.minHosts) return false
      if (this.wizardOp === 'scale_in' && this.selectedHosts.length >= this.memberHostIds.size) return false
      if (this.wizardOp === 'create' && this.selectedKey === 'redis' && this.mode === 'cluster' && this.replicas === '1' && this.selectedHosts.length % 2 !== 0) return false
      if (this.needMaster && !this.selectedHosts.some(h => h.id === this.masterHostId)) return false
      return true
    },
    canRun() {
      if (!this.canStep3) return false
      return this.visibleSharedVars.every(v => {
        if (!v.required) return true
        return !!(this.sharedParams[v.name] || v.default || '').trim()
      })
    },
    wizardHint() {
      if (this.step === 2 && !this.canStep3) return '主机数量或主节点不满足当前模式'
      if (this.step === 3 && !this.canRun) {
        const miss = this.visibleSharedVars.find(v => v.required && !(this.sharedParams[v.name] || v.default || '').trim())
        return miss ? '请填写' + miss.label : '参数未填写完整'
      }
      return ''
    },
    runningCount() { return this.instances.filter(t => t.status === 'deploying').length },
    preflightReady() { return !(this.preflight.unreachable && this.preflight.unreachable.length) },
    filteredLogs() {
      if (!this.logFilter) return this.recordLogs
      const kw = this.logFilter.toLowerCase()
      return this.recordLogs.filter(l => (l.ip||'').toLowerCase().includes(kw) || (l.text||'').toLowerCase().includes(kw) || (l.phase||'').toLowerCase().includes(kw))
    }
  },
  mounted() { this.loadInstances(); this.loadStacks(); this.connectSetup() },
  beforeUnmount() { this.closeDrawer(); this.closeSetup() },
  methods: {
    async loadStacks() {
      this.stacksLoading = true
      try { const r = await api.get('/stacks'); if (r.code === 0) this.stacks = r.data || [] } catch (e) { /* */ }
      finally { this.stacksLoading = false }
    },
    async loadHosts() {
      if (this.hostsLoading) return
      this.hostsLoading = true
      try {
        const r = await api.get('/hosts', { params: { page: 1, page_size: 200, status: 'online' } })
        if (r.code === 0) { this.hosts = r.data?.list || []; this.hostsLoaded = true }
      } catch (e) { /* */ } finally { this.hostsLoading = false }
    },
    async loadInstances() {
      this.instancesLoading = true
      try { const r = await api.get('/stacks/instances', { params: { page: 1, page_size: 50 } }); if (r.code === 0) this.instances = r.data?.list || [] } catch (e) { /* */ }
      finally { this.instancesLoading = false }
    },
    instCompTags(it) {
      if (!it) return []
      try {
        const arr = JSON.parse(it.components_json || '[]')
        return Array.isArray(arr) ? arr : []
      } catch (e) { return [] }
    },
    unusedCompsOf(it) {
      if (!it || it.stack_key !== 'bigdata') return []
      const have = new Set(this.instCompTags(it))
      return this.bigdataComps.filter(c => !c.required && !have.has(c.key))
    },
    removableCompsOf(it) {
      if (!it || it.stack_key !== 'bigdata') return []
      return this.instCompTags(it).filter(k => k !== 'hdfs').map(k => {
        const def = this.bigdataComps.find(c => c.key === k)
        return def || { key: k, label: this.compLabel(k), desc: '已安装，可单独卸载' }
      })
    },
    busyInst(it) { return !it || it.status === 'deploying' },
    activeInstHosts(it) { return (it?.hosts || []).filter(h => h.status !== 'removed') },
    isContainerStack(it) {
      if (!it) return true
      if (typeof it.requires_docker === 'boolean') return it.requires_docker
      const s = this.stacks.find(x => x.key === (it.stack_key || it.key))
      return !s || !!s.requires_docker
    },
    runtimeLabel(it) { return this.isContainerStack(it) ? '容器' : '主机安装' },
    compLabel(k) { return (this.bigdataComps.find(c => c.key === k) || {}).label || k },
    instStatusType(s) { return { ready: 'success', deploying: 'warning', partial: 'danger', failed: 'danger', uninstalled: 'info' }[s] || 'info' },
    instStatusLabel(s) { return { ready: '就绪', deploying: '变更中', partial: '部分成功', failed: '失败', uninstalled: '已卸载' }[s] || s },
    applyInstanceContext(it) {
      this.targetInstance = it
      this.selectedKey = it.stack_key
      this.mode = it.mode
      this.memberHostIds = new Set((it.hosts || []).filter(h => h.status !== 'removed').map(h => h.host_id))
      try { this.sharedParams = JSON.parse(it.params_json || '{}') || {} } catch (e) { this.sharedParams = {} }
      const tags = this.instCompTags(it)
      this.bdSel = tags.length ? tags.slice() : ['hdfs']
    },
    async prepareInstance(it, op) {
      this.resetWizard()
      this.instDrawerVisible = false
      this.wizardOp = op || 'create'
      let full = it
      try {
        const r = await api.get('/stacks/instances/' + it.id)
        if (r.code === 0) full = r.data
      } catch (e) { /* */ }
      this.applyInstanceContext(full)
      this.wizardVisible = true
      this.loadHosts()
      return full
    },
    openWizard() {
      this.resetWizard()
      this.wizardOp = 'create'
      this.wizardVisible = true
      this.loadStacks()
    },
    async openScaleOut(it) {
      await this.prepareInstance(it, 'scale_out')
      this.step = 2
    },
    async openScaleIn(it) {
      await this.prepareInstance(it, 'scale_in')
      this.step = 2
    },
    async openAddComp(it) {
      const full = await this.prepareInstance(it, 'add_component')
      this.bdSel = []
      this.step = 1
      this.selectedHostIds = new Set(this.memberHostIds)
      const master = (full.hosts || []).find(h => h.role === 'master' && h.status !== 'removed')
      this.masterHostId = master ? master.host_id : 0
    },
    async openRemoveComp(it) {
      await this.prepareInstance(it, 'remove_component')
      this.bdSel = []
      this.step = 1
    },
    async openInstance(it) {
      this.instDrawerVisible = true
      this.instDetail = it
      this.instRuns = []
      try {
        const d = await api.get('/stacks/instances/' + it.id)
        if (d.code === 0) this.instDetail = d.data
      } catch (e) { /* */ }
      try {
        const r = await api.get('/stacks/instances/' + it.id + '/runs')
        if (r.code === 0) this.instRuns = r.data || []
      } catch (e) { /* */ }
    },
    async renameInstance(it) {
      try {
        const { value } = await ElMessageBox.prompt('集群名称', '重命名', { inputValue: it.name, confirmButtonText: '保存', cancelButtonText: '取消' })
        const name = (value || '').trim()
        if (!name) return
        const r = await api.patch('/stacks/instances/' + it.id, { name })
        if (r.code !== 0) { ElMessage.error(r.message || '更新失败'); return }
        ElMessage.success('已改名')
        this.loadInstances()
        if (this.instDrawerVisible && this.instDetail && this.instDetail.id === it.id) this.openInstance(this.instDetail)
      } catch (e) { /* cancel */ }
    },
    async uninstallInstance(it) {
      try {
        await ElMessageBox.confirm('将停止全部节点上的套件服务，集群记录会保留为「已卸载」，之后仍可查看流程或删除记录。', '卸载 ' + it.name, {
          type: 'warning', confirmButtonText: '确认卸载', cancelButtonText: '取消'
        })
      } catch (e) { return }
      try {
        const r = await api.post('/stacks/instances/' + it.id + '/uninstall')
        if (r.code !== 0) { ElMessage.error(r.message || '卸载失败'); return }
        ElMessage.success('已开始卸载')
        await this.afterStackOp()
      } catch (e) { ElMessage.error(e.message || '卸载失败') }
    },
    async deleteInstance(it) {
      const msg = it.status === 'uninstalled'
        ? '删除集群记录，不可恢复。节点上的服务此前已卸载。'
        : '仅删除集群记录，不会停止节点上的服务。如需停服请先卸载。'
      try {
        await ElMessageBox.confirm(msg, '删除 ' + it.name, { type: 'warning', confirmButtonText: '删除记录', cancelButtonText: '取消' })
      } catch (e) { return }
      try {
        const r = await api.delete('/stacks/instances/' + it.id)
        if (r.code !== 0) { ElMessage.error(r.message || '删除失败'); return }
        ElMessage.success('已删除记录')
        this.instDrawerVisible = false
        this.loadInstances()
      } catch (e) { ElMessage.error(e.message || '删除失败') }
    },
    resetWizard() {
      this.step = 1; this.selectedKey = ''; this.mode = ''; this.replicas = '0'; this.masterHostId = 0
      this.bdSel = ['hdfs']
      this.selectedHostIds = new Set(); this.sharedParams = {}; this.hostParams = {}
      this.preflightVisible = false; this.preflight = {}; this.deploying = false
      this.wizardOp = 'create'; this.clusterName = ''; this.targetInstance = null; this.memberHostIds = new Set()
    },
    selectStack(s) {
      this.selectedKey = s.key
      if (s.key === 'bigdata') this.bdSel = ['hdfs']
      if (s.modes && s.modes.length) this.onModeChange(s.modes[0].key)
    },
    toggleBigdataComp(key, val) {
      const c = this.bigdataComps.find(x => x.key === key)
      if (c?.required && this.wizardOp === 'create') return
      const set = new Set(this.bdSel)
      if (val) {
        set.add(key)
        // 依赖自动联动：勾 HBase 带上 ZooKeeper，勾 Trino 带上 Hive
        ;(c?.deps || []).forEach(d => set.add(d))
      } else {
        // 被依赖的组件不能取消（先取消依赖它的组件）
        const needed = this.bigdataComps.some(x => this.bdSel.includes(x.key) && (x.deps || []).includes(key))
        if (needed) {
          ElMessage.warning(`组件 ${c.label} 正被其他已选组件依赖，请先取消对应组件`)
          return
        }
        set.delete(key)
      }
      if (this.wizardOp === 'remove_component' || this.wizardOp === 'add_component') {
        this.bdSel = [...set]
        return
      }
      this.bdSel = this.bigdataComps.map(x => x.key).filter(k => set.has(k))
    },
    onModeChange(key) {
      this.mode = key
      const m = (this.selectedStack?.modes || []).find(x => x.key === key)
      const home = m?.default_home_dir || (this.selectedStack?.host_vars || []).find(v => v.name === 'home_dir')?.default || '/data'
      const shared = {}
      ;(this.selectedStack?.shared_vars || []).forEach(v => {
        if (v.modes && v.modes.length && !v.modes.includes(key)) return
        shared[v.name] = this.sharedParams[v.name] || v.default || ''
      })
      this.sharedParams = shared
      this.hostVars.forEach(v => {
        if (v.name === 'home_dir') v._modeDefault = home
      })
    },
    goStep(n) {
      this.step = n
      if (n >= 2) this.loadHosts()
      if (n === 2 && this.needMaster && !this.masterHostId && this.selectedHosts.length) {
        this.masterHostId = this.selectedHosts[0].id
      }
    },
    toggleHost(id) {
      const next = new Set(this.selectedHostIds)
      if (next.has(id)) next.delete(id); else next.add(id)
      this.selectedHostIds = next
      if (this.needMaster) {
        if (!next.has(this.masterHostId)) this.masterHostId = next.size ? [...next][0] : 0
      }
    },
    toggleSelectAll() {
      if (this.selectedHosts.length === this.filteredHosts.length) this.selectedHostIds = new Set()
      else this.selectedHostIds = new Set(this.filteredHosts.map(h => h.id))
      if (this.needMaster && this.selectedHosts.length && !this.selectedHostIds.has(this.masterHostId)) {
        this.masterHostId = this.selectedHosts[0]?.id || 0
      }
    },
    toggleHostSortOrder() { this.hostSort = { ...this.hostSort, order: this.hostSort.order === 'asc' ? 'desc' : 'asc' } },
    sortHostList(list, sort) {
      const arr = list.slice()
      const cmp = sort.field === 'ip' ? window.cmpHostIP : window.cmpHostName
      arr.sort(cmp)
      if (sort.order === 'desc') arr.reverse()
      return arr
    },
    hostVarPlaceholder(v) {
      if (v.name === 'home_dir') return this.selectedMode?.default_home_dir || v.default
      return v.default || ''
    },
    hostParamValue(id, v) { return (this.hostParams[id] && this.hostParams[id][v.name]) || '' },
    onHostParam(id, name, val) {
      const cur = { ...(this.hostParams[id] || {}) }
      cur[name] = val
      this.hostParams = { ...this.hostParams, [id]: cur }
    },
    copyFirstToAll() {
      const first = this.selectedHosts[0]
      if (!first) return
      const src = this.hostParams[first.id] || {}
      const next = { ...this.hostParams }
      this.selectedHosts.slice(1).forEach(h => { next[h.id] = { ...src } })
      this.hostParams = next
    },
    async startPreflight() {
      if (!this.selectedStack?.requires_docker) {
        await this.confirmRun()
        return
      }
      this.deploying = true
      try {
        const r = await api.post('/stacks/preflight', { host_ids: this.selectedHosts.map(h => h.id) })
        if (r.code !== 0) { ElMessage.error(r.message || '预检失败'); return }
        this.preflight = r.data || {}
        this.preflightVisible = true
      } catch (e) { ElMessage.error(e.message || '预检失败') }
      finally { this.deploying = false }
    },
    async confirmRun() {
      this.deploying = true
      try {
        const r = await this.submitWizard()
        if (!r) return
        if (r.code !== 0) { ElMessage.error(r.message || '创建失败'); return }
        ElMessage.success('已开始执行')
        this.preflightVisible = false
        this.wizardVisible = false
        await this.afterStackOp()
      } catch (e) { ElMessage.error(e.message || '创建失败') }
      finally { this.deploying = false }
    },
    async confirmScaleIn() {
      try {
        await ElMessageBox.confirm('将停止并移除选中节点上的套件服务，集群记录会保留。', '确认缩容', { type: 'warning' })
      } catch (e) { return }
      this.deploying = true
      try {
        const r = await this.submitWizard()
        if (!r) return
        if (r.code !== 0) { ElMessage.error(r.message || '缩容失败'); return }
        ElMessage.success('已开始缩容')
        this.wizardVisible = false
        await this.afterStackOp()
      } catch (e) { ElMessage.error(e.message || '缩容失败') }
      finally { this.deploying = false }
    },
    async confirmRemoveComp() {
      try {
        await ElMessageBox.confirm('将在全部成员上卸载所选组件。HDFS 不能单独卸载，需整集群卸载。', '确认卸载组件', { type: 'warning' })
      } catch (e) { return }
      this.deploying = true
      try {
        const r = await this.submitWizard()
        if (!r) return
        if (r.code !== 0) { ElMessage.error(r.message || '卸载组件失败'); return }
        ElMessage.success('已开始卸载组件')
        this.wizardVisible = false
        await this.afterStackOp()
      } catch (e) { ElMessage.error(e.message || '卸载组件失败') }
      finally { this.deploying = false }
    },
    async submitWizard() {
      const host_params = {}
      this.selectedHosts.forEach(h => { if (this.hostParams[h.id]) host_params[String(h.id)] = this.hostParams[h.id] })
      const params = { ...this.sharedParams }
      if (this.selectedKey === 'redis' && this.mode === 'cluster') params.replicas = String(this.replicas)
      if (this.selectedKey === 'bigdata') params.components = this.bdSel.join(',')
      const body = {
        stack_key: this.selectedKey, mode: this.mode,
        name: this.clusterName.trim(),
        host_ids: this.selectedHosts.map(h => h.id),
        master_host_id: this.needMaster ? this.masterHostId : 0,
        params, host_params
      }
      if (this.wizardOp === 'create') return api.post('/stacks/run', body)
      const id = this.targetInstance?.id
      if (!id) { ElMessage.error('缺少集群实例'); return null }
      const path = { scale_out: 'scale-out', scale_in: 'scale-in', add_component: 'add-component', remove_component: 'remove-component' }[this.wizardOp]
      return api.post('/stacks/instances/' + id + '/' + path, body)
    },
    async afterStackOp() {
      await this.loadInstances()
      if (this.instDrawerVisible && this.instDetail?.id) {
        await this.openInstance(this.instDetail)
      }
    },
    connectSetup() {
      this.closeSetup()
      const source = new EventSource('/api/sse/stacks/setup', { withCredentials: true })
      this.setupSse = source
      source.addEventListener('init', (e) => {
        try { const d = JSON.parse(e.data); if (d.running && d.running.length) this.loadInstances() } catch (err) { /* */ }
      })
      source.addEventListener('track', (e) => {
        try {
          const d = JSON.parse(e.data)
          if (this.drawerVisible && this.recordMeta && d.run_id === this.recordMeta.id && d.host_id) {
            const h = this.recordHosts.find(x => x.host_id === d.host_id)
            if (h) {
              if (d.status) h.status = d.status
              if (d.prereq_status) h.prereq_status = d.prereq_status
              if (d.node_status) h.node_status = d.node_status
              if (d.bootstrap_status) h.bootstrap_status = d.bootstrap_status
            }
          }
        } catch (err) { /* */ }
      })
      source.addEventListener('done', () => { this.afterStackOp() })
      source.onerror = () => { /* */ }
    },
    closeSetup() { if (this.setupSse) { this.setupSse.close(); this.setupSse = null } },
    async openDrawer(row) {
      this.drawerVisible = true
      this.recordMeta = { id: row.id, status: row.status, stack_key: row.stack_key, stack_name: row.stack_name, mode: row.mode, created_at: row.created_at }
      this.recordHosts = []; this.logFilter = ''; this.disconnectLog()
      try {
        const r = await api.get('/stacks/runs/' + row.id)
        if (r.code === 0) {
          this.recordMeta = { ...this.recordMeta, ...r.data }
          this.recordHosts = r.data.hosts || []
        }
      } catch (e) { /* */ }
      this.connectLog(row.id)
    },
    closeDrawer() {
      this.disconnectLog()
      this.drawerVisible = false
      this.recordMeta = null; this.recordHosts = []; this.recordLogs = []
    },
    connectLog(runId) {
      this.disconnectLog()
      const source = new EventSource('/api/sse/stacks/log?run_id=' + runId, { withCredentials: true })
      this.sseLog = source
      source.addEventListener('init', (e) => {
        try {
          const d = JSON.parse(e.data)
          this.recordLogs = (d.logs || []).map(l => ({ id: l.id, ts: l.ts, ip: l.ip, phase: l.phase, text: l.text }))
          if (d.run_status && d.run_status !== 'running') this.disconnectLog()
          this.$nextTick(() => this.scrollLogsBottom())
        } catch (err) { /* */ }
      })
      source.addEventListener('log', (e) => {
        try {
          const l = JSON.parse(e.data)
          this.recordLogs.push({ id: l.id, ts: l.ts, ip: l.ip, phase: l.phase, text: l.text })
          this.$nextTick(() => this.scrollLogsBottom())
        } catch (err) { /* */ }
      })
      source.addEventListener('done', async (e) => {
        try { const d = JSON.parse(e.data); if (d.run_status) this.recordMeta.status = d.run_status } catch (err) { /* */ }
        try {
          const r = await api.get('/stacks/runs/' + runId)
          if (r.code === 0) { this.recordMeta = { ...this.recordMeta, ...r.data }; this.recordHosts = r.data.hosts || [] }
        } catch (err) { /* */ }
        this.disconnectLog(); this.afterStackOp()
      })
    },
    disconnectLog() { if (this.sseLog) { this.sseLog.close(); this.sseLog = null } },
    onLogScroll(e) {
      const el = e.target
      this.logAutoScroll = el.scrollHeight - el.scrollTop - el.clientHeight < 30
    },
    scrollLogsBottom() {
      if (!this.logAutoScroll) return
      const el = this.$refs.stackLogBox
      if (el) el.scrollTop = el.scrollHeight
    },
    hostRoleTag(h) {
      if (h.id === this.masterHostId) {
        if (this.selectedKey === 'elasticsearch' || this.selectedKey === 'rabbitmq') return '引导'
        return '主'
      }
      if (this.mode === 'replication' || this.mode === 'sentinel') return '从'
      if (this.selectedKey === 'rocketmq' && this.mode === 'broker') return '从'
      if (this.selectedKey === 'elasticsearch' || this.selectedKey === 'rabbitmq') return '加入'
      return '工作节点'
    },
    opLabel(op) { return { create: '创建', scale_out: '扩容', scale_in: '缩容', add_component: '加装', uninstall: '卸载', remove_component: '卸组件' }[op] || op || '创建' },
    modeLabel(m, stackKey) {
      const key = stackKey || this.recordMeta?.stack_key || this.selectedKey
      const s = this.stacks.find(x => x.key === key)
      const md = s && s.modes ? s.modes.find(x => x.key === m) : null
      if (md && md.label) return md.label
      return { replication: '主从', sentinel: '哨兵', cluster: '集群' }[m] || m
    },
    roleLabel(r) { return { master: '主', replica: '从', node: '节点', worker: '工作节点' }[r] || r },
    phaseName(p) { return { prereq: 'Docker', node: '节点', bootstrap: '初始化' }[p] || p || '' },
    phaseText(s) { return { pending: '等待', running: '进行', success: '成功', failed: '失败', skipped: '跳过' }[s] || s },
    phaseTag(s) { if (s === 'success' || s === 'skipped') return 'success'; if (s === 'failed') return 'danger'; if (s === 'running') return 'warning'; return 'info' },
    hostStatusCls(s) { if (s === 'success') return 'online'; if (s === 'failed') return 'offline'; if (s === 'running') return 'running'; return 'unverified' },
    hostStatusText(s) { return { pending: '等待中', running: '执行中', success: '成功', failed: '失败', skipped: '跳过' }[s] || s },
    logTime(ts) { return ts ? (ts.length >= 19 ? ts.slice(11, 19) : ts) : '' },
    taskTagType(s) { if (s === 'success') return 'success'; if (s === 'failed' || s === 'partial') return 'danger'; if (s === 'running') return 'warning'; return 'info' },
    taskStatusLabel(s) { return { running: '执行中', success: '已完成', partial: '部分成功', failed: '失败' }[s] || s },
    formatTime(t) {
      if (!t) return '-'
      const d = new Date(t.replace(' ', 'T') + (t.includes('Z') ? '' : 'Z'))
      return d.getFullYear() + '-' + String(d.getMonth()+1).padStart(2,'0') + '-' + String(d.getDate()).padStart(2,'0') + ' ' + String(d.getHours()).padStart(2,'0') + ':' + String(d.getMinutes()).padStart(2,'0')
    }
  }
}
