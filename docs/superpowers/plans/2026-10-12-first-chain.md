# First CLI Chain Implementation Plan

Goal: 实现现有 Spec 的首条 CLI 链路，不新增架构层。
Spec: docs/spec-v1.md、docs/architecture.md。
Stack: Go、SQLite (mattn/go-sqlite3)、YAML front matter (yaml.v3)、兼容 Chat Completions / Embeddings 的 HTTP API。

## 约束与检查重点

文章根目录定位为 shannon-blog/src/content/posts，只读。SQLite 保存文本、关键词与向量，以精确余弦排序配合关键词排名融合；这是小规模本地向量存储，不声称具有 ANN 加速。
过滤 draft/encrypted；不展开链接或图片。编码配置变化重建，内容未变重用向量。同步事务失败不发布新快照。源码不保存密钥。
检查：删除后旧引用、编码失败半更新、API 乱序或无效向量、非法动作和未读引用、模型预算和取消。

## 实施步骤

- [x] knowledge：先写更新/删除/缓存/失败原子性与出处测试，运行见失败；实现 Scan、Sync、Search、Read、Validate。
- [x] llm：先写 HTTP 契约、错误和 embedding 校验测试；实现可配置 Chat 与 Embed 客户端，使用超时、context、用量统计。
- [x] agent：先写搜索→读取→引用流程、伪造引用、预算和后续追问测试；实现 8 步 JSON 动作循环，会话退出不恢复。
- [x] CLI：读取配置/环境变量，扫描同步、交互与单次查询，返回原文及可打开来源，打印用量；新增本机 API 端到端测试。
- [x] 验证：自动化测试、race、vet、构建及真实博客扫描已通过；另完成一次真实 DeepSeek＋百炼 Cache Lab 检索，不以单例声称全面语义质量。

按用户已授权在当前会话执行；本轮不执行 Git 操作或提交。
