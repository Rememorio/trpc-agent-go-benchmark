## toolsearch (trpc-agent-go-impl)

### 功能
- 用 `trpc-agent-go/evaluation` 执行 toolsearch 评测
- 输出：整体耗时（evaluation executionTime + wall time）、tokens（chat / toolsearch / total）、每轮的期望工具与实际工具、每轮 tokens 与耗时
- 额外落盘一份结构化 summary：`<output-dir>/<evalSetResultId>_<mode>.summary.json`

### 工具库（重构后）
基于插件重构后的 **命名空间目录（namespace catalog）** 设计，工具库定义在 `catalog/` 包，
镜像了插件集成测试 `plugin/toolsearch/accuracy_test.go` 的自定义目录：

- 7 个业务命名空间 toolbox：`filesystem` / `git` / `document` / `process` / `network` / `iam` / `crm`
- 一组无命名空间的通用工具（`WithDeferredTools`）：calculator / get_current_time / base64_* 等
- 一个常驻 preset 工具：`web_search`

每个工具都是「仅元数据」（name + description），执行被打桩（返回固定 JSON），
所以 benchmark 期间唯一的真实网络流量是模型补全本身（knowledge 模式额外有 embedding 调用）。

### 运行
在本目录执行：
- `go run . -model deepseek-chat -mode keyword -evalset toolsearch-catalog-multiturn -max-tools 5`

### 重要参数
- `-mode`: `none | keyword | knowledge | dispatch`
  - `none`：不启用插件，所有工具直接提供给主模型（baseline）
  - `keyword`：`NewPlugin` + `WithToolboxes`，`tool_search` 用内置关键词匹配解析 `queries`（新默认）
  - `knowledge`：在 keyword 基础上加 `WithToolKnowledge`，`queries` 用 embedding（向量）相似度排序
  - `dispatch`：在 keyword 基础上加 `WithInvocationMode(DispatchToolCalls)`，模型只看到 `tool_search` + `call_tool` 两个工具
- `-data-dir`: 默认 `../data`（读取 `<data-dir>/<app>/<evalset>.{evalset,metrics}.json`）
- `-output-dir`: 默认 `../output`（evaluation result 落盘目录）
- `-embed-model`: 默认 `text-embedding-3-small`（仅 `knowledge` 模式使用）

### 关于重构后 API 的说明
重构后的插件**没有了「LLM 选 top-K」模式**。旧版由一次独立的 LLM 调用从工具列表里挑选 Top-K；
新版是**模型自己**在主对话里调用 `tool_search` 函数，针对命名空间目录检索并加载工具。因此：

- `keyword` / `dispatch` 模式**不产生独立的 out-of-band LLM 调用**——其开销（携带目录的更大 prompt、
  `tool_search` 结果、以及工具调用的 completion）已经计入 chat 桶。
- `knowledge` 模式唯一的 out-of-band 开销是 **embedding 调用**，通过 `countingEmbedder` 包装 embedder，
  在源头把 token 记入 toolsearch 桶（不依赖插件内部已内联化、每轮重置的 usage accumulator）。

### call_tool 模式与指标
`dispatch` 模式下模型不按名字调用工具，而是调用包装工具 `call_tool`（真实工具名在 `arguments.tool_name`）。
tool-trajectory 指标按**工具名**匹配，因此 benchmark 注册了一个自定义比较器 `unwrap_call_tool`
（见 `metric.go` + metrics 文件的 `compareName`）：先把 actual 轨迹里的 `call_tool` 改写成
`arguments.tool_name`，再套用标准的 subset + regex + 无序匹配。对其他模式没有 `call_tool`，此归一化为空操作。

### 环境变量
- `MODEL_NAME`: 未显式传 `-model` 时作为默认
- `OPENAI_API_KEY` / `OPENAI_BASE_URL`: 取决于所用 model provider（knowledge 模式还需 embedding 端点）

### 依赖
`go.mod` 通过 `replace` 指向本地 `../../../trpc-agent-go`（及其 `evaluation` 子模块），
以便对**重构后**的插件跑评测，而不是已发布的 v1.7.0。
