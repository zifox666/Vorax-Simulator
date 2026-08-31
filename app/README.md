# 渴瘾对局决策辅助

Python 3.12 / uv / 本地 PP-OCRv6 CPU / httpx.AsyncClient / Textual TUI。

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
./dist/VoraxAssistant/VoraxAssistant.exe --server http://127.0.0.1:8080

uv run python -X utf8 -m pytest -q
# 从项目根目录运行新增接口测试：
go test ./internal/transport -run '^TestVisible'
```

测试使用已有截图产生的 OCR JSON，覆盖空槽、文字规范化、数字核对、开局/奖励用具、刷新/开箱、漏扫、断线重试和服务端合法目标；不进行 UI 自动操作。
`--rollouts 1..64` 控制推演采样次数，默认 16。增加采样会增加耗时，结果仍是模拟规则下的近似推荐，不能保证最优。
