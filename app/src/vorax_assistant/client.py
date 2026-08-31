import httpx


class ServiceError(RuntimeError):
    pass


class Client:
    def __init__(self, url: str, rollouts: int = 16):
        self.http = httpx.AsyncClient(base_url=url.rstrip("/"), timeout=httpx.Timeout(35, connect=5))
        self.rollouts = rollouts

    async def close(self):
        await self.http.aclose()

    async def request(self, method: str, path: str, **kwargs) -> dict:
        try:
            response = await self.http.request(method, path, **kwargs)
            if response.status_code == 404:
                raise ServiceError("服务端缺少 OCR 接口，请使用本项目更新后的服务端")
            data = response.json()
            if response.is_error:
                raise ServiceError(data.get("message", f"服务端返回 HTTP {response.status_code}"))
            return data
        except httpx.HTTPError as exc:
            raise ServiceError(f"连接服务端失败：{exc}") from exc
        except ValueError as exc:
            raise ServiceError("服务端返回的内容不是 JSON") from exc

    async def catalog(self) -> dict:
        return await self.request("GET", "/api/v1/ai/catalog")

    async def suggest(self, visible: dict) -> dict:
        return await self.request("POST", "/api/v1/ai/visible", json={
            "visible": visible, "strategy": "sampler", "rollouts": self.rollouts,
        })
