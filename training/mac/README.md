# 在 Mac 上训练模型

客户端使用 Python、Gymnasium 和 `sb3-contrib` 的 MaskablePPO。动作掩码会阻止模型选择当前
不可执行的卡牌或目标。管理员需要先在模拟器 `/admin` 页面创建训练 API Key。

## 1. 安装

建议使用 Python 3.11 或 3.12。安装 Homebrew 和 uv 后，在仓库根目录执行：

```bash
brew install uv
uv sync --project training/python --extra train
```

## 2. 配置连接

如果模拟器也运行在这台 Mac：

```bash
export VORAX_SERVER='http://127.0.0.1:8080'
export VORAX_TRAINING_KEY='vxtrain_这里替换为完整密钥'
```

如果模拟器运行在另一台机器，把 `VORAX_SERVER` 改成它的 HTTPS 地址。不要把训练 Key 写进
脚本或提交到 Git；终端关闭后环境变量即失效。

## 3. 开始训练

```bash
uv run --project training/python vorax-train \
  --timesteps 2000000 \
  --envs 32 \
  --pet-refreshes 2 \
  --reward-mode tier \
  --floor-score 607000 \
  --excellent-score 721000 \
  --score-cap 1120000 \
  --preference-weight 0.1 \
  --preference-final-weight 0 \
  --preference-decay-fraction 0.4 \
  --foundation-refresh-weight 0.15 \
  --observation-encoder categorical \
  --learning-rate 0.0001 \
  --n-epochs 5 \
  --clip-range 0.15 \
  --target-kl 0.02 \
  --ent-coef 0.003 \
  --evaluate 300 \
  --eval-envs 128 \
  --tui \
  --output models/vorax-tier-ppo
```

客户端默认通过一次批量请求同步推进 16 局；API Key 的桶容量应至少为 `--envs`。Apple
Silicon 会自动使用 MPS；Intel Mac 自动使用 CPU。若 MPS 出现兼容性问题，可增加
`--device cpu`。训练完成后生成：

默认 `--reward-mode tier` 直接优化业务档位：607k 获得保底奖励，721k 获得更大的优秀奖励，
721k–1120k 之间缓慢增加，超过 1120k 后训练奖励严格封顶。每步采用档位效用差分，所以整局
累计奖励等于最终档位效用，不会因中间步骤重复领奖。需要复现实验时可传 `--reward-mode score`
恢复旧的 `score / reward-scale` 目标。

`--preference-weight` 会把 [`training/流派偏好.md`](../流派偏好.md) 中的开局判断、核心用具、
铺场药剂和增益药剂转换为额外训练奖励。每局在开局用具阶段锁定一个流派，之后不会因怪物
变异而换流派。默认在训练前 40% 从 0.1 线性衰减到 0，让后半程完全由档位结果决定。较低
学习率、5 个 epoch、0.15 裁剪和 0.02 KL 早停用于避免单轮策略更新过猛。

`--foundation-refresh-weight` 是独立的稀疏基础奖励：目标开局用具出现就选，缺失时使用 pet2
提供的用具刷新；前四瓶没有当前流派的核心／铺垫药剂时使用药剂刷新。错误刷新会扣分，目标
卡已出现时不会为了耗尽次数继续刷新。建议 pet2 训练使用 `0.1–0.15`，最终仍由档位奖励主导。

默认 `--observation-encoder categorical` 对类别做 one-hot、对大数值归一化并从网络输入中
移除动作掩码。`legacy` 仅用于续训旧编码模型；续训时以模型实际保存的编码为准。

- `models/vorax-mac-ppo.zip`：MaskablePPO 模型
- `models/vorax-mac-ppo.json`：训练版本、参数和评估分数，不包含 API Key
- `runs/`：TensorBoard 日志
- `reports/vorax-tier-ppo-seed42.json`：训练后自动生成的完整评估与逐局诊断

只要 `--evaluate` 大于 0，训练保存模型后会立即使用同一个模型完成固定种子评估，并把完整
报告自动写入 `reports/<模型名>-seed<seed>.json`，不需要再运行一次 `vorax-eval`。可用
`--report 路径.json` 覆盖默认位置，或用 `--evaluate 0` 同时跳过评估和报告。

诊断报告包含每局的初始攻略流派、期望与实际开局用具、两件奖励用具、八张药剂及目标槽、
方案、刷新位置、攻略偏离次数、基础动作错误次数、最终用具与六个槽位；摘要按流派和实际
开局用具分别计算均分、保底率、优秀率及错配率。

传入 `--tui` 可在终端实时查看训练总进度、吞吐、预计剩余时间、PPO 指标，以及并行环境 0 的阶段、分数、可选卡牌与合法动作数量。仪表盘还会显示原始奖励、解释方差、价值损失与近似 KL 的滚动折线，并将近期窗口与前一窗口对比为“改善 / 变弱 / 持平”。TUI 默认每秒重绘一次；使用 64+ 并行环境时，可用 `--tui-refresh 2` 降低终端渲染开销。按 `Ctrl+C` 可安全停止显示并中断训练。

查看训练曲线：

```bash
uv run --project training/python tensorboard --logdir runs
```

评估已保存模型（不会继续训练）：

```bash
uv run --project training/python vorax-eval \
  --model models/vorax-maskable-ppo.zip \
  --episodes 1000 \
  --envs 256 \
  --target-floor-rate 0.8 \
  --target-excellent-rate 0.5 \
  --pet-refreshes 2 \
  --report reports/vorax-eval-1000.json
```

输出除常规分数统计外，还包含失败、保底、优秀、封顶局数，保底率、优秀率，以及把低于
607k 计零并在 1120k 封顶后的有效期望。JSON 报告会保存每局分数，便于按同一批种子比较。
评估默认使用批量训练 API 并行推进；`--envs 256` 会同时评估 256 个固定种子，结果顺序和单环境
评估一致，但把每回合成百上千次串行 HTTP 请求压缩为少量批量请求。

续训：

```bash
uv run --project training/python vorax-train \
  --resume models/vorax-mac-ppo.zip \
  --timesteps 200000 \
  --output models/vorax-mac-ppo-v2
```

常用参数可通过 `uv run --project training/python vorax-train --help` 查看。
