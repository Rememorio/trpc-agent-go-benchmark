## ToolSearch Benchmark

该 benchmark 位于 `toolsearch`，用于评测**重构后**的 `trpc-agent-go/plugin/toolsearch`
（命名空间目录 + `tool_search` 函数 + 可选 embedding / call_tool）。它：

- 评估集为**多个 EvalCase 的多轮会话**（每个 case 一个 session，每行一轮）；用户输入写在
  `data/<app>/<evalset>.evalset.json` 的 `conversation` 里
- 使用 `trpc-agent-go/evaluation` 执行评估，指标为 `tool_trajectory_avg_score`（阈值 1，regex + subset）
- 输出 tokens 使用量（区分主对话 chat vs tool-search 阶段）与运行耗时（整体 + 每轮）

### 工具库
镜像插件集成测试 `plugin/toolsearch/accuracy_test.go` 的自定义目录：7 个业务命名空间
（`filesystem` / `git` / `document` / `process` / `network` / `iam` / `crm`）+ 一组无命名空间通用工具 +
一个 preset（`web_search`）。全部为「仅元数据」的打桩工具，详见 `trpc-agent-go-impl/catalog`。

### 检索模式
- `none`：不启用插件，所有工具直接给主模型（baseline）
- `keyword`：`tool_search` 用内置关键词匹配（新默认）
- `knowledge`：`tool_search` 用 embedding 语义检索
- `dispatch`：模型只看到 `tool_search` + `call_tool` 两个工具

### 快速开始
评估资产（evalset/metrics）通常只需生成一次，仓库内已放在 `data/` 下；日常跑评估直接执行即可。

在 `toolsearch/trpc-agent-go-impl` 目录运行（四种模式各跑一次，产出可对比的 summary）：
- `go run . -model <MODEL_NAME> -mode none      -evalset toolsearch-catalog-multiturn -max-tools 5`
- `go run . -model <MODEL_NAME> -mode keyword   -evalset toolsearch-catalog-multiturn -max-tools 5`
- `go run . -model <MODEL_NAME> -mode knowledge -evalset toolsearch-catalog-multiturn -max-tools 5`
- `go run . -model <MODEL_NAME> -mode dispatch  -evalset toolsearch-catalog-multiturn -max-tools 5`

### 输入与产物
- 评估输入：`data/<app>/<evalset>.evalset.json`（用户输入在 evalset 内的 `conversation` 里）
- 产物：
  - `data/<app>/<evalset>.evalset.json`
  - `data/<app>/<evalset>.metrics.json`
  - `output/<app>_*_*.evalset_result.json`
  - `output/<evalSetResultId>_<mode>.summary.json`（本次运行的结构化 summary）

更详细参数说明见 `trpc-agent-go-impl/README.md`。
