# claude-code-go E2E 技术设计

> 对标 [`agent-sdk/tests/e2e`](../../../agent-sdk/tests/e2e) 的真实 Claude CLI 测试体系。  
> 主 SDK 设计见 [../design.md](../design.md)。  
> parser 子包设计见 [../parser/design.md](../parser/design.md)。

---

## 1. 目标与边界

本文件只描述 **真实 Claude CLI E2E** 设计，不重复主设计里的 SDK 分层、parser 设计与 unit test 迁移。

E2E 的职责只有三类：

1. 验证 Go SDK 与真实 Claude CLI 的端到端连通性
2. 验证 auth / session / streaming / image / prompt / spawn args / agents 等关键用户路径
3. 产出可对拍的 artifacts，便于和 `agent-sdk`、`claude-code-python` 横向比对

测试迁移沿用主设计中的两类标准：

- **literal-port**：CLI 路径、spawn args、artifact 文件名、helper 语义、环境隔离等必须一致
- **semantic-port**：模型输出的宽松语义断言要求行为等价

---

## 2. 目录布局

```text
claude-code-go/
├── tests/
│   ├── e2e_real_cli_test.go
│   ├── config.go
│   ├── harness.go
│   └── reporters.go
├── testdata/
│   ├── fakeclaude/
│   │   └── main.go
│   └── images/
│       ├── red-square.png
│       ├── shapes-demo.png
│       └── receipt-demo.png
└── docs/
    └── e2e/
        └── design.md
```

说明：

- `testdata/images/*` 采用从 `agent-sdk/tests/e2e/fixtures/images/` **字节级复制**
- `e2e_real_cli_test.go` 对标 Node 版 `real-cli.test.ts`
- `config.go` / `harness.go` / `reporters.go` 属于跨仓库对拍契约，不是实现细节

---

## 3. 凭证加载与 setup 语义

E2E 配置从环境变量读取：

- `E2E_API_KEY`
- `E2E_AUTH_TOKEN`
- `E2E_BASE_URL`
- `E2E_MODEL`

与 `agent-sdk` 一致，无凭证时 **不能静默 skip**。必须保留一个 setup-failure 用例：

- `TestE2ESetup_RequiresE2EEnvVars`

这样可以保证：

- E2E 语义与上游一致
- 调用方清楚地看到“环境未配置”，而不是误以为测试不存在

---

## 4. 真实 CLI 路径约束

真实 E2E 默认优先解析 **仓库内安装** 的 Claude CLI，而不是依赖宿主机 PATH 上的 `claude`。

必须保留以下约束：

- raw `spawn` 事件中的 `command` / `args` 能体现仓库内 CLI 路径
- auth-path 测试里要断言 `spawn.command` 包含 `@anthropic-ai/claude-code/cli.js`
- 若未来允许覆盖 CLI 路径，也必须通过显式测试配置完成

---

## 5. 执行模型与并发约束

E2E 默认串行执行。

原因：

- `withOptionalPoisonedEnv()` 会故意污染宿主 `ANTHROPIC_*` 环境变量
- artifact 输出依赖同一 run-id 目录布局
- 某些用例会创建临时目录、调试文件、plugin 目录和 probe 文件

因此当前阶段：

- 禁止 `t.Parallel()`
- 禁止对 `tests/e2e` 做手工并行调度
- 若未来要并行，必须先把环境污染类用例改造成子进程隔离

---

## 6. Helper 契约

### 6.1 `config.go`

职责：

- 读取 `E2E_*` 环境变量
- 组装 `E2EConfig`
- 提供 `GetClientOptions()` / `ListAvailableAuthModes()`

默认 session 配置必须与 Node 版一致：

- `Bare = true`
- `SettingSources = ""`
- `Verbose = true`
- `IncludePartialMessages = true`
- `DangerouslySkipPermissions = true`

### 6.2 `harness.go`

必须保留的 helper：

