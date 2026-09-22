# 讲解卡目录（docs/walkthrough/）

本目录存放《代码讲解闸门》的交付留痕，规则正本见 [`AGENTS.md`](../../AGENTS.md)。

## 文件约定

- 讲解卡：`<模块>-<YYYYMMDD>.md`，与对应代码**同批提交**（commit-msg 钩子与 CI 会校验）；
- 学情诊断：`diagnosis/<模块>-<YYYYMMDD>.md`；
- 累计学习档案：`learning-profile.md`（按技术主题聚合历次诊断）。

模板见 `skills/code-explain-gate/references/walkthrough-template.md`。

## 本地钩子安装（每个组员克隆后执行一次）

```bash
git config core.hooksPath scripts/hooks
```

## 放行类改动的逃生通道

确属放行类改动（纯 CRUD/配置/文档/样式）不需要讲解卡时，在 commit message 首行写：

```
no-gate: <一行理由>
```

逃生记录会留在 git log 里，教师可审计；PR 场景下把同样的声明写在 PR 描述里。
