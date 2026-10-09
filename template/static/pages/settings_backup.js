// 设置 · 备份恢复：全部数据（库 + 部署资产）加密导出与恢复。
//
// 备份文件是自研容器格式 .iopsbak（scrypt + AES-256-GCM + zip，见 common/backupfmt）：
// 密码强制、位数不限但不能为空——它保护备份里的全部连接密码、凭据与 AI 密钥；
// 忘记密码没有任何找回手段，界面上必须把这句话钉在导出按钮旁边。
// 恢复走「暂存 + 重启换入」：进程内 SQLite 不能热替换库文件，见 main.restore.go。
window.SettingsBackup = {
  template: `
<div class="page-card">
  <div class="card-header">
    <div class="tool-list-title">
      <span class="tool-list-sub">备份 = 数据库快照 + 部署资产，整包 AES-256-GCM 加密</span>
    </div>
  </div>

  <el-row :gutter="16">
    <el-col :span="12">
      <div class="bk-pane">
        <div class="bk-title">创建备份</div>
        <div class="bk-field">
          <span class="bk-label">备份密码</span>
          <el-input v-model="password" type="password" show-password placeholder="必填，位数不限；忘记密码 = 备份作废" />
        </div>
        <div class="bk-field">
          <span class="bk-label">确认密码</span>
          <el-input v-model="password2" type="password" show-password placeholder="再输一遍，防止手滑" @keyup.enter="doExport" />
        </div>
        <div class="bk-field">
          <span class="bk-label">保存位置</span>
          <div class="bk-path">
            <el-input v-model="exportPath" class="bk-path-input" placeholder="选择或输入绝对路径（如 D:\\backup\\infra-ops.iopsbak）" />
            <el-button :disabled="!desktop" @click="pickExportPath">浏览</el-button>
          </div>
        </div>
        <el-alert class="bk-alert" type="warning" :closable="false" show-icon
          title="密码不存进备份，丢失后无法找回" description="备份包含全部连接密码、凭据与 AI 密钥，请把密码记在别处。" />
        <el-button type="primary" :loading="exporting" :disabled="!exportPath.trim()" @click="doExport">创建备份</el-button>
        <div class="bk-result" v-if="exportDone">{{ exportDone }}</div>
      </div>
    </el-col>
    <el-col :span="12">
      <div class="bk-pane">
        <div class="bk-title">恢复备份</div>
        <div class="bk-field">
          <span class="bk-label">备份文件</span>
          <div class="bk-path">
            <el-input v-model="restorePath" class="bk-path-input" placeholder="选择或输入 .iopsbak 文件路径" />
            <el-button :disabled="!desktop" @click="pickRestorePath">浏览</el-button>
          </div>
        </div>
        <div class="bk-field">
          <span class="bk-label">备份密码</span>
          <el-input v-model="restorePwd" type="password" show-password placeholder="创建备份时设置的密码" @keyup.enter="doPreview" />
        </div>
        <el-alert class="bk-alert" type="info" :closable="false" show-icon
          title="恢复 = 用备份整体覆盖当前数据" description="覆盖发生在重启时：确认后先暂存，重启应用即换入；当前数据会自动留档一份在 data 目录。" />
        <el-button :loading="previewing" :disabled="!restorePath.trim()" @click="doPreview">读取预览</el-button>
      </div>
    </el-col>
  </el-row>

  <el-dialog v-model="previewOpen" title="备份预览" width="640px" append-to-body>
    <template v-if="preview">
      <div class="set-kv">
        <div class="set-kv-item"><span>备份时间</span><code>{{ preview.manifest.created_at }}</code></div>
        <div class="set-kv-item"><span>应用版本</span><code>{{ preview.manifest.app_version || '—' }}</code></div>
        <div class="set-kv-item"><span>数据表</span><code>{{ preview.manifest.db_tables.length }} 张 / 共 {{ preview.manifest.total_rows }} 行</code></div>
        <div class="set-kv-item"><span>库大小</span><code>{{ fmtSize(preview.db_bytes) }}</code></div>
        <div class="set-kv-item"><span>随包资产</span><code>{{ preview.manifest.asset_count }} 个文件（{{ fmtSize(preview.manifest.asset_bytes) }}）</code></div>
      </div>
      <el-table :data="preview.files" size="small" max-height="220" class="bk-files">
        <el-table-column prop="name" label="包含内容" min-width="220" show-overflow-tooltip />
        <el-table-column label="大小" width="90">
          <template #default="{ row }">{{ fmtSize(row.size) }}</template>
        </el-table-column>
      </el-table>
    </template>
    <template #footer>
      <el-button @click="previewOpen = false">取消</el-button>
      <el-button type="danger" :loading="restoring" @click="doRestore">确认恢复（重启后生效）</el-button>
    </template>
  </el-dialog>
</div>`,
  data() {
    return {
      desktop: !!window.__INFRA_DESKTOP__,
      exporting: false, previewing: false, restoring: false,
      password: '', password2: '',
      exportPath: '', exportDone: '',
      restorePath: '', restorePwd: '',
      preview: null, previewOpen: false
    }
  },
  methods: {
    async pickExportPath() {
      try {
        const saved = await window.wails.Call.ByName('infra-ops/desktop.FileService.SaveFile',
          '保存备份文件', this.defaultName(), 'infra-ops 备份', '*.iopsbak')
        if (saved) this.exportPath = saved
      } catch (e) { ElMessage.error((e && e.message) || '选择保存位置失败') }
    },
    async pickRestorePath() {
      try {
        const picked = await window.wails.Call.ByName('infra-ops/desktop.FileService.PickBackupFile', '选择备份文件')
        if (picked) this.restorePath = picked
      } catch (e) { ElMessage.error((e && e.message) || '选择备份文件失败') }
    },
    defaultName() {
      const ts = new Date().toISOString().slice(0, 19).replace(/[:T]/g, '')
      return 'infra-ops-backup_' + ts + '.iopsbak'
    },
    async doExport() {
      if (this.exporting) return
      const pwd = this.password
      if (!pwd) { ElMessage.warning('备份密码不能为空——它保护的是全部连接密码与凭据'); return }
      if (pwd !== this.password2) { ElMessage.warning('两次输入的密码不一致'); return }
      this.exporting = true
      this.exportDone = ''
      try {
        const r = await api.post('/settings/backup/export', { path: this.exportPath, password: pwd }, { timeout: 0 })
        if (r.code !== 0) return // 拦截器已提示
        const d = r.data || {}
        this.exportDone = '已创建：' + d.path + '（' + this.fmtSize(d.size_bytes) + '）'
        ElMessage.success('备份完成')
      } catch (e) { /* 拦截器已提示 */ } finally { this.exporting = false }
    },
    async doPreview() {
      if (this.previewing) return
      if (!this.restorePwd) { ElMessage.warning('请输入创建备份时设置的密码'); return }
      this.previewing = true
      try {
        const r = await api.post('/settings/backup/preview', { path: this.restorePath, password: this.restorePwd }, { timeout: 0 })
        if (r.code !== 0) return
        this.preview = r.data
        this.previewOpen = true
      } catch (e) { /* 拦截器已提示 */ } finally { this.previewing = false }
    },
    async doRestore() {
      if (this.restoring) return
      try {
        await ElMessageBox.confirm(
          '恢复将用备份内容整体覆盖当前数据（含全部连接、凭据与历史记录）。当前数据会自动留档；确认后请重启应用生效。',
          '确认恢复', { confirmButtonText: '恢复并留档当前数据', cancelButtonText: '再想想', type: 'warning' })
      } catch (e) { return }
      this.restoring = true
      try {
        const r = await api.post('/settings/backup/restore', { path: this.restorePath, password: this.restorePwd }, { timeout: 0 })
        if (r.code !== 0) return
        this.previewOpen = false
        ElMessageBox.alert('恢复内容已暂存。请关闭并重新打开应用，重启后自动换入生效。', '暂存完成', { type: 'success', confirmButtonText: '知道了' })
      } catch (e) { /* 拦截器已提示 */ } finally { this.restoring = false }
    },
    fmtSize(b) {
      const v = Number(b || 0)
      if (v < 1024) return v + ' B'
      if (v < 1024 * 1024) return (v / 1024).toFixed(1) + ' KB'
      return (v / 1024 / 1024).toFixed(1) + ' MB'
    }
  }
}
