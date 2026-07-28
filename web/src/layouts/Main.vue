<template>
  <el-container style="height:100vh">
    <el-aside width="220px" style="background:#001529">
      <div class="logo">DeepSeek API</div>
      <el-menu :default-active="route.path" router background-color="#001529" text-color="#fff" active-text-color="#409eff">
        <el-menu-item index="/dashboard"><el-icon><Monitor /></el-icon><span>仪表盘</span></el-menu-item>
        <el-menu-item index="/conversations"><el-icon><ChatDotRound /></el-icon><span>对话记录</span></el-menu-item>
        <el-menu-item index="/accounts"><el-icon><User /></el-icon><span>DeepSeek 账号</span></el-menu-item>
        <el-menu-item index="/api-keys"><el-icon><Key /></el-icon><span>API Key</span></el-menu-item>
        <el-menu-item v-if="auth.isSuperAdmin" index="/admins"><el-icon><Setting /></el-icon><span>管理员</span></el-menu-item>
        <el-menu-item v-if="auth.isSuperAdmin" index="/audit-logs"><el-icon><Document /></el-icon><span>审计日志</span></el-menu-item>
      </el-menu>
    </el-aside>
    <el-container>
      <el-header style="display:flex;align-items:center;justify-content:flex-end;background:#fff;border-bottom:1px solid #eee">
        <span style="margin-right:auto">
          可用会话：<el-tag type="success">{{ available }}</el-tag>
          <span style="margin-left:12px">排队：<el-tag :type="queued ? 'warning' : 'info'">{{ queued }}</el-tag></span>
        </span>
        <span style="margin-right:16px">{{ auth.username }}（{{ roleLabel }}）</span>
        <el-button text @click="passwordDialog=true">修改密码</el-button>
        <el-button text @click="onLogout">退出</el-button>
      </el-header>
      <el-main><router-view /></el-main>
    </el-container>
    <el-dialog v-model="passwordDialog" title="修改密码" width="430px">
      <el-form label-width="90px">
        <el-form-item label="当前密码"><el-input v-model="passwordForm.current" type="password" show-password /></el-form-item>
        <el-form-item label="新密码"><el-input v-model="passwordForm.next" type="password" show-password /></el-form-item>
        <el-form-item label="确认密码"><el-input v-model="passwordForm.confirm" type="password" show-password /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="passwordDialog=false">取消</el-button>
        <el-button type="primary" @click="changePassword">保存并重新登录</el-button>
      </template>
    </el-dialog>
  </el-container>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Monitor, ChatDotRound, User, Key, Setting, Document } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import http from '../api/http'
import { useAuth } from '../store/auth'

const route = useRoute()
const router = useRouter()
const auth = useAuth()
const available = ref(0)
const queued = ref(0)
const passwordDialog = ref(false)
const passwordForm = reactive({ current: '', next: '', confirm: '' })
const roleLabel = computed(() => ({
  superadmin: '超级管理员',
  admin: '管理员',
  viewer: '只读'
}[auth.role] || auth.role))

async function refresh() {
  try {
    const { data } = await http.get('/healthz')
    available.value = data.available ?? 0
    const metrics = await http.get('/admin/metrics')
    queued.value = metrics.data.metrics?.queue_length ?? 0
  } catch {}
}
onMounted(refresh)
const timer = setInterval(refresh, 5000)
onUnmounted(() => clearInterval(timer))

async function changePassword() {
  if (passwordForm.next.length < 12) return ElMessage.warning('新密码至少 12 个字符')
  if (passwordForm.next !== passwordForm.confirm) return ElMessage.warning('两次输入的新密码不一致')
  await http.post('/admin/change-password', {
    current_password: passwordForm.current,
    new_password: passwordForm.next,
  })
  ElMessage.success('密码已修改，请重新登录')
  passwordDialog.value = false
  onLogout()
}

function onLogout() {
  auth.logout()
  router.push('/login')
}
</script>

<style scoped>
.logo { color:#fff; font-size:18px; text-align:center; padding:20px 0; font-weight:bold; }
</style>
