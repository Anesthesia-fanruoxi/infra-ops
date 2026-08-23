window.ChangePasswordDialog = {
  props: {
    modelValue: { type: Boolean, default: false },
    forced: { type: Boolean, default: false },
    oldPassword: { type: String, default: '' }
  },
  emits: ['update:modelValue', 'done'],
  template: `
<el-dialog
  title="修改密码"
  :model-value="modelValue"
  :show-close="!forced"
  :close-on-click-modal="!forced"
  :close-on-press-escape="!forced"
  width="440px"
  @update:model-value="$emit('update:modelValue', $event)"
>
  <!-- 强制修改警告条 -->
  <div v-if="forced" class="pwd-forced-banner">
    <svg class="pwd-forced-icon" viewBox="0 0 20 20" fill="currentColor"><path fill-rule="evenodd" d="M8.485 2.495c.673-1.167 2.357-1.167 3.03 0l6.28 10.875c.673 1.167-.168 2.625-1.516 2.625H3.72c-1.347 0-2.189-1.458-1.515-2.625L8.485 2.495zM10 5a.75.75 0 01.75.75v3.5a.75.75 0 01-1.5 0v-3.5A.75.75 0 0110 5zm0 9a1 1 0 100-2 1 1 0 000 2z" clip-rule="evenodd"/></svg>
    <span>检测到您仍在使用默认密码，必须修改后才能继续使用系统</span>
  </div>

  <!-- 智能模式：随机生成密码卡片 -->
  <div v-if="smartMode" class="pwd-smart">
    <div class="pwd-card">
      <div class="pwd-card-head">
        <svg class="pwd-shield-icon" viewBox="0 0 20 20" fill="currentColor"><path fill-rule="evenodd" d="M10 1a.75.75 0 01.65.39l1.7 3.44 3.8.55c.74.11 1.04 1.02.5 1.6l-2.75 2.68.65 3.78a.75.75 0 01-1.08.79L10 12.35l-3.87 2.01a.75.75 0 01-1.08-.79l.65-3.78L2.99 5.98c-.54-.58-.24-1.49.5-1.6l3.8-.55 1.7-3.44A.75.75 0 0110 1z" clip-rule="evenodd"/></svg>
        <span class="pwd-card-label">新密码已生成</span>
      </div>
      <div class="pwd-card-display">
        <code class="pwd-card-code" :title="generated">{{ generated }}</code>
        <button class="pwd-copy-btn" @click="copyToClipboard" :disabled="loading" title="复制密码">
          <svg v-if="!copied" viewBox="0 0 20 20" fill="currentColor" width="16" height="16"><path d="M8 2a1 1 0 000 2h2a1 1 0 100-2H8z"/><path d="M3 5a2 2 0 012-2 3 3 0 003 3h2a3 3 0 003-3 2 2 0 012 2v6h-4.586l1.293-1.293a1 1 0 00-1.414-1.414l-3 3a1 1 0 000 1.414l3 3a1 1 0 001.414-1.414L10.414 13H15v3a2 2 0 01-2 2H5a2 2 0 01-2-2V5z"/></svg>
          <svg v-else viewBox="0 0 20 20" fill="currentColor" width="16" height="16"><path fill-rule="evenodd" d="M16.707 5.293a1 1 0 010 1.414l-8 8a1 1 0 01-1.414 0l-4-4a1 1 0 011.414-1.414L8 12.586l7.293-7.293a1 1 0 011.414 0z" clip-rule="evenodd"/></svg>
        </button>
      </div>
      <div class="pwd-card-hint">
        <svg viewBox="0 0 20 20" fill="currentColor" width="13" height="13"><path fill-rule="evenodd" d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-7-4a1 1 0 11-2 0 1 1 0 012 0zM9 9a1 1 0 000 2v3a1 1 0 001 1h1a1 1 0 100-2v-3a1 1 0 00-1-1H9z" clip-rule="evenodd"/></svg>
        <span>已自动复制到剪贴板，请妥善保管</span>
      </div>
    </div>
  </div>

  <!-- 手动模式：三字段表单 -->
  <div v-else class="pwd-manual">
    <div v-if="forced" class="pwd-hint-row">
      <svg viewBox="0 0 20 20" fill="currentColor" width="14" height="14"><path fill-rule="evenodd" d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-7-4a1 1 0 11-2 0 1 1 0 012 0zM9 9a1 1 0 000 2v3a1 1 0 001 1h1a1 1 0 100-2v-3a1 1 0 00-1-1H9z" clip-rule="evenodd"/></svg>
      <span>为安全起见，请手动设置新密码</span>
    </div>
    <el-form ref="formRef" :model="form" :rules="rules" label-width="0">
      <el-form-item prop="old_password">
        <el-input v-model="form.old_password" type="password" placeholder="当前密码" show-password size="large" prefix-icon="Lock" />
      </el-form-item>
      <el-form-item prop="new_password">
        <el-input v-model="form.new_password" type="password" placeholder="新密码（至少 8 位）" show-password size="large" prefix-icon="Key" />
      </el-form-item>
      <el-form-item prop="confirm_password">
        <el-input v-model="form.confirm_password" type="password" placeholder="确认新密码" show-password size="large" prefix-icon="Key" />
      </el-form-item>
    </el-form>
  </div>

  <template #footer>
    <div class="pwd-footer">
      <el-button v-if="smartMode" @click="regenerate" :disabled="loading" :icon="RefreshIcon" class="pwd-btn-refresh">重新随机</el-button>
      <el-button @click="close" :disabled="forced && loading">取消</el-button>
      <el-button type="primary" :loading="loading" @click="handleSubmit" class="pwd-btn-confirm">确认修改</el-button>
    </div>
  </template>
</el-dialog>`,
  data() {
    return {
      loading: false,
      generated: '',
      copied: false,
      RefreshIcon: ElementPlusIconsVue.Refresh,
      form: { old_password: '', new_password: '', confirm_password: '' },
      rules: {
        old_password: [
          { required: true, message: '请输入旧密码', trigger: 'blur' }
        ],
        new_password: [
          { required: true, message: '请输入新密码', trigger: 'blur' },
          { min: 8, message: '密码长度不能少于 8 位', trigger: 'blur' }
        ],
        confirm_password: [
          { required: true, message: '请再次输入新密码', trigger: 'blur' },
          {
            validator: (rule, value, callback) => {
              if (value !== this.form.new_password) {
                callback(new Error('两次输入的密码不一致'))
              } else {
                callback()
              }
            },
            trigger: 'blur'
          },
          {
            validator: (rule, value, callback) => {
              if (value && value === this.form.old_password) {
                callback(new Error('新密码不得与旧密码相同'))
              } else {
                callback()
              }
            },
            trigger: 'blur'
          }
        ]
      }
    }
  },
  computed: {
    smartMode() {
      return !!this.oldPassword
    }
  },
  methods: {
    generateRandom() {
      const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789!@#$%^&*'
      const tests = [/[A-Z]/, /[a-z]/, /[0-9]/, /[!@#$%^&*]/]
      let pwd
      do {
        pwd = ''
        for (let i = 0; i < 16; i++) {
          pwd += chars.charAt(Math.floor(Math.random() * chars.length))
        }
      } while (!tests.every(re => re.test(pwd)))
      this.generated = pwd
    },
    async copyToClipboard() {
      try {
        await navigator.clipboard.writeText(this.generated)
      } catch {
        const ta = document.createElement('textarea')
        ta.value = this.generated
        ta.style.cssText = 'position:fixed;left:-9999px;top:-9999px'
        document.body.appendChild(ta)
        ta.select()
        document.execCommand('copy')
        document.body.removeChild(ta)
      }
      this.copied = true
      clearTimeout(this._copyTimer)
      this._copyTimer = setTimeout(() => { this.copied = false }, 1800)
    },
    regenerate() {
      this.generateRandom()
      this.copyToClipboard()
    },
    close() {
      if (this.forced) return
      this.$emit('update:modelValue', false)
      this.resetForm()
    },
    resetForm() {
      this.form = { old_password: '', new_password: '', confirm_password: '' }
      this.generated = ''
      this.copied = false
      clearTimeout(this._copyTimer)
      this.$refs.formRef?.clearValidate()
    },
    async handleSubmit() {
      let oldPwd, newPwd
      if (this.smartMode) {
        oldPwd = this.oldPassword
        newPwd = this.generated
      } else {
        try {
          await this.$refs.formRef.validate()
        } catch { return }
        oldPwd = this.form.old_password
        newPwd = this.form.new_password
      }
      this.loading = true
      try {
        const res = await api.post('/auth/password', {
          old_password: oldPwd,
          new_password: newPwd
        })
        if (res.code === 0) {
          this.$emit('done')
          this.$emit('update:modelValue', false)
          this.resetForm()
        } else {
          ElMessage.error(res.message || '修改失败')
        }
      } catch (e) {
        const msg = e.response?.data?.message || e.message || '网络错误'
        ElMessage.error(msg)
      } finally {
        this.loading = false
      }
    }
  },
  watch: {
    modelValue(val) {
      if (val && this.smartMode) {
        this.generateRandom()
        this.copyToClipboard()
      }
      if (!val) this.resetForm()
    }
  }
}
