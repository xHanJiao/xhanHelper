<script setup>
import { ref, onMounted } from 'vue'
import { api } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { useMobile } from '@/composables/useMobile'
import {
  NDataTable,
  NSelect,
  NDatePicker,
  NPagination,
  useMessage
} from 'naive-ui'
import PageTopBar from '@/components/PageTopBar.vue'
import PageHeader from '@/components/PageHeader.vue'

const auth = useAuthStore()
const message = useMessage()
const { isMobile } = useMobile()

const bills = ref([])
const loading = ref(false)
const page = ref(1)
const pageSize = 20
const total = ref(0)

const filterDateRange = ref(null)
const filterCategory = ref(null)
const draftStart = ref(null)
const draftEnd = ref(null)
const draftCategory = ref(null)

// 本地年月日拼串：toISOString() 是 UTC，东八区会整体差一天
function ymd(t) {
  const d = new Date(t)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

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
      if (start) url += `&start_date=${ymd(start)}`
      if (end) url += `&end_date=${ymd(end)}`
    }
    if (filterCategory.value) url += `&category=${encodeURIComponent(filterCategory.value)}`

    const res = await api.get(url)
    bills.value = res.bills || []
    total.value = res.total || 0
  } catch (e) {
    message.error('加载账单列表失败')
  } finally {
    loading.value = false
  }
}

function handleFilterChange() {
  page.value = 1
  fetchBills()
}

function openFilter() {
  draftStart.value = filterDateRange.value?.[0] ?? null
  draftEnd.value = filterDateRange.value?.[1] ?? null
  draftCategory.value = filterCategory.value
}

function applyFilters() {
  if (draftStart.value && draftEnd.value && draftStart.value > draftEnd.value) {
    message.warning('开始日期不能晚于结束日期')
    return
  }
  filterDateRange.value = (draftStart.value || draftEnd.value) ? [draftStart.value, draftEnd.value] : null
  filterCategory.value = draftCategory.value
  handleFilterChange()
}

// 草稿式：只清面板里的草稿，点「查看结果」才提交，避免清一次拉一次
function clearFilters() {
  draftStart.value = null
  draftEnd.value = null
  draftCategory.value = null
}

// 胶囊行上的「清除」：已生效的筛选一次清掉
function resetFilters() {
  filterDateRange.value = null
  filterCategory.value = null
  handleFilterChange()
}

function formatRange(range) {
  if (!range) return ''
  const fmt = (t) => {
    const d = new Date(t)
    return `${d.getMonth() + 1}/${d.getDate()}`
  }
  // 允许只选一端；两端都有才画区间
  if (range[0] && range[1]) return `${fmt(range[0])} – ${fmt(range[1])}`
  return fmt(range[0] || range[1])
}

async function handleExport() {
  try {
    let url = `/api/bills/export`
    const params = []
    if (filterDateRange.value) {
      const [start, end] = filterDateRange.value
      if (start) params.push(`start_date=${ymd(start)}`)
      if (end) params.push(`end_date=${ymd(end)}`)
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
    a.download = `bills-${ymd(Date.now())}.csv`
    a.click()
    URL.revokeObjectURL(a.href)
    message.success('导出成功')
  } catch (e) {
    message.error('导出失败')
  }
}

onMounted(fetchBills)
</script>

<template>
  <div style="height: 100%; display: flex; flex-direction: column; background: transparent;">
    <PageTopBar v-if="!isMobile" />
    <div class="page-scroll">
      <PageHeader
        title="账单"
        filterable
        :filter-count="(filterCategory ? 1 : 0) + (filterDateRange ? 1 : 0)"
        @open-filter="openFilter"
        @apply="applyFilters"
        @clear="clearFilters"
        @reset="resetFilters"
      >
        <template #actions>
          <button v-if="!isMobile" class="page-action" type="button" @click="handleExport">导出</button>
        </template>

        <template #pills>
          <span v-if="filterCategory" class="filter-pill">
            {{ filterCategory }}
            <button type="button" aria-label="移除分类筛选" @click="filterCategory = null; handleFilterChange()">×</button>
          </span>
          <span v-if="filterDateRange" class="filter-pill">
            {{ formatRange(filterDateRange) }}
            <button type="button" aria-label="移除日期筛选" @click="filterDateRange = null; handleFilterChange()">×</button>
          </span>
        </template>

        <template #filters>
          <div class="filter-label">分类</div>
          <NSelect
            v-model:value="draftCategory"
            :options="categoryOptions"
            size="small"
            clearable
            placeholder="全部分类"
            style="width: 100%; margin-bottom: 16px;"
          />
          <div class="filter-label">日期范围</div>
          <div style="display: flex; align-items: center; gap: 8px;">
            <NDatePicker
              v-model:value="draftStart"
              type="date"
              clearable
              size="small"
              placeholder="开始日期"
              style="flex: 1; min-width: 0;"
            />
            <span style="color: var(--color-text-muted);">–</span>
            <NDatePicker
              v-model:value="draftEnd"
              type="date"
              clearable
              size="small"
              placeholder="结束日期"
              style="flex: 1; min-width: 0;"
            />
          </div>
        </template>
      </PageHeader>

      <NDataTable
        :columns="columns"
        :data="bills"
        :loading="loading"
        :row-key="rowKey"
        size="small"
        :scroll-x="800"
      />
      <div v-if="total > pageSize" style="margin-top: var(--space-md); display: flex; justify-content: center;">
        <NPagination
          v-model:page="page"
          :page-count="Math.ceil(total / pageSize)"
          @update:page="fetchBills"
        />
      </div>
    </div>
  </div>
</template>
