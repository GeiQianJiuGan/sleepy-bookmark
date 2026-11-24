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
- 微信开发者工具里需要勾选「不校验合法域名」，并在根目录 `app.config.json` 中配置 `appId`（构建/开发时会自动同步到 `miniapp/src/manifest.json`）。
- 后端地址统一在根目录 `app.config.json` 的 `serverUrl` 中配置（也可在 `miniapp/.env.development` / `miniapp/.env.production` 中通过 `VITE_API_BASE` 按环境覆盖）。

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
2. 把根目录 `app.config.json` 的 `serverUrl`（或 `miniapp/.env.production` 的 `VITE_API_BASE`）改成 `https://你的域名`。
3. `npm run build:mp-weixin`，用微信开发者工具导入 `miniapp/dist/build/mp-weixin` 并上传，在小程序后台设为体验版。

### 登录失败时

登录页会显示原因，服务端日志（`journalctl -u sleepy-bookmark`）里有微信返回的原始错误码和 rid。

- **AppID 不一致**：小程序运行时的 AppID 和服务端的 `WX_APPID` 不是同一个。检查开发者工具「详情 → 基本信息」里的 AppID（测试号、游客模式都不行）。
- **登录凭证无效（40029）**：`wx.login` 的 code 只对签发它的 AppID 有效、只能用一次、5 分钟内有效。服务端的 AppID 和 AppSecret 不对时是另外两个错误码（40013、40125）。
- **微信开发者工具返回的是模拟登录凭证**：登录开发者工具的微信号不是该小程序的管理员或开发者时，`wx.login` 返回固定的 `the code is a mock one`。在小程序后台「成员管理」把该微信号加为开发者后重新登录开发者工具，或在手机上预览。
- **当前不在微信小程序内**：H5 版没有微信登录，只能连接 `DEV_LOGIN=true` 的后端。

## 功能

- 登录：启动时本机有 refresh token 就换新 token，没有或已失效就进登录页；登录页先弹《用户协议与隐私政策》，同意后才调用微信登录；「我的」里可以退出登录、查看政策和撤回同意
- 左上角的图标在小说 / 漫画之间整体切换，书架、搜索、历史都按当前模式过滤
- 书源管理：新增、批量导入、编辑、启停、删除、在线测试
- 多书源并发搜索，可以只搜某一个书源；搜索框右侧的按钮展开高级选项，可选模糊（默认，按书名相关度排序）或精确（书名完全一致）
- 搜索页按书源分组显示推荐，每个书源前 10 本（书源规则里配了 `recommend` 才有）
- 阅读器：小说和漫画都是上下滑动，不显示滚动条，滑到底自动接下一章；点屏幕中间弹出菜单（上一章 / 下一章、目录、背景、夜间模式；小说可调字号和行距，漫画可调页间距和图片线路）
- 书架、历史和阅读进度保存在服务器
- 阅读缓存：小说读过的章节和后面 3 章自动存在本机（小程序里上限 30 MB），满了先删最久没读的；漫画不在本机缓存
- 离线下载：章节和漫画图片保存到小程序本地文件（上限约 200 MB），和阅读缓存分开存放，不会被自动清理，阅读时优先读本地
- 服务器缓存：阅读时预抓后续章节，定时更新书架，全本缓存任务
- 加入书架的漫画由服务器保存全部图片（单独存放，不随图片缓存清理，上限 `KEPT_MAX_MB`），移出书架后退回图片缓存
- 漫画图片线路：打开漫画时同时从服务器和书源站点各取第一张图，哪边先到，这本漫画就走哪边；直连书源失败的图片自动改走服务器
- 书源不稳定时的处理：
  - 超时、连接失败、429、5xx 自动重试两次（遵守 `Retry-After`），请求带浏览器常见请求头并保留 Cookie
  - 搜索连续 3 次连不上的书源，之后 10 分钟的搜索会跳过它，不拖慢其他书源；书源管理页会显示最近的失败原因
  - 目录刷新失败时继续用上次的目录，已缓存的章节不受影响
  - 换源：在详情页或阅读页把一本书换到其他书源里的同名书，书架和阅读进度一起迁移
  - 页面内显示「重试」，不弹报错；网络抖动时读取类请求自动再试一次

## 接口

除 `/api/health`、`/api/auth/login`、`/api/auth/refresh`、`/api/img` 外都需要 `Authorization: Bearer <token>`。

| 接口 | 说明 |
|---|---|
| `POST /api/auth/login` | `{code, appId}` 换 `{token, refreshToken, expiresIn}`。`code` 来自 `wx.login`，`appId` 是小程序运行时的 AppID（可不传，传了会和服务端的 `WX_APPID` 核对）。`token` 有效 2 小时，`refreshToken` 有效 30 天 |
| `POST /api/auth/refresh` | `{refreshToken}` 换一组新的 token；返回 401 表示需要重新登录 |
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
