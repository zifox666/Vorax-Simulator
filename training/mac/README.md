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
  --timesteps 200000 \
  --envs 16 \
  --pet-refreshes 2 \
  --output models/vorax-mac-ppo
```

客户端默认通过一次批量请求同步推进 16 局；API Key 的桶容量应至少为 `--envs`。Apple
Silicon 会自动使用 MPS；Intel Mac 自动使用 CPU。若 MPS 出现兼容性问题，可增加
`--device cpu`。训练完成后生成：

- `models/vorax-mac-ppo.zip`：MaskablePPO 模型
- `models/vorax-mac-ppo.json`：训练版本、参数和评估分数，不包含 API Key
- `runs/`：TensorBoard 日志

查看训练曲线：

```bash
uv run --project training/python tensorboard --logdir runs
```

续训：

```bash
uv run --project training/python vorax-train \
  --resume models/vorax-mac-ppo.zip \
  --timesteps 200000 \
  --output models/vorax-mac-ppo-v2
```

常用参数可通过 `uv run --project training/python vorax-train --help` 查看。
