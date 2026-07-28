<template>
  <div>
    <el-card>
      <el-form inline @submit.prevent="load">
        <el-form-item label="API Key ID"><el-input v-model.number="q.api_key_id" clearable style="width:140px" /></el-form-item>
        <el-form-item label="账号 ID"><el-input v-model.number="q.account_id" clearable style="width:140px" /></el-form-item>
        <el-button type="primary" @click="load">查询</el-button>
      </el-form>
      <el-table :data="list" border v-loading="loading" style="margin-top:12px">
        <el-table-column prop="id" label="ID" width="300" />
        <el-table-column prop="model" label="模型" width="140" />
        <el-table-column prop="api_key_id" label="API Key" width="100" />
        <el-table-column prop="account_id" label="账号" width="80" />
        <el-table-column prop="status" label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="row.status === 'success' ? 'success' : row.status === 'failed' ? 'danger' : 'info'">{{ row.status }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="duration_ms" label="耗时(ms)" width="100" />
        <el-table-column prop="created_at" label="时间" width="180">
          <template #default="{ row }">{{ fmt(row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="100">
          <template #default="{ row }">
            <el-button text type="primary" @click="view(row)">详情</el-button>
          </template>
        </el-table-column>
      </el-table>
      <el-pagination style="margin-top:12px;justify-content:flex-end;display:flex"
        v-model:current-page="q.page" v-model:page-size="q.size" :total="total" layout="prev, pager, next, total" @current-change="load" />
    </el-card>

    <el-drawer v-model="drawer" title="对话详情" size="50%">
      <div v-if="cur">
        <h4>提示词</h4>
        <pre class="pre-box">{{ cur.prompt }}</pre>
        <h4>回复</h4>
        <pre class="pre-box">{{ cur.reply || '(空)' }}</pre>
        <p v-if="cur.error" style="color:#f56c6c">错误：{{ cur.error }}</p>
      </div>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { reactive, ref, onMounted } from 'vue'
import http from '../api/http'

const list = ref<any[]>([])
const total = ref(0)
const loading = ref(false)
const q = reactive({ page: 1, size: 20, api_key_id: undefined as number | undefined, account_id: undefined as number | undefined })
const drawer = ref(false)
const cur = ref<any>(null)

async function load() {
  loading.value = true
  try {
    const { data } = await http.get('/admin/conversations', { params: { ...q } })
    list.value = data.data
    total.value = data.total
  } finally { loading.value = false }
}
onMounted(load)

async function view(row: any) {
  const { data } = await http.get(`/admin/conversations/${row.id}`)
  cur.value = data
  drawer.value = true
}
function fmt(s: string) { return s ? new Date(s).toLocaleString('zh-CN') : '' }
</script>

<style scoped>
.pre-box { background:#f5f7fa; padding:12px; border-radius:4px; white-space:pre-wrap; word-wrap:break-word; max-height:300px; overflow:auto; }
</style>
