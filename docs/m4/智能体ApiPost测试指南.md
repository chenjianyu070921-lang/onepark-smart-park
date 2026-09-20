# 能源归因智能体 · ApiPost 测试指南

服务：`energy-agent-service`，端口 **8064**，无鉴权，纯 HTTP，ApiPost 直接填就能打。

## 一、先把服务跑起来

```bash
cd D:\gowork\onepark-smart-park\app\energy-agent-service
go run energyagent.go -f etc/energyagent-api.yaml
```

启动成功会看到：

```
Starting energy-agent server at 0.0.0.0:8064, llm=mock(未配置, 用规则原文)...
```

> 注意：服务启动时会自动跑一次定时巡检（扫昨天的数据），所以刚起来的一两秒可能有数据库写入日志，属正常。

**一个坑**：巡检一次要扫 9 个区域、查历史和分时数据，实测耗时 **14.6 秒**。ApiPost 如果设了默认超时（常见 5 秒/10 秒），会在接口还没算完时报超时。去 ApiPost 的「环境 → 超时设置」里调到 **60 秒以上**再测巡检接口。

## 二、三个接口

统一前缀：`http://127.0.0.1:8064`
请求头：`Content-Type: application/json`（POST 必填）
响应统一包在 `{"code":"0","msg":"ok","data":{...}}` 里，`code` 为 `"0"` 才是成功。

---

### 接口 1：手动触发巡检

| 项 | 值 |
|---|---|
| 方法 | **POST** |
| 地址 | `http://127.0.0.1:8064/api/agent/inspect` |

**Body（raw / JSON）**，三个字段全 optional，全都省略就是"扫全园区昨天的数据"：

```json
{
    "statDate": "2026-09-15",
    "zoneId": "SCEN-普涨区",
    "disableLLM": false
}
```

- `statDate`：统计哪天，格式 `YYYY-MM-DD`，不传默认昨天
- `zoneId`：只扫某个区域，不传表示全园区
- `disableLLM`：传 `true` 表示本次不调大模型，用规则原文。**答辩演示"有/无大模型对比"就靠这个开关**

最小可测版本（只带日期）：

```json
{"statDate": "2026-09-15"}
```

**正常返回**：

```json
{
  "code": "0",
  "msg": "ok",
  "data": {
    "runNo": "R20260917-142029",
    "statDate": "2026-09-15",
    "zoneCount": 9,
    "findCount": 6,
    "llmEnabled": false,
    "llmModel": "mock",
    "costMs": 14623,
    "suggestions": [
      {
        "id": 41,
        "zoneId": "SCEN-空转区",
        "deviceId": "",
        "category": "night_idle",
        "severity": 3,
        "title": "SCEN-空转区 夜间用电占全天 90%, 疑似空转",
        "reason": "...",
        "confidence": 0.95,
        "action": "核查夜间未关闭的照明、空调、饮水机与机房设备, 必要时设置定时断电",
        "status": 1
      }
    ]
  }
}
```

字段说明：
- `runNo`：本次运行编号，凭它可以去 `agent_run` / `agent_tool_call` 表查完整轨迹
- `findCount`：发现几条异常
- `llmModel`：`mock` = 没接真模型走规则原文；显示模型名（如 `deepseek-chat`）= 真模型在跑
- `category` 五种：`night_idle` 夜间空转、`zone_surge` 区域普涨、`device_spike` 单设备突增、`meter_backward` 读数回退、`data_missing` 数据缺失
- `severity`：3 高 / 2 中 / 1 低
- `status`：1 待审批 / 2 已通过 / 3 已驳回 / 4 已转工单

---

### 接口 2：建议列表（Query 参数）

| 项 | 值 |
|---|---|
| 方法 | **GET** |
| 地址 | `http://127.0.0.1:8064/api/agent/suggestions` |

在 ApiPost 的 **Query 参数** 表格里填（不要填到 Body 里）：

| 参数 | 必填 | 示例 | 说明 |
|---|---|---|---|
| page | 否 | `1` | 页码，不传默认 1 |
| pageSize | 否 | `10` | 每页条数，不传默认 10 |
| zoneId | 否 | `SCEN-普涨区` | 按区域筛 |
| category | 否 | `device_spike` | 按异常类型筛 |
| status | 否 | `1` | 1待审批 2已通过 3已驳回 4已转工单 |

