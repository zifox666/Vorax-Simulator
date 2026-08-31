# 有限信息 AI 模块（internal/ai）

服务端内置的"有限信息"AI：它只能看到玩家在 UI 上能看到的内容（培养槽、卡牌、分数、
奖励进度、刷新次数等），**看不到 seed 与三条 RNG 流**。信息边界由类型强制：
所有决策函数只接受 `*ai.Observation`，绝不接收 `*pb.GameState`。

模块不修改任何现有对局逻辑：`engine.Apply`、协议、前端都保持原样，只新增一个只读决策端点。

## 信息边界与公平推演

- **观察（Observation）**：由 `ai.FromGameState` 从真实状态构建，只提取 UI 渲染字段。
  观察 JSON 中不存在 `seed / initRng / offerRng / effectRng / runId / stateToken`。
  单元测试 `TestInformationBoundary` 会序列化观察并断言不含这些关键词。
- **推演（simEnv）**：AI 做算法决策（greedy / sampler）时需要推演未来。推演状态由
  `ai.RebuildState` 从观察重建，公开字段与观察一致，**RNG 被随机重播**。
  关键性质：本引擎的随机抽取除怪物稀有度外（按固定权重 45/30/20/5 抽取，见
  `effects.go` 的 `monsterRarityWeights`）都是均匀独立抽样，
  与 RNG 具体位置无关。因此从任意随机位置推演得到的终局分布，就是玩家面对未知未来时的
  真实条件分布 —— 每次推演 = 抽样一个"可能的未来"，不存在透视优势。
  `TestRebuildStateDistinctFutures` 验证重建状态的 RNG 各不相同。
- **对照（oracle）**：`agent/oracle.go` 只存在于研究代码中（见 `research/`），直接读取
  真实 RNG 做确定性束搜索，用于量化"信息隐藏的代价"，不作为服务端 AI 策略暴露。

## API

```
POST /api/v1/ai/decide
```

两种调用方式：

### 1. stateToken 模式（推荐，信息边界由服务端保证）

```json
{
  "stateToken": "local-v1.<base64(protobuf)>.<hmac>",
  "strategy": "sampler",
  "rollouts": 16
}
```

服务端验签恢复存档后，只把可见观察交给 AI。

### 2. observation 模式（外部研究脚本）

```json
{
  "observation": { "phase": "CHOOSING", "score": 516, "slots": [...], "cards": [...], ... },
  "strategy": "greedy",
  "samples": 24
}
```

### 响应

```json
{
  "action": { "type": "choose", "cardId": "awakening", "targetSlots": [2] },
  "strategy": "sampler",
  "observation": { "...AI 实际看到的全部可见字段..." }
}
```

`action.type` 为 `choose | refresh | skip_unknown`；`targetSlots` 是槽位序号（与 UI 一致），
调用方执行时将其映射为协议中的怪物 id。

### 输入观察结构（Observation）

| 字段 | 类型 | 说明 |
|---|---|---|
| phase | string | PREPARING / CHOOSING / FINISHED |
| stageLabel | string | 界面阶段标题（如"药剂选择 3 / 8"） |
| baseCursor / completedTurns | int | 基础选择进度 / 已完成回合 |
| score | int64 | 当前分数 |
| slots | SlotView[6] | 每个培养槽的怪物定义 ID、名称、族、稀有度、活性、数量（0 为空） |
| tools / toolNames | string[] | 已拥有用具 id 与名称 |
| offer | {kind, rewardThreshold} | 当前候选类别与用具奖励门槛 |
| cards | CardView[] | 候选卡：id、名称、描述、稀有度、可玩性、合法目标（槽位组合） |
| canSkip / canRefresh | bool | 可跳过未知器具 / 可刷新（`canRefresh` 仅展示，决策以计数为准） |
| potionRefreshes / toolRefreshes | int | **剩余刷新次数：药剂固定整局 3 次；用具 = 宠物提供的 0/1/2 次** |
| rewards | {jars, dropBonusPercent, nextThreshold, nextRewardLabel, toolClaims} | 奖励面板 |

### 刷新次数语义（重要）

两类刷新**互不通用**，由候选类别决定消耗哪个计数器：

| 候选类别 | 消耗的计数器 | 初始值 |
|---|---|---|
| 药剂（offer.kind=2） | potionRefreshes | 固定 3 次（整局） |
| 用具（offer.kind=3） | toolRefreshes | 宠物提供：创建对局时 petRefreshes = 0/1/2 |

- `POST /api/v1/ai/decide` 的 **observation 模式必须传入剩余刷新次数**
  （`potionRefreshes` / `toolRefreshes` 字段），否则决策枚举会缺失或错误地包含刷新动作。
- AI 决策**不信任外部传入的 `canRefresh` 标志**：刷新可用性由
  `offer.kind + 对应计数器 > 0` 重新推导。即使调用方传错 `canRefresh`，计数为 0 时
  也不会返回 `refresh` 动作（`TestRefreshAvailabilityByKindAndCounters` 覆盖）。
- 推演中 `refresh` 只消耗与候选类别匹配的计数器
  （`TestRefreshConsumesCorrectCounter` 覆盖：药剂候选刷新后 potionRefreshes 3→2、
  toolRefreshes 不变；用具候选反之）。
- 宠物 0/2 的分别决策与用具刷新实际使用次数见 `TestAIBenchmark` 输出。

## 策略

| strategy | 行为 | 说明 |
|---|---|---|
| random | 随机合法动作 | 基线 |
| greedy | 期望即时分最大（每动作抽样 samples 次一步推演） | 玩家直觉 |
| sampler | 期望终局分最大（每动作抽样 rollouts 次完整贪心对局） | 隐藏信息下的近似最优 |

参数 `samples`（默认 24，上限 128）与 `rollouts`（默认 16，上限 64）防止计算量失控。

## 已知结论（内部测试与 research/ 实验）

- **信息可见时**（AI 能看到 RNG）：确定性束搜索可稳定打出 2M–4.7M 分，远超贪心 2–4 倍、
  随机 20–40 倍 —— 这是"算法强"的直接证据。
- **信息隐藏时**（本模块）：算法仍是决定性因素，但采样方差显著；单局 `sampler` 可能输给
  `greedy`（集成测试仅断言正确性，统计对比见 `TestAIBenchmark`）。
- 服务端**按设计**把 RNG 明文下发给客户端（架构文档："不承诺隐藏种子或防止玩家分析未来
  随机结果"）。本模块刻意不利用这一点 —— 它只消费 UI 可见字段，作为"公平 AI"的参考实现；
  若要让真实玩家也处于同等信息水平，需要另行收紧协议。

## 测试

```powershell
go test ./internal/ai/ -v                    # 信息边界 / 重建 / 刷新语义 / 策略
go test ./internal/transport/ -run TestAIDecideObservationModeUsesRefreshes -v  # 外部请求传刷新次数
go test ./internal/application/ -run TestAIIntegration -v   # 端到端正确性
go test ./internal/application/ -run TestAIBenchmark -v     # 隐藏信息统计对比（宠物 0/2 分别报告，较慢）
```
