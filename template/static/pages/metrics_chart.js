// 监控查询 · 图表组件：归一化查询结果 → ECharts 折线图。
// 用 SVG 渲染器：不依赖 canvas，无头环境下的截图与 DOM 断言都方便（measure 渲染台依赖这一点）。
//
// 尺寸坑：容器藏在 v-show="drawn" 后面（display:none）。若此时就 echarts.init，
// ECharts 会按 0 尺寸做整套布局，事后 resize 也救不回网格/坐标轴/legend
// （实测表现为：x 轴刻度全挤在左端、折线只剩一小截、legend 分页成 1/N）。
// 所以 init 与 setOption 都推迟到 drawn=true 之后的 nextTick。
// 代理坑：chart 实例必须 markRaw 后再存进 data()。Vue 3 会把 data 里的对象深度响应式化，
// this.chart 读出来是 Proxy；以 Proxy 为 this 跑完 setOption 后，ECharts 内部状态已坏——
// 图看着正常，但下一次 update（窗口 resize → chart.resize）内部读
// series.coordinateSystem.type 时崩 "Cannot read properties of undefined (reading 'type')"。
// 另：别在 ResizeObserver 回调里调 resize()，窗口缩放用 resize 事件即可。
window.MetricsChart = {
  props: {
    result: { type: Object, default: null },
    maxSeries: { type: Number, default: 50 }
  },
  template: `
<div class="metrics-chart-wrap">
  <div ref="box" class="metrics-chart-box" v-show="drawn"></div>
  <div class="metrics-chart-trunc" v-if="drawn && truncated">序列过多，图中只画前 {{ maxSeries }} 条；完整数据见「统计表」页签</div>
  <empty-state v-if="!drawn" text="没有可绘制的数据" />
</div>`,
  data() {
    return { chart: null, drawn: false, truncated: false }
  },
  mounted() {
    this._onWinResize = () => {
      // 页面被切走（尺寸 0）时跳过，避免把图布局到 0 宽上
      if (this.chart && this.$refs.box && this.$refs.box.clientWidth > 0) this.chart.resize()
    }
    window.addEventListener('resize', this._onWinResize)
    this.draw()
  },
  beforeUnmount() {
    window.removeEventListener('resize', this._onWinResize)
    if (this.chart) { this.chart.dispose(); this.chart = null }
  },
  watch: { result() { this.draw() } },
  methods: {
    // 序列名：__name__ 优先，其余标签按名称排序（与后端 digest 的标签渲染一致）
    label(m) {
      if (!m) return ''
      const name = m.__name__ || ''
      const rest = Object.keys(m).filter(k => k !== '__name__').sort()
        .map(k => k + '="' + m[k] + '"').join(', ')
      return rest ? (name ? name + '{' + rest + '}' : '{' + rest + '}') : (name || '(无标签)')
    },
    // 组装 setOption 配置（一段一段对应结果数据，便于单独审阅）
    buildOption(shown) {
      const multi = shown.some(s => (s.points || []).length > 2)
      return {
        animation: false, // 大数据量下动画是负担，且无头截图会截到未完成的过渡态
        grid: { left: 8, right: 18, top: shown.length > 1 ? 38 : 14, bottom: 48, containLabel: true },
        legend: shown.length > 1
          ? { type: 'scroll', top: 0, itemWidth: 14, itemHeight: 8, textStyle: { fontSize: 11, color: '#6B7280' } }
          : { show: false },
        tooltip: {
          trigger: 'axis',
          confine: true,
          textStyle: { fontSize: 12 }
        },
        xAxis: {
          type: 'time',
          axisLine: { lineStyle: { color: '#E5E7EB' } },
          axisLabel: { color: '#9CA3AF', fontSize: 11, hideOverlap: true },
          splitLine: { show: false }
        },
        yAxis: {
          type: 'value',
          scale: true,
          axisLabel: { color: '#9CA3AF', fontSize: 11 },
          splitLine: { lineStyle: { color: '#F3F4F6' } }
        },
        dataZoom: [
          { type: 'inside' },
          { type: 'slider', height: 16, bottom: 6, borderColor: 'transparent', fillerColor: 'rgba(59,130,246,.12)', textStyle: { fontSize: 10, color: '#9CA3AF' } }
        ],
        series: shown.map(s => ({
          name: this.label(s.metric),
          type: 'line',
          showSymbol: !multi, // 单点 / 少点场景（instant 查询）画出点来才看得见
          symbolSize: 6,
          smooth: false,
          connectNulls: false, // 缺失点断开，不假装连续
          lineStyle: { width: 1.6 },
          emphasis: { focus: 'series' },
          data: (s.points || []).map(p => [p.ts, p.v])
        }))
      }
    },
    draw() {
      const series = (this.result && this.result.series) || []
      if (!series.length) {
        this.drawn = false
        this.truncated = false
        if (this.chart) this.chart.clear()
        return
      }
      this.truncated = series.length > this.maxSeries
      const shown = series.slice(0, this.maxSeries)
      // 先让容器脱离 display:none，等 DOM 更新后再 init / setOption（见文件头说明）
      this.drawn = true
      this.$nextTick(() => {
        if (!this.$refs.box) return
        if (!this.chart) this.chart = Vue.markRaw(echarts.init(this.$refs.box, null, { renderer: 'svg' }))
        else {
          // 刷新前先收起 tooltip：ECharts 的 _keepShow 会在 setOption 后约 50ms 重放上次
          // hover 位置；此刻 tooltip 容器已随组件重建销毁，延迟回调会抛
          // "Cannot read properties of null (reading 'offsetWidth')"。
          // hideTip 会清掉其内部记录的 _lastX/_lastY，使 keepShow 不再排队。
          this.chart.dispatchAction({ type: 'hideTip' })
          this.chart.resize() // 上一次布局后容器可能被隐藏 / 尺寸变过，先按当前尺寸校准
        }
        this.chart.setOption(this.buildOption(shown), true)
      })
    }
  }
}
