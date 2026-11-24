# sleepy-bookmark

自用的小说 / 漫画阅读微信小程序：无广告，书源可配置，支持收藏、历史、离线下载和服务器预缓存。

- `miniapp/`：小程序，uni-app + Vue 3 + TypeScript + Pinia + wot-design-uni
- `server/`：后端，Go 单二进制 + SQLite + 磁盘缓存
- `deploy/`：Docker Compose + Caddy（自动 HTTPS）
- `docs/source-rule.md`：书源规则格式

```
小程序 ──HTTPS──> Go 后端 ──> 书源规则引擎 ──> 各小说 / 漫画站点
                    ├─ SQLite：用户、书源、书籍、目录、收藏、历史
                    └─ 磁盘：章节正文 (gzip)、图片缓存
```

小程序只和后端通信。搜索、目录、正文和图片都由后端按书源规则抓取，图片经带签名的 `/api/img` 代理（解决防盗链和小程序合法域名限制）。

## 本地开发

需要 Go 1.27+ 和 Node 20+。

```bash
# 后端：免微信登录 + 内置演示书源
cd server
DEV_LOGIN=true DEMO_SOURCE=true go run ./cmd/server

# 小程序
cd miniapp
npm install
npm run dev:mp-weixin   # 用微信开发者工具导入 miniapp/dist/dev/mp-weixin
npm run dev:h5          # 或者在浏览器里调试
```

- `DEMO_SOURCE=true` 会在后端挂一个假的小说站和漫画站，并自动添加两个书源，搜索「演示」即可走通全流程。
- 微信开发者工具里需要勾选「不校验合法域名」，并在 `miniapp/src/manifest.json` 的 `mp-weixin.appid` 填入自己的 AppID。
- 后端地址在 `miniapp/.env.development` / `miniapp/.env.production` 的 `VITE_API_BASE`。

检查命令：

```bash
cd server && go vet ./... && go test ./...
cd miniapp && npm run type-check && npm run build:mp-weixin
```

## 后端配置（环境变量）

| 变量 | 默认值 | 说明 |
|---|---|---|
| `ADDR` | `:8080` | 监听地址 |
| `DATA_DIR` | `./data` | SQLite、正文缓存、图片缓存、自动生成的 JWT 密钥 |
| `PUBLIC_URL` | `http://127.0.0.1:<端口>` | 后端的对外地址，演示书源用它拼地址 |
| `WX_APPID` / `WX_SECRET` | 空 | 小程序凭据，用于 `wx.login` 换 openid |
| `OPENID_WHITELIST` | 空 | 允许登录的 openid，逗号分隔。留空时只有第一个登录的微信号能用 |
| `JWT_SECRET` | 自动生成 | 留空则生成后保存在 `DATA_DIR/jwt.secret` |
| `DEV_LOGIN` | `false` | 跳过微信登录，所有请求都是同一个用户。只用于本地开发 |
| `DEMO_SOURCE` | `false` | 挂载内置演示站点和书源 |
| `PREFETCH_AHEAD` | `5` | 打开一章时后台预抓后面几章 |
| `REFRESH_INTERVAL_MINUTES` | `180` | 定时刷新书架上书籍目录并缓存新章节的间隔 |
| `IMG_CACHE_MAX_MB` | `2048` | 图片缓存上限，超出后按最久未访问清理 |
| `KEPT_MAX_MB` | `10240` | 书架上的漫画保存图片的上限，这部分不会被自动清理；超出后新图片只进图片缓存 |

## 部署（体验版）

```bash
cd deploy
cp .env.example .env    # 填域名、WX_APPID、WX_SECRET
docker compose up -d --build
```

然后：

1. 在小程序后台把域名加入 request 和 downloadFile 合法域名。
2. 把 `miniapp/.env.production` 的 `VITE_API_BASE` 改成 `https://你的域名`。
3. `npm run build:mp-weixin`，用微信开发者工具导入 `miniapp/dist/build/mp-weixin` 并上传，在小程序后台设为体验版。

## 功能

- 左上角的图标在小说 / 漫画之间整体切换，书架、搜索、历史都按当前模式过滤
- 书源管理：新增、批量导入、编辑、启停、删除、在线测试
- 多书源并发搜索，可以只搜某一个书源；搜索框右侧的按钮展开高级选项，可选模糊（默认，按书名相关度排序）或精确（书名完全一致）
- 搜索页按书源分组显示推荐，每个书源前 10 本（书源规则里配了 `recommend` 才有）
- 小说阅读器：字号、四种背景、连续加载下一章、目录跳转
- 漫画阅读器：纵向长条、懒加载
- 书架、历史和阅读进度保存在服务器
- 离线下载：章节和漫画图片保存到小程序本地文件（上限约 200 MB），阅读时优先读本地
- 服务器缓存：阅读时预抓后续章节，定时更新书架，全本缓存任务
- 书源不稳定时的处理：
  - 超时、连接失败、429、5xx 自动重试两次（遵守 `Retry-After`），请求带浏览器常见请求头并保留 Cookie
  - 搜索连续 3 次连不上的书源，之后 10 分钟的搜索会跳过它，不拖慢其他书源；书源管理页会显示最近的失败原因
  - 目录刷新失败时继续用上次的目录，已缓存的章节不受影响
  - 换源：在详情页或阅读页把一本书换到其他书源里的同名书，书架和阅读进度一起迁移
  - 页面内显示「重试」，不弹报错；网络抖动时读取类请求自动再试一次

## 接口

除 `/api/health`、`/api/auth/login`、`/api/img` 外都需要 `Authorization: Bearer <token>`。

| 接口 | 说明 |
|---|---|
| `POST /api/auth/login` | `{code}` 换 token |
| `GET /api/img?s=&u=&sig=` | 图片代理，地址由后端签名生成 |
| `GET/POST /api/sources`，`PUT/DELETE /api/sources/:id` | 书源管理 |
| `PUT /api/sources/:id/enabled` | 启停 |
| `POST /api/sources/test` | 不保存，直接测试一条规则 |
| `GET /api/search?type=&q=&page=&source=&match=` | 搜索。`source` 是书源 id，不传搜全部；`match=exact` 只留书名完全一致的 |
| `GET /api/recommend?type=&source=` | 各书源的推荐，每个书源最多 10 本；`source` 不传返回全部 |
| `GET /api/books/:id`，`GET /api/books/:id/chapters?refresh=1` | 详情、目录 |
| `GET /api/chapters/:id` | 正文或图片列表 |
| `GET /api/books/:id/alternatives` | 其他书源里的同名书 |
| `POST /api/books/:id/switch` | `{to}`，把书架和阅读进度换到另一个书源的同一本书 |
| `POST /api/books/:id/cache`，`GET /api/tasks` | 全本缓存任务 |
| `GET/PUT/DELETE /api/favorites` | 书架 |
| `GET/PUT/DELETE /api/history` | 历史和阅读进度 |

抓取书源失败时返回 502 和 `{message, code}`，`code` 为 `timeout`、`network`、`busy`、`blocked`、`not_found`、`http` 或 `rule`。服务器需要走代理访问书源时，设置环境变量 `HTTPS_PROXY` / `HTTP_PROXY`。

## 已知限制

- 规则引擎不执行 JavaScript，需要 JS 渲染或有强反爬（如 Cloudflare 人机验证）的站点不支持，会提示「访问验证」并建议换源。
- 后端默认只带演示书源。其他书源按 `docs/source-rule.md` 编写；第三方站点改版后规则需要跟着调整。
