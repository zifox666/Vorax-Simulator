# 外部 AI 训练 API

训练 API 将模拟器作为有限信息强化学习环境暴露给外部程序。训练器只能获得玩家界面可见的
观察和由这些观察编码出的固定张量；`episodeToken` 使用 AES-GCM 加密，不能读取 seed、RNG
流或内部怪物 ID。

## 启用与创建 Key

设置管理员令牌并启动服务：

```powershell
$env:ADMIN_TOKEN = '替换为足够长的随机值'
go run ./cmd/server
```

打开 <http://127.0.0.1:8080/admin>，输入 `ADMIN_TOKEN` 后创建训练 Key。完整 Key 只显示一次。
Key 可配置桶容量、每秒补充令牌数和可选过期时间；批量 N 个环境操作消耗 N 个令牌。

未配置 `DATABASE_URL` 时，Key 哈希默认保存在 `.local/training-api-keys.json`；配置 PostgreSQL
后存入 `training_api_keys` 表。未配置 Redis 时使用当前进程的内存桶；配置 Redis 后桶状态在
多实例间共享。明确配置了 Redis 但连接失败时，训练接口返回 `503`。

## HTTP 流程

所有训练请求使用：

```text
Authorization: Bearer vxtrain_<id>_<secret>
Content-Type: application/json
```

获取稳定动作目录和张量规格：

```powershell
Invoke-RestMethod http://127.0.0.1:8080/api/v1/training/spec `
  -Headers @{Authorization="Bearer $env:VORAX_TRAINING_KEY"}
```

创建环境：

```json
POST /api/v1/training/reset
{"seed":"experiment-42","petRefreshes":2}
```

执行离散动作目录中的动作：

```json
POST /api/v1/training/step
{"episodeToken":"vte1....","actionIndex":0}
```

也可提交结构化动作：

```json
{"episodeToken":"vte1....","action":{"type":"choose","cardId":"awakening","targetSlots":[2]}}
```

`actionIndex` 与 `action` 必须且只能提供一个。只有响应 `actionMask` 中值为 `1` 的动作可执行。
响应的 `reward` 为本步前后分数差，`terminated` 表示自然终局，当前规则不会产生截断，因此
`truncated` 固定为 `false`。所有 ProtoJSON `int64` 字段均为十进制字符串。

批量端点为：

- `POST /api/v1/training/batch/reset`
- `POST /api/v1/training/batch/step`

请求结构是 `{"items":[...]}`，支持 1–256 项。结果保持输入顺序；单项错误放在对应结果的
`error` 字段中，其他项仍正常执行。鉴权、限流和整体 JSON 结构错误会拒绝整个请求。

## Gymnasium

```powershell
uv sync --project training/python --extra test
uv run --project training/python python training/python/examples/random_masked.py
```

```python
from vorax_gym import VoraxEnv

env = VoraxEnv("http://127.0.0.1:8080", "vxtrain_...")
observation, info = env.reset(seed=42)
observation, reward, terminated, truncated, info = env.step(0)
```

`VoraxEnv` 使用 `Discrete(actionCount)`；张量与 `action_mask` 位于固定 `Dict` 观察中，完整语义
观察位于 `info["semantic_observation"]`。`VoraxVectorEnv` 直接调用批量端点，并使用
`AutoresetMode.DISABLED`；终局后须通过 `reset(options={"reset_mask": mask})` 显式重置。

macOS 上直接训练并保存 MaskablePPO 模型的步骤见 [`training/mac/README.md`](../training/mac/README.md)。

## 版本与复现

`/training/spec` 返回 `specVersion`、`specHash`、规则/内容/RNG 版本及卡牌、怪物、动作索引。
训练代码应缓存并校验这些值；目录变化时必须重新读取规格。相同 seed 与动作轨迹会产生相同
观察和奖励，但加密令牌因随机 nonce 不会逐字节相同。令牌允许重放和分支，便于对照实验。
