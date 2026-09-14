// ES 控制台 · 字段侧栏（F3）：Discover 左栏。字段表来自本地快照（视图详情）。
window.EsFieldsPanel = {
  props: ['fields', 'syncedAt', 'syncStatus', 'syncError'],
  emits: ['insert', 'analyze-distinct', 'analyze-dim', 'refresh'],
  template: `
<div class="es-fields-panel">
  <div class="es-fields-head">
    <span class="es-fields-title">字段（{{fields.length}}）</span>
    <el-button text size="small" @click="$emit('refresh')"><el-icon><Refresh /></el-icon></el-button>
  </div>
  <div class="es-fields-sync">
    <template v-if="syncStatus==='failed'"><span class="es-sync-fail">同步失败：{{syncError||'未知原因'}}</span></template>
    <template v-else-if="syncedAt">字段同步于 {{(syncedAt||'').slice(0,16)}}</template>
    <template v-else>尚未同步</template>
  </div>
  <el-input v-model="kw" placeholder="过滤字段..." clearable size="small" />
  <div class="es-fields-list">
    <div v-for="f in filtered" :key="f.path" class="es-field-item">
      <div class="es-field-main" @click="$emit('insert', f)">
        <span class="es-field-name mono" :title="f.path">{{f.path}}</span>
        <span class="es-field-type" :class="typeClass(f)">{{typeLabel(f)}}</span>
      </div>
      <div class="es-field-badges">
        <span class="es-fcap" :class="{on: f.searchable}">可搜索</span>
        <span class="es-fcap" :class="{on: f.aggregatable}">可聚合</span>
        <span v-if="f.type_conflict" class="es-fwarn" title="同一字段在索引成员间存在多种类型，查询按最宽公共算子降级">多类型</span>
        <span v-if="f.partial" class="es-fwarn" title="仅部分匹配成员存在该字段">部分存在</span>
      </div>
      <div class="es-field-ops">
        <el-button v-if="f.searchable" text size="small" @click="$emit('insert', f)">插入查询</el-button>
        <el-button v-if="f.aggregatable" text size="small" @click="$emit('analyze-distinct', f)">去重计数</el-button>
        <el-button v-if="f.aggregatable" text size="small" @click="$emit('analyze-dim', f)">作为分析维度</el-button>
      </div>
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
    typeLabel(f) {
      if (f.type_conflict) return f.types.join('/')
      return f.types.join('/')
    },
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
