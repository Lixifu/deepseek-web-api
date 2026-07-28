<template>
  <el-card>
    <div style="display:flex;gap:10px;margin-bottom:12px">
      <el-input-number v-model="adminID" :min="0" placeholder="管理员 ID" />
      <el-select v-model="scope" style="width:140px">
        <el-option label="全部日志" value="all" />
        <el-option label="在线日志" value="active" />
        <el-option label="归档日志" value="archive" />
      </el-select>
      <el-select v-model="action" clearable placeholder="操作" style="width:150px">
        <el-option label="POST" value="post" />
        <el-option label="PATCH" value="patch" />
        <el-option label="DELETE" value="delete" />
        <el-option label="EXPORT" value="export" />
      </el-select>
      <el-button type="primary" @click="search">查询</el-button>
      <el-button @click="reset">重置</el-button>
      <el-button @click="exportLogs('csv')">导出 CSV</el-button>
      <el-button @click="exportLogs('json')">导出 JSON</el-button>
      <el-button type="warning" @click="archiveNow">立即归档</el-button>
    </div>
    <el-table :data="list" border v-loading="loading">
      <el-table-column prop="created_at" label="时间" width="190">
        <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
      </el-table-column>
      <el-table-column prop="admin_name" label="管理员" width="140" />
      <el-table-column prop="action" label="操作" width="90">
        <template #default="{ row }"><el-tag>{{ row.action.toUpperCase() }}</el-tag></template>
      </el-table-column>
      <el-table-column prop="resource" label="资源" width="130" />
      <el-table-column prop="resource_id" label="资源 ID" width="100" />
      <el-table-column prop="path" label="请求路径" min-width="230" />
      <el-table-column prop="client_ip" label="IP" width="150" />
      <el-table-column prop="status" label="状态码" width="90">
        <template #default="{ row }">
          <el-tag :type="row.status < 400 ? 'success' : 'danger'">{{ row.status }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="存储" width="90">
        <template #default="{ row }">
          <el-tag :type="row.archived ? 'info' : 'success'">{{ row.archived ? '归档' : '在线' }}</el-tag>
        </template>
      </el-table-column>
    </el-table>
    <el-pagination
      v-model:current-page="page"
      :page-size="size"
      :total="total"
      layout="total, prev, pager, next"
      style="margin-top:14px"
      @current-change="load"
    />
  </el-card>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import http from '../api/http'

const list = ref<any[]>([])
const loading = ref(false)
const adminID = ref(0)
const action = ref('')
const scope = ref('all')
const page = ref(1)
const size = 50
const total = ref(0)

async function load() {
  loading.value = true
  try {
    const { data } = await http.get('/admin/audit-logs', {
      params: {
        page: page.value,
        size,
        scope: scope.value,
        admin_id: adminID.value || undefined,
        action: action.value || undefined,
      },
    })
    list.value = data.data
    total.value = data.total
  } finally {
    loading.value = false
  }
}
onMounted(load)

function search() {
  page.value = 1
  load()
}
function reset() {
  adminID.value = 0
  action.value = ''
  scope.value = 'all'
  search()
}

function filterParams() {
  return {
    scope: scope.value,
    admin_id: adminID.value || undefined,
    action: action.value || undefined,
  }
}

async function exportLogs(format: 'csv' | 'json') {
  const response = await http.get('/admin/audit-logs/export', {
    params: { ...filterParams(), format },
    responseType: 'blob',
  })
  const url = URL.createObjectURL(response.data)
  const link = document.createElement('a')
  link.href = url
  link.download = `audit-logs-${new Date().toISOString().replace(/[:.]/g, '-')}.${format}`
  document.body.appendChild(link)
  link.click()
  link.remove()
  setTimeout(() => URL.revokeObjectURL(url), 0)
  if (response.headers['x-export-truncated'] === 'true') {
    ElMessage.warning('日志数量超过单次导出上限，文件仅包含前一部分记录')
  }
}

async function archiveNow() {
  await ElMessageBox.confirm('将达到归档期限的在线审计日志移入归档表，是否继续？', '立即归档')
  const { data } = await http.post('/admin/audit-logs/archive')
  if (data.skipped) {
    ElMessage.info('其他实例正在执行归档，请稍后刷新')
  } else {
    ElMessage.success(`已归档 ${data.archived} 条，清理 ${data.deleted} 条`)
  }
  load()
}
function formatTime(value: string) {
  return value ? new Date(value).toLocaleString('zh-CN') : '—'
}
</script>
