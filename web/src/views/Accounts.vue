<template>
  <el-card>
    <div style="margin-bottom:12px">
      <el-button v-if="auth.canManage" type="primary" @click="openCreate">新增账号</el-button>
      <el-button @click="load">刷新</el-button>
      <span style="margin-left:12px;color:#909399">可用会话：{{ available }}</span>
    </div>
    <el-table :data="list" border v-loading="loading">
      <el-table-column prop="id" label="ID" width="60" />
      <el-table-column prop="name" label="名称" />
      <el-table-column prop="status" label="状态" width="100">
        <template #default="{ row }">
          <el-tag :type="row.status === 'active' ? 'success' : row.status === 'expired' ? 'danger' : 'info'">{{ row.status }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="默认模型" width="220">
        <template #default="{ row }">
          <el-select v-model="row.default_model" :disabled="!auth.canManage" size="small" style="width:200px" @change="(v: string) => updateModel(row, v)">
            <el-option v-for="m in modelOptions" :key="m.value" :label="m.label" :value="m.value" />
          </el-select>
        </template>
      </el-table-column>
      <el-table-column label="池中" width="80">
        <template #default="{ row }">
          <el-tag v-if="row.in_pool" :type="row.healthy ? 'success' : 'danger'" size="small">{{ row.healthy ? '健康' : '异常' }}</el-tag>
          <span v-else>—</span>
        </template>
      </el-table-column>
      <el-table-column prop="last_used_at" label="上次使用" width="180">
        <template #default="{ row }">{{ fmt(row.last_used_at) }}</template>
      </el-table-column>
      <el-table-column v-if="auth.canManage" label="操作" width="320">
        <template #default="{ row }">
          <el-upload :show-file-list="false" :before-upload="(f:any) => upload(row, f)" accept=".json">
            <el-button text type="primary">上传登录态</el-button>
          </el-upload>
          <el-button text type="primary" @click="check(row)">健康检查</el-button>
          <el-button text type="warning" @click="toggle(row)">{{ row.status === 'active' ? '禁用' : '启用' }}</el-button>
          <el-button text type="danger" @click="del(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dialog" title="新增账号" width="420px">
      <el-form :model="form" label-width="80px">
        <el-form-item label="名称"><el-input v-model="form.name" /></el-form-item>
        <el-form-item label="备注"><el-input v-model="form.note" type="textarea" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialog=false">取消</el-button>
        <el-button type="primary" @click="create">创建</el-button>
      </template>
    </el-dialog>
  </el-card>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import http from '../api/http'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useAuth } from '../store/auth'

const auth = useAuth()
const list = ref<any[]>([])
const available = ref(0)
const loading = ref(false)
const dialog = ref(false)
const form = reactive({ name: '', note: '' })

// 模型选项
const modelOptions = [
  { label: '快速模式', value: 'deepseek-chat' },
  { label: '快速 + 深度思考', value: 'deepseek-chat-think' },
  { label: '快速 + 智能搜索', value: 'deepseek-chat-search' },
  { label: '快速 + 思考 + 搜索', value: 'deepseek-chat-think-search' },
  { label: '专家模式', value: 'deepseek-expert' },
  { label: '专家 + 深度思考', value: 'deepseek-expert-think' },
  { label: '专家 + 智能搜索', value: 'deepseek-expert-search' },
  { label: '专家 + 思考 + 搜索', value: 'deepseek-expert-think-search' },
  { label: '识图模式', value: 'deepseek-vision' },
]

async function load() {
  loading.value = true
  try {
    const { data } = await http.get('/admin/accounts')
    list.value = data.data
    available.value = data.available
  } finally { loading.value = false }
}
onMounted(load)

function openCreate() { form.name = ''; form.note = ''; dialog.value = true }
async function create() {
  if (!form.name) return ElMessage.warning('请输入名称')
  await http.post('/admin/accounts', { name: form.name, note: form.note, storage_path: '' })
  ElMessage.success('已创建，请上传登录态文件')
  dialog.value = false
  load()
}

async function updateModel(row: any, model: string) {
  try {
    await http.patch(`/admin/accounts/${row.id}`, { default_model: model })
    ElMessage.success(`默认模型已更新为 ${model}`)
  } catch (e) {
    ElMessage.error('更新失败')
    load()
  }
}

async function upload(row: any, file: File) {
  const fd = new FormData()
  fd.append('file', file)
  const { data } = await http.post(`/admin/accounts/${row.id}/storage-state`, fd)
  data.warning ? ElMessage.warning(`文件已保存，但热加载失败：${data.warning}`) : ElMessage.success('上传并热加载成功')
  load()
  return false
}

async function check(row: any) {
  ElMessage.info('正在检查，请稍候...')
  const { data } = await http.post(`/admin/accounts/${row.id}/health-check`)
  data.healthy ? ElMessage.success('登录态有效') : ElMessage.error('登录态已失效')
  load()
}

async function toggle(row: any) {
  const status = row.status === 'active' ? 'disabled' : 'active'
  await http.patch(`/admin/accounts/${row.id}`, { status })
  load()
}

async function del(row: any) {
  await ElMessageBox.confirm(`确定删除账号 ${row.name}?`, '确认', { type: 'warning' })
  await http.delete(`/admin/accounts/${row.id}`)
  load()
}
function fmt(s: string) { return s ? new Date(s).toLocaleString('zh-CN') : '—' }
</script>
