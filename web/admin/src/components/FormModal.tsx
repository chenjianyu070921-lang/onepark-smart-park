import { useEffect, type ReactNode } from 'react'
import { Form, Modal } from 'antd'

// Modal + Form 三件套在页面里重复了 10+ 处: open/confirmLoading/onOk 转 submit/
// destroyOnClose/成功后 resetFields. 这里收口, 页面只关心表单项与提交逻辑.
export interface FormModalProps<V> {
  open: boolean
  title: string
  /** 提交按钮 loading, 由页面在请求期间置 true */
  loading?: boolean
  width?: number
  okText?: string
  initialValues?: Partial<V>
  onCancel: () => void
  onSubmit: (values: V) => void | Promise<void>
  children: ReactNode
}

export default function FormModal<V = Record<string, unknown>>({
  open,
  title,
  loading = false,
  width = 520,
  okText = '确定',
  initialValues,
  onCancel,
  onSubmit,
  children,
}: FormModalProps<V>) {
  const [form] = Form.useForm()

  // 每次打开都重置: 避免上一次的输入(尤其是编辑场景)残留到下一次.
  useEffect(() => {
    if (open) {
      form.resetFields()
      if (initialValues) form.setFieldsValue(initialValues as never)
    }
  }, [open, form, initialValues])

  return (
    <Modal
      title={title}
      open={open}
      width={width}
      confirmLoading={loading}
      okText={okText}
      onCancel={onCancel}
      onOk={() => form.submit()}
      destroyOnClose
      maskClosable={false}
    >
      <Form
        form={form}
        layout="vertical"
        requiredMark={false}
        onFinish={(values) => onSubmit(values as V)}
      >
        {children}
      </Form>
    </Modal>
  )
}
