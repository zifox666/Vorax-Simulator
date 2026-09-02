# 脱敏牌局训练数据

服务端在配置 PostgreSQL 后同步记录每局牌局和每次合法决策。普通恢复请求、AI 推荐请求、失败或过期命令不会产生训练样本。

## 隐私边界

- `player_pseudonym` 与局 ID 都由服务端密钥进行 HMAC-SHA-256，不保存浏览器原始匿名 ID或原始对局 ID。
- 不保存 IP、User-Agent、请求 ID、状态令牌和三条 RNG 游标。
- 决策观察复用有限信息 AI 的 `Observation`，只包含玩家当时在界面可见的局面、候选和剩余刷新次数。
- 卡牌目标从本局怪物实例 ID转换为 0–5 的槽位序号。
- 种子为精确重放所必需，按原值保存；页面明确提醒玩家不要在种子中填写个人信息。
- 这是“假名化”而不是差分隐私：同一安装的多局记录仍可通过 keyed pseudonym 关联，以便训练跨局行为模型。

## 数据表

`training_episodes` 每局一行，包含假名玩家、种子、规则/内容/RNG 版本、宠物刷新配置、初始可见观察和样本数。

`training_transitions` 每次决策一行，包含：

- 决策前、后的 gameplay hash 与 revision；
- `refresh`、`skip_unknown` 或 `choose` 动作，所选卡牌和有序目标槽；
- `observation_before`、`observation_after` 与本步结算事件；
- 决策后分数和是否终局。

训练样本可直接查询：

```sql
SELECT
  e.seed,
  t.observation_before,
  jsonb_build_object(
    'type', t.action_type,
    'cardId', t.selected_card_id,
    'targetSlots', t.selected_target_slots
  ) AS action,
  t.observation_after,
  t.score_after,
  t.terminal
FROM training_transitions t
JOIN training_episodes e ON e.id = t.episode_id
ORDER BY t.recorded_at, t.revision_before;
```

创建和命令写入都使用幂等键；请求重试不会重复计数。同一签名存档若被分支练习，不同动作会保留为不同训练转移。

## 配置

- 默认使用 `DATABASE_URL`。
- `TELEMETRY_DATABASE_URL` 可指定独立 PostgreSQL；未配置任何数据库时，本地开发模式不记录。
- 建议设置长期固定、至少 32 随机字节的 `TELEMETRY_HMAC_KEY_BASE64`。未设置时会使用当前签名密钥；轮换签名密钥后，同一玩家的假名将无法与旧数据关联。

训练数据写入与命令响应同步完成。数据库写入失败时，本次命令返回服务内部错误，客户端可安全重试，避免悄悄漏记决策。
