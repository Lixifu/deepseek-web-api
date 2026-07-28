<template>
  <el-card>
    <div style="margin-bottom:12px">
      <el-button type="primary" @click="openCreate">新增管理员</el-button>
      <el-button @click="load">刷新</el-button>
    </div>
    <el-table :data="list" border v-loading="loading">
      <el-table-column prop="id" label="ID" width="70" />
      <el-table-column prop="username" label="用户名" />
      <el-table-column label="角色" width="180">
        <template #default="{ row }">
          <el-select v-model="row.role" size="small" @change="(role:string) => update(row, { role })">
            <el-option label="只读" value="viewer" />
            <el-option label="管理员" value="admin" />
            <el-option label="超级管理员" value="superadmin" />
          </el-select>
        </template>
      </el-table-column>
      <el-table-column label="状态" width="120">
        <template #default="{ row }">
          <el-switch v-model="row.enabled" @change="(enabled:boolean) => update(row, { enabled })" />
        </template>
      </el-table-column>
      <el-table-column prop="last_login_at" label="最后登录" width="190">
        <template #default="{ row }">{{ formatTime(row.last_login_at) }}</template>
      </el-table-column>
      <el-table-column prop="created_at" label="创建时间" width="190">
        <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="120">
        <template #default="{ row }">
          <el-button text type="warning" @click="openReset(row)">重置密码</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="createDialog" title="新增管理员" width="450px">
      <el-form label-width="80px">
        <el-form-item label="用户名"><el-input v-model="createForm.username" /></el-form-item>
        <el-form-item label="密码"><el-input v-model="createForm.password" type="password" show-password /></el-form-item>
        <el-form-item label="角色">
          <el-select v-model="createForm.role" style="width:100%">
            <el-option label="只读" value="viewer" />
            <el-option label="管理员" value="admin" />
            <el-option label="超级管理员" value="superadmin" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createDialog=false">取消</el-button>
        <el-button type="primary" @click="createAdmin">创建</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="resetDialog" title="重置管理员密码" width="430px">
      <el-alert type="warning" :closable="false" title="重置后该管理员的所有现有令牌会立即失效" style="margin-bottom:14px" />
      <el-input v-model="resetPassword" type="password" show-password placeholder="至少 12 个字符" />
      <template #footer>
        <el-button @click="resetDialog=false">取消</el-button>
        <el-button type="primary" @click="resetAdminPassword">确认重置</el-button>
      </template>
    </el-dialog>
  </el-card>
</template>

<script setup lang="ts">
import { reactive, ref, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import http from '../api/http'

const list = ref<any[]>([])
const loading = ref(false)
const createDialog = ref(false)
const resetDialog = ref(false)
const resetTarget = ref<any>(null)
const resetPassword = ref('')
const createForm = reactive({ username: '', password: '', role: 'viewer' })

async function load() {
  loading.value = true
  try {
    const { data } = await http.get('/admin/admins')
    list.value = data.data
  } finally {
    loading.value = false
  }
}
onMounted(load)

function openCreate() {
  Object.assign(createForm, { username: '', password: '', role: 'viewer' })
  createDialog.value = true
}

async function createAdmin() {
  if (!createForm.username) return ElMessage.warning('请输入用户名')
  if (createForm.password.length < 12) return ElMessage.warning('密码至少 12 个字符')
  await http.post('/admin/admins', createForm)
  ElMessage.success('管理员已创建')
  createDialog.value = false
  load()
}

async function update(row: any, changes: Record<string, any>) {
  try {
    await http.patch(`/admin/admins/${row.id}`, changes)
    ElMessage.success('已更新，原登录令牌已失效')
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error || '更新失败')
    load()
  }
}

function openReset(row: any) {
  resetTarget.value = row
  resetPassword.value = ''
  resetDialog.value = true
}

async function resetAdminPassword() {
  if (resetPassword.value.length < 12) return ElMessage.warning('密码至少 12 个字符')
  await http.patch(`/admin/admins/${resetTarget.value.id}`, { password: resetPassword.value })
  ElMessage.success('密码已重置')
  resetDialog.value = false
}

function formatTime(value: string) {
  return value ? new Date(value).toLocaleString('zh-CN') : '—'
}
</script>
