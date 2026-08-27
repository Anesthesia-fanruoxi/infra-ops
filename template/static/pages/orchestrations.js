window.OrchestrationsPage = {
  props: ['page', 'user', 'versionData'],
  mixins: [window.OrchDrawerMixin],
  template: `
<div class="orch-page">
  <section class="deploy-hero">
    <div class="deploy-hero-grid"></div>
    <div class="deploy-hero-glow"></div>
    <div class="deploy-hero-content">
      <div class="deploy-hero-row">
        <div>
          <span class="deploy-eyebrow">ORCHESTRATION</span>
          <h1>任务编排</h1>
          <p>流水线式编排多个部署单元 · 顺序串行 · 支持长任务</p>
        </div>
        <el-button type="primary" size="large" class="deploy-new-btn" @click="openEditor()">
          <el-icon style="margin-right:6px"><Plus /></el-icon>新建任务
        </el-button>
      </div>
    </div>
  </section>

  <!-- 任务记录列表 -->
  <div class="page-card">
    <div class="card-header">
      <span class="title">任务记录</span>
      <el-button size="small" text @click="loadList"><el-icon style="margin-right:4px"><Refresh /></el-icon>刷新</el-button>
    </div>
    <div class="orch-state-tabs">
      <span class="orch-state-tab" :class="{active: activeState===''}" @click="activeState=''">全部 <em>{{stateCounts.all}}</em></span>
      <span class="orch-state-tab" :class="{active: activeState==='running'}" @click="activeState='running'">运行中 <em>{{stateCounts.running}}</em></span>
      <span class="orch-state-tab" :class="{active: activeState==='not_started'}" @click="activeState='not_started'">未开始 <em>{{stateCounts.not_started}}</em></span>
      <span class="orch-state-tab" :class="{active: activeState==='finished'}" @click="activeState='finished'">已结束 <em>{{stateCounts.finished}}</em></span>
    </div>
    <el-table :data="displayList" style="width:100%" v-loading="loading" @row-click="rowClick">
      <el-table-column label="名称" min-width="180">
        <template #default="{row}"><span style="font-weight:600">{{row.name}}</span>
          <div v-if="row.description" style="font-size:11px;color:var(--text-faint)">{{row.description}}</div></template>
      </el-table-column>
      <el-table-column label="流水线" min-width="260">
        <template #default="{row}">
          <div class="pipe-mini">
            <template v-for="(s,idx) in pipePreview(row.id)" :key="idx">
              <span class="pipe-mini-arrow" v-if="idx>0">→</span>
              <span class="pipe-mini-node">{{s}}</span>
            </template>
          </div>
        </template>
      </el-table-column>
      <el-table-column label="步骤数" width="80">
        <template #default="{row}"><span class="mono">{{row.step_count}}</span></template>
      </el-table-column>
      <el-table-column label="状态" width="110">
        <template #default="{row}"><span class="status-badge" :class="stateClass(row.state)"><span class="dot"></span>{{stateLabel(row.state)}}</span></template>
      </el-table-column>
      <el-table-column label="结果" width="220">
        <template #default="{row}">
          <template v-if="row.state==='finished'">
            <span class="status-badge" :class="resultClass(row.result)"><span class="dot"></span>{{resultLabel(row.result)}}</span>
            <span class="mono orch-prog"><span class="ok">{{row.ok_hosts}}</span> 成功 · <span class="fail">{{row.fail_hosts}}</span> 失败 · 共 {{row.total_hosts}}</span>
          </template>
          <span v-else style="color:var(--text-faint)">-</span>
        </template>
      </el-table-column>
      <el-table-column label="更新时间" width="160">
        <template #default="{row}"><span class="mono" style="font-size:12px;color:var(--text-faint)">{{formatTime(row.updated_at)}}</span></template>
      </el-table-column>
      <el-table-column label="操作" width="200" fixed="right">
        <template #default="{row}">
          <template v-if="row.state==='not_started'">
            <el-button size="small" type="primary" @click="startRun(row)">运行</el-button>
            <el-button size="small" @click="openEditor(row)">编辑</el-button>
            <el-button size="small" type="danger" text @click="removeOrch(row)">删除</el-button>
          </template>
          <template v-else-if="row.state==='running'">
            <el-button size="small" type="primary" @click="openRunDrawer(row.last_run_id)">进度</el-button>
            <el-button size="small" type="danger" text @click="removeOrch(row)">删除</el-button>
          </template>
          <template v-else>
            <el-button size="small" @click="openRunDrawer(row.last_run_id)">详情</el-button>
            <el-button size="small" type="danger" text @click="removeOrch(row)">删除</el-button>
          </template>
        </template>
      </el-table-column>
    </el-table>
    <div v-if="!loading && !displayList.length" class="empty-state"><p>还没有任务记录，点击右上角「新建任务」创建</p></div>
  </div>

  <!-- 新建第一步：设定任务名称 -->
  <el-dialog v-model="nameDialogVisible" title="新建任务" width="440px" :close-on-click-modal="false">
    <el-form label-position="top" @submit.prevent>
      <el-form-item label="任务名称" required>
        <el-input v-model="nameDraft" placeholder="如：新机初始化" maxlength="50" show-word-limit
          @keydown.enter="confirmName" />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="nameDialogVisible=false">取消</el-button>
      <el-button type="primary" :disabled="!nameDraft.trim()" @click="confirmName">下一步：编排模板</el-button>
    </template>
  </el-dialog>

  <!-- 编辑任务：大弹框 + 流水线 -->
  <el-dialog v-model="editorVisible" :title="'任务：' + form.name" width="85%" top="10vh" class="orch-editor-dialog"
    :close-on-click-modal="false" @open="onEditorOpen">
    <el-form label-position="top">
      <div class="form-grid">
        <el-form-item label="任务名称" required><el-input v-model="form.name" placeholder="如：新机初始化" style="max-width:360px" /></el-form-item>
        <el-form-item label="说明"><el-input v-model="form.description" placeholder="可选" style="max-width:420px" /></el-form-item>
      </div>
    </el-form>

    <!-- 流水线 -->
    <div class="pipe-wrap">
      <div class="pipe-flow">
        <div v-for="(s,i) in form.steps" :key="s.uid" class="pipe-node"
          :class="{active: editIdx===i, dragging: dragIdx===i, 'drag-over': dragOver===i && dragIdx!==i}"
          draggable="true"
          @dragstart="onDragStart(i,$event)" @dragover.prevent="dragOver=i"
          @dragleave="dragOver=null" @dragend="onDragEnd" @drop.prevent="onDropTo(i)"
          @click="openDrawer(i)">
          <span class="pipe-seq">{{i+1}}</span>
          <div class="pipe-info">
            <b>{{s.template_name}}</b>
            <small>{{(s.hostIds||[]).length}} 台主机<template v-if="hostVarTotal(s)"> · {{hostVarTotal(s)}} 台覆盖变量</template></small>
          </div>
          <span class="pipe-tools" title="拖动节点调整顺序" @click.stop>
            <el-icon class="pipe-tool pipe-tool-danger" title="移除本步骤" @click="removeStep(i)"><Close /></el-icon>
          </span>
        </div>
        <div class="pipe-add" @click="addClick">
          <el-icon><Plus /></el-icon><span>添加步骤</span>
        </div>
      </div>
      <div class="pipe-hint">点击节点在右侧抽屉中编辑该步骤的主机与变量；执行顺序从左到右</div>
    </div>

    <template #footer>
      <el-button @click="editorVisible=false">取消</el-button>
      <span v-if="invalidReason" class="save-block-reason">{{invalidReason}}</span>
      <el-button type="primary" :disabled="!formValid" @click="saveOrch">保存</el-button>
    </template>
  </el-dialog>

  <!-- 步骤编辑抽屉 -->
  <el-drawer v-model="drawerVisible" :title="drawerTitle" size="50%" :close-on-click-modal="false">
    <!-- 新增模式：选模板 -->
    <div v-if="drawerMode==='add'" v-loading="templatesLoading">
      <div class="drawer-tip">选择一个模板作为新步骤：</div>
      <div class="tpl-pick-list">
        <div v-for="t in templates" :key="t.id" class="tpl-pick-item" @click="pickTemplate(t)">
          <div><b>{{t.name}}</b><div v-if="t.description" class="tpl-pick-desc">{{t.description}}</div></div>
          <el-tag size="small" type="info">{{(t.variables||[]).length}} 变量</el-tag>
        </div>
      </div>
    </div>

    <!-- 编辑模式 -->
    <template v-else-if="editStep">
      <div class="drawer-sec">
        <div class="drawer-sec-title">目标主机 <span class="deploy-sel-count">已选 {{(editStep.hostIds||[]).length}} 台</span></div>
        <div class="orch-unit-hosts">
          <el-input v-model="editStep.hostFilter" placeholder="搜索主机名 / IP" clearable size="small" style="width:180px" />
          <el-select v-model="editStep.tagFilter" multiple collapse-tags placeholder="按标签过滤" size="small"
            style="width:200px" :teleported="false" clearable>
            <el-option v-for="tg in allTags" :key="tg" :label="tg" :value="tg" />
          </el-select>
          <el-button size="small" @click="toggleAllUnitHosts(editStep)">{{ unitAllSelected(editStep) ? '取消全选' : '全选' }}</el-button>
          <span class="deploy-sel-count">已选 {{(editStep.hostIds||[]).length}} 台</span>
        </div>
        <div class="deploy-host-list deploy-host-list--unit" v-loading="hostLoading">
          <label v-for="h in unitHosts(editStep)" :key="h.id" class="deploy-host-item"
            :class="{'deploy-host-item--sel': (editStep.hostIds||[]).includes(h.id)}">
            <el-checkbox :model-value="(editStep.hostIds||[]).includes(h.id)" @change="toggleUnitHost(editStep,h.id)" />
            <span class="deploy-host-name">{{h.name}}</span>
            <span class="mono deploy-host-ip">{{h.ip}}</span>
            <span class="status-badge" :class="h.status"><span class="dot"></span>{{h.status==='online'?'在线':h.status==='offline'?'离线':'未验证'}}</span>
          </label>
          <div v-if="!unitHosts(editStep).length" class="deploy-placeholder">无匹配主机，请勾选本步骤要执行的主机</div>
        </div>
      </div>

      <template v-if="tplVars(editStep.template_id).length && (editStep.hostIds||[]).length">
        <div class="drawer-sec">
          <div class="drawer-sec-title">变量 · 逐台填写
            <el-button size="small" text type="primary" @click="copyFirstHostVars(editStep)">复制首台到其他</el-button>
            <el-button size="small" text @click="clearHostVars(editStep)">清空覆盖</el-button>
          </div>
          <div class="orch-hostvars-rows">
            <div class="orch-hostvar-row" v-for="hid in (editStep.hostIds||[])" :key="'hv'+hid">
              <div class="orch-hostvar-side">
                <span class="deploy-host-name">{{hostName(hid)}}</span>
                <span class="mono deploy-host-ip">{{hostIP(hid)}}</span>
                <span class="orch-hv-meta">
                  <span v-if="hostVarCount(editStep,hid)" class="deploy-var-hint">覆盖{{hostVarCount(editStep,hid)}}项</span>
                  <el-button size="small" text type="primary" @click="clearHostVar(editStep,hid)">重置</el-button>
                </span>
              </div>
              <div class="orch-step-params">
                <div class="orch-param" v-for="v in tplVars(editStep.template_id)" :key="'hv'+hid+v.name">
                  <label>{{v.label || v.name}}<b v-if="v.required">*</b></label>
                  <el-input size="small" :model-value="hostVarValue(editStep,hid,v)" @input="val => setHostVar(editStep,hid,v,val)"
                    :placeholder="v.default ? ('默认: ' + v.default) : '必填'" />
                </div>
              </div>
            </div>
          </div>
        </div>
      </template>
      <div v-else class="deploy-placeholder deploy-placeholder--ok">该模板无需变量</div>

      <div class="drawer-sec">
        <div class="drawer-sec-title">失败策略</div>
        <div class="orch-fail-row">
          <el-checkbox v-model="editStep.continue_on_error">本步骤失败的仍继续参与后续步骤</el-checkbox>
          <span class="orch-retry">重试
            <el-input-number v-model="editStep.retry_count" size="small" :min="0" :max="5" controls-position="right" style="width:90px" /> 次 ·
            间隔 <el-input-number v-model="editStep.retry_interval_sec" size="small" :min="5" :max="600" controls-position="right" style="width:110px" /> 秒
          </span>
        </div>
      </div>
    </template>

    <template #footer>
      <div style="display:flex;align-items:center;gap:8px">
        <el-button v-if="drawerMode==='edit'" type="danger" text @click="removeStep(editIdx)">删除本步骤</el-button>
        <span v-if="invalidReason && drawerMode==='edit'" class="save-block-reason">{{invalidReason}}</span>
        <span style="flex:1"></span>
        <el-button @click="drawerVisible=false">取消</el-button>
        <el-button type="primary" @click="saveStep">保存步骤</el-button>
      </div>
      <div class="drawer-save-hint">「保存步骤」仅应用到当前流水线；点击外层「保存」才会写入数据库</div>
    </template>
  </el-drawer>

  <!-- 运行抽屉：三层结构（只读监控） -->
  <el-drawer v-model="runDrawerVisible" size="50%" :with-header="false" @close="closeRunDrawer">
    <div class="orch-run-drawer" v-loading="drawerLoading">
      <!-- 头部：run 元信息 -->
      <div class="orch-rd-head" v-if="runMeta">
        <div class="orch-rd-title">{{ runMeta.name }} <span class="mono">#{{ runMeta.id }}</span></div>
        <div class="orch-rd-meta">
          <span class="status-badge" :class="runStatusCls(runStatus)"><span class="dot"></span>{{ runBadgeLabel }}</span>
          <span class="mono orch-prog"><span class="ok">{{ runMeta.ok_hosts }}</span> 成功 · <span class="fail">{{ runMeta.fail_hosts }}</span> 失败 · 共 {{ runMeta.total_hosts }} 台</span>
          <span class="mono orch-rd-time">{{ formatTime(runMeta.created_at) }}</span>
          <template v-if="runStatus === 'running'">
            <span v-if="followMode" class="orch-rd-follow">● 自动追踪中</span>
            <span v-else class="orch-rd-follow orch-rd-follow--paused">已暂停自动追踪，点击执行中步骤恢复</span>
          </template>
        </div>
      </div>
      <!-- L1 步骤条 -->
      <div class="orch-rd-l1">
        <template v-for="(s, i) in drawerSteps" :key="s.seq">
          <span v-if="i>0" class="orch-rd-arrow">→</span>
          <span class="orch-rd-step" :class="stepCls(s)" :title="s.name" @click="selectStep(s.seq)">
            <span class="orch-rd-step-no">{{ s.seq }}</span>
            <span class="orch-rd-step-name">{{ s.name }}</span>
            <span class="orch-rd-step-mark">{{ stepMark(s) }}</span>
            <span v-if="stepIsActive(s)" class="orch-rd-step-active"></span>
          </span>
        </template>
        <div v-if="!drawerSteps.length" class="orch-rd-empty">暂无步骤</div>
      </div>
      <!-- L2 主机层 -->
      <div class="orch-rd-l2">
        <div class="orch-rd-sec-title">步骤 {{ selectedSeq }} · 主机</div>
        <div class="orch-rd-hosts">
          <div v-for="h in drawerHosts" :key="h.host_id" class="orch-rd-host">
            <span class="mono orch-rd-host-ip">{{ h.host_ip }}</span>
            <span class="orch-rd-host-name">{{ h.host_name }}</span>
            <span class="status-badge" :class="hostStatusCls(h.status)"><span class="dot"></span>{{ hostStatusLabel(h.status) }}</span>
          </div>
          <div v-if="!drawerHosts.length" class="orch-rd-empty">该步骤暂无主机</div>
        </div>
      </div>
      <!-- L3 日志层 -->
      <div class="orch-rd-l3">
        <div class="orch-rd-sec-title">
          <span>日志 · 步骤 {{ selectedSeq }}</span>
          <el-select v-model="logFilterIp" size="small" clearable placeholder="全部主机" style="width:190px" :teleported="false">
            <el-option v-for="h in drawerHosts" :key="'lf'+h.host_id"
              :label="h.host_ip + (h.host_name ? ' · ' + h.host_name : '')" :value="h.host_ip" />
          </el-select>
        </div>
        <div class="orch-rd-logs" ref="orchLogBox" @scroll="onLogScroll">
          <div v-for="l in filteredLogs" :key="l.id" class="orch-rd-log">
            <span class="mono orch-rd-log-time">{{ logTime(l.ts) }}</span>
            <span class="mono orch-rd-log-ip">{{ l.ip }}</span>
            <span class="orch-rd-log-text">{{ l.text }}</span>
          </div>
          <div v-if="!filteredLogs.length" class="orch-rd-empty">{{ drawerLogs.length ? '当前过滤条件下无日志' : '暂无日志' }}</div>
        </div>
      </div>
    </div>
  </el-drawer>
</div>`,

  data() {
    return {
      allList: [], activeState: '', loading: false,
      detailCache: {}, // orchId -> [templateName...] 列表页流水线预览
      templates: [], templatesLoading: false, hostOptions: [], hostLoading: false,
      editorVisible: false,
      nameDialogVisible: false, nameDraft: '',
      dragIdx: null, dragOver: null, editSnapshot: null, stepSaved: false,
      form: { id: 0, name: '', description: '', steps: [] },
      uidSeed: 1,
      drawerVisible: false, drawerMode: 'edit', editIdx: -1,
      saving: false
    }
  },
  computed: {
    allTags() {
      const set = new Set()
      this.hostOptions.forEach(h => set.add(h.tag || 'other'))
      return [...set].sort()
    },
    formValid() { return !this.invalidReason },
    invalidReason() {
      if (!this.form.name) return '请填写任务名称'
      if (!this.form.steps.length) return '请至少添加一个步骤'
      for (let i = 0; i < this.form.steps.length; i++) {
        const s = this.form.steps[i]
        if (!s.template_id) return '步骤 ' + (i+1) + ' 未选择模板'
        if (!(s.hostIds || []).length) return '步骤 ' + (i+1) + '（' + s.template_name + '）尚未选择主机'
      }
      return ''
    },
    editStep() {
      if (this.drawerMode !== 'edit' || this.editIdx < 0) return null
      return this.form.steps[this.editIdx] || null
    },
    drawerTitle() {
      if (this.drawerMode === 'add') return '添加步骤 · 选择模板'
      const s = this.editStep
      return s ? ('步骤 ' + (this.editIdx + 1) + ' · ' + s.template_name) : ''
    },
    displayList() {
      let arr = this.allList
      if (this.activeState) arr = arr.filter(o => o.state === this.activeState)
      const pri = { running: 0, not_started: 1, finished: 2 }
      return [...arr].sort((a, b) => {
        const pa = pri[a.state] ?? 9, pb = pri[b.state] ?? 9
        if (pa !== pb) return pa - pb
        return (b.updated_at || '').localeCompare(a.updated_at || '')
      })
    },
    stateCounts() {
      const c = { all: this.allList.length, running: 0, not_started: 0, finished: 0 }
      this.allList.forEach(o => { if (c[o.state] !== undefined) c[o.state]++ })
      return c
    }
  },
  watch: {
    drawerVisible(v) {
      if (!v && !this.stepSaved && this.editSnapshot) {
        try {
          const snap = JSON.parse(this.editSnapshot)
          const cur = this.form.steps[this.editIdx]
          if (cur && cur.uid === snap.uid) this.form.steps.splice(this.editIdx, 1, snap)
        } catch(e) {}
      }
      if (!v) { this.editSnapshot = null; this.stepSaved = false }
    }
  },
  mounted() { this.loadList() },
  beforeUnmount() { this.disconnectRunSse() },
  methods: {
    async loadList() {
      this.loading = true
      try {
        const r = await api.get('/orchestrations')
        if (r.code === 0) {
          this.allList = r.data || []
          this.allList.forEach(o => this.fetchPipePreview(o))
        }
      } catch(e) {} finally { this.loading = false }
    },
    async fetchPipePreview(o) {
      try {
        const r = await api.get('/orchestrations/' + o.id)
        if (r.code === 0) this.detailCache[o.id] = (r.data.steps || []).map(s => s.template_name)
      } catch(e) {}
    },
    pipePreview(id) { return this.detailCache[id] || [] },
    async onEditorOpen() {
      if (!this.templates.length) {
        this.templatesLoading = true
        try { const r = await api.get('/deploy/templates'); if (r.code === 0) this.templates = r.data || [] } catch(e) {} finally { this.templatesLoading = false }
      }
      if (!this.hostOptions.length) {
        this.hostLoading = true
        try { const r = await api.get('/hosts', { params: { page:1, page_size:500 } }); if (r.code === 0) this.hostOptions = r.data?.list || [] } catch(e) {} finally { this.hostLoading = false }
      }
    },
    tplVars(tid) {
      const t = this.templates.find(x => x.id === tid)
      return t?.variables || []
    },
    hostName(hid) { return this.hostOptions.find(h => h.id === hid)?.name || ('#' + hid) },
    hostIP(hid) { return this.hostOptions.find(h => h.id === hid)?.ip || '' },
    /* ===== 编辑器 ===== */
    openEditor(row) {
      if (row) {
        api.get('/orchestrations/' + row.id).then(r => {
          if (r.code !== 0) return
          const o = r.data.orchestration
          this.form = {
            id: o.id, name: o.name, description: o.description,
            steps: (r.data.steps || []).map(s => ({
              uid: this.uidSeed++, template_id: s.template_id, template_name: s.template_name,
              hostIds: s.host_ids || [], hostVars: s.host_vars || {},
              continue_on_error: !!s.continue_on_error,
              retry_count: s.retry_count || 0, retry_interval_sec: s.retry_interval_sec || 30,
              hostFilter: ''
            }))
          }
          this.editIdx = -1
          this.editorVisible = true
        })
      } else {
        this.nameDraft = ''
        this.nameDialogVisible = true
      }
    },
    confirmName() {
      const name = this.nameDraft.trim()
      if (!name) return
      this.form = { id: 0, name, description: '', steps: [] }
      this.editIdx = -1
      this.nameDialogVisible = false
      this.editorVisible = true
    },
    /* ===== 流水线节点 ===== */
    addClick() { this.editIdx = -1; this.drawerMode = 'add'; this.drawerVisible = true },
    pickTemplate(t) {
      this.form.steps.push({
        uid: this.uidSeed++, template_id: t.id, template_name: t.name,
        hostIds: [], hostVars: {}, tagFilter: [],
        continue_on_error: false, retry_count: 0, retry_interval_sec: 30, hostFilter: ''
      })
      this.editIdx = this.form.steps.length - 1
      this.drawerMode = 'edit'
      this.stepSaved = true
    },
    removeStep(i) {
      if (this.editIdx === i) { this.stepSaved = true; this.drawerVisible = false }
      this.form.steps.splice(i, 1)
      if (this.editIdx > i) this.editIdx--
      else if (this.editIdx === i) this.editIdx = -1
    },
    onDragStart(i, e) { this.dragIdx = i; e.dataTransfer.effectAllowed = 'move' },
    onDragEnd() { this.dragIdx = null; this.dragOver = null },
    onDropTo(i) {
      const from = this.dragIdx
      this.dragIdx = null; this.dragOver = null
      if (from == null || from === i) return
      const arr = this.form.steps
      const editedUid = this.editIdx >= 0 ? arr[this.editIdx]?.uid : null
      const [m] = arr.splice(from, 1)
      arr.splice(i, 0, m)
      if (editedUid != null) this.editIdx = arr.findIndex(s => s.uid === editedUid)
    },
    openDrawer(i) {
      this.editIdx = i; this.drawerMode = 'edit'
      this.editSnapshot = JSON.stringify(this.form.steps[i])
      this.stepSaved = false
      this.drawerVisible = true
    },
    saveStep() {
      if (!this.editStep) return
      this.stepSaved = true
      this.drawerVisible = false
      ElMessage.success('步骤已应用，点击外层「保存」写入数据库')
    },
    /* ===== 抽屉内：主机 ===== */
    unitHosts(s) {
      const kw = (s.hostFilter || '').toLowerCase()
      const tags = s.tagFilter || []
      let list = this.hostOptions
      if (kw) list = list.filter(h => (h.name||'').toLowerCase().includes(kw) || (h.ip||'').includes(kw))
      if (tags.length) list = list.filter(h => tags.includes(h.tag || 'other'))
      return list
    },
    unitAllSelected(s) {
      const list = this.unitHosts(s)
      return list.length > 0 && list.every(h => (s.hostIds||[]).includes(h.id))
    },
    toggleUnitHost(s, id) {
      if (!s.hostIds) s.hostIds = []
      const i = s.hostIds.indexOf(id)
      i >= 0 ? s.hostIds.splice(i, 1) : s.hostIds.push(id)
    },
    toggleAllUnitHosts(s) {
      const list = this.unitHosts(s)
      if (this.unitAllSelected(s)) s.hostIds = (s.hostIds||[]).filter(id => !list.some(h => h.id === id))
      else list.forEach(h => { if (!(s.hostIds||[]).includes(h.id)) s.hostIds.push(h.id) })
    },
    /* ===== 抽屉内：变量 ===== */
    hostVarValue(s, hid, v) {
      const hv = (s.hostVars || {})[String(hid)] || {}
      return hv[v.name] !== undefined ? hv[v.name] : ''
    },
    setHostVar(s, hid, v, val) {
      const key = String(hid)
      if (!s.hostVars) s.hostVars = {}
      if (!s.hostVars[key]) s.hostVars[key] = {}
      if (val === '' || val == null) delete s.hostVars[key][v.name]
      else s.hostVars[key][v.name] = val
    },
    hostVarCount(s, hid) { return Object.keys((s.hostVars || {})[String(hid)] || {}).length },
    hostVarTotal(s) { return Object.values(s.hostVars || {}).filter(m => Object.keys(m).length).length },
    clearHostVar(s, hid) { if (s.hostVars) delete s.hostVars[String(hid)] },
    clearHostVars(s) { s.hostVars = {} },
    copyFirstHostVars(s) {
      const ids = s.hostIds || []
      if (ids.length < 2) return
      const src = (s.hostVars || {})[String(ids[0])]
      if (!src || !Object.keys(src).length) { ElMessage.warning('首台主机没有覆盖项'); return }
      ids.slice(1).forEach(hid => { s.hostVars[String(hid)] = { ...src } })
    },
    /* ===== 保存 / 删除 ===== */
    async saveOrch() { const ok = await this.doSave(); if (ok) { this.editorVisible = false } },
    async doSave() {
      const payload = {
        name: this.form.name, description: this.form.description, enabled: true,
        steps: this.form.steps.map(s => ({
          template_id: s.template_id,
          host_ids: s.hostIds || [],
          host_vars: s.hostVars || {},
          continue_on_error: !!s.continue_on_error,
          retry_count: +s.retry_count || 0,
          retry_interval_sec: +s.retry_interval_sec || 30 }))
      }
      try {
        const r = this.form.id ? await api.put('/orchestrations/' + this.form.id, payload) : await api.post('/orchestrations', payload)
        if (r.code === 0) {
          if (!this.form.id && r.data?.id) this.form.id = r.data.id // 新建后回写，避免再次保存撞重名
          ElMessage.success('已保存'); this.loadList(); return true
        }
        ElMessage.error(r.message || '保存失败')
        return false
      } catch(e) { return false }
    },
    removeOrch(row) {
      ElMessageBox.confirm('确认删除任务「' + row.name + '」？将同时删除其运行记录与执行明细。', '提示', { type: 'warning' })
        .then(async () => { const r = await api.delete('/orchestrations/' + row.id); if (r.code === 0) { ElMessage.success('已删除'); this.loadList() } })
        .catch(() => {})
    },
    /* ===== 运行 ===== */
    startRun(row) {
      ElMessageBox.confirm(
        '按顺序执行任务「' + row.name + '」共 ' + row.step_count + ' 个步骤？任务记录仅执行一次，结束后不可重跑。',
        '确认执行', { type: 'warning', confirmButtonText: '开始执行', cancelButtonText: '取消' })
      .then(async () => {
        try {
          const r = await api.post('/orchestrations/' + row.id + '/run', {})
          if (r.code === 0) {
            ElMessage.success('任务已开始执行')
            const runId = r.data?.run_id
            if (runId) this.openRunDrawer(runId)
          } else {
            ElMessage.error(r.message || '执行失败')
          }
        } catch(e) {}
      }).catch(() => {})
    },
    stateLabel(s) { return { running:'运行中', not_started:'未开始', finished:'已结束' }[s] || '未开始' },
    stateClass(s) { return { running:'running', not_started:'unverified', finished:'completed' }[s] || 'unverified' },
    resultLabel(s) { return { success:'成功', partial:'部分成功', failed:'失败' }[s] || s },
    resultClass(s) { return { success:'online', partial:'warning', failed:'offline' }[s] || 'unverified' },
    rowClick(row) {
      if (row.state === 'running' || row.state === 'finished') this.openRunDrawer(row.last_run_id)
    },
    formatTime(t) {
      if (!t) return '-'
      const d = new Date(t.replace(' ', 'T') + (t.includes('Z') ? '' : 'Z'))
      return d.getFullYear() + '-' + String(d.getMonth()+1).padStart(2,'0') + '-' + String(d.getDate()).padStart(2,'0') + ' ' + String(d.getHours()).padStart(2,'0') + ':' + String(d.getMinutes()).padStart(2,'0')
    }
  }
}
