# 企业知识库（M7）· 模块说明

> 来源：项目经理要求在 OnePark 项目上新增一个「企业知识库」二开模块（选题来自 `二开项目-汇总.xlsx` 第 18 项，
> 原项目 `youngyangyang04/GopherAI`）。本模块按 OnePark 现有的架构约定**重写实现**，不直接搬原仓库代码。
>
> 状态：**可运行、可演示**（2026-10-13）。答辩用法见 `docs/m5/14-M5答辩演示稿-45分钟.md` 的「附四」。

---

## 一、它解决什么问题

园区自己的制度、流程、历史工单散在一堆文档里：新人查东西靠问人，老员工休假就没人答得上。
本模块把这些材料变成**可问答的知识库**，并且守住一条底线 ——

> **每个答案都能回溯到原文；检索不到就明说"没有依据"，不编。**

无依据的回答在企业知识库里是负资产：它看起来像答案，却没法核实，还会让人做出错误决定。

## 二、架构与关键取舍

```
前端「企业知识库」页
      │  HTTP（网关注入 x-tenant-id / x-user-id）
      ▼
kb-service(8071)                          ← app/kb-service
      ├── chunker    切片：按「标题层级 + 段落」合并，超长才按句硬拆
      ├── retriever  检索：Retriever 接口 + keyword 实现（BM25，2-gram 中文切词，分数归一化 0~1）
      ├── answer     回答：llm / extractive / no_hit 三种模式，失败自动降级
      └── llm        大模型客户端（可选）：OpenAI 兼容 + mock，与 M4 智能体服务同构
      ▼
kb_db：kb_document / kb_chunk / kb_qa_log / kb_feedback
```

三个取舍，都是为"明天能上、后天能扩"做的：

| 取舍 | 选择 | 理由 / 代价 |
|---|---|---|
| 检索实现 | **MySQL + Go 侧 BM25**，不引入向量库/ES | 本机零新增中间件、可离线、结果确定可单测。代价：语义召回弱于向量检索 —— 但**接口已抽好**，换向量实现只需加一个 `Retriever` 实现 + 改配置项 |
| 生成层 | **默认可不用大模型**（抽取式） | 答辩现场可能断网/无 key；"能回答 + 有出处 + 不编造"才是价值底线。配上 key 自动升级生成式，失败回退 |
| 切片 | 按标题层级 + 段落合并（不是固定字数硬切） | 引用时能说清"出自哪一节"，人也能回原文核对；代价是切片长度不均，靠 `MaxLen/MinLen` 兜住 |

## 三、接口（6 个）

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/kb/documents` | 录入文档（自动切片；同 tenant+title+source = 覆盖重建） |
| GET | `/api/kb/documents` | 文档列表 |
| DELETE | `/api/kb/documents/:id` | 删除文档（软删文档 + 清切片） |
| POST | `/api/kb/ask` | 问答：返回 answer / mode / citations / topScore / elapsedMs |
| GET | `/api/kb/qa-logs` | 问答日志（审计与效果评估的数据源） |
| POST | `/api/kb/feedback` | 对某条问答投票（有用 / 没用），回流给效果评估 |

契约文件：`app/kb-service/kb.api`。错误码：`M7-E-3xxx`（见 `app/kb-service/internal/ecode`）。

## 四、语料与怎么跑

```powershell
# 1) 建库建表
mysql -uroot < deploy/sql/kb_tables.sql

# 2) 灌语料（3 份园区运营规范 + 历史工单）
$env:KB_MYSQL_DSN='root:onepark123@tcp(127.0.0.1:3306)/kb_db?charset=utf8mb4&parseTime=True&loc=Local'
$env:WORKORDER_MYSQL_DSN='root:onepark123@tcp(127.0.0.1:3306)/workorder_db?charset=utf8mb4&parseTime=True&loc=Local'
go run ./app/kb-service/cmd/ingest -f app/kb-service/etc/kb-api.yaml -dir docs/kb/corpus -tenant 1

# 3) 起服务
go run ./app/kb-service -f app/kb-service/etc/kb-api.yaml     # 8071
```

- 语料目录：`docs/kb/corpus/`（⚠️ 三份制度是**演示语料**，内容为示例性运营规范，不是真实制度文件）
- 提问：前端「智能应用 → 企业知识库」页，或 `POST /api/kb/ask`

## 五、测试

```powershell
$env:KB_MYSQL_DSN='root:onepark123@tcp(127.0.0.1:3306)/kb_db?...'
go test ./app/kb-service/... -count=1
```

覆盖：切片（标题路径 / 剔代码块 / 序号连续 / 超长硬拆 / 短碎片丢弃）、检索（排序 / 阈值过滤 / 空语料 / 空问题 / 结果可复现）、
回答（拒答文案 / 抽取式出处 / 大模型失败回退 / 成功走生成式）、端到端（录入覆盖 / 命中带引用 / 无依据拒答 / **租户隔离** / 反馈与删除）。
测试用独立租户 + defer 清理，不会污染演示数据。

## 六、与 OnePark 的结合点（下一步）

1. **调度页「推荐处置方案」**：工单详情里调用知识库，返回相似历史工单的处置经验（需要 dispatch → kb 的 gRPC）。
2. **告警处置建议**：M3 告警事件到达时附上处置规范原文。
3. **合同/制度问答**：招商租赁页嵌入"这条合同条款依据哪份规范"。

## 七、已知限制（如实写）

1. **检索是关键词 BM25**，没有说话人式改写与向量语义召回；同义不同词（"欠费" vs "逾期"）召回会漏。
2. **不支持文件上传解析**（PDF/Word/网页），第一版只吃纯文本 + markdown；文件解析要引入解析库并发服务。
3. **没有文档级权限**：目前只做租户隔离（同租户内所有文档对所有人可见），部门/角色/文档级权限是第二版的事。
4. **效果评估只有数据，没有看板**：日志里存了 mode / top_score / 反馈，但还没做召回率、A/B 这类评测脚本。
5. **与 M4 的 `llm` 包是同构的两份代码**（未收敛到 `common/llm`）—— 收敛需要与 M4 负责人对齐，本次答辩前不动别人的模块。
