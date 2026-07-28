<template>
  <div>
    <el-row :gutter="16">
      <el-col :span="6" v-for="c in cards" :key="c.label" style="margin-bottom:16px">
        <el-card shadow="hover">
          <div class="card-label">{{ c.label }}</div>
          <div class="card-value">{{ c.value }}</div>
        </el-card>
      </el-col>
    </el-row>
    <el-card style="margin-top:16px">
      <template #header>调用统计</template>
      <el-table :data="stat ? [stat] : []" border>
        <el-table-column prop="total_calls" label="总调用" />
        <el-table-column prop="success_calls" label="成功" />
        <el-table-column prop="failed_calls" label="失败" />
        <el-table-column label="成功率">
          <template #default="{ row }">
            {{ row.total_calls ? ((row.success_calls / row.total_calls) * 100).toFixed(1) : '0' }}%
          </template>
        </el-table-column>
        <el-table-column prop="active_accounts" label="活跃账号" />
        <el-table-column prop="total_accounts" label="总账号" />
      </el-table>
    </el-card>
    <el-card style="margin-top:16px">
      <template #header>Prometheus 运行时指标（自本进程启动）</template>
      <el-descriptions :column="4" border>
        <el-descriptions-item label="平均调用耗时">{{ Number(metrics.average_latency_ms || 0).toFixed(0) }} ms</el-descriptions-item>
        <el-descriptions-item label="运行时成功率">{{ Number(metrics.success_rate || 0).toFixed(1) }}%</el-descriptions-item>
        <el-descriptions-item label="队列长度">{{ metrics.queue_length || 0 }}</el-descriptions-item>
        <el-descriptions-item label="浏览器内存">{{ formatBytes(metrics.browser_memory_bytes || 0) }}</el-descriptions-item>
        <el-descriptions-item label="健康账号">{{ metrics.account_healthy || 0 }} / {{ metrics.account_total || 0 }}</el-descriptions-item>
        <el-descriptions-item label="忙碌账号">{{ metrics.account_busy || 0 }}</el-descriptions-item>
        <el-descriptions-item label="进程内成功">{{ metrics.success_calls || 0 }}</el-descriptions-item>
        <el-descriptions-item label="进程内失败">{{ metrics.failed_calls || 0 }}</el-descriptions-item>
      </el-descriptions>
      <div style="margin-top:10px;color:#909399;font-size:12px">Prometheus 抓取地址：/metrics</div>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import http from '../api/http'

const stat = ref<any>(null)
const available = ref(0)
const metrics = ref<any>({})

async function load() {
  const { data } = await http.get('/admin/dashboard')
  stat.value = data.stats
  available.value = data.available
  const runtime = await http.get('/admin/metrics')
  metrics.value = runtime.data.metrics || {}
}
onMounted(load)
const timer = setInterval(load, 5000)
onUnmounted(() => clearInterval(timer))

const cards = computed(() => [
  { label: '可用会话', value: available.value },
  { label: '总调用', value: stat.value?.total_calls ?? 0 },
  { label: '成功调用', value: stat.value?.success_calls ?? 0 },
  { label: '活跃账号', value: stat.value?.active_accounts ?? 0 },
  { label: '平均耗时', value: `${Number(metrics.value.average_latency_ms || 0).toFixed(0)} ms` },
  { label: '成功率', value: `${Number(metrics.value.success_rate || 0).toFixed(1)}%` },
  { label: '等待队列', value: metrics.value.queue_length ?? 0 },
  { label: '浏览器内存', value: formatBytes(metrics.value.browser_memory_bytes || 0) }
])

function formatBytes(bytes: number) {
  if (!bytes) return '0 MB'
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}
</script>

<style scoped>
.card-label { color:#909399; font-size:14px; }
.card-value { font-size:28px; font-weight:bold; margin-top:8px; }
</style>