最简：`http://127.0.0.1:8064/api/agent/suggestions?page=1&pageSize=10`
筛选示例：`http://127.0.0.1:8064/api/agent/suggestions?status=1&category=device_spike`

**返回**：

```json
{
  "code": "0",
  "msg": "ok",
  "data": {
    "total": 15,
    "page": 1,
    "pageSize": 10,
    "list": [ ...同上的 SuggestionItem... ]
  }
}
```

> **测试顺序建议**：先跑接口 1，再调接口 2 列表，从返回的 `list` 里挑一个 `id`，拿去测接口 3。不要猜 id，每次巡检后 id 都不一样。

---

### 接口 3：审批建议

| 项 | 值 |
|---|---|
| 方法 | **POST** |
| 地址 | `http://127.0.0.1:8064/api/agent/suggestion/approve` |

**Body（raw / JSON）**：

```json
{
    "id": 41,
    "approved": true,
    "reviewer": "张老师",
    "comment": "属实, 安排检修"
}
```

- `id`：从接口 2 的列表里取
- `approved`：`true` 通过 / `false` 驳回
- `reviewer`：**必填**，空的话会报错
- `comment`：选填

**返回**：

```json
{
  "code": "0",
  "msg": "ok",
  "data": {
    "id": 41,
    "status": 2,
    "workorderNo": ""
  }
}
```

`workorderNo` 目前恒为空串——审批转工单要等 M6 的 `workorder-service` 实现完才接得上，现在只改状态。

**几个边界可以顺便测**（答辩"异常也考虑了"的素材）：

| 场景 | Body | 预期 |
|---|---|---|
| 通过 | `{"id":41,"approved":true,"reviewer":"张老师"}` | status 变 2 |
| 驳回 | `{"id":42,"approved":false,"reviewer":"张老师"}` | status 变 3 |
| 重复审批同一条 | 再发一次通过 | 报"已审批过"，不会被重置 |
| 不存在的 id | `{"id":999999,"approved":true,"reviewer":"张老师"}` | 报错，提示建议不存在 |
| 缺审批人 | `{"id":41,"approved":true}` | 报错，提示 reviewer 必填 |

---

## 三、幂等怎么演示

同一天连续发 **3 次** 接口 1（参数完全相同），然后调接口 2 看 `total`：**应该一直是不变的数**，不会越跑越多。

原理是 `agent_suggestion` 表上有唯一键 `uk_dedup(stat_date, zone_id, device_id, category)`，重复发现走 UPSERT 覆盖而不是新增。这一条是答辩常被问的"会不会刷屏"，可以直接现场演示。

另外：已经审批过的建议（status=2/3），后面再巡检**不会被重置回"待审批"**——否则人工白审了。

## 四、大模型开关（演示层2用）

编辑 `app/energy-agent-service/etc/energyagent-api.yaml`：

```yaml
LLM:
  Enable: true
  BaseURL: https://api.deepseek.com/v1
  APIKey: sk-xxxxxx
  Model: deepseek-chat
```

改完重启服务，启动日志会变成 `llm=deepseek-chat`。

**没 key 也能演示**：保持 `Enable: false`，用接口 1 的 `disableLLM` 字段做对比——同一天跑两次，一次 `true` 一次 `false`，对比 `reason` 文案的差异。

**真模型挂了也不怕**：如果 key 填了但调不通（网络、余额、401），会自动降级成规则原文，报告照常出，接口不会报错。想现场演示这个降级，就把 `BaseURL` 改成一个瞎填的地址。

## 五、已知的一个观感问题

参数填错（比如漏了 `reviewer`、传了不存在的 id）时，**HTTP 状态码是 500，但响应体里的 code 是 `M4-E-xxxx` 这种业务码**。

原因是全项目用的 `common/response.Fail` 把 E 级别错误统一映射成 500。响应体判断逻辑是对的，只是状态码不好看。这是全项目约定，改 `common/` 会波及别的小组，所以没动——如果你们组想改成 400，改 `common/response` 一处即可。

## 六、库里数据已清理

之前用假模型验证链路时写进库的 6 条建议带有「【假模型润色】」前缀，已删除并重跑生成干净数据。当前库里 `agent_suggestion` 15 条、`agent_run` 16 条，无测试污染残留。
