<template>
  <el-card>
    <div style="margin-bottom:12px">
      <el-button v-if="auth.canManage" type="primary" @click="openCreate">生成 API Key</el-button>
      <el-button @click="load">刷新</el-button>
    </div>
    <el-table :data="list" border v-loading="loading">
      <el-table-column prop="id" label="ID" width="60" />
      <el-table-column prop="name" label="名称" />
      <el-table-column label="Key" width="200">
        <template #default="{ row }">
          <span style="font-family:monospace">sk-dsk-{{ row.key_prefix }}…</span>
        </template>
      </el-table-column>
      <el-table-column label="今日用量" width="120">
        <template #default="{ row }">
          <el-tooltip effect="dark" placement="top">
            <template #content>
              成功 {{ row.success_cnt }} / 失败 {{ row.failed_cnt }}
            </template>
            <span>{{ row.today_used }}</span>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column label="剩余额度" width="100">
        <template #default="{ row }">
          <span v-if="row.remaining < 0" style="color:#909399">不限</span>
          <span v-else-if="row.remaining === 0" style="color:#f56c6c;font-weight:600">0</span>
          <span v-else>{{ row.remaining }}</span>
        </template>
      </el-table-column>
      <el-table-column label="日配额" width="130">
        <template #default="{ row }">
          <span>{{ row.quota_per_day > 0 ? row.quota_per_day : '不限' }}</span>
          <el-button v-if="auth.canManage" text type="primary" size="small" @click="openQuota(row)" style="margin-left:6px">修改</el-button>
        </template>
      </el-table-column>
      <el-table-column label="绑定模型" width="200">
        <template #default="{ row }">
          <el-select
            v-model="row.default_model"
            :disabled="!auth.canManage"
            placeholder="不绑定"
            size="small"
            style="width:100%"
            @change="(v: string) => updateModel(row, v)"
          >
            <el-option label="不绑定（用请求的 model）" value="" />
            <el-option
              v-for="m in modelOptions"
              :key="m.value"
              :label="m.label"
              :value="m.value"
            />
          </el-select>
        </template>
      </el-table-column>
      <el-table-column prop="allowed_models" label="可用模型" min-width="120" />
      <el-table-column label="状态" width="90">
        <template #default="{ row }">
          <el-tag :type="row.enabled ? 'success' : 'info'">{{ row.enabled ? '启用' : '已停用' }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="created_at" label="创建时间" width="170">
        <template #default="{ row }">{{ fmt(row.created_at) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="200" fixed="right">
        <template #default="{ row }">
          <el-button text type="primary" @click="openUsage(row)">用量</el-button>
          <el-button v-if="auth.canManage" text type="warning" @click="toggle(row)">{{ row.enabled ? '停用' : '启用' }}</el-button>
          <el-button v-if="auth.canManage" text type="danger" @click="del(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dialog" title="生成 API Key" width="480px">
      <el-form :model="form" label-width="90px">
        <el-form-item label="名称"><el-input v-model="form.name" /></el-form-item>
        <el-form-item label="日配额">
          <el-input-number v-model="form.quota_per_day" :min="0" />
          <span style="margin-left:8px;color:#909399;font-size:12px">0 表示不限</span>
        </el-form-item>
        <el-form-item label="绑定模型">
          <el-select v-model="form.default_model" placeholder="不绑定（用请求的 model）" clearable style="width:100%">
            <el-option
              v-for="m in modelOptions"
              :key="m.value"
              :label="m.label"
              :value="m.value"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="可用模型">
          <el-checkbox-group v-model="form.allowed_models">
            <el-checkbox
              v-for="m in modelOptions"
              :key="m.value"
              :label="m.value"
            >{{ m.label }}</el-checkbox>
          </el-checkbox-group>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialog=false">取消</el-button>
        <el-button type="primary" @click="create">生成</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="keyDialog" title="API Key 已生成" width="500px">
      <el-alert type="warning" :closable="false" title="该 Key 仅显示一次，请立即复制保存！" style="margin-bottom:12px" />
      <el-input v-model="newKey" readonly>
        <template #append><el-button @click="copy">复制</el-button></template>
      </el-input>
    </el-dialog>

    <el-dialog v-model="quotaDialog" title="修改日配额" width="420px">
      <el-form label-width="90px">
        <el-form-item label="Key 名称">
          <span>{{ quotaTarget?.name }}</span>
        </el-form-item>
        <el-form-item label="当前配额">
          <span>{{ quotaTarget?.quota_per_day ? quotaTarget.quota_per_day : '不限' }}</span>
        </el-form-item>
        <el-form-item label="新配额">
          <el-input-number v-model="quotaInput" :min="0" />
          <span style="margin-left:8px;color:#909399;font-size:12px">0 表示不限</span>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="quotaDialog=false">取消</el-button>
        <el-button type="primary" @click="saveQuota">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="usageDialog" :title="`用量趋势 - ${usageTarget?.name || ''}`" width="760px" @opened="renderChart">
      <div v-if="usageLoading" v-loading="true" style="height:320px"></div>
      <div v-else>
        <el-descriptions :column="4" border size="small" style="margin-bottom:12px">
          <el-descriptions-item label="今日已用">{{ usageSummary.today_used }}</el-descriptions-item>
          <el-descriptions-item label="成功">{{ usageSummary.success }}</el-descriptions-item>
          <el-descriptions-item label="失败">{{ usageSummary.failed }}</el-descriptions-item>
          <el-descriptions-item label="日配额">
            {{ usageSummary.quota > 0 ? usageSummary.quota : '不限' }}
            <span v-if="usageSummary.remaining >= 0" style="margin-left:6px;color:#909399">（剩余 {{ usageSummary.remaining }}）</span>
          </el-descriptions-item>
        </el-descriptions>
        <div ref="chartRef" style="width:100%;height:320px"></div>
      </div>
    </el-dialog>
  </el-card>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted, onBeforeUnmount, nextTick } from 'vue'
