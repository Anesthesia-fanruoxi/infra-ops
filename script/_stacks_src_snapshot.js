// 探活表格：把「a, b, c」这类逗号列表拆成数组，供省略号 + tooltip 展示
const splitList = (s) => String(s == null ? '' : s).split(/,\s*/).map(x => x.trim()).filter(Boolean)

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
      <div class="stack-inst-top">
        <div class="stack-inst-avatar" :class="stackAvatar(it).c">{{stackAvatar(it).t}}</div>
        <div class="stack-inst-title">
          <div class="stack-inst-name">{{it.name}}<span v-if="instIsHa(it)" class="stack-ha-badge" style="margin-left:6px;position:relative;top:-1px">HA</span></div>
          <div class="stack-inst-meta">{{it.stack_name}} · {{modeLabel(it.mode, it.stack_key)}}</div>
        </div>
        <div class="stack-inst-side">
          <span class="stack-runtime" :class="isContainerStack(it) ? 'is-container' : 'is-host'">{{runtimeLabel(it)}}</span>
          <span class="stack-inst-status" :class="'is-'+it.status"><i class="dot"></i>{{instStatusLabel(it.status)}}</span>
        </div>
      </div>
      <div class="stack-inst-tags">
        <template v-if="isEsCwh(it)">
          <span v-for="t in instTierTags(it)" :key="t" class="es-tier-chip">{{t}}</span>
        </template>
        <el-tag v-else v-for="c in instDisplayComps(it)" :key="c" size="small" effect="plain" round>{{compLabel(it.stack_key, c)}}</el-tag>
      </div>
      <div class="stack-inst-foot">
        <span class="stack-inst-hosts"><el-icon :size="14"><Monitor /></el-icon>{{it.host_count || 0}} 台主机</span>
        <div class="stack-inst-ops" @click.stop>
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
    <div v-if="!instancesLoading && !instances.length" class="stack-inst-empty">
      <el-icon :size="30" style="opacity:.45;margin-bottom:8px"><Box /></el-icon>
      <div>还没有集群实例，点击右上角新建</div>
    </div>
  </div>

  <el-dialog v-model="wizardVisible" :title="wizardTitle" width="85%" top="6vh" class="stack-wizard-dialog" :close-on-click-modal="false" @closed="resetWizard">
    <el-steps :active="wizardStepIndex" finish-status="success" align-center style="margin-bottom:20px">
      <el-step v-if="wizardOp==='create' || wizardOp==='add_component' || wizardOp==='remove_component'" :title="wizardFirstStepTitle" />
      <el-step v-if="wizardOp!=='remove_component'" :title="wizardOp==='scale_in' ? '选择移除主机' : '选择主机'" />
      <el-step v-if="wizardOp!=='scale_in' && wizardOp!=='remove_component'" title="填写参数" />
    </el-steps>

    <div v-show="step===1">
      <div v-if="wizardOp==='add_component' || wizardOp==='remove_component'">
        <component v-if="opFormComp" :is="opFormComp" :ctx="selfCtx" />
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
              <div class="stack-card-ava" :class="stackAvatar(s).c">{{stackAvatar(s).t}}</div>
              <div class="stack-card-name">{{s.name}}</div>
              <span class="stack-runtime" :class="s.requires_docker ? 'is-container' : 'is-host'">{{runtimeLabel(s)}}</span>
              <el-switch v-if="s.ha_support && wizardOp==='create'" size="small" :model-value="sharedParams.ha==='true'" :disabled="selectedKey!==s.key" :inline-prompt="true" active-text="HA" inactive-text="" @click.stop.prevent @change="e => onHaToggle(e)"></el-switch>
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
        <component v-if="step1FormComp" :is="step1FormComp" :ctx="selfCtx" />
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
        <p class="stack-hint stack-ha-hint" v-if="selectedStack?.ha_support && sharedParams.ha==='true'">本次安装所有支持 HA 的组件将双实例部署，依赖 ZooKeeper 已自动勾选，最低需 3 台主机。</p>
        <p class="stack-hint" v-for="h in step1Hints" :key="h">{{h}}</p>
      </div>
      </div>
    </div>

    <div v-show="step===2">
      <!-- 大数据等角色规划套件：表格勾选成员 → 按组件指定主角色 -->
      <template v-if="useRolePlan">
        <div class="deploy-host-toolbar">
          <el-input v-model="hostFilter" placeholder="搜索主机名 / IP / 标签" clearable size="small" style="width:220px" />
          <el-select v-model="hostSort.field" size="small" style="width:100px" :teleported="false"><el-option label="按主机名" value="name" /><el-option label="按 IP" value="ip" /></el-select>
          <el-button size="small" @click="toggleHostSortOrder">{{hostSort.order==='asc' ? '↑ 升序' : '↓ 降序'}}</el-button>
          <el-button size="small" @click="toggleSelectAll">{{ selectedHosts.length === filteredHosts.length && filteredHosts.length ? '取消全选' : '全选' }}</el-button>
          <span class="deploy-sel-count">已选 {{selectedHosts.length}} / {{filteredHosts.length}} 台 · {{hostHint}}</span>
        </div>
        <el-table
          ref="roleHostTable"
          :data="filteredHosts"
          size="small"
          max-height="320"
          v-loading="hostsLoading"
          class="stack-role-host-table"
          row-key="id"
          @selection-change="onRoleHostSelection"
          empty-text="无匹配主机"
        >
          <el-table-column type="selection" width="48" :reserve-selection="true" />
          <el-table-column prop="name" label="主机名" min-width="140" />
          <el-table-column label="IP" min-width="130">
            <template #default="{row}"><span class="mono">{{row.ip}}</span></template>
          </el-table-column>
          <el-table-column label="标签" width="100">
            <template #default="{row}"><span class="tag-badge other" style="font-size:10px">{{row.tag || 'other'}}</span></template>
          </el-table-column>
          <el-table-column label="状态" width="90">
            <template #default="{row}">
              <span class="status-badge" :class="row.status"><span class="dot"></span>{{row.status==='online'?'在线':row.status==='offline'?'离线':'未验证'}}</span>
            </template>
          </el-table-column>
          <el-table-column label="已指定角色" min-width="180">
            <template #default="{row}">
              <span class="faint">{{hostRolePlanSummary(row) || '—'}}</span>
            </template>
          </el-table-column>
        </el-table>
        <component v-if="step===2 && step2FormComp" :is="step2FormComp" :ctx="selfCtx" />
      </template>

      <!-- Redis 等仍用主从/引导节点单选 -->
      <template v-else>
        <component v-if="step===2 && step2FormComp" :is="step2FormComp" :ctx="selfCtx" />
        <!-- 扩缩容边界（全部套件，拍板 ②）：落点角色冻结，扩缩容只操作数据/工作节点 -->
        <div v-if="wizardOp==='scale_out'" class="stack-freeze-hint">
          主节点 / 引导节点已随集群部署冻结，扩容不改变主节点布局；新机自动承担从属 / 成员等全员角色。
        </div>
        <div v-if="wizardOp==='scale_in'" class="stack-freeze-hint">
          缩容仅限数据 / 工作节点：承载落点角色的成员已标红置灰，不可移除（如需调整主节点布局请重装）。
        </div>
        <div class="deploy-host-toolbar">
          <el-input v-model="hostFilter" placeholder="搜索主机名 / IP / 标签" clearable size="small" style="width:220px" />
          <el-select v-model="hostSort.field" size="small" style="width:100px" :teleported="false"><el-option label="按主机名" value="name" /><el-option label="按 IP" value="ip" /></el-select>
          <el-button size="small" @click="toggleHostSortOrder">{{hostSort.order==='asc' ? '↑ 升序' : '↓ 降序'}}</el-button>
          <el-button size="small" @click="toggleSelectAll">{{ selectedHosts.length === filteredHosts.length ? '取消全选' : '全选' }}</el-button>
          <span class="deploy-sel-count">已选 {{selectedHosts.length}} 台 · {{hostHint}}</span>
        </div>
        <div class="deploy-host-list deploy-host-list--dialog" v-loading="hostsLoading">
          <label v-for="h in filteredHosts" :key="h.id" class="deploy-host-item" :class="{'deploy-host-item--sel': selectedHostIds.has(h.id), 'deploy-host-item--locked': !!scaleInProtected[h.ip]}">
            <el-checkbox :model-value="selectedHostIds.has(h.id)" :disabled="!!scaleInProtected[h.ip]" @change="toggleHost(h.id)" />
            <span class="deploy-host-name">{{h.name}}</span>
            <span class="mono deploy-host-ip">{{h.ip}}</span>
            <span class="tag-badge other" style="font-size:10px">{{h.tag || 'other'}}</span>
            <span class="status-badge" :class="h.status"><span class="dot"></span>{{h.status==='online'?'在线':h.status==='offline'?'离线':'未验证'}}</span>
            <el-tag v-if="scaleInProtected[h.ip]" size="small" type="danger" effect="plain" style="margin-left:auto">{{scaleInProtected[h.ip]}} · 不可缩容</el-tag>
            <el-radio v-if="needMaster && selectedHostIds.has(h.id)" :model-value="masterHostId" :label="h.id" @change="masterHostId=h.id" style="margin-left:auto">主节点</el-radio>
          </label>
          <div v-if="!filteredHosts.length && !hostsLoading" class="deploy-placeholder">无匹配主机</div>
        </div>
      </template>
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
          <el-switch v-if="v.type==='bool'" :model-value="sharedParams[v.name]==='true'" @change="e => onBoolVar(v.name, e)"></el-switch>
          <el-input v-else size="small" v-model="sharedParams[v.name]" :placeholder="v.default || ''" :show-password="v.name==='password'" />
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
            <el-tag v-if="useRolePlan" size="small" type="info">{{hostRolePlanSummary(h) || '工作节点'}}</el-tag>
            <el-tag v-else-if="needMaster" size="small" :type="h.id===masterHostId ? 'danger' : 'info'">{{hostRoleTag(h)}}</el-tag>
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
    <!-- 部署前角色预览（全部套件）：与部署前置一同确认落点，未通过不能确认部署 -->
    <div v-if="needOpPlan" class="stack-plan-preview" style="margin-top:12px">
      <div class="stack-plan-preview-head">
        <span>角色计划预览 · 确认落点后开始部署</span>
        <span v-if="opPlan" class="faint">{{opPlan.generated_by === 'manual' ? '含手动指定' : '自动分配'}}</span>
        <el-button v-if="opPlanErr" size="small" text type="primary" @click="refreshOpPlan">重试</el-button>
      </div>
      <div v-if="opPlanErr" class="stack-plan-preview-warn"><div>{{opPlanErr}}</div></div>
      <div v-else-if="opPlan" class="stack-plan-preview-grid">
        <div v-for="h in opPlan.hosts" :key="h.host_ip" class="stack-plan-preview-row">
          <span class="mono stack-plan-preview-ip">{{h.host_ip}}</span>
          <div class="stack-plan-preview-chips">
            <span v-for="(r, i) in h.roles" :key="i" class="stack-plan-chip" :class="[r.source==='manual' ? 'is-manual' : 'is-auto', roleTextClass(r)]" :title="r.source==='manual' ? '手动指定' : '自动分配'">{{r.label}}</span>
            <span v-if="!(h.roles||[]).length" class="faint">—</span>
          </div>
        </div>
      </div>
      <div v-else class="faint" style="font-size:12px;padding-top:6px">正在生成角色计划预览…</div>
      <div class="plan-legend"><span class="plan-legend-item"><i class="dot is-manual"></i>手动</span><span class="plan-legend-item"><i class="dot is-auto"></i>自动</span><span class="plan-legend-sep"></span><span class="plan-legend-item rt-primary">主/Active</span><span class="plan-legend-item rt-standby">备/Standby</span><span class="plan-legend-item rt-quorum">仲裁</span><span class="plan-legend-item rt-worker">工作/成员</span><span class="plan-legend-item rt-aux">辅助</span></div>
      <div v-if="opPlan && (opPlan.warnings||[]).length" class="stack-plan-preview-warn">
        <div v-for="(w, i) in opPlan.warnings" :key="i">{{w}}</div>
      </div>
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
      <el-tag v-for="c in instDisplayComps(instDetail)" :key="c" size="small">{{compLabel(instDetail.stack_key, c)}}</el-tag>
    </div>
    <div class="stack-inst-ops stack-drawer-ops" style="margin-bottom:16px" v-if="instDetail">
      <el-button v-if="canReinstall(instDetail)" size="small" type="primary" @click="reinstallInstance(instDetail)">重新安装</el-button>
      <el-button size="small" type="primary" plain :loading="verifying" :disabled="instDetail.status==='uninstalled'" @click="openVerifyDialog(instDetail)">探活</el-button>
      <el-button v-if="caDownloadAvailable" size="small" type="primary" plain @click="downloadCa">下载 CA 证书</el-button>
      <el-button size="small" :disabled="busyInst(instDetail) || instDetail.status==='uninstalled'" @click="openScaleOut(instDetail)">扩容</el-button>
      <el-button size="small" :disabled="busyInst(instDetail) || instDetail.status==='uninstalled' || activeInstHosts(instDetail).length<2" @click="openScaleIn(instDetail)">缩容</el-button>
      <el-button v-if="isPlatformStack(instDetail)" size="small" :disabled="busyInst(instDetail) || instDetail.status==='uninstalled'" :title="unusedCompsOf(instDetail).length ? '加装尚未安装的组件' : '所有组件均已安装'" @click="openAddComp(instDetail)">加装组件</el-button>
      <el-button v-if="isPlatformStack(instDetail)" size="small" :disabled="busyInst(instDetail) || instDetail.status==='uninstalled'" :title="removableCompsOf(instDetail).length ? '按组件卸载' : '没有可单独卸载的组件'" @click="openRemoveComp(instDetail)">卸载组件</el-button>
      <el-button v-if="instDetail.status==='failed' || instDetail.status==='partial'" size="small" type="warning" plain :disabled="busyInst(instDetail)" @click="purgeInstance(instDetail)">清理残留</el-button>
    </div>
    <div class="drawer-sec-title"><span>成员节点</span><span class="inst-mem-count">{{(instDetail.hosts||[]).filter(x => x.status!=='removed').length}} 台</span></div>
    <div class="inst-mem-grid">
      <div class="inst-mem-card" v-for="h in (instDetail.hosts||[]).filter(x => x.status!=='removed')" :key="h.id" :class="h.role === 'master' ? 'is-master' : ''">
        <div class="inst-mem-top">
          <span class="inst-mem-name" :title="h.host_name">{{h.host_name}}</span>
          <el-tag size="small" :type="h.role === 'master' ? 'primary' : 'info'" effect="light">{{roleLabel(h.role)}}</el-tag>
        </div>
        <div class="mono inst-mem-ip">{{h.host_ip}}</div>
        <div class="inst-mem-tags">
          <template v-for="tag in memTags(h)" :key="tag"><span class="inst-mem-tag">{{tag}}</span></template>
          <span v-if="!memTags(h).length" class="inst-mem-tag is-idle">工作正常</span>
        </div>
      </div>
      <div v-if="!(instDetail.hosts||[]).filter(x => x.status!=='removed').length" class="faint inst-mem-empty">暂无成员</div>
    </div>
    <!-- 角色计划（全部套件）：部署前物化的落点契约，部署/探活/扩缩容共读（docs/角色物化设计.md） -->
    <template v-if="instDetail.id">
      <div class="drawer-sec-title inst-runs-title">
        <span>角色计划</span>
        <span class="inst-mem-count">
          <el-button v-if="instPlan" size="small" text type="primary" @click="replanInst">重新规划</el-button>
          <span v-else class="faint">加载中…</span>
        </span>
      </div>
      <div v-if="instPlan" class="inst-plan-box">
        <div class="inst-plan-meta">rev {{instPlan.rev}} · {{instPlan.ha ? 'HA' : '单机'}} · <span class="mono">{{instPlan.generated_at}}</span></div>
        <div v-for="h in instPlan.hosts" :key="h.host_ip" class="inst-plan-row">
          <span class="mono inst-plan-ip">{{h.host_ip}}</span>
          <div class="inst-plan-chips">
            <span v-for="(r, i) in h.roles" :key="i" class="inst-plan-chip" :class="[r.source==='manual' ? 'is-manual' : 'is-auto', roleTextClass(r)]" :title="r.source==='manual' ? '手动指定' : '自动分配'">{{r.label}}</span>
            <span v-if="!(h.roles||[]).length" class="faint">—</span>
          </div>
        </div>
        <div class="plan-legend" style="margin-top:2px"><span class="plan-legend-item"><i class="dot is-manual"></i>手动</span><span class="plan-legend-item"><i class="dot is-auto"></i>自动</span><span class="plan-legend-sep"></span><span class="plan-legend-item rt-primary">主/Active</span><span class="plan-legend-item rt-standby">备/Standby</span><span class="plan-legend-item rt-quorum">仲裁</span><span class="plan-legend-item rt-worker">工作/成员</span><span class="plan-legend-item rt-aux">辅助</span></div>
      </div>
    </template>
    <div class="drawer-sec-title inst-runs-title"><span>流程记录</span><span class="inst-mem-count">{{instRuns.length}} 条</span></div>
    <div class="inst-runs-wrap">
      <el-table :data="instRuns" size="small" height="100%" class="inst-runs-table" empty-text="暂无流程记录" @row-click="openDrawer">
        <el-table-column label="ID" width="72" align="center"><template #default="{row}"><span class="mono">#{{row.id}}</span></template></el-table-column>
        <el-table-column label="操作" width="76"><template #default="{row}"><span class="inst-op">{{opLabel(row.op)}}</span></template></el-table-column>
        <el-table-column label="状态" width="120" align="center"><template #default="{row}"><span class="inst-status" :class="row.status"><i class="dot"></i>{{taskStatusLabel(row.status)}}</span></template></el-table-column>
        <el-table-column label="时间"><template #default="{row}"><span class="mono faint">{{formatTime(row.created_at)}}</span></template></el-table-column>
      </el-table>
    </div>
  </el-drawer>

  <el-dialog v-model="verifyVisible" :title="'集群探活 · ' + (verifyTarget?.name || '')" width="65%" top="6vh" class="stack-verify-dialog" :close-on-click-modal="false">
    <div v-loading="verifying" class="stack-verify-body">
      <!-- 摘要横幅 -->
      <div class="sv-banner" :class="verifyResult?.ok ? 'is-ok' : 'is-fail'">
        <div class="sv-banner-icon">
          <svg v-if="verifyResult?.ok" viewBox="0 0 24 24" width="28" height="28" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"/><polyline points="22 4 12 14.01 9 11.01"/></svg>
          <svg v-else viewBox="0 0 24 24" width="28" height="28" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>
        </div>
        <div class="sv-banner-text">
          <div class="sv-banner-summary">{{verifyResult?.summary || '正在探活…'}}</div>
          <div class="sv-banner-meta">
            <span v-if="verifyResult?.checked_at" class="mono">{{verifyResult.checked_at}}</span>
            <span v-if="verifyResult?.hint" class="sv-banner-hint">{{verifyResult.hint}}</span>
          </div>
        </div>
      </div>

      <!-- 组件标签：bigdata 多组件探活 -->
      <div v-if="isBigdataVerify" class="sv-tabs">
        <div class="sv-tab" :class="{ active: verifyActiveTab === 'all' }" @click="verifyActiveTab = 'all'">查看所有</div>
        <div class="sv-tab" v-for="c in verifyComponents" :key="c.key" :class="{ active: verifyActiveTab === c.key }" @click="verifyActiveTab = c.key">{{c.label}}</div>
      </div>

      <!-- 组件视角：选中具体组件（仅 bigdata） -->
      <template v-if="isBigdataVerify && verifyActiveTab !== 'all'">
        <div class="sv-section">
          <div class="sv-section-title">接入地址 · {{currentVerifyCompLabel}}</div>
          <div class="sv-ep-grid">
            <div class="sv-ep-host-card" v-for="hg in verifyCompEndpointsByHost" :key="hg.host_ip">
              <div class="sv-ep-host-header">
                <span class="mono sv-ep-host-ip">{{hg.host_ip}}</span>
              </div>
              <div class="sv-ep-host-eps">
                <div class="sv-ep-mini" v-for="ep in hg.endpoints" :key="ep.url">
                  <span class="sv-ep-badge" :class="epProtocolClass(ep.url)">{{epProtocolLabel(ep.url)}}</span>
                  <span class="mono sv-ep-mini-url">{{ep.url}}</span>
                  <span class="sv-ep-copy" @click="copyText(ep.url)" title="复制">
                    <svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
                  </span>
                </div>
              </div>
            </div>
            <div v-if="!verifyCompEndpointsByHost.length" class="sv-empty">该组件暂无接入地址</div>
          </div>
        </div>

        <div class="sv-section">
          <div class="sv-section-title">{{currentVerifyCompLabel}} 节点状态 <span class="sv-section-count">{{verifyCompHostRows.length}} 台</span></div>
          <el-table :data="verifyCompHostRows" size="small" empty-text="该组件暂无节点结果" class="sv-node-table" :show-overflow-tooltip="false">
            <el-table-column prop="ip" label="IP" min-width="130" :show-overflow-tooltip="false">
              <template #default="{row}"><span class="mono">{{row.ip}}</span></template>
            </el-table-column>
            <el-table-column prop="host_name" label="主机名" min-width="120" :show-overflow-tooltip="false" />
            <el-table-column prop="ok" label="状态" width="70" align="center" :show-overflow-tooltip="false">
              <template #default="{row}">
                <span class="sv-table-dot" :class="row.ok ? 'ok' : 'fail'"></span>
              </template>
            </el-table-column>
            <el-table-column prop="role" label="角色" width="90" :show-overflow-tooltip="false">
              <template #default="{row}">
                <span class="sv-role-badge" :class="row.role === 'master' ? 'is-master' : 'is-replica'">{{row.role}}</span>
              </template>
            </el-table-column>
            <el-table-column prop="instances" label="容器实例" min-width="200" :show-overflow-tooltip="false">
              <template #default="{row}">
                <el-tooltip v-if="row.instList && row.instList.length" placement="top" :show-after="120" raw-content :content="tipHtml(row.instList)">
                  <span class="sv-cell-text mono">{{row.instances}}</span>
                </el-tooltip>
                <span v-else class="sv-cell-text mono" :class="{'sv-cell-muted': !row.instances || row.instances === '无'}">{{row.instances}}</span>
              </template>
            </el-table-column>
            <el-table-column prop="version" label="版本" width="90" :show-overflow-tooltip="false" />
            <el-table-column prop="uptime" label="运行时间" min-width="140" :show-overflow-tooltip="false">
              <template #default="{row}">
                <el-tooltip v-if="row.uptime && row.uptime !== '-'" placement="top" :show-after="120" raw-content :content="tipHtml(row.uptimeList)">
                  <span class="sv-cell-text">{{row.uptime}}</span>
                </el-tooltip>
                <span v-else class="sv-cell-text sv-cell-muted">—</span>
              </template>
            </el-table-column>
          </el-table>
        </div>
      </template>

      <!-- 整体视角：常规集群 或 bigdata「查看所有」 -->
      <template v-else>
        <!-- 接入地址 -->
        <div class="sv-section">
          <div class="sv-section-title">接入地址</div>
          <div class="sv-ep-grid">
            <div class="sv-ep-host-card" v-for="hg in verifyEndpointsByHost" :key="hg.host_ip">
              <div class="sv-ep-host-header">
                <template v-if="esVerifyHostRole(hg)">
                  <span class="sv-ep-host-role is-es">{{esVerifyHostRole(hg)}}</span>
                </template>
                <template v-else>
                  <span class="sv-ep-host-role" :class="hg.role === 'master' ? 'is-master' : 'is-replica'">{{hg.role === 'master' ? '主' : '从'}}</span>
                </template>
                <span class="mono sv-ep-host-ip">{{hg.host_ip}}</span>
              </div>
              <div class="sv-ep-host-eps">
                <div class="sv-ep-mini" v-for="ep in hg.endpoints" :key="ep.url">
                  <span class="sv-ep-badge" :class="epProtocolClass(ep.url)">{{epProtocolLabel(ep.url)}}</span>
                  <span class="mono sv-ep-mini-url">{{ep.url}}</span>
                  <span class="sv-ep-copy" @click="copyText(ep.url)" title="复制">
                    <svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
                  </span>
                </div>
              </div>
            </div>
            <div v-if="!verifyEndpointsByHost.length" class="sv-empty">暂无接入地址</div>
          </div>
        </div>

        <!-- 节点状态 -->
        <div class="sv-section">
          <div class="sv-section-title">节点状态 <span class="sv-section-count">{{verifyHostRows.length}} 台</span></div>
          <el-table :data="verifyHostRows" size="small" empty-text="暂无节点结果" class="sv-node-table" :show-overflow-tooltip="false">
            <el-table-column prop="ip" label="IP" min-width="130" :show-overflow-tooltip="false">
              <template #default="{row}"><span class="mono">{{row.ip}}</span></template>
            </el-table-column>
            <el-table-column prop="host_name" label="主机名" min-width="120" :show-overflow-tooltip="false" />
            <el-table-column prop="status" label="状态" width="70" align="center" :show-overflow-tooltip="false">
              <template #default="{row}">
                <span class="sv-table-dot" :class="row.ok ? 'ok' : 'fail'"></span>
              </template>
            </el-table-column>
            <el-table-column prop="role" label="角色" width="90" :show-overflow-tooltip="false">
              <template #default="{row}">
                <span class="sv-role-badge" :class="row.role === 'master' ? 'is-master' : 'is-replica'">{{row.role}}</span>
              </template>
            </el-table-column>
            <el-table-column prop="replication" :label="verifyTarget?.stack_key === 'redis' ? '复制' : '实例'" min-width="200" :show-overflow-tooltip="false">
              <template #default="{row}">
                <el-tooltip v-if="row.instList && row.instList.length" placement="top" :show-after="120" raw-content :content="tipHtml(row.instList)">
                  <span class="sv-cell-text mono"><span class="sv-cell-count">{{row.instList.length}}</span>{{row.replication}}</span>
                </el-tooltip>
                <span v-else class="sv-cell-text mono" :class="{'sv-cell-muted': !row.replication || row.replication === '—'}">{{row.replication || '—'}}</span>
              </template>
            </el-table-column>
            <el-table-column prop="version" label="版本" width="90" :show-overflow-tooltip="false" />
            <el-table-column prop="uptime" label="运行时间" min-width="140" :show-overflow-tooltip="false">
              <template #default="{row}">
                <el-tooltip v-if="row.uptime && row.uptime !== '-'" placement="top" :show-after="120" raw-content :content="tipHtml(row.uptimeList)">
                  <span class="sv-cell-text">{{row.uptime}}</span>
                </el-tooltip>
                <span v-else class="sv-cell-text sv-cell-muted">—</span>
              </template>
            </el-table-column>
          </el-table>
        </div>
      </template>

      <!-- 访问密码 -->
      <div class="sv-section" v-if="verifyPassword">
        <div class="sv-section-title">访问密码</div>
        <div class="sv-pass-row">
          <span class="sv-pass-dots">●●●●●●●●●●●●</span>
          <span class="sv-pass-label">已配置</span>
          <div class="sv-pass-copy" @click="copyText(verifyPassword)" title="复制密码">
            <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
          </div>
        </div>
      </div>

      <!-- 提示信息 -->
      <div class="sv-notes" v-if="verifyResult?.notes?.length">
        <div class="sv-note-item" v-for="(n,i) in verifyResult.notes" :key="i">
          <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="flex-shrink:0;margin-top:2px"><circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/></svg>
          <span>{{n}}</span>
        </div>
      </div>
      <!-- 客户端接入提示 -->
      <div class="sv-client-hint" v-if="verifyResult?.client_hint">
        <div class="sv-client-hint-label">客户端接入</div>
        <pre class="sv-client-hint-code">{{verifyResult.client_hint}}</pre>
      </div>
    </div>
    <template #footer>
      <el-button @click="verifyVisible=false">关闭</el-button>
      <el-button type="primary" :loading="verifying" :disabled="!verifyTarget || verifyTarget.status==='uninstalled'" @click="runVerify">重新探活</el-button>
    </template>
  </el-dialog>

  <el-drawer v-model="drawerVisible" :title="'套件运行 #' + (recordMeta?.id || '')" size="48%" class="deploy-drawer"
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
      bdSel: ['hdfs'], replicas: '0', masterHostId: 0, masters: {},
      hosts: [], hostsLoading: false, hostsLoaded: false, selectedHostIds: new Set(),
      hostFilter: '', hostSort: { field: 'name', order: 'asc' },
      sharedParams: {}, hostParams: {},
      deploying: false, wizardVisible: false, wizardOp: 'create', clusterName: '',
      targetInstance: null, memberHostIds: new Set(),
      scaleInProtected: {}, instPlan: null, opPlan: null, opPlanErr: '', opPlanLoading: false,
      instances: [], instancesLoading: false,
      instDrawerVisible: false, instDetail: null, instRuns: [],
      verifyVisible: false, verifyTarget: null, verifyResult: null, verifying: false, verifyActiveTab: 'all',
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
    selfCtx() { return this },
    formEntry() { return (window.StackForms && window.StackForms[this.selectedKey]) || null },
    step1FormComp() { return this.formEntry?.selectComp || null },
    opFormComp() { return this.formEntry?.opComp || null },
    step2FormComp() { return this.formEntry?.step2Comp || null },
    step1Hints() { return this.formEntry?.hints || [] },
    wizardTitle() {
      const name = this.targetInstance?.name || ''
      return {
        create: '新建集群', scale_out: '扩容 · ' + name, scale_in: '缩容 · ' + name,
        add_component: '加装组件 · ' + name, remove_component: '卸载组件 · ' + name
      }[this.wizardOp] || '套件部署'
    },
    wizardFirstStepTitle() {
      return { create: '选择套件', add_component: '选择加装组件', remove_component: '选择卸载组件' }[this.wizardOp] || '选择套件'
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
    needMaster() { return this.wizardOp === 'create' && !!this.selectedMode?.assign_master && !this.useRolePlan },
    // 部署前角色预览门禁（全部套件，docs/角色物化设计.md §二·全套件覆盖）：
    // bigdata 走 useRolePlan 分支自带预览；其余套件在主机勾选后自动生成，未出预览不能进下一步
    needOpPlan() {
      return !this.useRolePlan && (this.wizardOp === 'create' || this.wizardOp === 'scale_out')
    },
    // 大数据等：表格勾选 + 按组件指定主角色（不再用单一「主节点」单选）
    useRolePlan() {
      return !!(this.formEntry?.masterComps?.length) &&
        (this.wizardOp === 'create' || this.wizardOp === 'add_component')
    },
    filteredHosts() {
      let list = this.hosts
      if (this.wizardOp === 'scale_out') list = list.filter(h => !this.memberHostIds.has(h.id))
      if (this.wizardOp === 'scale_in' || this.wizardOp === 'add_component') list = list.filter(h => this.memberHostIds.has(h.id))
      if (this.hostFilter) {
        const kw = this.hostFilter.toLowerCase()
        list = list.filter(h => (h.name||'').toLowerCase().includes(kw) || (h.ip||'').toLowerCase().includes(kw) || (h.tag||'').toLowerCase().includes(kw))
      }
      return this.sortHostList(list, this.hostSort)
    },
    hostVars() {
      const list = this.selectedStack?.host_vars || []
      const byMode = list.filter(v => !v.modes || !v.modes.length || v.modes.includes(this.mode))
      const fn = this.formEntry?.filterHostVars
      return fn ? fn(this, byMode) : byMode
    },
    visibleSharedVars() {
      const list = (this.selectedStack?.shared_vars || [])
        .filter(v => !v.modes || !v.modes.length || v.modes.includes(this.mode))
      const fn = this.formEntry?.visibleSharedVars
      return fn ? fn(this, list) : list
    },
    selectedHosts() { return this.hosts.filter(h => this.selectedHostIds.has(h.id)) },
    minHosts() {
      if (this.wizardOp === 'scale_out' || this.wizardOp === 'scale_in' || this.wizardOp === 'add_component') return 1
      const v = this.formEntry?.minHosts?.(this)
      if (typeof v === 'number') return v
      return this.selectedMode?.min_hosts || 2
    },
    showBootstrapPill() { return this.recordHosts.some(h => h.bootstrap_status && h.bootstrap_status !== 'skipped') },
    showPrereqPill() { return this.isContainerStack(this.recordMeta) },
    hostHint() {
      if (this.wizardOp === 'scale_out') return '选择要加入的新主机'
      if (this.wizardOp === 'scale_in') return '选择要移除的成员（不能全删，请用卸载）'
      if (this.useRolePlan) {
        return this.wizardOp === 'add_component'
          ? '勾选安装目标成员，再在下方为各组件指定主角色'
          : '至少 ' + this.minHosts + ' 台；勾选后在下方按组件指定 NameNode / Master 等主角色'
      }
      return this.selectedMode?.host_hint || ''
    },
    canStep2() {
      if (this.wizardOp === 'add_component' || this.wizardOp === 'remove_component') return this.bdSel.length > 0
      return !!(this.selectedKey && this.mode)
    },
    canStep3() {
      if (this.selectedHosts.length < this.minHosts) return false
      if (this.wizardOp === 'scale_in' && this.selectedHosts.length >= this.memberHostIds.size) return false
      if (this.formEntry?.canStep3 && this.formEntry.canStep3(this) === false) return false
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
      if (this.step === 2 && !this.canStep3) {
        if (this.useRolePlan && this.wizardOp === 'create' && this.bdSel.includes('hdfs') &&
          (!this.masters.hdfs || !this.selectedHostIds.has(this.masters.hdfs))) {
          return '请勾选主机并指定 HDFS NameNode'
        }
        // ES 冷热温：拦截几乎都因主机行未勾 master / 数据层角色，给出可操作的指引
        if (this.selectedKey === 'elasticsearch' && this.mode === 'cold_warm_hot') {
          const hasMaster = this.selectedHosts.some(h => {
            const hp = this.hostParams[h.id] || {}
            return String(hp.roles || '').split(',').map(s => s.trim()).includes('master')
          })
          if (!hasMaster) return '请在上方主机行勾选至少 1 台「master 候选」（数量已满足，最少 ' + this.minHosts + ' 台）'
          return '请在上方主机行勾选数据层角色（hot/warm/cold 至少 1 台）'
        }
        return '主机数量或角色规划不满足当前模式'
      }
      if (this.step === 3 && !this.canRun) {
        const miss = this.visibleSharedVars.find(v => v.required && !(this.sharedParams[v.name] || v.default || '').trim())
        return miss ? '请填写' + miss.label : '参数未填写完整'
      }
      return ''
    },
    runningCount() { return this.instances.filter(t => t.status === 'deploying').length },
    logDrawerLocked() {
      return !!(this.drawerVisible && this.recordMeta && this.recordMeta.op === 'add_component' && this.recordMeta.status === 'running')
    },
    verifyParams() {
      try { return JSON.parse(this.verifyTarget?.params_json || '{}') || {} } catch (e) { return {} }
    },
    verifyPassword() { return (this.verifyParams.password || '').trim() },
    verifyEndpoints() {
      const it = this.verifyTarget
      const extractIP = (url) => { const m = url && url.match(/(\d+\.\d+\.\d+\.\d+)/); return m ? m[1] : url }
      const inferRole = (name) => { if (/主\b|master/i.test(name)) return 'master'; if (/从\b|replica|slave/i.test(name)) return 'replica'; return '' }
      if (!it) return (this.verifyResult?.endpoints || []).map(ep => ({ name: ep.name, component: ep.component, url: ep.url, role: ep.role || inferRole(ep.name), host_ip: extractIP(ep.url) }))
      if (this.verifyResult?.endpoints?.length) {
        return this.verifyResult.endpoints.map(ep => ({ name: ep.name, component: ep.component, url: ep.url, role: ep.role || inferRole(ep.name), host_ip: extractIP(ep.url) }))
      }
      const hosts = (it.hosts || []).filter(h => h.status !== 'removed')
      const p = this.verifyParams
      const port = p.port || '6379'
      const out = []
      hosts.forEach(h => {
        let hp = {}
        try { hp = JSON.parse(h.params_json || '{}') || {} } catch (e) { hp = {} }
        const pt = hp.port || port
        if (it.stack_key === 'redis') {
          const isMaster = h.role === 'master'
          const name = (it.mode === 'replication' || it.mode === 'sentinel')
            ? (isMaster ? 'Redis 主' : 'Redis 从')
            : 'Redis'
          out.push({ name, url: 'redis://' + h.host_ip + ':' + pt, role: h.role, host_ip: h.host_ip, host_name: h.host_name })
          if (it.mode === 'sentinel') {
            out.push({ name: 'Sentinel', url: 'redis-sentinel://' + h.host_ip + ':' + (hp.sentinel_port || p.sentinel_port || '26379'), role: h.role, host_ip: h.host_ip, host_name: h.host_name })
          }
        } else if (it.stack_key === 'bigdata') {
          // 优先用探活接口返回的 endpoints（按组件拆分）
          const fromApi = (this.verifyResult?.endpoints || []).filter(ep => (ep.role === h.role || true) && (ep.url || '').includes(h.host_ip))
          if (fromApi.length) {
            fromApi.forEach(ep => out.push({ name: ep.name, component: ep.component, url: ep.url, role: h.role, host_ip: h.host_ip, host_name: h.host_name }))
          } else {
            out.push({ name: (this.roleLabel(h.role) || '节点'), component: '', url: 'http://' + h.host_ip, role: h.role, host_ip: h.host_ip, host_name: h.host_name })
          }
        } else {
          out.push({ name: (this.roleLabel(h.role) || '节点'), url: 'tcp://' + h.host_ip, role: h.role, host_ip: h.host_ip, host_name: h.host_name })
        }
      })
      return out
    },
    verifyEndpointsByHost() {
      const eps = this.verifyEndpoints
      const map = new Map()
      eps.forEach(ep => {
        const key = ep.host_ip || ep.url
        if (!map.has(key)) {
          map.set(key, { host_ip: key, host_name: ep.host_name || key, role: ep.role || '', endpoints: [] })
        }
        map.get(key).endpoints.push(ep)
      })
      return Array.from(map.values())
    },
    // 探活接入地址主机头：冷热温按实际分层角色展示，其余沿用主/从
    esVerifyHostRole(hg) {
      const it = this.verifyTarget
      if (!this.isEsCwh(it)) return ''
      const parts = []
      const seen = new Set()
      ;(hg.endpoints || []).forEach(ep => {
        const r = (ep.role || '').trim()
        if (r && !seen.has(r)) { seen.add(r); parts.push(this.esRoleLabel(r)) }
      })
      return parts.join(' / ')
    },
    verifyHostRows() {
      return (this.verifyResult?.hosts || []).map(h => {
        const check = (name) => (h.checks || []).find(c => c.name === name)
        const roleRaw = (h.live_role || check('角色')?.detail || h.role || '').toString()
        const isMaster = /^master\b/i.test(roleRaw) || roleRaw === 'master'
        const isReplica = /^(slave|replica|worker)\b/i.test(roleRaw) || roleRaw === 'replica' || roleRaw === 'slave' || roleRaw === 'worker'
        let role = roleRaw.split('·')[0].trim() || this.roleLabel(h.role) || '-'
        if (isMaster) role = 'master'
        else if (isReplica) role = roleRaw === 'worker' || h.role === 'worker' ? 'worker' : 'replica'
        const replCheck = check('复制')
        const instCheck = check('实例')
        const roleDetail = check('角色')?.detail || ''
        let replication = ''
        if (instCheck?.detail) {
          replication = instCheck.detail
        } else if (isReplica || role === 'replica') {
          const m = roleDetail.match(/跟随\s+(\S+)/)
          if (m) replication = m[1]
          else if (replCheck?.detail) {
            const link = replCheck.detail.match(/master_link_status=(\S+)/)
            replication = link ? link[1] : replCheck.detail
          }
        } else if (replCheck?.detail) {
          replication = replCheck.detail
        }
        const verDetail = check('版本')?.detail || ''
        let version = verDetail, uptime = ''
        if (verDetail.includes('·')) {
          const parts = verDetail.split('·').map(s => s.trim())
          version = parts[0] || ''
          uptime = parts.slice(1).join(' · ')
        }
        return {
          host_id: h.host_id,
          ip: h.host_ip,
          host_name: h.host_name,
          ok: !!h.ok,
          role,
          replication,
          instList: replication.includes(',') ? splitList(replication) : [],
          version: version || '-',
          uptime: uptime || '-',
          uptimeList: uptime ? splitList(uptime) : []
        }
      })
    },
    // —— 多组件（bigdata）探活：组件标签式 ——
    isBigdataVerify() { return this.verifyTarget?.stack_key === 'bigdata' },
    COMP_LABELS() { return { hdfs: 'HDFS', zookeeper: 'ZooKeeper', yarn: 'YARN', spark: 'Spark', flink: 'Flink', hive: 'Hive', hbase: 'HBase', trino: 'Trino' } },
    verifyComponents() {
      if (!this.isBigdataVerify) return []
      const order = (this.verifyParams.components || '').split(',').map(s => s.trim()).filter(Boolean)
      const seen = new Set(), keys = []
      ;[...order, ...(this.verifyResult?.endpoints || []).map(e => e.component)]
        .forEach(k => { if (k && !seen.has(k)) { seen.add(k); keys.push(k) } })
      const L = this.COMP_LABELS
      return keys.map(k => ({ key: k, label: L[k] || k.toUpperCase() }))
    },
    currentVerifyCompLabel() {
      const c = this.verifyComponents.find(c => c.key === this.verifyActiveTab)
      return c ? c.label : ''
    },
    verifyCompEndpoints() {
      const comp = this.verifyActiveTab
      if (!comp || comp === 'all') return []
      return this.verifyEndpoints.filter(ep => ep.component === comp)
    },
    verifyCompEndpointsByHost() {
      const eps = this.verifyCompEndpoints
      const map = new Map()
      eps.forEach(ep => {
        const key = ep.host_ip || ep.url
        if (!map.has(key)) {
          map.set(key, { host_ip: key, host_name: ep.host_name || key, endpoints: [] })
        }
        map.get(key).endpoints.push(ep)
      })
      return Array.from(map.values())
    },
    verifyCompHostRows() {
      const comp = this.verifyActiveTab
      if (!comp || comp === 'all') return []
      const fullMap = Object.fromEntries(this.verifyHostRows.map(r => [r.ip, r]))
      const out = []
      for (const h of (this.verifyResult?.hosts || [])) {
        const compChecks = (h.checks || []).filter(c => c.component === comp)
        if (!compChecks.length) continue
        const f = fullMap[h.host_ip] || {}
        const instances = compChecks.filter(c => c.ok).map(c => c.name.replace(/^容器\s*/, ''))
        const instStr = instances.length ? instances.join(', ') : '无'
        const uptimeStr = f.uptime || '-'
        out.push({
          ip: h.host_ip, host_name: h.host_name,
          ok: compChecks.every(c => c.ok),
          role: f.role || '-',
          instances: instStr,
          instList: instances.length > 1 ? instances : [],
          version: f.version || '-',
          uptime: uptimeStr,
          uptimeList: uptimeStr !== '-' ? splitList(uptimeStr) : []
        })
      }
      return out
    },
    preflightReady() {
      if (this.preflight.unreachable && this.preflight.unreachable.length) return false
      // 部署前角色预览（全部套件）：预览未生成或失败时不允许确认部署
      if (this.needOpPlan && !this.opPlan) return false
      return true
    },
    filteredLogs() {
      if (!this.logFilter) return this.recordLogs
      const kw = this.logFilter.toLowerCase()
      return this.recordLogs.filter(l => (l.ip||'').toLowerCase().includes(kw) || (l.text||'').toLowerCase().includes(kw) || (l.phase||'').toLowerCase().includes(kw))
    }
  },
  mounted() { this.loadInstances(); this.loadStacks(); this.connectSetup() },
  watch: {
    // 主节点变更 / 角色规划联动：清理失效指向
    masterHostId() { this.syncMasters() }
  },
  beforeUnmount() { this.closeDrawer(); this.closeSetup() },
  methods: {
    // 探活表格 tooltip：把长列表渲染成逐行文本（已转义），配合列内省略号使用
    escHtml(s) { return String(s == null ? '' : s).replace(/[&<>"]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c])) },
    tipHtml(list) {
      const arr = (list || []).map(x => String(x || '').trim()).filter(Boolean)
      if (!arr.length) return ''
      return '<div class="sv-tip-list">' + arr.map(x => this.escHtml(x)).join('<br>') + '</div>'
    },
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
    instDisplayComps(it) {
      return this.instCompTags(it).filter(c => c && c !== it.mode)
    },
    // Elasticsearch 冷热温/集群角色与 tier 的中文标签
    esRoleLabel(r) { return { master: 'master 候选', coordinator: '纯协调', data_hot: '数据-hot', data_warm: '数据-warm', data_cold: '数据-cold' }[r] || r },
    isEsCwh(it) { return !!it && it.stack_key === 'elasticsearch' && it.mode === 'cold_warm_hot' },
    hostRolesTags(h) {
      let hp = {}
      try { hp = JSON.parse((h && h.params_json) || '{}') || {} } catch (e) { hp = {} }
      const roles = String(hp.roles || '').split(',').map(s => s.trim()).filter(Boolean)
      return roles.length ? roles.map(r => this.esRoleLabel(r)) : []
    },
    // 实例卡片/抽屉：冷热温实例展示分层分布（数据-hot/warm/cold 聚合；含 master/协调）
    instTierTags(it) {
      if (!this.isEsCwh(it)) return []
      const seen = new Set()
      ;(it.hosts || []).forEach(h => this.hostRolesTags(h).forEach(t => seen.add(t)))
      const order = ['数据-hot', '数据-warm', '数据-cold', 'master 候选', '纯协调']
      return order.filter(t => seen.has(t))
    },
    // 抽屉成员：冷热温模式下按角色渲染分层标签，其余套件沿用 HA 角色矩阵
    esMemberRoles(h) {
      if (!this.isEsCwh(this.instDetail)) return []
      return this.hostRolesTags(h)
    },
    // 冷热温 + SSL 开启时可下载 CA 证书
    caDownloadAvailable() {
      const it = this.instDetail || this.verifyTarget
      if (!this.isEsCwh(it)) return false
      try { return JSON.parse(it.params_json || '{}').ssl_enabled === 'true' } catch (e) { return false }
    },
    async downloadCa() {
      const it = this.instDetail || this.verifyTarget
      if (!it) return
      try {
        const text = await window.api.get('/stacks/instances/' + it.id + '/ca', { responseType: 'text', timeout: 30000 })
        const str = String(text || '')
        if (!str.includes('BEGIN CERTIFICATE')) { ElMessage.error('未获取到 CA 证书'); return }
        const blob = new Blob([str], { type: 'application/x-pem-file' })
        const url = URL.createObjectURL(blob)
        const a = document.createElement('a'); a.href = url; a.download = (it.name || 'es') + '-ca.crt'; a.click()
        URL.revokeObjectURL(url)
        ElMessage.success('已下载 CA 证书')
      } catch (e) { ElMessage.error(e.message || '下载 CA 失败') }
    },
    // 抽屉成员标签统一入口：冷热温取角色分层，bigdata HA 取角色矩阵，其余空
    memTags(h) {
      if (this.isEsCwh(this.instDetail)) return this.esMemberRoles(h)
      return this.haDrawnRoles(h.host_ip)
    },
    // 集群实例是否以 HA 模式创建（bigdata）
    instIsHa(it) {
      if (!it || it.stack_key !== 'bigdata') return false
      try { return JSON.parse(it.params_json || '{}').ha === 'true' } catch (e) { return false }
    },
    // 实例抽屉：按主机展示 HA 全角色标签（镜像后端确定性分配，仅用于展示）
    haDrawnRoles(ip) {
      const it = this.instDetail
      if (!it || it.stack_key !== 'bigdata' || !this.instIsHa(it)) return []
      let params, masters
      try { params = JSON.parse(it.params_json || '{}'); masters = JSON.parse(params.masters || '{}') || {} } catch (e) { return [] }
      const comps = (params.components || '').split(',').map(x => x.trim()).filter(Boolean)
      const hosts = (it.hosts || []).filter(x => x.status !== 'removed').slice().sort((a, b) => (a.seq || 0) - (b.seq || 0))
      const ips = hosts.map(h => h.host_ip)
      const primary = (hosts.find(h => h.role === 'master') || {}).host_ip || ips[0] || ''
      const primaryOf = comp => masters[comp] || primary
      const secondaryOf = (comp, key) => masters[key] || (ips.find(i => i !== primaryOf(comp)) || '')
      const inComps = c => comps.includes(c)
      const out = []
      if (inComps('hdfs')) {
        if (ip === primaryOf('hdfs')) out.push('NN1')
        else if (ip === secondaryOf('hdfs', 'hdfs_nn2')) out.push('NN2·Standby')
        else if ((masters.hdfs_jns ? String(masters.hdfs_jns).split(',') : ips.slice(0, 3)).includes(ip)) out.push('JN')
      }
      if (inComps('yarn') && ip === primaryOf('yarn')) out.push('RM1')
      if (inComps('yarn') && ip === secondaryOf('yarn', 'yarn_rm2')) out.push('RM2·Standby')
      if (inComps('spark') && ip === primaryOf('spark')) out.push('Master-1')
      if (inComps('spark') && ip === secondaryOf('spark', 'spark_m2')) out.push('Master-2')
      if (inComps('flink') && ip === primaryOf('flink')) out.push('JM1')
      if (inComps('flink') && ip === secondaryOf('flink', 'flink_jm2')) out.push('JM2·Standby')
      if (inComps('hbase') && ip === primaryOf('hbase')) out.push('HMaster-1')
      if (inComps('hbase') && ip === secondaryOf('hbase', 'hbase_hm2')) out.push('HMaster-2')
      if (inComps('hive')) {
        const ms = [primaryOf('hive')]; if (String(masters.hive_ms2 || '')) ms.push(masters.hive_ms2)
        const hs = [primaryOf('hive')]; if (String(masters.hive_hs2b || '')) hs.push(masters.hive_hs2b)
        if (ms.includes(ip)) out.push(ms[1] === ip ? 'MS2·Standby' : 'Metastore')
        if (hs.includes(ip)) out.push(hs[1] === ip ? 'HS2·Standby' : 'HiveServer2')
        if (ip === (masters.hive_db || primaryOf('hive'))) out.push('MetaDB')
      }
      if (inComps('zookeeper')) {
        const zk = masters.zookeeper_ips ? String(masters.zookeeper_ips).split(',') : ips.slice(0, 3)
        const idx = zk.indexOf(ip)
        if (idx >= 0) out.push('ZK-' + (idx + 1))
      }
      return out
    },
    installedCompDefs(it) {
      if (!it) return []
      const defs = this.compsOf(it.stack_key)
      return this.instDisplayComps(it).map(k => defs.find(d => d.key === k) || { key: k, label: k, desc: '' })
    },
    async removeOneComp(it, def) {
      try {
        await ElMessageBox.confirm('将在全部成员上停止并卸载 ' + def.label + '，集群记录保留。', '卸载组件 · ' + def.label, {
          type: 'warning', confirmButtonText: '确认卸载', cancelButtonText: '取消'
        })
      } catch (e) { return }
      try {
        const hostIds = (it.hosts || []).filter(h => h.status !== 'removed').map(h => h.host_id)
        const r = await api.post('/stacks/instances/' + it.id + '/remove-component', {
          stack_key: it.stack_key, mode: it.mode, name: it.name,
          host_ids: hostIds, master_host_id: 0,
          params: { components: def.key }, host_params: {}
        })
        if (r.code !== 0) { ElMessage.error(r.message || '卸载组件失败'); return }
        ElMessage.success('已开始卸载组件')
        await this.afterStackOp({ ...r.data, op: 'remove_component' })
      } catch (e) { ElMessage.error(e.message || '卸载组件失败') }
    },
    compsOf(key) { return (window.StackForms && window.StackForms[key]?.comps) || [] },
    unusedCompsOf(it) {
      const comps = this.compsOf(it?.stack_key)
      if (!comps.length) return []
      const have = new Set(this.instCompTags(it))
      return comps.filter(c => !c.required && !have.has(c.key))
    },
    removableCompsOf(it) {
      const comps = this.compsOf(it?.stack_key)
      if (!comps.length) return []
      const locked = comps.filter(c => c.required).map(c => c.key)
      return this.instCompTags(it).filter(k => !locked.includes(k)).map(k => {
        const def = comps.find(c => c.key === k)
        return def || { key: k, label: k, desc: '已安装，可单独卸载' }
      })
    },
    busyInst(it) { return !it || it.status === 'deploying' },
    // 安装未成功（失败 / 部分成功），可重新安装或卸载
    instFailed(it) { return !!it && (it.status === 'failed' || it.status === 'partial') },
    instReady(it) { return !!it && it.status === 'ready' },
    instUninstalled(it) { return !!it && it.status === 'uninstalled' },
    canReinstall(it) { return !!(it && !this.busyInst(it) && it.status !== 'ready') },
    isPlatformStack(it) {
      if (!it) return false
      if (it.category === 'platform') return true
      return this.compsOf(it.stack_key).length > 0
    },
    activeInstHosts(it) { return (it?.hosts || []).filter(h => h.status !== 'removed') },
    isContainerStack(it) {
      if (!it) return true
      if (typeof it.requires_docker === 'boolean') return it.requires_docker
      const s = this.stacks.find(x => x.key === (it.stack_key || it.key))
      return !s || !!s.requires_docker
    },
    runtimeLabel(it) { return this.isContainerStack(it) ? '容器' : '主机安装' },
    stackAvatar(it) {
      const map = {
        redis: { t: 'R', c: 'sv-redis' },
        kafka: { t: 'K', c: 'sv-kafka' },
        elasticsearch: { t: 'ES', c: 'sv-es' },
        rabbitmq: { t: 'MQ', c: 'sv-rabbitmq' },
        rocketmq: { t: 'RM', c: 'sv-rocketmq' },
        nacos: { t: 'NA', c: 'sv-nacos' },
        powerjob: { t: 'PJ', c: 'sv-powerjob' },
        bigdata: { t: 'BD', c: 'sv-bigdata' }
      }
      const m = map[it && (it.stack_key || it.key)]
      if (m) return m
      const n = ((it && (it.stack_name || it.name)) || '?').trim()
      const ascii = n.match(/[A-Za-z]/)
      return { t: ascii ? ascii[0].toUpperCase() : n.slice(0, 1), c: this.isContainerStack(it) ? 'is-container' : 'is-host' }
    },
    compLabel(stackKey, k) {
      const def = this.compsOf(stackKey).find(c => c.key === k)
      if (def) return def.label
      // 运行期组件（不在组件目录里，如 HA 的 metastore_db）给出中文名而不是裸键名
      return { metastore_db: 'Hive MetaDB' }[k] || k
    },
    instStatusType(s) { return { ready: 'success', deploying: 'warning', partial: 'danger', failed: 'danger', uninstalled: 'info' }[s] || 'info' },
    instStatusLabel(s) { return { ready: '就绪', deploying: '变更中', partial: '部分成功', failed: '失败', uninstalled: '已卸载' }[s] || s },
    applyInstanceContext(it) {
      this.targetInstance = it
      this.selectedKey = it.stack_key
      this.mode = it.mode
      this.memberHostIds = new Set((it.hosts || []).filter(h => h.status !== 'removed').map(h => h.host_id))
      try { this.sharedParams = JSON.parse(it.params_json || '{}') || {} } catch (e) { this.sharedParams = {} }
      const fe = window.StackForms && window.StackForms[it.stack_key]
      if (fe?.initSelection) fe.initSelection(this, it)
    },
    async prepareInstance(it, op) {
      this.resetWizard()
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
      // 落点角色保护（全部套件，docs/角色物化设计.md §3.3）：承载落点角色的成员置灰，仅数据/工作节点可缩容
      this.scaleInProtected = {}
      try {
        const r = await api.get('/stacks/instances/' + it.id + '/plan')
        if (r.code === 0 && r.data) {
          const m = {}
          ;(r.data.hosts || []).forEach(h => {
            const singles = (h.roles || []).filter(x => (x.scope || '') === '')
            if (singles.length) m[h.host_ip] = singles.map(x => x.label).join('+')
          })
          this.scaleInProtected = m
        }
      } catch (e) { /* 读取失败时由后端兜底拦截 */ }
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
      this.loadInstPlan(it.id)
      try {
        const r = await api.get('/stacks/instances/' + it.id + '/runs')
        if (r.code === 0) this.instRuns = r.data || []
      } catch (e) { /* */ }
    },
    async loadInstPlan(id) {
      this.instPlan = null
      try {
        const r = await api.get('/stacks/instances/' + id + '/plan')
        if (r.code === 0) this.instPlan = r.data
      } catch (e) { /* */ }
    },
    // 角色类型 → 文字颜色（底框颜色区分手动/自动，文字颜色区分角色，docs/角色物化设计.md §五）
    roleTextClass(r) {
      const role = (r || {}).role || ''
      if (['nn1','rm1','spark_m1','jm1','hm1','ms1','hs2a','db','coordinator','master','boot_master','boot','seed'].includes(role)) return 'rt-primary'
      if (['nn2','rm2','spark_m2','jm2','hm2','ms2','hs2b','replica','slave','data'].includes(role)) return 'rt-standby'
      if (['zk','jn','zkfc','broker'].includes(role)) return 'rt-quorum'
      if (['ui','sentinel'].includes(role)) return 'rt-aux'
      return 'rt-worker'
    },
    // 部署前角色预览（全部套件，docs/角色物化设计.md §二）：create 取所选主机；scale_out 取既有成员 + 新选主机
    async refreshOpPlan() {
      if (!this.needOpPlan || !this.selectedKey || !this.mode) return
      const ids = this.wizardOp === 'scale_out'
        ? [...new Set([...this.memberHostIds, ...this.selectedHostIds])]
        : [...this.selectedHostIds]
      if (!ids.length) { this.opPlan = null; this.opPlanErr = ''; return }
      const masterHost = ((this.targetInstance || {}).hosts || []).find(h => h.role === 'master' && h.status !== 'removed')
      this.opPlanLoading = true
      this.opPlanErr = ''
      try {
        const r = await api.post('/stacks/plan/preview', {
          stack_key: this.selectedKey,
          mode: this.mode,
          host_ids: ids,
          master_host_id: this.masterHostId || (this.wizardOp === 'scale_out' && masterHost ? masterHost.host_id : 0),
          params: this.sharedParams || {},
          host_params: this.hostParams || {}
        })
        if (r.code === 0) { this.opPlan = r.data; this.opPlanErr = '' }
        else { this.opPlan = null; this.opPlanErr = r.message || '角色计划预览失败' }
      } catch (e) {
        this.opPlan = null; this.opPlanErr = (e && e.message) || '角色计划预览失败'
      }
      this.opPlanLoading = false
    },
    async replanInst() {
      if (!this.instDetail) return
      try {
        const r = await api.post('/stacks/instances/' + this.instDetail.id + '/replan')
        if (r.code !== 0) { ElMessage.error(r.message || '重规划失败'); return }
        ElMessage.success('角色计划已重规划' + (r.data && r.data.rev ? '（rev ' + r.data.rev + '）' : ''))
        this.instPlan = r.data
      } catch (e) { ElMessage.error(e.message || '重规划失败') }
    },
    async openVerifyDialog(it) {
      this.verifyVisible = true
      this.verifyResult = null
      this.verifyTarget = it
      try {
        const d = await api.get('/stacks/instances/' + it.id)
        if (d.code === 0) this.verifyTarget = d.data
      } catch (e) { /* */ }
      await this.runVerify()
    },
    async runVerify() {
      const it = this.verifyTarget
      if (!it || it.status === 'uninstalled') return
      this.verifying = true
      try {
        const r = await api.post('/stacks/instances/' + it.id + '/verify', {}, { timeout: 120000 })
        if (r.code !== 0) { ElMessage.error(r.message || '探活失败'); return }
        this.verifyResult = r.data
        if (!r.data?.ok) ElMessage.warning(r.data?.summary || '探活未通过')
      } catch (e) {
        ElMessage.error(e.message || '探活失败')
      } finally { this.verifying = false }
    },
    epProtocolLabel(url) {
      if (!url) return 'TCP'
      if (url.startsWith('redis-sentinel://')) return 'SENTINEL'
      if (url.startsWith('redis://')) return 'REDIS'
      if (url.startsWith('http://') || url.startsWith('https://')) return 'HTTP'
      if (url.startsWith('zk://')) return 'ZK'
      if (url.startsWith('mongodb://')) return 'MONGO'
      if (url.startsWith('kafka://')) return 'KAFKA'
      if (url.startsWith('rocketmq://')) return 'MQ'
      return 'TCP'
    },
    epProtocolClass(url) {
      const p = this.epProtocolLabel(url).toLowerCase()
      return 'proto-' + p
    },
    copyText(text) {
      if (!text) return
      if (navigator.clipboard) {
        navigator.clipboard.writeText(text).then(() => ElMessage.success('已复制')).catch(() => {})
      } else {
        const ta = document.createElement('textarea'); ta.value = text; document.body.appendChild(ta); ta.select()
        try { document.execCommand('copy'); ElMessage.success('已复制') } catch (e) { /* */ }
        document.body.removeChild(ta)
      }
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
    async reinstallInstance(it) {
      try {
        await ElMessageBox.confirm('第一步会自动清理：停止并移除各节点残留容器、清空服务数据目录（避免上次失败污染），随后按原主机和参数全新部署。实例记录保留。', '重新安装 ' + it.name, {
          type: 'warning', confirmButtonText: '开始重装', cancelButtonText: '取消'
        })
      } catch (e) { return }
      try {
        const r = await api.post('/stacks/instances/' + it.id + '/reinstall')
        if (r.code !== 0) { ElMessage.error(r.message || '重装失败'); return }
        ElMessage.success('已开始重新安装')
        await this.afterStackOp({ ...r.data, op: 'reinstall' })
      } catch (e) { ElMessage.error(e.message || '重装失败') }
    },
    async uninstallInstance(it) {
      try {
        await ElMessageBox.confirm('将停止并移除全部节点上的套件容器；服务数据目录默认保留（重装会先自动清空），实例记录保留为「已卸载」。', '卸载 ' + it.name, {
          type: 'warning', confirmButtonText: '确认卸载', cancelButtonText: '取消'
        })
      } catch (e) { return }
      try {
        const r = await api.post('/stacks/instances/' + it.id + '/uninstall')
        if (r.code !== 0) { ElMessage.error(r.message || '卸载失败'); return }
        ElMessage.success('已开始卸载')
        await this.afterStackOp({ ...r.data, op: 'uninstall' })
      } catch (e) { ElMessage.error(e.message || '卸载失败') }
    },
    async purgeInstance(it) {
      try {
        await ElMessageBox.confirm(
          '⚠️ 将停止并删除全部节点上的套件容器、数据卷与服务目录（' + (it.home_hint || '含 Hive/HBase 等全部数据') + '），删除后不可恢复！' +
          '清理完成后集群转为「已卸载」，可重新安装。',
          '清理残留 · ' + it.name, {
            type: 'error', confirmButtonText: '确认清理', cancelButtonText: '取消'
          })
      } catch (e) { return }
      try {
        const r = await api.post('/stacks/instances/' + it.id + '/uninstall', { purge: true })
        if (r.code !== 0) { ElMessage.error(r.message || '清理失败'); return }
        ElMessage.success('已开始清理残留')
        await this.afterStackOp({ ...r.data, op: 'uninstall' })
      } catch (e) { ElMessage.error(e.message || '清理失败') }
    },
    async deleteInstance(it) {
      if (it.status !== 'uninstalled') { ElMessage.warning('请先卸载后再删除'); return }
      const msg = '将删除本地集群配置与全部流程记录，不可恢复。服务器上的服务已在卸载时停止。'
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
      this.step = 1; this.selectedKey = ''; this.mode = ''; this.replicas = '0'; this.masterHostId = 0; this.masters = {}
      Object.values(window.StackForms || {}).forEach(fe => { if (fe.resetSelection) fe.resetSelection(this) })
      this.selectedHostIds = new Set(); this.sharedParams = {}; this.hostParams = {}
      this.preflightVisible = false; this.preflight = {}; this.deploying = false
      this.wizardOp = 'create'; this.clusterName = ''; this.targetInstance = null; this.memberHostIds = new Set()
      this.opPlan = null; this.opPlanErr = ''; this.opPlanLoading = false
    },
    selectStack(s) {
      this.selectedKey = s.key
      this.masters = {}
      const fe = window.StackForms && window.StackForms[s.key]
      if (fe?.onSelect) fe.onSelect(this)
      if (s.modes && s.modes.length) this.onModeChange(s.modes[0].key)
    },
    formToggle(key, val) {
      const fe = this.formEntry
      if (fe?.toggle) fe.toggle(this, key, val)
      this.syncMasters()
    },
    // 角色规划：清理未勾选组件 / 已移除主机的指向
    syncMasters() {
      const fe = this.formEntry
      if (!fe?.masterComps && !fe?.haRoles) return
      const valid = new Set(this.selectedHostIds)
      const keep = new Set()
      ;(fe.masterComps || []).forEach(mc => { if (this.bdSel.includes(mc.key)) keep.add(mc.key) })
      ;(fe.haRoles || []).forEach(g => { if (this.bdSel.includes(g.comp)) g.roles.forEach(r => keep.add(r.key)) })
      Object.keys(this.masters).forEach(k => {
        const v = this.masters[k]
        if (!keep.has(k)) { delete this.masters[k]; return }
        if (Array.isArray(v)) this.masters[k] = v.filter(id => valid.has(id))
        else if (!valid.has(v)) delete this.masters[k]
      })
    },
    isHaMode() { return this.sharedParams?.ha === 'true' },
    // 提交用：组件→主角色/从角色主机 IP 的 JSON（未指定的回落 NameNode / 主节点）
    mastersForSubmit() {
      const fe = this.formEntry
      if (!fe?.masterComps && !fe?.haRoles) return ''
      const byId = {}
      this.hosts.forEach(h => { byId[h.id] = h.ip })
      const nnId = this.masters.hdfs || this.resolveMasterHostId()
      const primary = (nnId && byId[nnId]) || (this.hosts.find(h => h.id === this.masterHostId) || {}).ip || ''
      const out = {}
      if (!this.isHaMode() || !fe.haRoles) {
        ;(fe.masterComps || []).forEach(mc => {
          if (!this.bdSel.includes(mc.key)) return
          const ip = (this.masters[mc.key] && byId[this.masters[mc.key]]) || primary
          if (ip) out[mc.key] = ip
        })
        return Object.keys(out).length ? JSON.stringify(out) : ''
      }
      // HA：全角色矩阵（主角色行与从角色 key 一同提交，后端确定性算法接管自动落点）
      ;(fe.haRoles || []).forEach(grp => {
        if (!this.bdSel.includes(grp.comp)) return
        grp.roles.forEach(r => {
          const v = this.masters[r.key]
          if (r.multi) {
            const ips = (Array.isArray(v) ? v : []).map(id => byId[id]).filter(Boolean)
            if (ips.length) out[r.key] = ips.join(',')
          } else {
            let ip = v && byId[v]
            if (r.key === grp.comp && !ip) ip = primary
            if (ip) out[r.key] = ip
          }
        })
      })
      return Object.keys(out).length ? JSON.stringify(out) : ''
    },
    resolveMasterHostId() {
      if (this.useRolePlan) {
        const nn = this.masters.hdfs
        if (nn && this.selectedHostIds.has(nn)) return nn
        if (this.masterHostId && this.selectedHostIds.has(this.masterHostId)) return this.masterHostId
        return this.selectedHosts[0]?.id || 0
      }
      if (this.needMaster) return this.masterHostId
      return this.masterHostId || 0
    },
    hostRolePlanSummary(h) {
      const fe = this.formEntry
      if ((!fe?.masterComps && !fe?.haRoles) || !h) return ''
      const labels = []
      ;(fe.masterComps || []).forEach(mc => {
        if (!this.bdSel.includes(mc.key)) return
        if (this.masters[mc.key] === h.id) labels.push(mc.label)
      })
      ;(fe.haRoles || []).forEach(grp => {
        if (!this.bdSel.includes(grp.comp)) return
        grp.roles.forEach(r => {
          const v = this.masters[r.key]
          if (r.multi) {
            if (Array.isArray(v) && v.includes(h.id)) labels.push(r.label.replace(/ ×\d+$/, ''))
          } else if (v === h.id && !labels.includes(r.label)) {
            labels.push(r.label)
          }
        })
      })
      return labels.join(' · ')
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
    onBoolVar(name, val) {
      this.$set(this.sharedParams, name, val ? 'true' : 'false')
    },
    onHaToggle(val) {
      this.sharedParams.ha = val ? 'true' : 'false'
      const fe = this.formEntry
      if (fe?.onHaToggle) fe.onHaToggle(this, val)
    },
    goStep(n) {
      this.step = n
      if (n >= 2) {
        Promise.resolve(this.loadHosts()).then(() => {
          if (this.needMaster && !this.masterHostId && this.selectedHosts.length) {
            this.masterHostId = this.selectedHosts[0].id
          }
          if (this.useRolePlan) this.ensureDefaultRoles()
          this.$nextTick(() => this.syncRoleHostTableSelection())
        })
      }
    },
    onRoleHostSelection(rows) {
      if (this._syncingRoleTable) return
      this.selectedHostIds = new Set((rows || []).map(h => h.id))
      this.syncMasters()
      this.ensureDefaultRoles()
    },
    syncRoleHostTableSelection() {
      if (!this.useRolePlan) return
      const table = this.$refs.roleHostTable
      if (!table) return
      const rows = this.filteredHosts || []
      this._syncingRoleTable = true
      table.clearSelection()
      rows.forEach(h => {
        if (this.selectedHostIds.has(h.id)) table.toggleRowSelection(h, true)
      })
      this.$nextTick(() => { this._syncingRoleTable = false })
    },
    ensureDefaultRoles() {
      if (!this.useRolePlan || !this.selectedHosts.length) return
      const first = this.selectedHosts[0].id
      // 新建：NameNode 默认落在首台已选主机
      if (this.wizardOp === 'create' && this.bdSel.includes('hdfs')) {
        if (!this.masters.hdfs || !this.selectedHostIds.has(this.masters.hdfs)) {
          this.masters = { ...this.masters, hdfs: first }
        }
      }
      this.masterHostId = this.resolveMasterHostId()
    },
    toggleHost(id) {
      const next = new Set(this.selectedHostIds)
      if (next.has(id)) next.delete(id); else next.add(id)
      this.selectedHostIds = next
      if (this.needMaster) {
        if (!next.has(this.masterHostId)) this.masterHostId = next.size ? [...next][0] : 0
      }
      this.syncMasters()
      if (this.useRolePlan) this.ensureDefaultRoles()
      this.$nextTick(() => this.syncRoleHostTableSelection())
    },
    toggleSelectAll() {
      // 缩容（bigdata）：全选跳过承载落点角色的成员
      const pool = this.filteredHosts.filter(h => !this.scaleInProtected[h.ip])
      if (this.selectedHosts.length === pool.length && pool.length) this.selectedHostIds = new Set()
      else this.selectedHostIds = new Set(pool.map(h => h.id))
      if (this.needMaster && this.selectedHosts.length && !this.selectedHostIds.has(this.masterHostId)) {
        this.masterHostId = this.selectedHosts[0]?.id || 0
      }
      this.syncMasters()
      if (this.useRolePlan) this.ensureDefaultRoles()
      this.$nextTick(() => this.syncRoleHostTableSelection())
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
      // 部署前角色预览（全部套件，docs/角色物化设计.md §二）：参数已就绪，与部署前置一同确认落点
      if (this.needOpPlan) {
        await this.refreshOpPlan()
        if (!this.opPlan) { ElMessage.error(this.opPlanErr || '角色计划预览未生成，无法部署'); return }
      }
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
        const startedOp = this.wizardOp
        this.wizardVisible = false
        await this.afterStackOp({ ...r.data, op: startedOp })
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
        const startedOp = this.wizardOp
        this.wizardVisible = false
        await this.afterStackOp({ ...r.data, op: startedOp })
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
        const startedOp = this.wizardOp
        this.wizardVisible = false
        await this.afterStackOp({ ...r.data, op: startedOp })
      } catch (e) { ElMessage.error(e.message || '卸载组件失败') }
      finally { this.deploying = false }
    },
    async submitWizard() {
      const host_params = {}
      this.selectedHosts.forEach(h => { if (this.hostParams[h.id]) host_params[String(h.id)] = this.hostParams[h.id] })
      const extra = (this.formEntry?.extraParams && this.formEntry.extraParams(this)) || {}
      const params = { ...this.sharedParams, ...extra }
      const body = {
        stack_key: this.selectedKey, mode: this.mode,
        name: this.clusterName.trim(),
        host_ids: this.selectedHosts.map(h => h.id),
        master_host_id: this.resolveMasterHostId(),
        params, host_params
      }
      if (this.wizardOp === 'create') return api.post('/stacks/run', body)
      const id = this.targetInstance?.id
      if (!id) { ElMessage.error('缺少集群实例'); return null }
      const path = { scale_out: 'scale-out', scale_in: 'scale-in', add_component: 'add-component', remove_component: 'remove-component' }[this.wizardOp]
      return api.post('/stacks/instances/' + id + '/' + path, body)
    },
    async afterStackOp(data) {
      await this.loadInstances()
      if (this.instDrawerVisible && this.instDetail?.id) {
        await this.openInstance(this.instDetail)
      }
      const runId = data && (data.run_id || data.id)
      if (runId) {
        this.instDrawerVisible = false
        await this.openDrawer({ id: runId, status: 'running', op: data.op || this.wizardOp || 'create' })
      }
    },
    onLogDrawerBeforeClose(done) {
      if (this.logDrawerLocked) {
        ElMessage.warning('加装进行中，请等待完成后再关闭')
        return
      }
      done()
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
      const isMaster = h.id === this.masterHostId
      const t = this.formEntry?.roleTag?.(this, isMaster)
      if (t) return t
      if (isMaster) return '主'
      if (this.mode === 'replication' || this.mode === 'sentinel') return '从'
      return '工作节点'
    },
    opLabel(op) { return { create: '创建', reinstall: '重装', scale_out: '扩容', scale_in: '缩容', add_component: '加装', uninstall: '卸载', remove_component: '卸组件' }[op] || op || '创建' },
    modeLabel(m, stackKey) {
      const key = stackKey || this.recordMeta?.stack_key || this.selectedKey
      const s = this.stacks.find(x => x.key === key)
      const md = s && s.modes ? s.modes.find(x => x.key === m) : null
      if (md && md.label) return md.label
      return { replication: '主从', sentinel: '哨兵', cluster: '集群', standalone: '单机', single: '单机', ha: '高可用', kraft: 'KRaft 集群', zk: 'ZK 集群' }[m] || m
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
      const d = new Date(t.replace(' ', 'T'))
      return d.getFullYear() + '-' + String(d.getMonth()+1).padStart(2,'0') + '-' + String(d.getDate()).padStart(2,'0') + ' ' + String(d.getHours()).padStart(2,'0') + ':' + String(d.getMinutes()).padStart(2,'0')
    }
  }
}
