// 索引模板 tab · 「可视化创建」对话框。
// 版式与「生命周期 · 可视化创建 · 冷热温」同构（复用 .es-qc-* 类族）：左表单 + 竖分隔 + 右实时 JSON 预览。
// 只负责收集与展示，请求体拼装/校验/重叠判定全在 window.EsTplBuild（纯函数，可被冒烟测试直接调用）。
// 硬约束（docs/ES控制台设计.md §3.3）：提交前二次确认并完整回显将发出的请求（本组件可能一次发两条：
// 组件模板先于索引模板），命名白名单与内置只读由后端兜底。
window.EsTemplateQuickDialog = {
  props: ['conn'],
  template: `
<el-dialog v-model="visible" class="es-tqc-dialog" title="可视化创建 · 索引模板" width="980px" append-to-body @closed="onClosed">
  <div class="es-lc-toolbar" style="margin-bottom:12px; align-items:center">
    <span class="es-qc-hint" style="margin:0">预设</span>
    <el-radio-group :model-value="activePreset" size="small" @update:model-value="applyPreset">
      <el-radio-button v-for="p in presets" :key="p.key" :value="p.key">{{p.label}}</el-radio-button>
    </el-radio-group>
    <span class="es-qc-hint" style="margin:0">{{presetHint}}</span>
  </div>

  <div class="es-qc-body">
    <div class="es-qc-left">
      <el-form label-position="top" size="small">

        <el-form-item label="模板名称" required>
          <el-input v-model="f.name" placeholder="例如：xafq_credit_api_access_log_tpl" />
          <div class="es-qc-hint">不得含 * , ?，不得以 _ 或 . 开头（与后端 validateResourceName 同口径）</div>
        </el-form-item>

        <el-form-item label="index_patterns" required>
          <div class="es-tc-chips">
            <el-tag v-for="(p, i) in f.patterns" :key="p + i" closable size="small" @close="removePattern(i)">{{p}}</el-tag>
            <span v-if="!f.patterns.length" class="es-qc-hint" style="margin:0">尚未添加</span>
          </div>
          <div class="es-tc-add">
            <el-input v-model="patternInput" placeholder="xafq_credit_api_access_log*" @keyup.enter="addPattern" />
            <el-button @click="addPattern">添加</el-button>
          </div>
          <div v-if="probing" class="es-qc-hint">探测中…</div>
          <div v-else-if="probeSummary" class="es-qc-hint" style="color:var(--ok,#16A34A)">{{probeSummary}}</div>
          <div v-if="probeErr" class="es-qc-hint" style="color:var(--warn,#D97706)">{{probeErr}}</div>
        </el-form-item>

        <el-form-item label="优先级（priority）">
          <el-input-number v-model="f.priority" :min="0" :max="9999" style="width:100%" />
          <div class="es-qc-hint">通配兜底 20、具体业务 21；两个模板都匹配同一索引时，优先级高者生效</div>
        </el-form-item>

        <el-form-item label="关联生命周期">
          <el-switch v-model="f.lifecycleOn" active-text="关联 ILM 策略" inactive-text="不关联" />
          <div v-if="f.lifecycleOn" style="width:100%; margin-top:8px">
            <el-select v-model="f.policyName" placeholder="选择已存在的策略" style="width:100%" v-loading="loadingPolicies">
              <el-option v-for="p in policies" :key="p.name" :label="p.name" :value="p.name" />
            </el-select>
            <div class="es-tc-chain">
              <template v-for="(ph, i) in phaseChain" :key="ph.name">
                <span v-if="i" style="font-size:12px; color:var(--text-sub,#9CA3AF)">→</span>
                <span class="es-tc-node" :style="phaseStyle(ph.name)">{{ph.label}}</span>
              </template>
              <span v-if="!phaseChain.length" class="es-qc-hint" style="margin:0">请选择策略以查看阶段</span>
            </div>
            <div v-if="!policies.length && !loadingPolicies" class="es-qc-hint">该集群暂无业务 ILM 策略，可先去「生命周期」tab 用可视化创建生成一个</div>
            <div v-if="policyChainHint" class="es-qc-hint" :style="policyChainHintColor">{{policyChainHint}}</div>
            <div v-if="missingTierHint" class="es-qc-hint" style="color:var(--warn,#D97706)">{{missingTierHint}}</div>
          </div>
          <div v-else class="es-qc-hint">不关联则索引不受 ILM 管理，不会自动迁移或清理</div>
        </el-form-item>

        <el-form-item label="索引结构">
          <div class="es-tc-grid3">
            <div><div class="es-qc-hint" style="margin:0 0 4px">主分片</div><el-input-number v-model="f.shards" :min="1" :max="1024" controls-position="right" style="width:100%" /></div>
            <div><div class="es-qc-hint" style="margin:0 0 4px">副本</div><el-input-number v-model="f.replicas" :min="0" :max="10" controls-position="right" style="width:100%" /></div>
            <div><div class="es-qc-hint" style="margin:0 0 4px">刷新间隔</div><el-input v-model="f.refreshInterval" /></div>
          </div>
          <div class="es-tc-grid2" style="margin-top:8px">
            <div><div class="es-qc-hint" style="margin:0 0 4px">translog 持久化</div>
              <el-select v-model="f.translogDurability" style="width:100%">
                <el-option label="异步 async（写入快，允许丢最近一批）" value="async" />
                <el-option label="逐请求 request（不丢，写入慢）" value="request" />
              </el-select>
            </div>
            <div><div class="es-qc-hint" style="margin:0 0 4px">translog flush 阈值</div><el-input v-model="f.translogFlush" /></div>
          </div>
          <div v-if="f.replicas === 0" class="es-qc-hint" style="color:var(--warn,#D97706)">副本 0：节点故障时该索引不可读也不可写（日志场景常见取舍，但要有心理准备）</div>
        </el-form-item>

        <el-form-item label="初始分层（_tier_preference）">
          <el-select v-model="f.tier" style="width:100%">
            <el-option v-for="t in tierOptions" :key="t.value" :value="t.value" :disabled="t.disabled"
              :label="t.value + (t.count ? ' · ' + t.count + ' 个节点' : (t.disabled ? ' · 无此类节点' : ''))" />
          </el-select>
          <div class="es-qc-hint">只声明索引<b>新建时</b>落在哪一层；后续迁往 warm / cold 由 ILM 自动改写这一项</div>
          <div v-if="!tierDetected" class="es-qc-hint">未从节点角色识别出分层（集群可能未按冷热温划分），已放开全部选项</div>
        </el-form-item>

        <el-form-item label="时间字段">
          <div class="es-tc-grid2">
            <div>
              <el-select v-model="f.timeField" filterable allow-create default-first-option style="width:100%" placeholder="选择或直接输入">
                <el-option v-for="c in timeCandidates" :key="c.name" :label="c.name + (c.format ? '  (' + c.format + ')' : '')" :value="c.name" />
              </el-select>
            </div>
            <div><el-input v-model="f.timeFormat" placeholder="epoch_millis" /></div>
          </div>
          <div class="es-qc-hint">候选与格式探测自命中索引的 mapping；同名格式不一致时会在此列出（如 epoch_millis / strict_date_optional_time）</div>
          <div v-if="timeConflictHint" class="es-qc-hint" style="color:var(--warn,#D97706)">{{timeConflictHint}}</div>
        </el-form-item>

        <el-form-item label="公共层（组件模板）">
          <el-switch v-model="f.useBase" active-text="抽取公共层" inactive-text="不抽取" />
          <div class="es-qc-hint">把「生命周期 / 初始分层 / 刷新间隔 / translog」放进一个组件模板供多个索引模板共用；改一处、处处生效。分片数、副本数、mappings 留在本模板里</div>
          <div v-if="f.useBase" style="width:100%; margin-top:8px">
            <el-radio-group v-model="f.baseMode" size="small">
              <el-radio value="new" label="new">新建 / 覆盖组件模板</el-radio>
              <el-radio value="existing" label="existing">复用已有组件模板</el-radio>
            </el-radio-group>
            <div style="margin-top:8px">
              <el-select v-if="f.baseMode === 'existing'" v-model="f.baseName" style="width:100%" placeholder="选择已有组件模板" v-loading="loadingBases">
                <el-option v-for="b in baseList" :key="b.name" :label="b.name" :value="b.name" />
              </el-select>
              <el-input v-else v-model="f.baseName" placeholder="xafq-logs-base" />
            </div>
            <div v-if="f.baseMode === 'new'" class="es-qc-hint">保存时会<b>依次发出两条请求</b>：先 PUT 组件模板，再 PUT 索引模板（后者 composed_of 引用前者）</div>
            <div v-if="baseExists" class="es-qc-hint" style="color:var(--warn,#D97706)">组件模板 {{f.baseName}} 已存在，本次为覆盖</div>
            <div v-if="baseRefNames.length" class="es-qc-hint" style="color:var(--warn,#D97706)">已被 {{baseRefNames.length}} 个索引模板引用：{{baseRefNames.join('、')}} —— 覆盖公共层会同时改变它们的行为</div>
          </div>
          <div v-else class="es-qc-hint">公共项会直接写进本模板，单独维护；只有一个索引模板时这样更少一层间接</div>
        </el-form-item>

      </el-form>
    </div>

    <div class="es-qc-divider"></div>

    <div class="es-qc-right">
      <div class="es-area-label">JSON 预览（实时） · 将发出 {{requests.length}} 条请求</div>
      <pre class="es-source es-qc-preview">{{preview}}</pre>
      <div class="es-qc-hint">预览即提交内容；需要手写 JSON（aliases / analyzer / dynamic_templates）请走工具栏的「JSON 直接提交」</div>
      <div v-if="overlapList.length" style="margin-top:10px">
        <div class="es-qc-hint" style="color:var(--warn,#D97706); margin:0">与本模板 pattern 重叠的已有模板</div>
        <div v-for="o in overlapList" :key="o.name" class="es-qc-hint" style="color:var(--warn,#D97706); margin:4px 0 0">
          {{o.name}}（{{o.patterns.join('、')}} · priority {{o.priority}}）→ 重叠处<b>{{o.wins ? '本模板' : '对方'}}</b>生效
        </div>
      </div>
      <div v-else-if="f.patterns.length" class="es-qc-hint" style="margin-top:10px">与现有 {{existingTemplates.length}} 个索引模板均不重叠</div>
    </div>
  </div>

  <el-drawer v-model="simDrawer" title="保存前模拟（_simulate）" size="55%" append-to-body class="es-raw-drawer">
    <div class="es-mapping-hint">给定假想索引名的合并结果（settings / mappings / aliases）</div>
    <div class="es-qc-hint">{{simNote}}</div>
    <pre class="es-source" v-loading="simulating">{{simText}}</pre>
  </el-drawer>

  <template #footer>
    <el-button @click="visible=false">取消</el-button>
    <el-button @click="doSimulate" :loading="simulating">保存前模拟</el-button>
    <el-button type="primary" @click="save" :loading="saving">确认创建</el-button>
  </template>
</el-dialog>`,
  data() {
    return {
      visible: false, f: EsTplBuild.withPreset('detail'), activePreset: 'detail', patternInput: '',
      presets: EsTplBuild.PRESETS.slice(),
      policies: [], loadingPolicies: false,
      nodes: [],
      baseList: [], loadingBases: false,
      existingTemplates: [],
      probing: false, probe: null, probeErr: '', probeTimer: null,
      saving: false, simDrawer: false, simText: '', simulating: false, simNote: ''
    }
  },
  computed: {
    requests() { return EsTplBuild.buildRequests(this.f) },
    preview() { return EsTplBuild.previewText(this.f) },
    presetHint() { const p = this.presets.filter(x => x.key === this.activePreset)[0]; return p ? p.hint : '' },
    policyObj() { const p = this.policies.filter(x => x.name === this.f.policyName)[0]; return p ? p.policy : null },
    phaseChain() { return EsTplBuild.phaseChain(this.policyObj) },
    policyChainHint() {
      if (!this.policyObj) return ''
      return EsTplBuild.autoDeletes(this.policyObj)
        ? '' : '该策略没有 delete 阶段：数据长期保留，不会自动清理'
    },
    policyChainHintColor() { return EsTplBuild.autoDeletes(this.policyObj) ? '' : 'color:var(--warn,#D97706)' },
    // 各层可用节点数。节点角色在 _nodes 里可能是 data_hot 这种带层的，也可能是老的 hot/warm/cold，
    // 两种写法都归一化（见 es.js 的 roleShort 映射）。
    tierCounts() {
      const of = { data_hot: 'data_hot', hot: 'data_hot', data_warm: 'data_warm', warm: 'data_warm',
        data_cold: 'data_cold', cold: 'data_cold', data_frozen: 'data_frozen',
        frozen: 'data_frozen', data_content: 'data_content' }
      const c = {}
      this.nodes.forEach(n => (n.roles || []).forEach(r => {
        const t = of[r]
        if (t) c[t] = (c[t] || 0) + 1
      }))
      return c
    },
    // 一个层都没识别出来（集群未按冷热温划分）时不做过滤，放开全部选项
    tierDetected() { return Object.keys(this.tierCounts).length > 0 },
    tierOptions() {
      const all = ['data_hot', 'data_warm', 'data_cold', 'data_frozen', 'data_content']
      return all.map(v => ({ value: v, count: this.tierCounts[v] || 0,
        disabled: this.tierDetected && !this.tierCounts[v] }))
    },
    // 策略要迁往某层、但集群根本没有这类节点时，索引会静默停在 hot 层等待（不报错），必须提示
    missingTierHint() {
      if (!this.policyObj || !this.tierDetected) return ''
      const miss = EsTplBuild.phaseTiers(this.policyObj).filter(t => !this.tierCounts[t])
      return miss.length ? '该策略会迁往 ' + miss.join(' / ') + '，但集群没有对应的层节点：索引会停在当前层等待，不会报错' : ''
    },
    timeCandidates() {
      const p = this.probe
      if (!p) return []
      const dates = (p.fields || []).filter(x => (x.types || []).indexOf('date') >= 0)
      const names = (p.time_fields || []).slice()
      dates.forEach(d => { if (names.indexOf(d.path) < 0) names.push(d.path) })
      return names.map(n => {
        const hit = dates.filter(d => d.path === n)[0]
        return { name: n, format: (hit && hit.format) || '' }
      })
    },
    timeConflictHint() {
      const c = this.timeCandidates.filter(x => x.name === this.f.timeField)[0]
      return (c && c.format && this.f.timeFormat && c.format !== this.f.timeFormat)
        ? '已有索引里该字段的 format 是 ' + c.format + '，与你填的不一致；不一致时新索引的时间字段可能按另一种格式解析' : ''
    },
    probeSummary() {
      const p = this.probe
      if (!p) return ''
      const n = (p.indices || []).length
      const docs = Number(p.docs_count || 0)
      const ds = (p.data_streams || []).length
      return '探测命中 ' + n + ' 个索引' + (ds ? ' · ' + ds + ' 个数据流' : '') + ' · ' + docs.toLocaleString() + ' 条文档' + (p.truncated ? '（已截断）' : '')
    },
    overlapList() { return EsTplBuild.overlaps(this.f, this.existingTemplates) },
    baseRefNames() { return EsTplBuild.baseRefs(this.f.baseName, this.existingTemplates) },
    baseExists() { return this.baseList.some(b => b.name === this.f.baseName) }
  },
  watch: {
    'f.patterns': { deep: true, handler() { this.scheduleProbe() } },
    'f.timeField'(v) {
      const c = this.timeCandidates.filter(x => x.name === v)[0]
      if (c && c.format) this.f.timeFormat = c.format
    }
  },
  beforeUnmount() { if (this.probeTimer) clearTimeout(this.probeTimer) },
  methods: {
    open() {
      this.f = EsTplBuild.withPreset('detail')
      this.activePreset = 'detail'
      this.patternInput = ''; this.probe = null; this.probeErr = ''
      this.simText = ''; this.simDrawer = false
      this.visible = true
      this.loadPolicies(); this.loadNodes(); this.loadBaseList(); this.loadExistingTemplates()
    },
    onClosed() { if (this.probeTimer) clearTimeout(this.probeTimer) },
    applyPreset(k) {
      const keep = { name: this.f.name, patterns: this.f.patterns.slice(), timeField: this.f.timeField,
        timeFormat: this.f.timeFormat, policyName: this.f.policyName, lifecycleOn: this.f.lifecycleOn }
      this.activePreset = k
      this.f = Object.assign(EsTplBuild.withPreset(k), keep)
    },
    addPattern() {
      const v = (this.patternInput || '').trim()
      if (!v) return
      if (this.f.patterns.indexOf(v) >= 0) { this.patternInput = ''; return }
      this.f.patterns.push(v); this.patternInput = ''
    },
    removePattern(i) { this.f.patterns.splice(i, 1) },
    phaseStyle(name) {
      const m = { hot: '#FCEBEB/#A32D2D', warm: '#FAEEDA/#854F0B', cold: '#E6F1FB/#185FA5',
        frozen: '#EEEDFE/#534AB7', delete: '#F1EFE8/#444441' }
      const pair = (m[name] || '#F1EFE8/#444441').split('/')
      return 'background:' + pair[0] + ';color:' + pair[1]
    },
    scheduleProbe() {
      if (this.probeTimer) clearTimeout(this.probeTimer)
      if (!this.f.patterns.length) { this.probe = null; this.probeErr = ''; return }
      this.probeTimer = setTimeout(() => { this.doProbe() }, 400)
    },
    async doProbe() {
      const pattern = this.f.patterns.join(',')
      this.probing = true; this.probeErr = ''
      try {
        const r = await api.post('/es/' + this.conn.id + '/index-pattern/probe', { index_pattern: pattern })
        if (r.code === 0) { this.probe = r.data } else { this.probe = null }
      } catch (e) { this.probeErr = '探测失败：' + (e.message || e) } finally { this.probing = false }
    },
    async loadPolicies() {
      this.loadingPolicies = true
      try {
        const r = await api.get('/es/' + this.conn.id + '/ilm/policies', { params: { system: '0' } })
        if (r.code === 0) {
          this.policies = (r.data && r.data.list) || []
          // 只有一个业务策略时直接选中：预置的意义就是「点一下就能出」，让用户再挑一次仓库里唯一的策略没有价值。
          // 有多个时不猜——选错策略是会影响数据保留期的决定，交给用户。
          if (this.f.lifecycleOn && !this.f.policyName && this.policies.length === 1) {
            this.f.policyName = this.policies[0].name
          }
        }
      } catch (e) { /* */ } finally { this.loadingPolicies = false }
    },
    async loadNodes() {
      try {
        const r = await api.get('/es/' + this.conn.id + '/nodes')
        if (r.code === 0) this.nodes = (r.data && r.data.list) || []
      } catch (e) { /* */ }
    },
    async loadBaseList() {
      this.loadingBases = true
      try {
        const r = await api.get('/es/' + this.conn.id + '/component-templates')
        if (r.code === 0) {
          this.baseList = ((r.data && r.data.component_templates) || []).map(x => ({ name: x.name, ...(x.component_template || {}) }))
        }
      } catch (e) { /* */ } finally { this.loadingBases = false }
    },
    async loadExistingTemplates() {
      try {
        const r = await api.get('/es/' + this.conn.id + '/index-templates')
        if (r.code === 0) {
          this.existingTemplates = ((r.data && r.data.index_templates) || []).map(x => ({ name: x.name, ...(x.index_template || {}) }))
        }
      } catch (e) { /* */ }
    },
    async doSimulate() {
      const errs = EsTplBuild.validate(this.f, this.existingTemplates)
      if (errs.length) { this.$message.error(errs[0]); return }
      let name
      try {
        const res = await this.$prompt('输入假想索引名（_simulate 不接受通配符）', '保存前模拟', {
          inputValue: EsTplBuild.mockIndexName(this.f)
        })
        name = res.value
      } catch (e) { return }
      const body = EsTplBuild.simulateBody(this.f)
      this.simulating = true; this.simDrawer = true; this.simText = ''
      this.simNote = (EsTplBuild.usingBase(this.f) && this.f.baseMode !== 'existing')
        ? '口径：组件模板尚未创建，composed_of 在 ES 侧解析不到，故把公共层内联展开后模拟——下面看到的是「保存后」的合并结果。'
        : '口径：按当前 composed_of 原样交给 ES 解析。'
      try {
        const r = await api.post('/es/' + this.conn.id + '/index-templates/_simulate', { template: body, index_name: name })
        if (r.code === 0) this.simText = JSON.stringify(r.data.template || r.data, null, 2)
      } catch (e) { /* */ } finally { this.simulating = false }
    },
    async save() {
      const errs = EsTplBuild.validate(this.f, this.existingTemplates)
      if (errs.length) { this.$message.error(errs[0]); return }
      const reqs = EsTplBuild.buildRequests(this.f)
      const overwrite = this.existingTemplates.some(t => t.name === this.f.name.trim())
      const head = (overwrite ? '模板 ' + this.f.name.trim() + ' 已存在，本次为覆盖。\n' : '')
        + (reqs.length > 1 ? '本操作会依次发出 ' + reqs.length + ' 条请求（组件模板先于索引模板）：' : '即将向 ES 发出请求：')
      try {
        await this.$confirm(EsTplBuild.echoHTML(head, reqs), '确认创建', {
          type: 'warning', dangerouslyUseHTMLString: true, customStyle: { maxWidth: '760px' }
        })
      } catch (e) { return }
      this.saving = true
      try {
        for (const r of reqs) {
          const isBase = r.path.indexOf('/_component_template/') === 0
          const url = '/es/' + this.conn.id + (isBase ? '/component-templates/' : '/index-templates/')
            + encodeURIComponent(r.path.split('/').pop())
          const res = await api.put(url, r.body)
          if (res.code !== 0) { this.$message.error(r.label + '保存失败'); return }
        }
        this.$message.success('已创建：' + this.f.name.trim())
        this.visible = false
        this.$emit('saved')
      } catch (e) { /* */ } finally { this.saving = false }
    }
  }
}