import * as echarts from 'echarts/core'
import type { ECharts } from 'echarts/core'
import { BarChart } from 'echarts/charts'
import { GridComponent, LegendComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import http from '../api/http'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useAuth } from '../store/auth'

echarts.use([BarChart, GridComponent, LegendComponent, TooltipComponent, CanvasRenderer])

const auth = useAuth()
const list = ref<any[]>([])
const loading = ref(false)
const dialog = ref(false)
const keyDialog = ref(false)
const newKey = ref('')
const form = reactive({
  name: '',
  quota_per_day: 1000,
  allowed_models: ['deepseek-chat', 'deepseek-reasoner'],
  default_model: '',
})

// 模型选项（与后端 SupportedModels() 对齐）
const modelOptions = [
  { value: 'deepseek-chat', label: '快速模式' },
  { value: 'deepseek-reasoner', label: 'DeepSeek Reasoner（深度思考兼容名）' },
  { value: 'deepseek-chat-think', label: '快速 + 深度思考' },
  { value: 'deepseek-chat-search', label: '快速 + 联网搜索' },
  { value: 'deepseek-chat-think-search', label: '快速 + 深度思考 + 联网搜索' },
  { value: 'deepseek-expert', label: '专家模式' },
  { value: 'deepseek-expert-think', label: '专家 + 深度思考' },
  { value: 'deepseek-expert-search', label: '专家 + 联网搜索' },
  { value: 'deepseek-expert-think-search', label: '专家 + 深度思考 + 联网搜索' },
  { value: 'deepseek-vision', label: '识图模式' },
]

// 配额修改
const quotaDialog = ref(false)
const quotaTarget = ref<any>(null)
const quotaInput = ref(1000)

// 用量趋势
const usageDialog = ref(false)
const usageLoading = ref(false)
const usageTarget = ref<any>(null)
const usageSummary = reactive({ today_used: 0, success: 0, failed: 0, quota: 0, remaining: -1 })
const chartRef = ref<HTMLElement | null>(null)
let chartInst: ECharts | null = null

