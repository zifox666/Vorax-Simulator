# 渴瘾实验室 / Vorax Simulator

对渴瘾玩法的简单模拟

## 内置 AI 模块

服务端内置一个"有限信息"AI 决策模块（`internal/ai`）：它只能看到玩家在 UI 上能看到的内容，
看不到 seed 与 RNG 流，用于研究"隐藏信息下算法能否打出最优解"。接口与说明见
[`docs/ai.md`](docs/ai.md)：

```powershell
POST /api/v1/ai/decide
# { "stateToken": "...", "strategy": "sampler", "rollouts": 16 }
# → { "action": {"type":"choose","cardId":"...","targetSlots":[2]}, "observation": {...} }
```

## 外部 AI 训练

服务端提供有限信息的单局与批量 `reset/step` API、加密无状态训练令牌，以及 Gymnasium
单环境/VectorEnv 客户端。设置 `ADMIN_TOKEN` 后可在 `/admin` 创建带过期时间和令牌桶的训练
API Key。完整接口、部署和 Python 示例见 [`docs/training-api.md`](docs/training-api.md)。

## 开始使用

推荐使用 Docker

```powershell
docker compose -f docker-compose.example.yaml up --build
```

首次构建完成后，在浏览器打开 <http://127.0.0.1:8080/>。服务仅绑定本机，关闭终端或按 `Ctrl+C` 即可停止。

再次启动不需要重新构建：

```powershell
docker compose -f docker-compose.example.yaml up
```

Docker 会保留模拟器签名密钥和内容数据库，因此容器重启后仍可验证已有存档。若要连同这些本地数据一起清除，请执行：

```powershell
docker compose -f docker-compose.example.yaml down -v
```

此操作不可恢复，会清除 Docker 中的模拟器密钥和内容数据；浏览器中的历史记录仍需在浏览器设置中另行清除。

### 不使用 Docker

需要 Go 1.24 或更高版本：

```powershell
go run ./cmd/server
```

然后访问 <http://127.0.0.1:8080/>。首次运行会在 `.local/signing.key` 创建本地签名密钥，请勿删除或覆盖它，否则现有存档无法验证。

## 如何游玩

1. 选择宠物提供的 0／1／2 次用具刷新；种子留空即可自动生成。
2. 接受或跳过未知器具，再从候选用具、药剂或方案中作出选择。
3. 选中药剂后，按提示选择培养槽并确认。开局先选一件核心用具，不计回合且不触发回合结束效果。八次药剂和三次方案正常计回合，8000／28000 分赠送的用具各增加一个回合，整局最多 13 回合（2＋8＋3）。
4. 历史页面可继续未完成的对局、用同一种子重开，或验证完整操作记录。

相同的种子只有在规则版本、内容版本、宠物配置和操作序列都一致时，才保证得到相同结果。

## 真实游戏辅助决策参考

本项目附带一个简单的OCR决策处理，使用方法参考 [Readme](app/README.md)，请自行构建

## 本地数据与网络

对局、历史分数和操作记录存储在当前浏览器的 IndexedDB 中，不会跨设备同步。请始终通过同一域名和端口访问，例如 `127.0.0.1:8080`，否则浏览器会视为不同的本地存储空间。

界面会优先加载 Vue 的固定版本 CDN；网络不可用或加载超时时，会自动使用项目内置副本。继续对局仍需要本地服务保持运行。

### 通过 HTTPS 域名访问

若使用 Nginx、Caddy、Cloudflare 等反向代理为网站提供 HTTPS，请在启动服务的环境中设置准确的公开地址，再启动容器：

```powershell
$env:PUBLIC_ORIGIN='https://example.com'
docker compose -f docker-compose.example.yaml up -d --build
```

`PUBLIC_ORIGIN` 必须与浏览器地址栏中的协议和域名完全一致，不要包含路径或末尾斜杠。它让服务在 TLS 由反向代理终止时仍能正确验证同源请求。

## 致谢

感谢 B站 UP @明玄丶 的教学视频（[《渴瘾玩法教学》](https://www.bilibili.com/video/BV1vMN16AEWU)）与卡牌整理，本模拟器基于其内容参考完善。

## 免责声明与许可

模拟器内容为测试资料，正确资讯请以官方游戏内的内容为准。本项目不隶属于火炬之光: 无限，也未得到心动网络股份有限公司的认可。心动网络对本项目的内容或功能不承担任何责任，也不对使用本项目而产生的任何损害承担责任。

与这些商标相关的所有艺术作品、截图、角色、车辆、故事情节、世界事实或其他可识别的知识产权特征同样属于心动网络的知识产权。本项目遵循心动网络的使用许可条款，仅用于学习交流和非商业用途。

本项目作者拥有版权的源代码以 [GPL-3.0-only](./LICENSE) 发布；该许可不授予任何游戏相关商标、艺术作品、截图、角色、车辆、故事情节、世界观资料或其他第三方知识产权的使用权。完整的第三方声明见 [NOTICE.md](./NOTICE.md)。Vue 3 以 MIT 许可证提供，其文本保留在 `web/assets/vue.LICENSE`。
