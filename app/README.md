# 渴瘾对局决策辅助

Python 3.12 / uv / 本地 PP-OCRv6 CPU / 云端算法或本地 MaskablePPO / Textual TUI。

## 启动

先在项目根目录启动**包含 `/api/v1/ai/catalog` 和 `/api/v1/ai/visible` 的新版服务端**：

```powershell
go run ./cmd/server
```

另开终端进入 `app`：

```powershell
uv sync --locked --python 3.12
uv run vorax-assistant --pet 0
# 服务端在其他地址时：
uv run vorax-assistant --server http://127.0.0.1:8080 --pet 2
```

按 `S` 打开设置，可在两种决策方式之间切换：

- **云端纯算法**：沿用服务端采样推演，`--rollouts 1..64` 控制采样次数。
- **本地模型**：服务端只校验可见数据并生成张量和动作掩码，MaskablePPO 在客户端 CPU 上确定性推理。正式模式要求版本一致；测试模式可做兼容投影。服务端不替模型选择动作。

本地模型不随客户端打包。将训练生成的两个同名文件放到程序旁的 `models` 目录：

```text
VoraxAssistant/
├─ VoraxAssistant.exe
├─ model/                  # 随程序提供的 OCR 模型
└─ models/                 # 用户自行放入，不进入安装包
   ├─ vorax-mac-ppo.zip
   └─ vorax-mac-ppo.json
```

`.json` 保存版本信息和训练时的完整模型契约；正式模式会严格核对，避免动作索引错位。目录中只有一个 `.zip` 时可自动选择；有多个时在设置中指定。源码运行本地模型需安装额外依赖：

```powershell
uv sync --locked --extra local-model
uv run vorax-assistant --decision-backend local
```

实机流程测试时可在设置中开启“测试模式”，或传入 `--test-mode`。测试模式不因规则版本、
内容版本、规格哈希或模型元数据缺失而强制结束当前对局：有完整模型契约时，客户端按卡牌、
怪物、用具和动作的语义 ID 将当前数据投影到模型训练时的目录；新增内容置零或屏蔽，已移除
动作不再交给模型选择。旧模型没有完整契约时会采用已知旧格式迁移，无法识别的格式则按位置
尽力适配。服务端内容在对局中变化时，旧建议会清除，已移除用具改记为未知后继续记录。
只有模型文件无法加载、没有任何合法动作或张量结构根本无法输入模型时才会停止。测试模式的
推荐可能有明显偏差，仅用于验证实机流程；正式使用仍保持严格核对。

模型使用现有 `model/ch_PP-OCRv6_det_infer.onnx`、`ch_PP-OCRv6_rec_infer.onnx` 和 `ch_ppocrv6_dict.txt`。
首次安装依赖需要网络，识别本身在本机 CPU 完成；只有整理后的可见数据发往配置的服务端。
RapidOCR 3.9.2 的本地模型参数见[官方说明](https://rapidai.github.io/RapidOCRDocs/main/install_usage/rapidocr/parameters/)。

## 离线输入与诊断

```powershell
# 只运行 OCR，不需要服务端，也不操作游戏窗口：
uv run vorax-assistant --image screenshots/2.png --ocr-only --output .local/ocr.json
# 用 OCR JSON 直接接入服务端：
uv run vorax-assistant --ocr-json .local/ocr.json --data .local/example --new
```

输入格式如下，坐标单位为截图像素，四点为文字框顶点；画面尺寸由 OCR 适配层携带：

```json
{"width":2789,"height":1645,"lines":[{"text":"手术准备","box":[[141,63],[395,63],[395,135],[141,135]],"confidence":0.99}]}
```

此例仅说明结构，不是完整对局输入。完整示例在 `tests/fixtures/2.json`。

## 打包与精简测试

```powershell
./build.ps1
# 分发整个 dist/VoraxAssistant 目录，不要只复制 exe。
# dist/VoraxAssistant/models 为空，模型由用户自行放入。
./dist/VoraxAssistant/VoraxAssistant.exe --server http://127.0.0.1:8080

uv run python -X utf8 -m pytest -q
# 从项目根目录运行新增接口测试：
go test ./internal/transport -run '^TestVisible'
```

测试使用已有截图产生的 OCR JSON，覆盖空槽、文字规范化、数字核对、开局/奖励用具、刷新/开箱、漏扫、断线重试和服务端合法目标；不进行 UI 自动操作。
`--rollouts 1..64` 控制推演采样次数，默认 16。增加采样会增加耗时，结果仍是模拟规则下的近似推荐，不能保证最优。