async function load() {
  loading.value = true
  try {
    const { data } = await http.get('/admin/api-keys')
    list.value = data.data
  } finally { loading.value = false }
}
onMounted(load)

onBeforeUnmount(() => {
  if (chartInst) { chartInst.dispose(); chartInst = null }
})

function openCreate() {
  form.name = ''
  form.quota_per_day = 1000
  form.allowed_models = ['deepseek-chat', 'deepseek-reasoner']
  form.default_model = ''
  dialog.value = true
}
async function create() {
  if (!form.name) return ElMessage.warning('请输入名称')
  const { data } = await http.post('/admin/api-keys', { ...form })
  newKey.value = data.key
  dialog.value = false
  keyDialog.value = true
  load()
}
function copy() {
  navigator.clipboard.writeText(newKey.value)
  ElMessage.success('已复制')
}
async function toggle(row: any) {
  await http.patch(`/admin/api-keys/${row.id}`, { enabled: !row.enabled })
  load()
}
async function updateModel(row: any, v: string) {
  try {
    await http.patch(`/admin/api-keys/${row.id}`, { default_model: v })
    ElMessage.success(v ? `已绑定模型：${modelLabel(v)}` : '已取消绑定')
  } catch {
    ElMessage.error('更新失败')
    load()
  }
}
function modelLabel(v: string): string {
  const m = modelOptions.find(o => o.value === v)
  return m ? m.label : v
}
async function del(row: any) {
  await ElMessageBox.confirm(`确定删除 Key ${row.name}?`, '确认', { type: 'warning' })
  await http.delete(`/admin/api-keys/${row.id}`)
  load()
}

function openQuota(row: any) {
  quotaTarget.value = row
  quotaInput.value = row.quota_per_day > 0 ? row.quota_per_day : 0
  quotaDialog.value = true
}
async function saveQuota() {
  if (!quotaTarget.value) return
  try {
    await http.patch(`/admin/api-keys/${quotaTarget.value.id}`, { quota_per_day: quotaInput.value })
    ElMessage.success('配额已更新')
    quotaDialog.value = false
    load()
  } catch {
    ElMessage.error('更新失败')
  }
}

async function openUsage(row: any) {
  usageTarget.value = row
  usageDialog.value = true
  usageLoading.value = true
  try {
    const { data } = await http.get(`/admin/api-keys/${row.id}/usage?days=7`)
    usageSummary.today_used = data.today_used
    usageSummary.success = data.success
    usageSummary.failed = data.failed
    usageSummary.quota = data.quota
    usageSummary.remaining = data.remaining
    pendingPoints = data.points
  } catch {
    ElMessage.error('加载用量失败')
    pendingPoints = []
  } finally {
    usageLoading.value = false
  }
}

let pendingPoints: any[] = []
function renderChart() {
  nextTick(() => {
    if (!chartRef.value) return
    if (chartInst) { chartInst.dispose() }
    chartInst = echarts.init(chartRef.value)
    const hours = pendingPoints.map((p: any) => fmtHour(p.hour))
    const success = pendingPoints.map((p: any) => p.success)
    const failed = pendingPoints.map((p: any) => p.failed)
    chartInst.setOption({
      tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' } },
      legend: { data: ['成功', '失败'] },
      grid: { left: 40, right: 20, top: 40, bottom: 60 },
      xAxis: { type: 'category', data: hours, axisLabel: { rotate: 45 } },
      yAxis: { type: 'value', minInterval: 1 },
      series: [
        { name: '成功', type: 'bar', stack: 'u', data: success, itemStyle: { color: '#67c23a' } },
        { name: '失败', type: 'bar', stack: 'u', data: failed, itemStyle: { color: '#f56c6c' } },
      ],
    })
  })
}

function fmt(s: string) { return s ? new Date(s).toLocaleString('zh-CN') : '' }
function fmtHour(s: string) {
  if (!s) return ''
  const d = new Date(s)
  return `${d.getMonth()+1}/${d.getDate()} ${String(d.getHours()).padStart(2,'0')}:00`
}
</script>
