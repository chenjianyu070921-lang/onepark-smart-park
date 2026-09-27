import { useCallback, useEffect, useRef, useState } from 'react'

// 列表页样板: 承载 data/total/loading/query/翻页/筛选/刷新.
// 现有页面里这套逻辑逐字重复了 9 处, 新增页面统一走这里; 存量页面后续按需迁移.
export interface ListResp<T> {
  list: T[]
  total: number
}

export interface BaseQuery {
  page: number
  page_size: number
}

export interface TableQueryResult<T, Q extends BaseQuery> {
  list: T[]
  total: number
  loading: boolean
  query: Q
  /** 合并筛选条件并回到第 1 页(筛选变化后留在原页会看到空列表) */
  setQuery: (patch: Partial<Q>) => void
  /** 仅翻页/改页大小 */
  setPage: (page: number, pageSize?: number) => void
  reload: () => void
  pagination: {
    current: number
    pageSize: number
    total: number
    showTotal: (t: number) => string
    onChange: (page: number, pageSize: number) => void
  }
}

export function useTableQuery<T, Q extends BaseQuery>(
  fetcher: (q: Q) => Promise<ListResp<T>>,
  initialQuery: Q,
  deps: unknown[] = [],
): TableQueryResult<T, Q> {
  const [list, setList] = useState<T[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [query, setQueryState] = useState<Q>(initialQuery)
  // 触发重载用计数器, 而不是把 reload 本身塞进依赖(会让 fetcher 每次重建都抖动).
  const [tick, setTick] = useState(0)

  // fetcher 每次渲染都是新函数, 用 ref 持有最新引用, 避免它进入 fetch 的依赖.
  const fetcherRef = useRef(fetcher)
  fetcherRef.current = fetcher

  const fetchData = useCallback(async (q: Q) => {
    setLoading(true)
    try {
      const resp = await fetcherRef.current(q)
      setList(resp?.list ?? [])
      setTotal(resp?.total ?? 0)
    } catch {
      // 错误提示由 axios 拦截器统一弹出, 这里只保证 loading 收尾且不残留脏数据.
      setList([])
      setTotal(0)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void fetchData(query)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, tick, fetchData, ...deps])

  const setQuery = useCallback((patch: Partial<Q>) => {
    setQueryState((q) => ({ ...q, ...patch, page: 1 }))
  }, [])

  const setPage = useCallback((page: number, pageSize?: number) => {
    setQueryState((q) => ({
      ...q,
      page,
      page_size: pageSize ?? q.page_size,
    }))
  }, [])

  const reload = useCallback(() => setTick((t) => t + 1), [])

  return {
    list,
    total,
    loading,
    query,
    setQuery,
    setPage,
    reload,
    pagination: {
      current: query.page,
      pageSize: query.page_size,
      total,
      showTotal: (t: number) => `共 ${t} 条`,
      onChange: setPage,
    },
  }
}
