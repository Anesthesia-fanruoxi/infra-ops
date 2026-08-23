window.LoginPage = {
  props: ['version'],
  template: `
<div class="login-page">
  <div class="login-brand">
    <div class="brand-content">
      <div class="logo-mark">i</div>
      <div class="main-title">infra-ops</div>
      <div class="sub-title">服务器基建统一管理</div>
      <div class="brand-desc">集中管理主机凭据、SSH 密钥与运维操作审计，安全可控。</div>
    </div>
    <div class="login-footer">v{{version || '—'}}</div>
  </div>
  <div class="login-form-wrap">
    <div class="login-box">
      <h3>登录</h3>
      <el-form :model="form" @submit.prevent="handleLogin">
        <el-form-item><el-input v-model="form.username" placeholder="账号" size="large" /></el-form-item>
        <el-form-item><el-input v-model="form.password" type="password" placeholder="口令" size="large" show-password @keyup.enter="handleLogin" /></el-form-item>
        <div v-if="error" class="login-error">{{error}}</div>
        <el-button class="login-btn" type="primary" :loading="loading" @click="handleLogin">登录</el-button>
      </el-form>
    </div>
  </div>
</div>`,
  data() {
    return { form: { username: '', password: '' }, loading: false, error: '' }
  },
  methods: {
    async handleLogin() {
      this.error = ''
      if (!this.form.username || !this.form.password) { this.error = '请输入账号和密码'; return }
      this.loading = true
      try {
        const res = await api.post('/auth/login', this.form)
        if (res.code === 0) {
          await this.$root.loadUser()
          if (res.data?.must_change_password === true) {
            this.$root.openChangePassword(this.form.password)
          } else {
            location.hash = '#/overview'
          }
        } else { this.error = res.message || '登录失败' }
      } catch (e) { this.error = '网络错误' } finally { this.loading = false }
    }
  }
}