import { request } from './client'

// M1 设备接入（device-service，:8001）。字段与 app/device-service/device.api 对齐。
//
// ⚠️ 两处与 M5 不一样的约定，集中在这里适配，不要让每个页面各自记一遍：
//   1. 分页参数是 **page/size**（M5 用的是 page/page_size）；
//   2. `createdAt` 是**字符串**（后端的字段类型就是 string），交给页面直接展示，不要当时间戳 *1000。

export interface DeviceItem {
  deviceId: string
  deviceName: string
  productKey: string
  status: number // 0离线 1在线 2故障（device 表 comment 口径）
  location: string
  createdAt: string
}

// 与 model/device_model.go 的枚举一致。
export const DEVICE_STATUS: Record<number, { label: string; color: string }> = {
  0: { label: '离线', color: 'default' },
  1: { label: '在线', color: 'success' },
  2: { label: '故障', color: 'error' },
}

export interface ProductItem {
  productKey: string
  productName: string
  status: number // 1启用 0禁用（product 表 comment 口径）
  createdAt: string
}

export const PRODUCT_STATUS: Record<number, { label: string; color: string }> = {
  1: { label: '启用', color: 'success' },
  0: { label: '禁用', color: 'default' },
}

interface M1Query {
  page: number
  page_size: number
  status?: number
  productKey?: string
}

// M1 的分页参数名与 M5 不同（page/size），在适配层收敛成项目统一口径。
// status 传 -1 表示"不限"（后端 default=-1 的口径），这里不传空值避免被当成 0 过滤成"离线"。
function toM1Params(q: M1Query): Record<string, unknown> {
  const p: Record<string, unknown> = { page: q.page, size: q.page_size }
  if (q.status !== undefined && q.status !== -1) p.status = q.status
  if (q.productKey) p.productKey = q.productKey
  return p
}

export function listDevices(q: M1Query) {
  return request<{ total: number; list: DeviceItem[] }>({
    url: '/devices',
    method: 'get',
    params: toM1Params(q),
  })
}

export function listProducts(q: { page: number; page_size: number; status?: number }) {
  return request<{ total: number; list: ProductItem[] }>({
    url: '/products',
    method: 'get',
    params: toM1Params(q),
  })
}
