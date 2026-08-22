window.HostsPage = {
  props: ['page', 'user', 'versionData'],
  template: `
<div class="hosts-page">
  <section class="hosts-intro">
    <div class="hosts-intro-copy"><span class="hosts-eyebrow">HOST INVENTORY</span><h1>主机管理</h1><p>集中管理主机接入、状态与资源巡检</p></div>
    <div class="hosts-intro-stats"><div class="hosts-mini-stat"><span>当前页</span><strong>{{list.length}}</strong></div><div class="hosts-mini-stat"><span>主机总数</span><strong>{{total}}</strong></div></div>
  </section>
  <div class="page-card hosts-panel">
    <div class="card-header hosts-toolbar">
      <div class="filter-bar hosts-filters">
        <el-select v-model="filter.status" placeholder="状态" clearable style="width:115px"><el-option label="在线" value="online" /><el-option label="离线" value="offline" /><el-option label="未验证" value="unverified" /></el-select>
        <el-input v-model="filter.name" placeholder="主机名称，模糊搜索" clearable style="width:170px" />
        <el-input v-model="filter.ip" placeholder="IP 地址，模糊搜索" clearable style="width:170px" />
        <el-input v-model="filter.tag" placeholder="标签，模糊搜索" clearable style="width:150px" />
      </div>
      <div class="header-extra hosts-actions"><el-radio-group v-model="viewMode" size="small" class="hosts-view-switch"><el-radio-button label="table">表格</el-radio-button><el-radio-button label="card">卡片</el-radio-button></el-radio-group><el-button plain @click="openBatch">批量新增</el-button><el-button type="primary" @click="openForm(null)">新增主机</el-button></div>
    </div>
    <el-table v-if="viewMode==='table'" class="hosts-table" :data="list" style="width:100%" v-loading="loading">
      <el-table-column label="主机" min-width="180">
        <template #default="{row}"><div><span style="font-weight:500">{{row.name}}</span><div class="mono" style="font-size:12px;color:#9CA3AF">{{row.ip}}:{{row.port}}</div></div></template>
      </el-table-column>
      <el-table-column label="标签" width="120">
        <template #default="{row}"><span class="tag-badge" :class="tagClass(row.tag)">{{tagLabel(row.tag)}}</span></template>
      </el-table-column>
      <el-table-column label="配置" width="160">
        <template #default="{row}"><span class="mono" style="font-size:12.5px;color:#6B7280">{{specText(row)}}</span></template>
      </el-table-column>
      <el-table-column label="状态" width="100">
        <template #default="{row}"><span class="status-badge" :class="row.status"><span class="dot"></span>{{statusText(row.status)}}</span></template>
      </el-table-column>
      <el-table-column label="负载" width="140">
        <template #default="{row}"><div class="metric" v-if="info(row)"><div class="metric-bar"><div class="fill" :class="loadCls(row)" :style="{width:loadPct(row)+'%'}"></div></div><span class="metric-value">{{loadPct(row)}}%</span></div><span v-else style="color:#9CA3AF">-</span></template>
      </el-table-column>
      <el-table-column label="内存" width="140">
        <template #default="{row}"><div class="metric" v-if="info(row)"><div class="metric-bar"><div class="fill" :class="memCls(row)" :style="{width:memPct(row)+'%'}"></div></div><span class="metric-value">{{memPct(row)}}%</span></div><span v-else style="color:#9CA3AF">-</span></template>
      </el-table-column>
      <el-table-column label="磁盘" width="140">
        <template #default="{row}"><div class="metric" v-if="info(row)"><div class="metric-bar"><div class="fill" :class="diskCls(row)" :style="{width:diskPct(row)+'%'}"></div></div><span class="metric-value">{{diskPct(row)}}%</span></div><span v-else style="color:#9CA3AF">-</span></template>
      </el-table-column>
      <el-table-column label="" width="200" fixed="right">
        <template #default="{row}"><div class="ops-cell"><el-button size="small" text @click="openDetail(row)">详情</el-button><el-button size="small" text type="primary" :loading="row._testing" @click="testConn(row)">测试</el-button><el-button size="small" text @click="openForm(row)">编辑</el-button><el-button size="small" text type="danger" @click="delHost(row)">删除</el-button></div></template>
      </el-table-column>
    </el-table>
    <div v-else class="hosts-card-grid" v-loading="loading">
      <article v-for="row in list" :key="row.id" class="host-card">
        <div class="host-card-top"><div class="host-card-title"><span class="host-card-status" :class="row.status"></span><div><h3>{{row.name}}</h3><div class="mono host-card-endpoint">{{row.ip}}:{{row.port}}</div></div></div><el-dropdown @command="command => handleCardCommand(command, row)"><el-button text class="host-card-more">...</el-button><template #dropdown><el-dropdown-menu><el-dropdown-item command="detail">查看详情</el-dropdown-item><el-dropdown-item command="test">测试连接</el-dropdown-item><el-dropdown-item command="edit">编辑主机</el-dropdown-item><el-dropdown-item command="delete" divided>删除主机</el-dropdown-item></el-dropdown-menu></template></el-dropdown></div>
        <div class="host-card-meta"><span class="tag-badge" :class="tagClass(row.tag)">{{tagLabel(row.tag)}}</span><span class="status-badge" :class="row.status"><span class="dot"></span>{{statusText(row.status)}}</span><span class="mono host-card-spec">{{specText(row)}}</span></div>
        <div class="host-card-metrics"><div class="host-card-metric"><span>负载</span><div class="metric"><div class="metric-bar"><div class="fill" :class="loadCls(row)" :style="{width:loadPct(row)+'%'}"></div></div><span class="metric-value">{{info(row)?loadPct(row)+'%':'-'}}</span></div></div><div class="host-card-metric"><span>内存</span><div class="metric"><div class="metric-bar"><div class="fill" :class="memCls(row)" :style="{width:memPct(row)+'%'}"></div></div><span class="metric-value">{{info(row)?memPct(row)+'%':'-'}}</span></div></div><div class="host-card-metric"><span>磁盘</span><div class="metric"><div class="metric-bar"><div class="fill" :class="diskCls(row)" :style="{width:diskPct(row)+'%'}"></div></div><span class="metric-value">{{info(row)?diskPct(row)+'%':'-'}}</span></div></div></div>
        <div class="host-card-actions"><el-button size="small" text @click="openDetail(row)">详情</el-button><el-button size="small" text type="primary" :loading="row._testing" @click="testConn(row)">测试连接</el-button><el-button size="small" text @click="openForm(row)">编辑</el-button></div>
      </article>
      <empty-state v-if="!loading && !list.length" text="暂无主机" />
    </div>
    <div class="hosts-pagination"><el-pagination v-model:current-page="pg" :page-size="20" :total="total" layout="total, prev, pager, next" @current-change="connectSSE" /></div>
  </div>
  <el-drawer v-model="drawer" :title="detail?.name" size="480px">
      <div class="detail-section"><h4>基本信息</h4><div class="detail-grid"><div class="detail-item"><div class="label">IP</div><div class="value mono">{{detail.ip}}</div></div><div class="detail-item"><div class="label">端口</div><div class="value mono">{{detail.port}}</div></div><div class="detail-item"><div class="label">标签</div><div class="value">{{detail.tag || '其他'}}</div></div><div class="detail-item"><div class="label">状态</div><div class="value"><span class="status-badge" :class="detail.status"><span class="dot"></span>{{statusText(detail.status)}}</span></div></div></div></div>
      <div class="detail-section" v-if="info(detail)"><h4>系统信息</h4><div class="detail-grid"><div class="detail-item"><div class="label">主机名</div><div class="value mono">{{info(detail).hostname||'-'}}</div></div><div class="detail-item"><div class="label">操作系统</div><div class="value">{{info(detail).os||'-'}}</div></div><div class="detail-item"><div class="label">内核</div><div class="value mono">{{info(detail).kernel||'-'}}</div></div><div class="detail-item"><div class="label">运行时长</div><div class="value mono">{{info(detail).uptime||'-'}}</div></div></div></div>
      <div class="detail-section" v-if="info(detail)"><h4>资源使用</h4><div class="detail-grid"><div class="detail-item"><div class="label">CPU</div><div class="value mono">{{info(detail).cpu_cores||'-'}} 核</div></div><div class="detail-item"><div class="label">内存</div><div class="metric"><div class="metric-bar" style="max-width:100%"><div class="fill" :class="memCls(detail)" :style="{width:memPct(detail)+'%'}"></div></div><span class="metric-value">{{memPct(detail)}}%</span></div></div><div class="detail-item" style="grid-column:span 2"><div class="label">磁盘</div><div class="metric"><div class="metric-bar" style="max-width:100%"><div class="fill" :class="diskCls(detail)" :style="{width:diskPct(detail)+'%'}"></div></div><span class="metric-value">{{diskPct(detail)}}%</span></div></div></div></div>
      <div class="detail-section" v-if="info(detail)"><h4>资源使用</h4><div class="detail-grid"><div class="detail-item"><div class="label">CPU</div><div class="value mono">{{info(detail).cpu_cores||'-'}} 核</div></div><div class="detail-item"><div class="label">内存</div><div class="metric"><div class="metric-bar" style="max-width:100%"><div class="fill" :class="memCls(detail)" :style="{width:memPct(detail)+'%'}"></div></div><span class="metric-value">{{memPct(detail)}}%</span></div></div><div class="detail-item" style="grid-column:span 2"><div class="label">磁盘</div><div class="metric"><div class="metric-bar" style="max-width:100%"><div class="fill" :class="diskCls(detail)" :style="{width:diskPct(detail)+'%'}"></div></div><span class="metric-value">{{diskPct(detail)}}%</span></div></div></div></div>
    </div>
  </el-drawer>
  <el-dialog v-model="dialog" :title="form.id?'编辑主机':'新增主机'" width="640px">
    <el-form :model="form" label-position="top">
      <div class="form-grid">
        <el-form-item label="主机名称" required><el-input v-model="form.name" /></el-form-item>
        <el-form-item label="IP" required><el-input v-model="form.ip" /></el-form-item>
        <el-form-item label="端口"><el-input-number v-model="form.port" :min="1" :max="65535" style="width:100%" /></el-form-item>
        <el-form-item label="标签"><el-input v-model="form.tag" placeholder="请输入标签，支持中英文和数字" clearable /></el-form-item>
        <el-form-item label="凭据">
          <el-select v-model="form.credential_id" clearable placeholder="请选择凭据" style="width:100%" :loading="credLoading" @visible-change="onCredDropdown">
            <el-option v-for="c in creds" :key="c.id" :label="c.name" :value="c.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="备注" class="span-2"><el-input v-model="form.remark" type="textarea" :rows="2" /></el-form-item>
      </div>
    </el-form>
    <template #footer><el-button @click="dialog=false">取消</el-button><el-button type="primary" :loading="saving" @click="save">保存</el-button></template>
  </el-dialog>
  <el-dialog v-model="batchDialog" title="批量新增主机" width="640px">
    <el-form :model="batchForm" label-position="top" :disabled="batchSubmitting">
      <el-form-item label="凭据" required><el-select v-model="batchForm.credential_id" placeholder="请选择凭据" style="width:100%" :loading="credLoading" @visible-change="onCredDropdown"><el-option v-for="c in creds" :key="c.id" :label="c.name" :value="c.id" /></el-select></el-form-item>
      <el-form-item label="IP 列表" required>
        <el-input v-model="batchForm.ips" type="textarea" :rows="6" placeholder="172.16.1.11-20&#10;172.16.2.5" @input="onIpsInput" />
        <div class="batch-preview" :class="{err:preview.err}">{{preview.err || ('共 '+preview.count+' 个 IP'+(preview.dup?('，其中 '+preview.dup+' 个已存在'):''))}}</div>
      </el-form-item>
      <div class="form-grid">
        <el-form-item label="标签"><el-input v-model="batchForm.tag" placeholder="请输入标签，支持中英文和数字" clearable /></el-form-item>
        <el-form-item label="端口"><el-input-number v-model="batchForm.port" :min="1" :max="65535" style="width:100%" /></el-form-item>
      </div>
      <el-form-item label="备注"><el-input v-model="batchForm.remark" /></el-form-item>
       <el-form-item><el-switch v-model="batchForm.auto_test" /><span style="margin-left:8px;font-size:12px;color:var(--text-sub)">创建后自动测试连接</span></el-form-item>
    </el-form>
      <template #footer><el-button @click="batchDialog=false">取消</el-button><el-button type="primary" :loading="batchSubmitting" @click="batchSubmit">{{batchSubmitting?'正在批量创建...':'批量创建'}}</el-button></template>
  </el-dialog>
   <el-dialog v-model="result.show" title="批量创建结果" width="640px">
     <div class="result-summary"><span class="rs rs-ok">成功 {{result.created}}</span><span class="rs rs-fail">失败 {{result.failed}}</span><span class="rs rs-skip">跳过 {{result.skipped}}</span></div>
    <el-table :data="result.results" max-height="360" size="small">
      <el-table-column label="IP" width="130"><template #default="{row}"><span class="mono">{{row.ip}}</span></template></el-table-column>
       <el-table-column label="主机名称" min-width="150"><template #default="{row}">{{row.name||'-'}}</template></el-table-column>
       <el-table-column label="结果" min-width="180"><template #default="{row}"><span class="status-badge" :class="resultCls(row)">{{resultText(row)}}</span><div v-if="row.error_code" class="mono" style="font-size:11px;color:#dc2626;margin-top:3px">{{errText(row.error_code)}}</div></template></el-table-column>
    </el-table>
      <template #footer><el-button @click="result.show=false">关闭</el-button><el-button type="primary" @click="closeResultRefresh">关闭并刷新列表</el-button></template>
  </el-dialog>
</div>`,
  data() {
    return { list: [], loading: false, pg: 1, total: 0, filter: { tag: '', status: '', name: '', ip: '' }, viewMode: localStorage.getItem('hosts-view-mode') || 'table', drawer: false, detail: null, dialog: false, form: {}, creds: [], credLoading: false, credsLoaded: false, saving: false, eventSource: null, batchDialog: false, batchForm: { credential_id: null, ips: '', port: 22, tag: 'other', remark: '', auto_test: true }, batchSubmitting: false, batchTimer: null, preview: { count: 0, dup: 0, err: '' }, result: { show: false, created: 0, failed: 0, skipped: 0, results: [] } }
  },
  mounted() { this.connectSSE() },
  beforeUnmount() { if (this.eventSource) this.eventSource.close() },
  watch: {
    'filter.status'() { this.pg = 1; this.connectSSE() },
    'filter.name'() { this.pg = 1; this.connectSSE() },
    'filter.ip'() { this.pg = 1; this.connectSSE() },
    'filter.tag'() { this.pg = 1; this.connectSSE() },
    viewMode(value) { localStorage.setItem('hosts-view-mode', value) }
  },
  methods: {
    sseUrl() {
      const params = new URLSearchParams({ page: this.pg, page_size: 20 })
      if (this.filter.tag) params.set('tag', this.filter.tag)
      if (this.filter.status) params.set('status', this.filter.status)
      if (this.filter.name) params.set('name', this.filter.name)
      if (this.filter.ip) params.set('ip', this.filter.ip)
      return '/api/sse/hosts?' + params.toString()
    },
    connectSSE() {
      if (this.eventSource) this.eventSource.close()
      this.loading = true
      this.eventSource = new EventSource(this.sseUrl(), { withCredentials: true })
      this.eventSource.addEventListener('hosts', (e) => {
        try {
          const data = JSON.parse(e.data)
          this.list = data.list || []
          this.total = data.total || 0
        } catch (err) { /* */ } finally { this.loading = false }
      })
      this.eventSource.addEventListener('host.status', (e) => {
        try {
          const data = JSON.parse(e.data)
          const item = this.list.find(h => h.id === data.id)
          if (item) {
            item.status = data.status
            item.latency_ms = data.latency_ms
            if (data.info_json && data.info_json !== '{}') item.info_json = data.info_json
          }
        } catch (err) { /* */ }
      })
      this.eventSource.onerror = () => { /* SSE 连接异常，等待重连 */ }
    },
    onCredDropdown(visible) {
      if (visible && !this.credsLoaded && !this.credLoading) {
        this.loadCreds()
      }
    },
    async loadCreds() {
      this.credLoading = true
      try {
        const r = await api.get('/credentials', { params: { page: 1, page_size: 100 } })
        this.creds = r.data?.list || []
        this.credsLoaded = true
      } catch (e) { /* */ } finally { this.credLoading = false }
    },
    // ---- 批量新增主机 ----
    openBatch() {
      this.batchDialog = true
      this.batchForm = { credential_id: null, ips: '', port: 22, tag: 'other', remark: '', auto_test: true }
      this.preview = { count: 0, dup: 0, err: '' }
      if (!this.credsLoaded) this.loadCreds()
    },
    onIpsInput() {
      clearTimeout(this.batchTimer)
      this.batchTimer = setTimeout(() => this.parsePreview(), 300)
    },
    parsePreview() {
      const r = this.parseIPs(this.batchForm.ips || '')
      if (r.error) { this.preview = { count: 0, dup: 0, err: r.error }; return }
      const dup = r.ips.filter(ip => this.list.some(h => h.ip === ip)).length
      this.preview = { count: r.ips.length, dup, err: '' }
    },
    parseIPs(raw) {
      const fields = raw.split(/[\n,;\s]+/).filter(s => s.trim())
      const seen = {}, out = []
      for (const f of fields) {
        const r = this.expandRange(f.trim())
        if (r.error) return r
        for (const ip of r.ips) { if (!seen[ip]) { seen[ip] = true; out.push(ip) } }
      }
      return { ips: out }
    },
    expandRange(item) {
      const parts = item.split('-')
       if (parts.length === 1) { if (!this.validIPv4(item)) return { error: '非法 IP: ' + item }; return { ips: [item] } }
      if (parts.length === 2) {
        const start = parts[0], end = parts[1]
         if (!this.validIPv4(start)) return { error: '非法 IP: ' + start }
         if (end.indexOf('.') >= 0) { if (!this.validIPv4(end)) return { error: '非法 IP: ' + end }; return this.expandRange2(start, end) }
        const n = parseInt(end, 10)
         if (isNaN(n) || n < 1 || n > 254) return { error: '非法范围结束地址: ' + end }
        return this.expandRange2(start, this.joinIP(start, n))
      }
       return { error: '非法范围: ' + item }
    },
    validIPv4(s) { const p = s.split('.'); return p.length === 4 && p.every(x => /^\d{1,3}$/.test(x) && +x >= 0 && +x <= 255) },
    expandRange2(start, end) {
      const sp = start.split('.'), ep = end.split('.')
       if (sp[0] !== ep[0] || sp[1] !== ep[1] || sp[2] !== ep[2]) return { error: '范围不能跨网段: ' + start + '-' + end }
      const s = +sp[3], e = +ep[3]
       if (e < s) return { error: '范围起始值不能大于结束值: ' + start + '-' + end }
      const out = []
      for (let i = s; i <= e; i++) out.push(this.joinIP(start, i))
      return { ips: out }
    },
    joinIP(start, last) { const p = start.split('.'); return p[0] + '.' + p[1] + '.' + p[2] + '.' + last },
    async batchSubmit() {
       if (!this.batchForm.credential_id) { this.$message.warning('请选择凭据'); return }
       if (!this.batchForm.ips || !this.batchForm.ips.trim()) { this.$message.warning('请输入 IP 列表'); return }
      this.batchSubmitting = true
      try {
        const r = await api.post('/hosts/batch', {
          credential_id: this.batchForm.credential_id,
          ips: this.batchForm.ips,
          port: this.batchForm.port || 22,
          tag: this.batchForm.tag || 'other',
          remark: this.batchForm.remark || '',
          auto_test: this.batchForm.auto_test !== false
        }, { timeout: 300000 })
        if (r.code === 0) {
          this.result = { show: true, created: r.data.created, skipped: r.data.skipped, failed: r.data.total - r.data.created - r.data.skipped, results: r.data.results || [] }
          this.batchDialog = false
        } else {
           this.$message.error(r.message || '批量创建失败')
        }
       } catch (e) { this.$message.error('请求超时或网络错误') } finally { this.batchSubmitting = false }
    },
     resultText(r) { if (r.status === 'skipped_exists') return '跳过'; if (r.error_code) return '失败'; return '成功' },
    resultCls(r) { if (r.status === 'skipped_exists') return 'unverified'; if (r.error_code) return 'offline'; return 'online' },
     errText(code) { return { 1001: 'SSH 连接失败', 1002: 'SSH 认证失败', 1003: 'host key 发生变化', 1004: '信息采集失败' }[code] || ('错误 ' + code) },
    closeResultRefresh() { this.result.show = false; this.connectSSE() },
    info(row) { if (!row.info_json || row.info_json === '{}') return null; try { return typeof row.info_json === 'string' ? JSON.parse(row.info_json) : row.info_json } catch (e) { return null } },
    specText(row) { const i = this.info(row); if (!i) return '-'; return (i.cpu_cores||'?')+'C/'+(i.mem_total_mb||'?')+'M' },
    loadPct(row) { const i = this.info(row); if (!i || !i.load1 || !i.cpu_cores) return 0; return Math.round(i.load1/i.cpu_cores*100) },
    memPct(row) { const i = this.info(row); return i?.mem_used_percent ? Math.round(i.mem_used_percent) : 0 },
    diskPct(row) { const i = this.info(row); if (!i?.disk?.[0]) return 0; return Math.round(i.disk[0].used_percent || 0) },
    loadCls(row) { const p = this.loadPct(row); return p<60?'low':p<80?'mid':'high' },
    memCls(row) { const p = this.memPct(row); return p<60?'low':p<80?'mid':'high' },
    diskCls(row) { const p = this.diskPct(row); return p<60?'low':p<80?'mid':'high' },
    tagClass() { return 'other' },
     tagLabel(tag) { return tag || '其他' },
    tagType() { return 'info' },
    handleCardCommand(command, row) { if (command === 'detail') this.openDetail(row); else if (command === 'test') this.testConn(row); else if (command === 'edit') this.openForm(row); else if (command === 'delete') this.delHost(row) },
     statusText(s) { return s==='online'?'在线':s==='offline'?'离线':'未验证'} ,
    openDetail(row) { this.detail = row; this.drawer = true },
    openForm(row) { this.form = row ? {...row} : { name:'', ip:'', port:22, tag:'other', credential_id:null, remark:'' }; this.dialog = true },
    async save() {
      this.saving = true
      try {
        if (this.form.id) await api.put('/hosts/'+this.form.id, this.form); else await api.post('/hosts', this.form)
        this.dialog = false; this.connectSSE()
      } catch (e) { /* */ } finally { this.saving = false }
    },
    async testConn(row) {
      row._testing = true
       try { const r = await api.post('/hosts/'+row.id+'/test'); if (r.code===0) { row.status='online'; this.$message.success('连接成功') } else { this.$message.error(r.message||'连接失败') } } catch (e) { this.$message.error('网络错误') } finally { row._testing = false }
    },
    delHost(row) {
      this.$confirm('确认删除该主机？','提示',{type:'warning'}).then(async()=>{ try { await api.delete('/hosts/'+row.id); this.connectSSE() } catch(e){} }).catch(()=>{})
    }
  }
}

