// 扩展 axios 请求配置: silent = true 时不弹全局错误提示(页面自行处理).
import 'axios'

declare module 'axios' {
  export interface AxiosRequestConfig {
    silent?: boolean
  }
}
