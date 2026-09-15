// 套件部署页模板片段 · verify（原样迁入；套件专属表达式已改为通用分发名，类名统一 .stk-*）。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  P.tpl.verify = `  <el-dialog v-model="verifyVisible" :title="'集群探活 · ' + (verifyTarget?.name || '')" width="65%" top="6vh" class="stk-verify-dialog" :close-on-click-modal="false">
    <div v-loading="verifying" class="stk-verify-body">
      <!-- 摘要横幅 -->
      <div class="stk-vf-banner" :class="verifyResult?.ok ? 'is-ok' : 'is-fail'">
        <div class="stk-vf-banner-icon">
          <svg v-if="verifyResult?.ok" viewBox="0 0 24 24" width="28" height="28" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"/><polyline points="22 4 12 14.01 9 11.01"/></svg>
          <svg v-else viewBox="0 0 24 24" width="28" height="28" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>
        </div>
        <div class="stk-vf-banner-text">
          <div class="stk-vf-banner-summary">{{verifyResult?.summary || '正在探活…'}}</div>
          <div class="stk-vf-banner-meta">
            <span v-if="verifyResult?.checked_at" class="mono">{{verifyResult.checked_at}}</span>
            <span v-if="verifyResult?.hint" class="stk-vf-banner-hint">{{verifyResult.hint}}</span>
          </div>
        </div>
      </div>

      <!-- 组件标签：bigdata 多组件探活 -->
      <div v-if="verifyTabbed" class="stk-vf-tabs">
        <div class="stk-vf-tab" :class="{ active: verifyActiveTab === 'all' }" @click="verifyActiveTab = 'all'">查看所有</div>
        <div class="stk-vf-tab" v-for="c in verifyComponents" :key="c.key" :class="{ active: verifyActiveTab === c.key }" @click="verifyActiveTab = c.key">{{c.label}}</div>
      </div>

      <!-- 组件视角：选中具体组件（仅 bigdata） -->
      <template v-if="verifyTabbed && verifyActiveTab !== 'all'">
        <div class="stk-vf-section">
          <div class="stk-vf-section-title">接入地址 · {{currentVerifyCompLabel}} <span class="stk-vf-section-count">{{verifyCompEndpointsByHost.length}} 台主机 · {{verifyCompEndpoints.length}} 个端口</span></div>
          <div class="stk-vf-ep-box">
            <div class="stk-vf-ep-grid" v-if="verifyCompEndpointsByHost.length">
              <div class="stk-vf-ep-host-card" v-for="hg in verifyCompEndpointsByHost" :key="hg.host_ip">
                <div class="stk-vf-ep-host-header">
                  <span class="mono stk-vf-ep-host-ip">{{hg.host_ip}}</span>
                  <span class="stk-vf-section-count">{{hg.endpoints.length}} 个端口</span>
                </div>
                <div class="stk-vf-ep-host-eps">
                  <div class="stk-vf-ep-mini" v-for="ep in hg.endpoints" :key="ep.url">
                    <span class="stk-vf-ep-svc" :class="epRoleClass(ep.name, hg.role)" :title="ep.name + ' · ' + epRoleText(ep.name, hg.role)">{{ep.name}}</span>
                    <span class="stk-vf-ep-badge" :class="epProtocolClass(ep.url)">{{epProtocolLabel(ep.url)}}</span>
                    <span class="mono stk-vf-ep-mini-url" :title="ep.url">{{ep.url}}</span>
                    <span class="stk-vf-ep-acts"><a v-if="isWebUrl(ep.url)" class="stk-vf-ep-open" :href="ep.url" target="_blank" rel="noopener" title="打开 Web 界面">
                      <svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/><polyline points="15 3 21 3 21 9"/><line x1="10" y1="14" x2="21" y2="3"/></svg>
                    </a>
                    <span class="stk-vf-ep-copy" @click="copyText(ep.url)" title="复制地址">
                      <svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
                    </span></span>
                  </div>
                </div>
              </div>
            </div>
            <div v-else class="stk-vf-empty">该组件暂无接入地址</div>
          </div>
        </div>

        <div class="stk-vf-section">
          <div class="stk-vf-section-title">{{currentVerifyCompLabel}} 节点状态 <span class="stk-vf-section-count">{{verifyCompHostRows.length}} 台</span></div>
          <el-table :data="verifyCompHostRows" size="small" empty-text="该组件暂无节点结果" class="stk-vf-node-table" :show-overflow-tooltip="false">
            <el-table-column prop="ip" label="IP" min-width="130" :show-overflow-tooltip="false">
              <template #default="{row}"><span class="mono">{{row.ip}}</span></template>
            </el-table-column>
            <el-table-column prop="host_name" label="主机名" min-width="120" :show-overflow-tooltip="false" />
            <el-table-column prop="ok" label="状态" width="70" align="center" :show-overflow-tooltip="false">
              <template #default="{row}">
                <span class="stk-vf-table-dot" :class="row.ok ? 'ok' : 'fail'"></span>
              </template>
            </el-table-column>
            <el-table-column prop="role" label="角色" width="90" :show-overflow-tooltip="false">
              <template #default="{row}">
                <span class="stk-vf-role-badge" :class="row.role === 'master' ? 'is-master' : 'is-replica'">{{row.role}}</span>
              </template>
            </el-table-column>
            <el-table-column prop="instances" :label="verifyCompCountLabel" min-width="200" :show-overflow-tooltip="false">
              <template #default="{row}">
                <el-tooltip v-if="row.instList && row.instList.length" placement="top" :show-after="120" raw-content :content="tipHtml(row.instList)">
                  <span class="stk-vf-cell-text mono">{{row.instances}}</span>
                </el-tooltip>
                <span v-else class="stk-vf-cell-text mono" :class="{'stk-vf-cell-muted': !row.instances || row.instances === '无'}">{{row.instances}}</span>
              </template>
            </el-table-column>
            <el-table-column prop="version" label="版本" width="90" :show-overflow-tooltip="false" />
            <el-table-column prop="uptime" label="运行时间" min-width="140" :show-overflow-tooltip="false">
              <template #default="{row}">
                <el-tooltip v-if="row.uptime && row.uptime !== '-'" placement="top" :show-after="120" raw-content :content="tipHtml(row.uptimeList)">
                  <span class="stk-vf-cell-text">{{row.uptime}}</span>
                </el-tooltip>
                <span v-else class="stk-vf-cell-text stk-vf-cell-muted">—</span>
              </template>
            </el-table-column>
          </el-table>
        </div>
      </template>

      <!-- 整体视角：常规集群 或 bigdata「查看所有」 -->
      <template v-else>
        <!-- 接入地址 -->
        <div class="stk-vf-section">
          <div class="stk-vf-section-title">接入地址 <span class="stk-vf-section-count">{{verifyEndpointsByHost.length}} 台主机 · {{verifyEndpoints.length}} 个端口</span></div>
          <div class="stk-vf-ep-box">
            <div class="stk-vf-ep-grid" v-if="verifyEndpointsByHost.length">
              <div class="stk-vf-ep-host-card" v-for="hg in verifyEndpointsByHost" :key="hg.host_ip">
                <div class="stk-vf-ep-host-header">
                  <template v-if="verifyHostRole(hg)">
                    <span class="stk-vf-ep-host-role is-es">{{verifyHostRole(hg)}}</span>
                  </template>
                  <template v-else>
                    <span class="stk-vf-ep-host-role" :class="hg.role === 'master' ? 'is-master' : 'is-replica'">{{hg.role === 'master' ? '主' : '从'}}</span>
                  </template>
                  <span class="mono stk-vf-ep-host-ip">{{hg.host_ip}}</span>
                  <span class="stk-vf-section-count">{{hg.endpoints.length}} 个端口</span>
                </div>
                <div class="stk-vf-ep-host-eps">
                  <div class="stk-vf-ep-mini" v-for="ep in hg.endpoints" :key="ep.url">
                    <span class="stk-vf-ep-svc" :class="epRoleClass(ep.name, hg.role)" :title="ep.name + ' · ' + epRoleText(ep.name, hg.role)">{{ep.name}}</span>
                    <span class="stk-vf-ep-badge" :class="epProtocolClass(ep.url)">{{epProtocolLabel(ep.url)}}</span>
                    <span class="mono stk-vf-ep-mini-url" :title="ep.url">{{ep.url}}</span>
                    <span class="stk-vf-ep-acts"><a v-if="isWebUrl(ep.url)" class="stk-vf-ep-open" :href="ep.url" target="_blank" rel="noopener" title="打开 Web 界面">
                      <svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/><polyline points="15 3 21 3 21 9"/><line x1="10" y1="14" x2="21" y2="3"/></svg>
                    </a>
                    <span class="stk-vf-ep-copy" @click="copyText(ep.url)" title="复制地址">
                      <svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
                    </span></span>
                  </div>
                </div>
              </div>
            </div>
            <div v-else class="stk-vf-empty">暂无接入地址</div>
          </div>
        </div>

        <!-- 节点状态 -->
        <div class="stk-vf-section">
          <div class="stk-vf-section-title">节点状态 <span class="stk-vf-section-count">{{verifyHostRows.length}} 台</span></div>
          <el-table :data="verifyHostRows" size="small" empty-text="暂无节点结果" class="stk-vf-node-table" :show-overflow-tooltip="false">
            <el-table-column prop="ip" label="IP" min-width="130" :show-overflow-tooltip="false">
              <template #default="{row}"><span class="mono">{{row.ip}}</span></template>
            </el-table-column>
            <el-table-column prop="host_name" label="主机名" min-width="120" :show-overflow-tooltip="false" />
            <el-table-column prop="status" label="状态" width="70" align="center" :show-overflow-tooltip="false">
              <template #default="{row}">
                <span class="stk-vf-table-dot" :class="row.ok ? 'ok' : 'fail'"></span>
              </template>
            </el-table-column>
            <el-table-column prop="role" label="角色" width="90" :show-overflow-tooltip="false">
              <template #default="{row}">
                <span class="stk-vf-role-badge" :class="row.role === 'master' ? 'is-master' : 'is-replica'">{{row.role}}</span>
              </template>
            </el-table-column>
            <el-table-column prop="replication" :label="verifyCountLabel" min-width="200" :show-overflow-tooltip="false">
              <template #default="{row}">
                <el-tooltip v-if="row.instList && row.instList.length" placement="top" :show-after="120" raw-content :content="tipHtml(row.instList)">
                  <span class="stk-vf-cell-text mono"><span class="stk-vf-cell-count">{{row.instList.length}}</span>{{row.replication}}</span>
                </el-tooltip>
                <span v-else class="stk-vf-cell-text mono" :class="{'stk-vf-cell-muted': !row.replication || row.replication === '—'}">{{row.replication || '—'}}</span>
              </template>
            </el-table-column>
            <el-table-column prop="version" label="版本" width="90" :show-overflow-tooltip="false" />
            <el-table-column prop="uptime" label="运行时间" min-width="140" :show-overflow-tooltip="false">
              <template #default="{row}">
                <el-tooltip v-if="row.uptime && row.uptime !== '-'" placement="top" :show-after="120" raw-content :content="tipHtml(row.uptimeList)">
                  <span class="stk-vf-cell-text">{{row.uptime}}</span>
                </el-tooltip>
                <span v-else class="stk-vf-cell-text stk-vf-cell-muted">—</span>
              </template>
            </el-table-column>
          </el-table>
        </div>
      </template>

      <!-- 访问密码 -->
      <div class="stk-vf-section" v-if="verifyPassword">
        <div class="stk-vf-section-title">访问密码</div>
        <div class="stk-vf-pass-row">
          <span class="stk-vf-pass-dots">●●●●●●●●●●●●</span>
          <span class="stk-vf-pass-label">已配置</span>
          <div class="stk-vf-pass-copy" @click="copyText(verifyPassword)" title="复制密码">
            <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
          </div>
        </div>
      </div>

      <!-- 提示信息 -->
      <div class="stk-vf-notes" v-if="verifyResult?.notes?.length">
        <div class="stk-vf-note-item" v-for="(n,i) in verifyResult.notes" :key="i">
          <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="flex-shrink:0;margin-top:2px"><circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/></svg>
          <span>{{n}}</span>
        </div>
      </div>
      <!-- 客户端接入提示 -->
      <div class="stk-vf-client-hint" v-if="verifyResult?.client_hint">
        <div class="stk-vf-client-hint-label">客户端接入</div>
        <pre class="stk-vf-client-hint-code">{{verifyResult.client_hint}}</pre>
      </div>
    </div>
    <template #footer>
      <el-button @click="verifyVisible=false">关闭</el-button>
      <el-button type="primary" :loading="verifying" :disabled="!verifyTarget || verifyTarget.status==='uninstalled'" @click="runVerify">重新探活</el-button>
    </template>
  </el-dialog>
`
})()
