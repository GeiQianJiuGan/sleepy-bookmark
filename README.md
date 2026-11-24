# 书签睡着了（sleepy-bookmark）

一个自用的小说 / 漫画**阅读器**：微信小程序 + 自建后端。它只是一个通用的阅读工具，**不提供、不内置、不分发任何小说、漫画或其他内容，也不附带任何第三方站点的书源规则**。

> **使用前请阅读**
>
> - 本项目是阅读器软件，不是内容平台。仓库里没有任何作品内容，也没有指向任何第三方网站的配置；唯一自带的「演示书源」是后端自己生成的虚构文字和色块图片，只用于开发和测试。
> - 「书源」是使用者自己填写的一段解析规则，用来告诉阅读器如何读取某个网页。接入哪些来源、阅读什么内容，由使用者自行决定并自行承担责任。请只接入你**有权访问和使用**的来源（自有内容、公有领域作品、已获授权或站点明确允许的内容），并遵守来源站点的服务条款和 robots 协议。
> - 请支持正版。不要用本项目获取、缓存或传播侵犯他人著作权的内容。服务器缓存和离线下载只为个人阅读，不得用于再分发。
> - 规则引擎不执行 JavaScript，不处理登录、验证码和人机验证，遇到这类访问控制会直接报错。本项目不会、也不接受任何用于绕过访问限制、付费墙或技术保护措施的改动。
> - 本项目仅供个人学习和自用，没有广告，不做任何商业用途，与任何内容站点没有关联。作者不对使用者自行配置的来源及由此产生的后果负责。
> - 如果你认为本仓库中的内容侵犯了你的权益，请在 Issues 中说明，核实后会尽快处理。

## 组成

- `miniapp/`：小程序，uni-app + Vue 3 + TypeScript + Pinia + wot-design-uni
- `server/`：后端，Go 单二进制 + SQLite + 磁盘缓存
- `deploy/`：Docker Compose + Caddy（自动 HTTPS）
- `docs/source-rule.md`：书源规则格式

```
小程序 ──HTTPS──> Go 后端 ──> 书源规则引擎 ──> 使用者自行配置的来源
                    ├─ SQLite：用户、书源、书籍、目录、收藏、历史
                    └─ 磁盘：章节正文 (gzip)、图片缓存
```

小程序只和后端通信。搜索、目录、正文和图片都由后端按使用者配置的书源规则读取，图片经带签名的 `/api/img` 转发（小程序只允许请求已登记的域名）。

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
- 先把根目录的 `app.config.example.json` 复制为 `app.config.json`（不进仓库），填上自己小程序的 `appId`。`miniapp/src/manifest.json` 也不进仓库，每次 `npm run dev:*` / `npm run build:*` 时由 `miniapp/manifest.base.json` 加上这个 `appId` 生成；要改 manifest 的其他内容，改 `manifest.base.json`。
- 微信开发者工具里需要勾选「不校验合法域名」。
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
| `TLS_ADDR`、`TLS_CERT`、`TLS_KEY` | 空 | 填了就在 `TLS_ADDR` 上再开一个 HTTPS 端口，证书和私钥是 PEM 文件路径。放在 Cloudflare 后面时用 Cloudflare 支持的 HTTPS 端口（如 `:8443`），「完全」模式下自签证书即可 |
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
- **微信开发者工具返回的是模拟登录凭证**：开发者工具的 `wx.login` 没有拿到真实 code，返回了固定的 `the code is a mock one`。依次检查：「设置 → 代理设置」改为不使用代理（走系统代理时会出现）；左上角登录的微信号是该小程序的管理员或开发者；「清缓存 → 全部清除」后重新编译。手机上预览不受影响。
- **当前不在微信小程序内**：H5 版没有微信登录，只能连接 `DEV_LOGIN=true` 的后端。

## 功能

- 登录：启动时本机有 refresh token 就换新 token，没有或已失效就进登录页；登录页先弹《用户协议与隐私政策》，同意后才调用微信登录；「我的」里可以退出登录、查看政策和撤回同意
- 左上角的图标在小说 / 漫画之间整体切换，书架、搜索、历史都按当前模式过滤
- 书源管理：新增、批量导入、编辑、启停、删除、在线测试
- 多书源并发搜索，可以只搜某一个书源；搜索框右侧的按钮展开高级选项，可选模糊（默认，按书名相关度排序）或精确（书名完全一致）
- 搜索页没有输入关键词时是两列瀑布流，向下滑动继续加载，分「推荐」和「排行」两个视图（书源规则里配了 `recommend` 才有）
  - 排行：选一个书源，按站点榜单的顺序看
  - 推荐：各书源的榜单合并成一份，同一本书只出现一次，书架和历史里已有的书不再推荐（算法见 [docs/source-rule.md](docs/source-rule.md#推荐和排行)）
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
| `GET /api/rank?source=&offset=&limit=` | 一个书源的榜单，分页（每页默认 20、最多 50 本），返回 `total` 和 `books` |
| `GET /api/feed?type=&offset=&limit=` | 跨书源去重的推荐，分页；每本带 `sources`（上榜的书源数）和 `reason` |
| `GET /api/recommend?type=&source=` | 旧版小程序用：各书源榜单的前 10 本 |
| `GET /api/books/:id`，`GET /api/books/:id/chapters?refresh=1` | 详情、目录 |
| `GET /api/chapters/:id` | 正文或图片列表 |
| `GET /api/books/:id/alternatives` | 其他书源里的同名书 |
| `POST /api/books/:id/switch` | `{to}`，把书架和阅读进度换到另一个书源的同一本书 |
| `POST /api/books/:id/cache`，`GET /api/tasks` | 全本缓存任务 |
| `GET/PUT/DELETE /api/favorites` | 书架 |
| `GET/PUT/DELETE /api/history` | 历史和阅读进度 |

读取书源失败时返回 502 和 `{message, code}`，`code` 为 `timeout`、`network`、`busy`、`blocked`、`not_found`、`http` 或 `rule`。服务器需要走代理访问书源时，设置环境变量 `HTTPS_PROXY` / `HTTP_PROXY`。

## 已知限制

- 规则引擎不执行 JavaScript，也不处理登录、验证码和人机验证。需要这些才能访问的页面不支持，会提示「访问验证」，不会尝试绕过。
- 后端只带虚构的演示书源（`DEMO_SOURCE=true`）。实际使用的书源需要使用者按 [docs/source-rule.md](docs/source-rule.md) 自行编写；来源页面改版后规则需要跟着调整。
- 小程序没有上架，按体验版自用设计：登录默认只允许第一个登录的微信号（或 `OPENID_WHITELIST` 里的账号）。

## 隐私

- 登录只使用微信 `wx.login` 返回的 openid 区分账号，不获取头像、昵称、手机号；登录前需同意小程序内的《用户协议与隐私政策》。
- 服务器保存的只有书源配置、书架、历史和阅读进度；AppSecret 只存在于服务端环境变量，不进入仓库和小程序。
