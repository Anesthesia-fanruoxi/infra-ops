// 套件部署页模板片段 · wizard（原样迁入；套件专属表达式已改为通用分发名，类名统一 .stk-*）。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  P.tpl.wizard = `  <el-dialog v-model="wizardVisible" :title="wizardTitle" width="85%" top="6vh" class="stk-wizard-dialog" :close-on-click-modal="false" @closed="resetWizard">
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
      <div v-for="g in stackGroups" :key="g.key" class="stk-cat">
        <div class="stk-cat-head">
          <span class="stk-cat-title">{{g.label}}</span>
          <span class="stk-cat-hint">{{g.hint}}</span>
        </div>
        <div class="stk-card-grid">
          <button v-for="s in g.stacks" :key="s.key" type="button" class="stk-card" :class="{'stk-card--sel': selectedKey===s.key}" @click="selectStack(s)">
            <div class="stk-card-head">
              <div class="stk-card-ava" :class="stackAvatar(s).c">{{stackAvatar(s).t}}</div>
              <div class="stk-card-name">{{s.name}}</div>
              <span class="stk-runtime" :class="s.requires_docker ? 'is-container' : 'is-host'">{{runtimeLabel(s)}}</span>
              <el-switch v-if="s.ha_support && wizardOp==='create'" size="small" :model-value="sharedParams.ha==='true'" :disabled="selectedKey!==s.key" :inline-prompt="true" active-text="HA" inactive-text="" @click.stop.prevent @change="e => onHaToggle(e)"></el-switch>
            </div>
            <div class="stk-card-modes">
              <el-tag v-for="m in s.modes" :key="m.key" size="small" :type="mode===m.key ? 'primary' : 'info'" effect="plain">{{m.label}}</el-tag>
            </div>
            <div class="stk-card-desc">{{s.description}}</div>
          </button>
        </div>
      </div>
      <div v-if="!stacks.length && !stacksLoading" class="deploy-placeholder">暂无可用套件</div>
      <div v-if="selectedStack" class="stk-mode-box">
        <component v-if="step1FormComp" :is="step1FormComp" :ctx="selfCtx" />
        <template v-else>
          <div class="deploy-host-vars-title">部署模式</div>
          <div class="stk-mode-grid">
            <label v-for="m in selectedStack.modes" :key="m.key" class="stk-mode" :class="{'stk-mode--sel': mode===m.key}">
              <el-radio :model-value="mode" :label="m.key" @change="onModeChange(m.key)">{{m.label}}</el-radio>
              <p>{{m.description}}</p>
              <span class="faint">{{m.host_hint}}</span>
            </label>
          </div>
        </template>
        <p class="stk-hint" v-if="selectedStack">{{selectedStack.requires_docker ? '该套件以 Docker 容器运行，部署前会检查并按需安装 Docker。' : '该套件直接安装在主机上，不使用容器，也不会检查 Docker。'}}</p>
        <p class="stk-hint stk-ha-hint" v-if="selectedStack?.ha_support && sharedParams.ha==='true'">本次安装所有支持 HA 的组件将双实例部署，依赖 ZooKeeper 已自动勾选，最低需 3 台主机。</p>
        <p class="stk-hint" v-for="h in step1Hints" :key="h">{{h}}</p>
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
          class="stk-role-host-table"
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
        <div v-if="wizardOp==='scale_out'" class="stk-freeze-hint">
          主节点 / 引导节点已随集群部署冻结，扩容不改变主节点布局；新机自动承担从属 / 成员等全员角色。
        </div>
        <div v-if="wizardOp==='scale_in'" class="stk-freeze-hint">
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
        <!-- 角色计划预览（通用套件）：第二步勾选主机即确认落点，不再藏进开始部署的弹框 -->
        <div v-if="needOpPlan" class="stk-plan-preview" style="margin-top:12px">
          <div class="stk-plan-preview-head">
            <span>角色计划预览 · 确认落点后开始部署</span>
            <span v-if="opPlan" class="faint">{{opPlan.generated_by === 'manual' ? '含手动指定' : '自动分配'}}</span>
            <el-button size="small" text type="primary" :loading="opPlanLoading" @click="refreshOpPlan">{{opPlan ? '刷新' : '生成预览'}}</el-button>
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
          <div v-else class="faint" style="font-size:12px;padding-top:6px">勾选主机后自动生成角色计划预览…</div>
          <div class="stk-legend"><span class="stk-legend-item"><i class="dot is-manual"></i>手动</span><span class="stk-legend-item"><i class="dot is-auto"></i>自动</span><span class="stk-legend-sep"></span><span class="stk-legend-item rt-primary">主/Active</span><span class="stk-legend-item rt-standby">备/Standby</span><span class="stk-legend-item rt-quorum">仲裁</span><span class="stk-legend-item rt-worker">工作/成员</span><span class="stk-legend-item rt-aux">辅助</span></div>
          <div v-if="opPlan && (opPlan.warnings||[]).length" class="stk-plan-preview-warn">
            <div v-for="(w, i) in opPlan.warnings" :key="i">{{w}}</div>
          </div>
        </div>
      </template>
    </div>

    <div v-show="step===3">
      <div class="deploy-host-vars-head" v-if="wizardOp==='create'">
        <span class="deploy-host-vars-title">集群名称</span>
      </div>
      <div class="deploy-host-var-fields stk-shared-fields" v-if="wizardOp==='create'" style="margin-bottom:12px">
        <div class="deploy-host-var-field">
          <label>名称</label>
          <el-input size="small" v-model="clusterName" :placeholder="defaultClusterName" />
        </div>
      </div>
      <div class="deploy-host-vars-head">
        <span class="deploy-host-vars-title">集群共享参数</span>
      </div>
      <div class="deploy-host-var-fields stk-shared-fields">
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
  </el-dialog>`
})()