- `ExecuteBufferedCase()`
- `ExecuteStreamedCase()`
- `CreateTempWorkspace()`
- `WriteProbeFile()`
- `WritePromptFile()`
- `CreateEmptyPluginDir()`
- `CleanupPath()`
- `GetSpawnEvent()`
- `GetFlagValues()`
- `HasFlag()`
- `ReadDebugFile()`
- `ParseJsonResponse()`
- `WithOptionalPoisonedEnv()`

其中两个 helper 要严格对齐：

1. `ParseJsonResponse()`  
   顺序必须是：
   - 先去 code fence
   - 再尝试直接 JSON 解析
   - 失败后再抽取首个 JSON value

2. `WithOptionalPoisonedEnv()`  
   必须通过污染宿主 `ANTHROPIC_API_KEY` / `ANTHROPIC_AUTH_TOKEN` /
   `ANTHROPIC_BASE_URL` 来验证 SDK 不会继承隐式凭据

### 6.3 `reporters.go`

职责：

- 创建 `artifactDir`
- 写入结构化产物
- 输出 `[E2E] key=value` 风格终端摘要

---

## 7. Artifact 契约

### 7.1 目录布局

artifact 根目录格式：

```text
tests/e2e/artifacts/<run-id>/<case-name>/
```

其中：

- `run-id = <ISO8601 替换冒号和点>-<pid>`
- `case-name` 需做文件名安全化处理

### 7.2 必须落盘的文件

每个 case 必须写入：

- `input.json`
- `relay-events.json`
- `raw-events.ndjson`
- `final-response.txt`
- `summary.json`
- `terminal-transcript.txt`

### 7.3 序列化格式

- `input.json` / `relay-events.json` / `summary.json`
  - 使用 2-space pretty JSON
  - 文件末尾补 `\n`
- `raw-events.ndjson`
  - 每行一个 JSON object
  - 文件末尾补 `\n`
- `terminal-transcript.txt`
  - 使用 `[E2E] key=value` 行格式
  - 至少包含 case、auth_mode、options、input、raw_event_count、relay_event_count、
    final_response、artifact_dir

---

## 8. 逐条映射附录

本附录对应
[agent-sdk/tests/e2e/cases/real-cli.test.ts](../../../agent-sdk/tests/e2e/cases/real-cli.test.ts)
的 14 条真实 CLI 用例。

