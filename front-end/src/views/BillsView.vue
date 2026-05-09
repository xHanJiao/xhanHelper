<script setup>
import { ref, onMounted } from 'vue'
import { api } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import {
  NDataTable,
  NButton,
  NSelect,
  NDatePicker,
  NPagination,
  NUpload,
  useMessage
} from 'naive-ui'

const auth = useAuthStore()
const message = useMessage()

const bills = ref([])
const loading = ref(false)
const page = ref(1)
const pageSize = 20
const total = ref(0)

const filterDateRange = ref(null)
const filterCategory = ref(null)

const categoryOptions = [
  { label: '全部', value: null },
  { label: '餐饮', value: '餐饮' },
  { label: '服装', value: '服装' },
  { label: '交通', value: '交通' },
  { label: '居住', value: '居住' },
  { label: '旅游', value: '旅游' },
  { label: '美容', value: '美容' },
  { label: '人情', value: '人情' },
  { label: '日用', value: '日用' },
  { label: '数码', value: '数码' },
  { label: '学习', value: '学习' },
  { label: '医疗', value: '医疗' },
  { label: '娱乐', value: '娱乐' }
]

const columns = [
  { title: '项目', key: 'item', width: 140, ellipsis: { tooltip: true } },
  { title: '金额', key: 'amount', width: 80, render: (row) => `¥${row.amount?.toFixed(2)}` },
  { title: '一级分类', key: 'category1', width: 90 },
  { title: '二级分类', key: 'category2', width: 90 },
  { title: '消费类型', key: 'consume_type', width: 90 },
  { title: '日期', key: 'date', width: 110 },
  { title: '支付方式', key: 'pay_type', width: 90 },
  { title: '归属人', key: 'owner', width: 80 },
  { title: '备注', key: 'remark', width: 120, ellipsis: { tooltip: true } }
]

function rowKey(row) { return row.id }

async function fetchBills() {
  loading.value = true
  try {
    const offset = (page.value - 1) * pageSize
    let url = `/api/bills?limit=${pageSize}&offset=${offset}`
    if (filterDateRange.value) {
      const [start, end] = filterDateRange.value
      if (start) url += `&start_date=${new Date(start).toISOString().split('T')[0]}`
      if (end) url += `&end_date=${new Date(end).toISOString().split('T')[0]}`
    }
    if (filterCategory.value) url += `&category=${encodeURIComponent(filterCategory.value)}`

    const res = await api.get(url)
    bills.value = Array.isArray(res) ? res : (res.bills || [])
    total.value = res.total || bills.value.length
  } catch (e) {
    message.error('加载账单列表失败')
  } finally {
    loading.value = false
  }
}

function handleFilter() {
  page.value = 1
  fetchBills()
}

async function handleExport() {
  try {
    let url = `/api/bills/export`
    const params = []
    if (filterDateRange.value) {
      const [start, end] = filterDateRange.value
      if (start) params.push(`start_date=${new Date(start).toISOString().split('T')[0]}`)
      if (end) params.push(`end_date=${new Date(end).toISOString().split('T')[0]}`)
    }
    if (filterCategory.value) params.push(`category=${encodeURIComponent(filterCategory.value)}`)
    if (params.length) url += '?' + params.join('&')

    const res = await fetch(url, {
      headers: { 'Authorization': `Bearer ${auth.token}` }
    })
    if (!res.ok) throw new Error(`HTTP ${res.status}`)

    const blob = await res.blob()
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = `bills-${new Date().toISOString().split('T')[0]}.csv`
    a.click()
    URL.revokeObjectURL(a.href)
    message.success('导出成功')
  } catch (e) {
    message.error('导出失败')
  }
}

function handleImport({ file }) {
  const reader = new FileReader()
  reader.onload = async (e) => {
    try {
      const res = await api.postRaw('/api/bills/import', e.target.result, 'text/plain')
      if (res.imported !== undefined) {
        message.success(`导入完成：成功 ${res.imported} 条，跳过 ${res.skipped} 条`)
      }
      fetchBills()
    } catch (err) {
      message.error('导入失败')
    }
  }
  reader.readAsText(file.file)
}

onMounted(fetchBills)
</script>

<template>
  <div style="height: 100vh; display: flex; flex-direction: column; background: var(--color-bg-page);">
    <div style="padding: 16px 24px; border-bottom: 1px solid var(--color-border); background: var(--color-surface);">
      <div style="display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 12px;">
        <span style="font-size: 16px; font-weight: 500;">账单管理</span>
        <div style="display: flex; align-items: center; gap: 12px; flex-wrap: wrap;">
          <NDatePicker
            v-model:value="filterDateRange"
            type="daterange"
            clearable
            style="width: 240px;"
            placeholder="选择日期范围"
          />
          <NSelect
            v-model:value="filterCategory"
            :options="categoryOptions"
            style="width: 120px;"
            placeholder="分类"
          />
          <NButton size="small" @click="handleFilter">筛选</NButton>
          <NUpload :show-file-list="false" accept=".csv" @change="handleImport">
            <NButton size="small">导入CSV</NButton>
          </NUpload>
          <NButton size="small" @click="handleExport">导出CSV</NButton>
        </div>
      </div>
    </div>
    <div style="flex: 1; overflow-y: auto; padding: 24px;">
      <NDataTable
        :columns="columns"
        :data="bills"
        :loading="loading"
        :row-key="rowKey"
        size="small"
        flex-height
      />
      <div v-if="total > pageSize" style="margin-top: 16px; display: flex; justify-content: center;">
        <NPagination
          v-model:page="page"
          :page-count="Math.ceil(total / pageSize)"
          @update:page="fetchBills"
        />
      </div>
    </div>
  </div>
</template>
