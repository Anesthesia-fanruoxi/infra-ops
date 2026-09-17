// ES 控制台 · 字段侧栏（F3）：只做两件事——展示字段数、点击过滤右侧文档显示的字段。
// 点击字段 = 右侧只显示该字段（时间字段恒显示在文档头），再点一次取消；选中项以底部高亮条标记。
window.EsFieldsPanel = {
  props: ['fields', 'syncedAt', 'syncStatus', 'syncError', 'selected'],
  emits: ['select', 'refresh'],
  template: `
<div class="es-fields-panel">
  <div class="es-fields-head">
    <span class="es-fields-title">字段列表（{{fields.length}}）</span>
    <el-button text size="small" @click="$emit('refresh')"><el-icon><Refresh /></el-icon></el-button>
  </div>
  <div class="es-fields-sync">
    <template v-if="syncStatus==='failed'"><span class="es-sync-fail">同步失败：{{syncError||'未知原因'}}</span></template>
    <template v-else-if="syncedAt">字段同步于 {{(syncedAt||'').slice(0,16)}}</template>
    <template v-else>尚未同步</template>
  </div>
  <el-input v-model="kw" placeholder="过滤字段..." clearable size="small" />
  <div class="es-fields-list">
    <div v-for="f in filtered" :key="f.path" class="es-field-item" :class="{'is-sel': f.path===selected}"
         :title="f.path" @click="$emit('select', f.path)">
      <span class="es-field-name mono">{{f.path}}</span>
      <span class="es-field-type" :class="typeClass(f)">{{(f.types||[]).join('/')}}</span>
    </div>
    <div v-if="!filtered.length" class="es-fields-empty">无匹配字段</div>
  </div>
</div>`,
  data() { return { kw: '' } },
  computed: {
    filtered() {
      const k = this.kw.trim().toLowerCase()
      return k ? (this.fields || []).filter(f => f.path.toLowerCase().includes(k)) : (this.fields || [])
    }
  },
  methods: {
    typeClass(f) {
      if (f.type_conflict) return 't-conflict'
      const t = (f.types || [])[0] || ''
      if (t === 'text') return 't-text'
      if (t === 'keyword') return 't-keyword'
      if (t === 'date' || t.startsWith('date_')) return 't-date'
      if (['long', 'integer', 'short', 'byte', 'float', 'double', 'scaled_float', 'half_float'].includes(t)) return 't-number'
      if (t === 'boolean') return 't-bool'
      return 't-other'
    }
  }
}