1. **Source:** `real-cli.test.ts:68` `requires E2E env vars`。**Target:** `e2e_real_cli_test.go::TestE2ESetup_RequiresE2EEnvVars`。`literal-port`。要求无凭证时保留一个 setup-failure 用例，而不是静默 skip。
2. **Source:** `real-cli.test.ts:74` `loads local secrets and default session settings`。**Target:** `e2e_real_cli_test.go::TestE2EConfig_LoadsLocalSecretsAndDefaultSessionSettings`。`literal-port`。要求 E2E 配置层读到 model、`bare=true`、`settingSources=""`、`includePartialMessages=true`。
3. **Source:** `real-cli.test.ts:85` `runs the apiKey path through ClaudeCode options when configured`。**Target:** `e2e_real_cli_test.go::TestE2EAuthPaths_RunsTheAPIKeyPathThroughClaudeCodeOptionsWhenConfigured`。`literal-port`。要求 API key 路径下返回 JSON 中 `auth_mode=api-key`，宿主脏环境不泄漏，spawn command 指向仓库内 Claude CLI。
4. **Source:** `real-cli.test.ts:114` `runs the authToken + baseUrl path through ClaudeCode options when configured`。**Target:** `e2e_real_cli_test.go::TestE2EAuthPaths_RunsTheAuthTokenAndBaseURLPathThroughClaudeCodeOptionsWhenConfigured`。`literal-port`。要求 auth-token + baseUrl 路径同样能成功，并返回 `auth_mode=auth-token`。
5. **Source:** `real-cli.test.ts:141` `preserves context across multiple run calls on the same session`。**Target:** `e2e_real_cli_test.go::TestE2ESessionLifecycle_PreservesContextAcrossMultipleRunCallsOnTheSameSession`。`literal-port`。要求同一 `Session` 多轮保持上下文，且 `ResumeSession()` 跨实例恢复后仍能记住前文 token。
6. **Source:** `real-cli.test.ts:182` `emits text deltas when includePartialMessages is true`。**Target:** `e2e_real_cli_test.go::TestE2EStreaming_EmitsTextDeltasWhenIncludePartialMessagesIsTrue`。`literal-port`。要求 streaming 模式在开启 partial messages 时实际收到 `text_delta` 与 `turn_complete`。
7. **Source:** `real-cli.test.ts:204` `still completes when includePartialMessages is false`。**Target:** `e2e_real_cli_test.go::TestE2EStreaming_StillCompletesWhenIncludePartialMessagesIsFalse`。`literal-port`。要求关闭 partial messages 后仍正常结束，并能拿到最终文本。
8. **Source:** `real-cli.test.ts:225` `understands a simple red square image`。**Target:** `e2e_real_cli_test.go::TestE2EImageInput_UnderstandsASimpleRedSquareImage`。`literal-port`。要求图片走 `stream-json` 而不是 `--image`，模型输出中识别出 red + square。
9. **Source:** `real-cli.test.ts:252` `counts obvious shapes from a synthetic image`。**Target:** `e2e_real_cli_test.go::TestE2EImageInput_CountsObviousShapesFromASyntheticImage`。`semantic-port`。要求模型至少识别出 3 个明显图形，并返回非空形状列表。
10. **Source:** `real-cli.test.ts:276` `extracts a visible snippet from a synthetic receipt image`。**Target:** `e2e_real_cli_test.go::TestE2EImageInput_ExtractsAVisibleSnippetFromASyntheticReceiptImage`。`semantic-port`。要求模型能从 receipt 图中提取一段明显可见的文本片段。
11. **Source:** `real-cli.test.ts:301` `applies systemPrompt and appendSystemPrompt behavior to the final output`。**Target:** `e2e_real_cli_test.go::TestE2EOptionBehavior_AppliesSystemPromptAndAppendSystemPromptBehaviorToTheFinalOutput`。`literal-port`。要求最终 JSON 同时包含 `SYS_TAG_ALPHA` 与 `APPEND_TAG_BETA` 两个标记。
12. **Source:** `real-cli.test.ts:321` `reads system prompts from files and can access cwd/additionalDirectories`。**Target:** `e2e_real_cli_test.go::TestE2EOptionBehavior_ReadsSystemPromptsFromFilesAndCanAccessCwdAndAdditionalDirectories`。`literal-port`。要求模型能读到 `cwd` 和 `additionalDirectories` 中的 probe 文件，并体现 `systemPromptFile` / `appendSystemPromptFile` 的提示词效果。
13. **Source:** `real-cli.test.ts:372` `records tool restrictions, debug files, settings and plugin directory in the real spawn args`。**Target:** `e2e_real_cli_test.go::TestE2EOptionBehavior_RecordsToolRestrictionsDebugFilesSettingsAndPluginDirectoryInTheRealSpawnArgs`。`literal-port`。要求真实 CLI 的 spawn args 中完整出现 `allowedTools`、`disallowedTools`、`tools`、`settings`、`pluginDir`、`debug`、`debugFile`、`betas`、`name` 等 flag，并生成非空 debug file。
14. **Source:** `real-cli.test.ts:433` `uses configured agent identity and noSessionPersistence blocks implicit reuse`。**Target:** `e2e_real_cli_test.go::TestE2ESessionModesAndAgents_UsesConfiguredAgentIdentityAndNoSessionPersistenceBlocksImplicitReuse`。`literal-port`。要求 agent 角色配置能影响最终回答，同时 `NoSessionPersistence=true` 的第二次执行不能隐式继承前一轮记忆。
