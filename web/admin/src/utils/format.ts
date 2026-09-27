import dayjs from 'dayjs'

// 后端时间字段不统一: 既有 unix 秒(int64), 也有毫秒, 还有 'yyyy-MM-dd' 字符串.
// 统一入口避免每个页面各写一遍 dayjs.unix 导致秒/毫秒串味(表现为 1970 或 5xxxx 年).
export function fmtTime(v?: number | string | null, pattern = 'YYYY-MM-DD HH:mm'): string {
  if (v === undefined || v === null || v === '') return '-'
  if (typeof v === 'number') {
    // 1e12 约等于 2001 年的毫秒时间戳, 小于它只可能是秒级时间戳.
    const ms = v < 1e12 ? v * 1000 : v
    return dayjs(ms).format(pattern)
  }
  const d = dayjs(v)
  return d.isValid() ? d.format(pattern) : String(v)
}

// 金额在后端一律用 decimal 字符串承载(避免浮点精度丢失), 前端只做展示层格式化.
export function fmtMoney(v?: string | number | null): string {
  if (v === undefined || v === null || v === '') return '-'
  const n = typeof v === 'number' ? v : Number(v)
  if (!Number.isFinite(n)) return String(v)
  return n.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

export function fmtNumber(v?: number | null, fraction = 2): string {
  if (v === undefined || v === null || !Number.isFinite(v)) return '-'
  return v.toLocaleString('zh-CN', {
    minimumFractionDigits: 0,
    maximumFractionDigits: fraction,
  })
}

// 同比/环比等可能为 null(无历史数据), 统一渲染为占位符而不是 'NaN%'.
export function fmtPercent(v?: number | null, fraction = 1): string {
  if (v === undefined || v === null || !Number.isFinite(v)) return '—'
  return `${(v * 100).toFixed(fraction)}%`
}

export interface OptionItem<T = number> {
  value: T
  label: string
}

// 枚举 Map -> antd Select options. 支持 string 与 {label} 两种枚举写法.
export function toOptions<T extends string | number>(
  map: Record<string | number, string | { label: string }>,
): OptionItem<T>[] {
  return Object.entries(map).map(([k, v]) => ({
    value: Number(k) as T,
    label: typeof v === 'string' ? v : v.label,
  }))
}
