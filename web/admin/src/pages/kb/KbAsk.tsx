import { useCallback, useEffect, useState } from 'react'
import {
  Alert,
  Button,
  Card,
  Collapse,
  Empty,
  Input,
  message,
  Modal,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import {
  ask,
  createDocument,
  deleteDocument,
  listDocuments,
  listQaLogs,
  SAMPLE_QUESTIONS,
  sendFeedback,
  type AskResp,
  type DocumentItem,
  type QaLog,
} from '../../api/kb'

const MODE_META: Record<string, { color: string; label: string; hint: string }> = {
  llm: { color: 'processing', label: '生成式', hint: '由大模型基于检索到的材料组织语言' },
  extractive: { color: 'success', label: '抽取式', hint: '直接引用知识库原文（未配置大模型或调用失败时的兜底）' },
  no_hit: { color: 'warning', label: '无依据', hint: '知识库里没有相关材料，明确拒答而不编造' },
}

// 企业知识库：文档接入 + 带出处问答 + 反馈
//
// 这一页要传达的核心只有一句：**回答必须能回溯到原文**。
// 所以回答卡片里"引用"不是可选装饰，而是与正文同等重要的部分。
export default function KbAskPage() {
  const [question, setQuestion] = useState('')
  const [loading, setLoading] = useState(false)
  const [result, setResult] = useState<AskResp | null>(null)
  const [voted, setVoted] = useState<0 | 1 | 2>(0)

  const [docs, setDocs] = useState<DocumentItem[]>([])
  const [docsLoading, setDocsLoading] = useState(false)
  const [logs, setLogs] = useState<QaLog[]>([])

  const [openCreate, setOpenCreate] = useState(false)
  const [form, setForm] = useState({ title: '', category: '制度', source: '', content: '' })
  const [saving, setSaving] = useState(false)

  const loadDocs = useCallback(async () => {
    setDocsLoading(true)
    try {
      const r = await listDocuments({ page: 1, pageSize: 50 })
      setDocs(r.list ?? [])
    } catch {
      // 错误提示由 client 拦截器统一弹出
    } finally {
      setDocsLoading(false)
    }
  }, [])

  const loadLogs = useCallback(async () => {
    try {
      const r = await listQaLogs({ page: 1, pageSize: 10 })
      setLogs(r.list ?? [])
    } catch {
      /* 同上 */
    }
  }, [])

  useEffect(() => {
    void loadDocs()
    void loadLogs()
  }, [loadDocs, loadLogs])

  const doAsk = async (q?: string) => {
    const text = (q ?? question).trim()
    if (!text) {
      message.warning('先输入一个问题')
      return
    }
    setQuestion(text)
    setLoading(true)
    setVoted(0)
    try {
      const r = await ask(text)
      setResult(r)
      void loadLogs()
    } catch {
      /* 拦截器已提示 */
    } finally {
      setLoading(false)
    }
  }

  const doVote = async (vote: 1 | 2) => {
    if (!result) return
    try {
      await sendFeedback(result.qaId, vote)
      setVoted(vote)
      message.success(vote === 1 ? '已记录：这条回答有用' : '已记录：这条回答没用')
    } catch {
      /* 拦截器已提示 */
    }
  }

  const doCreate = async () => {
    if (!form.title.trim() || !form.content.trim()) {
      message.warning('标题和正文都要填')
      return
    }
    setSaving(true)
    try {
      const r = await createDocument({
        title: form.title.trim(),
        category: form.category.trim() || '未分类',
        source: form.source.trim(),
        content: form.content,
      })
      message.success(`已录入，切成 ${r.chunkCount} 段`)
      setOpenCreate(false)
      setForm({ title: '', category: '制度', source: '', content: '' })
      void loadDocs()
    } catch {
      /* 拦截器已提示 */
    } finally {
      setSaving(false)
    }
  }

  const doDelete = (row: DocumentItem) => {
    Modal.confirm({
      title: `删除《${row.title}》?`,
      content: '会连同切片一起清理，之后相关问题将变为「无依据」。',
      okText: '删除',
      cancelText: '取消',
      onOk: async () => {
        await deleteDocument(row.id)
        message.success('已删除')
        void loadDocs()
      },
    })
  }

  const docColumns: ColumnsType<DocumentItem> = [
    { title: '标题', dataIndex: 'title', ellipsis: true },
    { title: '分类', dataIndex: 'category', width: 100, render: (v: string) => <Tag>{v}</Tag> },
    { title: '切片', dataIndex: 'chunkCount', width: 80 },
    {
      title: '出处',
      dataIndex: 'source',
      ellipsis: true,
      render: (v: string) => (
        <Tooltip title={v}>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {v || '-'}
          </Typography.Text>
        </Tooltip>
      ),
    },
    {
      title: '操作',
      key: 'op',
      width: 80,
      render: (_, row) => (
        <Button type="link" size="small" danger onClick={() => doDelete(row)}>
          删除
        </Button>
      ),
    },
  ]

  const resultMeta = result ? MODE_META[result.mode] : null

  return (
    <div style={{ padding: 16 }}>
      <Card
        title="企业知识库 · 问答"
        extra={
          <Space>
            <Button onClick={() => setOpenCreate(true)}>录入文档</Button>
            <Button onClick={() => void loadDocs()}>刷新文档</Button>
          </Space>
        }
      >
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 12 }}
          message="回答只依据知识库里的材料，并在下方给出出处；检索不到会明确说「没有依据」，不做推测。"
        />
        <Space direction="vertical" size={10} style={{ width: '100%' }}>
          <Input.TextArea
            rows={2}
            value={question}
            onChange={(e) => setQuestion(e.target.value)}
            placeholder="例如：工单派出去没人接怎么办？（Ctrl+Enter 提问）"
            onKeyDown={(e) => {
              if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) void doAsk()
            }}
          />
          <Space wrap>
            <Button type="primary" loading={loading} onClick={() => void doAsk()}>
              提问
            </Button>
            <Typography.Text type="secondary">示例问题：</Typography.Text>
            {SAMPLE_QUESTIONS.map((q) => (
              <Button key={q} size="small" onClick={() => void doAsk(q)}>
                {q}
              </Button>
            ))}
          </Space>
        </Space>

        {result ? (
          <Card
            style={{ marginTop: 16 }}
            size="small"
            title={
              <Space>
                <Tag color={resultMeta?.color}>{resultMeta?.label}</Tag>
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                  {resultMeta?.hint}
                </Typography.Text>
              </Space>
            }
            extra={
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                最高分 {result.topScore} · 耗时 {result.elapsedMs}ms · 引用 {result.citations.length} 处
              </Typography.Text>
            }
          >
            <Typography.Paragraph style={{ whiteSpace: 'pre-wrap', marginBottom: 12 }}>
              {result.answer}
            </Typography.Paragraph>

            {result.citations.length > 0 ? (
              <Collapse
                size="small"
                items={result.citations.map((c, i) => ({
                  key: String(c.chunkId),
                  label: (
                    <Space>
                      <Typography.Text strong>{i + 1}.</Typography.Text>
                      <Typography.Text>《{c.title}》第 {c.seq} 段</Typography.Text>
                      <Tag color="blue">相关度 {c.score}</Tag>
                    </Space>
                  ),
                  children: (
                    <Space direction="vertical" size={6} style={{ width: '100%' }}>
                      <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                        出处：{c.source || '未标注'}
                      </Typography.Text>
                      <Typography.Text style={{ whiteSpace: 'pre-wrap' }}>{c.snippet}</Typography.Text>
                    </Space>
                  ),
                }))}
              />
            ) : (
              <Alert type="warning" showIcon message="本次回答没有引用任何原文 —— 因为没有找到依据。" />
            )}

            {result.citations.length > 0 ? (
              <Space style={{ marginTop: 12 }}>
                <Typography.Text type="secondary">这条回答有用吗？</Typography.Text>
                <Button size="small" type={voted === 1 ? 'primary' : 'default'} onClick={() => void doVote(1)}>
                  有用
                </Button>
                <Button size="small" danger={voted === 2} onClick={() => void doVote(2)}>
                  没用
                </Button>
                {voted ? <Typography.Text type="success">已记录</Typography.Text> : null}
              </Space>
            ) : null}
          </Card>
        ) : (
          <Empty style={{ marginTop: 24 }} description="还没有提问" />
        )}
      </Card>

      <Card title={`知识库文档（${docs.length}）`} style={{ marginTop: 16 }} loading={docsLoading}>
        <Table
          rowKey="id"
          size="small"
          columns={docColumns}
          dataSource={docs}
          pagination={{ pageSize: 8, hideOnSinglePage: true }}
        />
      </Card>

      <Card title="最近问答（审计与效果评估的数据源）" style={{ marginTop: 16 }}>
        <Table
          rowKey="id"
          size="small"
          dataSource={logs}
          pagination={false}
          columns={[
            { title: '问题', dataIndex: 'question', ellipsis: true },
            {
              title: '模式',
              dataIndex: 'mode',
              width: 100,
              render: (m: string) => <Tag color={MODE_META[m]?.color}>{MODE_META[m]?.label ?? m}</Tag>,
            },
            { title: '最高分', dataIndex: 'topScore', width: 90 },
            { title: '耗时(ms)', dataIndex: 'elapsedMs', width: 90 },
            { title: '时间', dataIndex: 'createdAt', width: 170 },
          ]}
        />
      </Card>

      <Modal
        title="录入知识库文档"
        open={openCreate}
        onOk={() => void doCreate()}
        confirmLoading={saving}
        onCancel={() => setOpenCreate(false)}
        okText="录入并切片"
        cancelText="取消"
        width={720}
        destroyOnClose
      >
        <Space direction="vertical" size={10} style={{ width: '100%' }}>
          <Input
            placeholder="标题，例如：园区访客管理办法"
            value={form.title}
            onChange={(e) => setForm({ ...form, title: e.target.value })}
          />
          <Space>
            <Input
              style={{ width: 160 }}
              placeholder="分类（制度/流程/经验）"
              value={form.category}
              onChange={(e) => setForm({ ...form, category: e.target.value })}
            />
            <Input
              style={{ width: 380 }}
              placeholder="出处（可空，例如 docs/kb/corpus/01-工单处置规范.md）"
              value={form.source}
              onChange={(e) => setForm({ ...form, source: e.target.value })}
            />
          </Space>
          <Input.TextArea
            rows={12}
            placeholder="正文：支持 markdown 标题与段落，系统会按「标题层级 + 段落」切片"
            value={form.content}
            onChange={(e) => setForm({ ...form, content: e.target.value })}
          />
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            提示：同一「标题 + 出处」重复录入会**覆盖重建切片**，适合更新制度版本。
          </Typography.Text>
        </Space>
      </Modal>
    </div>
  )
}
