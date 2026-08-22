window.AuditPage = {
  props: ['page', 'user', 'versionData'],
  template: `
<div>
  <div class="page-card">
    <div class="card-header">
      <div class="filter-bar">
        <el-select v-model="actionFilter" placeholder="操作类型" clearable style="width:140px" @change="load">
          <el-option label="全部" value="" /><el-option label="认证" value="auth" /><el-option label="主机" value="host" /><el-option label="凭据" value="credential" />
        </el-select>
      </div>
    </div>
    <el-table :data="list" style="width:100%" v-loading="loading">
      <el-table-column label="时间" width="170">
        <template #default="{row}"><span class="mono" style="font-size:12.5px;color:#6B7280">{{row.created_at}}</span></template>
      </el-table-column>
      <el-table-column label="操作" width="120">
        <template #default="{row}"><span class="action-badge" :class="actionClass(row.action)">{{row.action}}</span></template>
      </el-table-column>
      <el-table-column label="目标" prop="target" min-width="140">
        <template #default="{row}"><span class="mono">{{row.target||'-'}}</span></template>
      </el-table-column>
      <el-table-column label="详情" prop="detail" min-width="200" show-overflow-tooltip />
      <el-table-column label="来源 IP" width="140">
        <template #default="{row}"><span class="mono" style="font-size:12.5px">{{row.source_ip||'-'}}</span></template>
      </el-table-column>
    </el-table>
    <div style="display:flex;justify-content:flex-end;margin-top:16px"><el-pagination v-model:current-page="pg" :page-size="20" :total="total" layout="total, prev, pager, next" @current-change="load" /></div>
  </div>
</div>`,
  data() { return { list: [], loading: false, pg: 1, total: 0, actionFilter: '' } },
  mounted() { this.load() },
  methods: {
    async load() {
      this.loading = true
      try {
        const params = { page: this.pg, page_size: 20 }
        if (this.actionFilter) params.action = this.actionFilter
        const r = await api.get('/audit-logs', { params })
        if (r.code === 0) { this.list = r.data?.list || []; this.total = r.data?.total || 0 }
      } catch (e) { /* */ } finally { this.loading = false }
    },
    actionClass(action) { const a = (action || '').toLowerCase(); if (a.startsWith('auth')) return 'auth'; if (a.startsWith('host')) return 'host'; if (a.startsWith('credential')) return 'credential'; return 'other' }
  }
}