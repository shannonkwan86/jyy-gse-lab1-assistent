# Personal Assistant

通过真实需求、增量交付、验收与长期维护，练习 long horizon 软件工程的个人助手。

当前已实现首条链路：CLI 输入 → 扫描 Markdown 变化 → 同步关键词与向量索引 → 受限 Agent 搜索候选 → 读取原文 → 结果及出处。

## 运行

要求 Go 1.26+、CGO 与 C 编译器（SQLite 驱动需要）。当前已在 macOS arm64 / Go 1.26.4 验证。

首次检出后创建本地配置（不要覆盖已有配置）：

```sh
cp configs/config.example.json configs/config.local.json
chmod 600 configs/config.local.json
```

设置自己的 `articles_dir`，通过示例指定的环境变量提供密钥；也可删除 `api_key_env` 后填写本地 `api_key`。本地配置被 Git 忽略，不随仓库分发。两个服务分别配置：

- `chat`：DeepSeek 的 `base_url`、`model` 和 `api_key`。
- `embedding`：已预填百炼北京地址与 `text-embedding-v4`，只需填写北京地域的 `api_key`。DeepSeek 对话配置保持独立。

也可参考 [配置示例](configs/config.example.json)，使用 `api_key_env` 指定环境变量；设置该字段时优先读取环境变量。不要提交密钥。地址须为 API 基础地址，例如带 `/v1` 的地址，不包含 `/chat/completions` 或 `/embeddings` 后缀。服务需支持 Chat Completions JSON 输出或 Embeddings 请求。

```sh
# 仅本机扫描：无需配置或密钥，不生成向量、不上传文章
go run ./cmd/assistant -scan /Users/gsh/Projects/shannon-blog/src/content/posts

# 单次查询
go run ./cmd/assistant -query '找我记录矩阵转置和缓存优化的文章'

# 连续对话，/exit 退出；Ctrl+C 取消并退出
go run ./cmd/assistant

# 结构化结果（进度和累计用量输出到 stderr）
go run ./cmd/assistant -query '我对上下文工程有什么思考？' -json
```

配置中的相对路径以配置文件所在目录为基准。首次查询会把符合范围的文章片段发往 embedding 服务；对话服务接收问题、会话历史、搜索与读取结果。不会读取图片或跟随外部链接。运行前确认所选服务适合处理这些内容。

## 已实现行为与限制

- 递归扫描普通 `.md` 文件，解析 YAML front matter，排除 `draft: true`、`encrypted: true`、符号链接、FIFO 等非普通文件与隐藏目录；不修改原文。
- 标题与段落切分，约 1200 字符上限；长代码块也可能分段。保留行号、文件哈希和标题。
- SQLite 持久保存片段、关键词和向量；向量采用精确余弦搜索，与中英文关键词排名通过 RRF 合并。没有 ANN 索引或独立数据库服务；小规模下搜索会遍历片段。
- 内容与编码配置缓存，最多 10 个输入一批编码；每次用户查询先扫描。完整资料快照和编码配置未变化时跳过缓存查询及索引重写，重启后也可复用该判断；发生变化时批量查询编码缓存，新快照完整后事务发布。失败则停止该查询；成功的编码缓存留给重试。
- 搜索结果是候选，向量分数不代表事实正确或置信度。当前不设语义阈值，相关性由模型读取判断，需真实案例调优。
- Agent 默认 8 步，只允许 search/final；search 内部自动读取并校验至多 5 个候选，模型可直接引用返回证据。支持同会话最近 3 轮追问，退出不恢复。
- 正文的独立 `[片段ID]` 标记必须与引用列表对应；独立的字母、数字、下划线或连字符方括号标识符保留给引用，普通 Markdown 链接、行内代码和常用围栏代码块不作为引用。最终引用必须读取过，发布前复核所有已读证据的文件版本，包括未进入最终引用列表的片段。资料不足时仍可无引用拒答，但不能携带未登记的引用。程序不能自动证明所有语义结论正确。模型协议异常明确失败，不自动无限修复或重试。
- 单次 HTTP 超时 45 秒，整次查询默认 180 秒（含同步，可配置）；扫描遍历、分段读取和切分检查取消，底层单次文件系统调用不保证硬实时中断。Ctrl+C 取消并退出，API 错误不当作资料缺失。
- 打开索引前将数据库及已有 SQLite 辅助文件权限限制为 `0600`，拒绝非普通文件或符号链接；新目录为 `0700`，不修改已有父目录权限。私有文件权限不等于加密。
- stderr 输出工具动作、耗时与会话累计调用/用量，未返回用量的调用计入 calls 但不伪造 token；尚无持久日志或成本换算。
- 当前为单进程 CLI，不支持多实例同时更新同一索引；保留原文件与数据库备份由用户管理。删除片段会移除可查询记录，编码缓存为重试保留，不等于安全擦除历史数据。

## 验证

```sh
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/assistant
```

测试使用临时文章、真实 SQLite 和本机 HTTP 模拟服务，覆盖端到端链路、追问、增改删、缓存与模型变更、失败不发布、引用检查、超预算及 API 协议。模拟向量仅检验检索管道。

审查后新增验证：非普通文件过滤及取消、数据库/辅助文件权限、无变化快照零写入、成功编码批次的失败重试、所有已读证据复核、正文引用对应，以及普通 Markdown 链接、行内代码和常用围栏代码示例的兼容性。引用格式识别不是完整 Markdown 解析器。2026-10-12 重构后非缓存测试、race、vet、CLI 构建及离线合成验收通过；搜索自动读取修复后，用户在自己的命令行确认原始 ICPC 南昌站查询已成功运行；该结果为用户验收反馈，未记录本次用量，不代表全面语义质量评估。

2026-10-12 已用真实 DeepSeek 和百炼完成一次检索：14 篇文章、154 个片段，Agent 经 search → read → final 找到 Cache Lab 的矩阵转置部分并输出出处。该案例证明首条链路能运行，不代表已完成全面语义质量评估。用量与后续工作见决策记录和 roadmap。

## 结构与设计

```text
cmd/assistant/       CLI、配置组装与端到端测试
internal/agent/      受限循环、工具分派与会话
internal/knowledge/ Markdown、索引同步、混合检索与出处
internal/llm/        配置、对话与 embedding HTTP 客户端
configs/            配置示例与被忽略的本地配置
docs/              规格、契约、决策和实施记录
testdata/           合成资料约定
```

- [第一版规格](docs/spec-v1.md)
- [运行与失败契约](docs/architecture.md)
- [决策记录](docs/decisions.md)
- [后续工作](docs/roadmap.md)
- [首条链路实现清单](docs/superpowers/plans/2026-10-12-first-chain.md)

手机、邮件、持久业务任务均未实现。个人文章、凭据、索引不提交仓库。
