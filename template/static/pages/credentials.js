window.CredentialsPage = {
  props: ['page', 'user', 'versionData'],
  template: `
<div>
  <div class="page-card">
    <div class="card-header">
      <span class="title">凭据列表</span>
      <div class="header-extra"><el-button type="primary" @click="openForm(null)">新增凭据</el-button></div>
    </div>
    <el-table :data="list" style="width:100%" v-loading="loading">
      <el-table-column label="名称" prop="name" min-width="140" />
      <el-table-column label="类型" width="100">
        <template #default="{row}"><span class="action-badge" :class="row.type==='private_key'?'auth':'credential'">{{row.type==='private_key'?'私钥':'密码'}}</span></template>
      </el-table-column>
      <el-table-column label="用户名" width="120">
        <template #default="{row}"><span class="mono">{{row.username||'-'}}</span></template>
      </el-table-column>
      <el-table-column label="指纹" width="180">
        <template #default="{row}"><span class="mono" :title="row.fingerprint" style="font-size:12px">{{row.fingerprint?row.fingerprint.slice(0,16)+'…':'-'}}</span></template>
      </el-table-column>
      <el-table-column label="备注" prop="remark" min-width="120" show-overflow-tooltip />
      <el-table-column label="更新时间" width="160">
        <template #default="{row}"><span class="mono" style="font-size:12.5px;color:#6B7280">{{row.updated_at||'-'}}</span></template>
      </el-table-column>
      <el-table-column label="" width="120" fixed="right">
        <template #default="{row}"><div class="ops-cell"><el-button size="small" text @click="openForm(row)">编辑</el-button><el-button size="small" text type="danger" @click="delCred(row)">删除</el-button></div></template>
      </el-table-column>
    </el-table>
    <div style="display:flex;justify-content:flex-end;margin-top:16px"><el-pagination v-model:current-page="pg" :page-size="20" :total="total" layout="total, prev, pager, next" @current-change="load" /></div>
  </div>
  <div class="tip-bar">私钥加密存储，保存后不可再查看明文</div>
  <el-dialog v-model="dialog" :title="form.id?'编辑凭据':'新增凭据'" width="560px">
    <el-form :model="form" label-position="top">
      <el-form-item label="名称" required><el-input v-model="form.name" /></el-form-item>
      <el-form-item label="类型"><el-select v-model="form.type" style="width:100%"><el-option label="私钥" value="private_key" /><el-option label="密码" value="password" /></el-select></el-form-item>
      <el-form-item label="用户名" required><el-input v-model="form.username" /></el-form-item>
      <el-form-item label="密钥/密码" required><el-input v-model="form.secret" type="password" show-password :placeholder="form.id?'留空则不修改':''" /></el-form-item>
      <el-form-item label="备注"><el-input v-model="form.remark" type="textarea" :rows="2" /></el-form-item>
    </el-form>
    <template #footer><el-button @click="dialog=false">取消</el-button><el-button type="primary" :loading="saving" @click="save">保存</el-button></template>
  </el-dialog>
</div>`,
  data() { return { list: [], loading: false, pg: 1, total: 0, dialog: false, form: {}, saving: false } },
  mounted() { this.load() },
  methods: {
    async load() {
      this.loading = true
      try { const r = await api.get('/credentials', { params: { page: this.pg, page_size: 20 } }); if (r.code === 0) { this.list = r.data?.list || []; this.total = r.data?.total || 0 } } catch (e) { /* */ } finally { this.loading = false }
    },
    openForm(row) { this.form = row ? { ...row, secret: '' } : { name: '', type: 'private_key', username: '', secret: '', remark: '' }; this.dialog = true },
    async save() {
      this.saving = true
      try {
        if (this.form.id) { const d = { ...this.form }; if (!d.secret) delete d.secret; await api.put('/credentials/' + this.form.id, d) } else { await api.post('/credentials', this.form) }
        this.dialog = false; this.load()
      } catch (e) { /* */ } finally { this.saving = false }
    },
    delCred(row) {
      this.$confirm('确认删除该凭据？被引用的凭据无法删除。', '提示', { type: 'warning' }).then(async () => { try { await api.delete('/credentials/' + row.id); this.load() } catch (e) { /* */ } }).catch(() => {})
    }
  }
}