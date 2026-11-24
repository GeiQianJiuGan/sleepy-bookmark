# 书源规则

一个书源是一段 JSON，在小程序「我的 → 书源管理」里新增或编辑。新增时也可以粘贴一个 JSON 数组，一次导入多个书源。

保存前可以在编辑页输入关键词点「测试」，后端会依次执行搜索、目录、第一章正文并返回结果，不会写入数据库。

## 规则字符串

每个规则字段都是一个字符串：

```
[类型:]表达式[@属性][##正则[##替换]]
```

| 类型 | 例子 | 说明 |
|---|---|---|
| `css:` | `css:.list li a@href` | CSS 选择器。`@属性` 可以是 `text`（默认）、`html`、`outerHtml` 或任意 HTML 属性 |
| `xpath:` | `xpath://div[@id="x"]//a/@href` | XPath |
| `json:` | `json:data.list.#.name` | [gjson](https://github.com/tidwall/gjson) 路径。响应是 JSON 时可以省略 `json:` 前缀 |
| `regex:` | `regex:var url = "(.*?)"` | 取每个匹配的第一个捕获组 |

- 列表（`list`）下面的字段规则是相对每个列表项求值的。表达式留空表示列表项本身，例如 `@href`、`@text`。
- `##正则##替换` 对取到的值做替换，省略替换就是删除。例如 `css:.author##^作者：` 去掉前缀，`id##^##book/` 在前面加上 `book/`。替换里用 `${1}` 引用捕获组。
- 可以接多组 `##正则##替换`，按顺序依次执行，例如列表页没有封面、但封面地址能从书的地址换算出来时：`css:a@href##^.*/(\d+)/(\d+)\.html$##/image/${1}/${2}/${2}s.jpg`。
- 取到的链接如果是相对地址，会按所在页面的地址补全。

## 字段

```jsonc
{
  "name": "某站",                 // 必填
  "type": "novel",                // 必填，novel 或 comic
  "baseUrl": "https://example.com", // 必填
  "headers": { "User-Agent": "...", "Referer": "..." }, // 可选，抓页面和图片时都会带上
  "charset": "gbk",               // 可选，留空自动识别；填了之后搜索词也按这个编码发送
  "rateLimitMs": 500,             // 可选，对该站两次请求的最小间隔

  "search": {
    "url": "/search?q={{key}}&page={{page}}", // 必填，相对 baseUrl 或完整地址
    "method": "POST",             // 可选，默认 GET
    "body": "keyword={{key}}",    // 可选，POST 的请求体
    "list": "css:.result li",     // 必填，每一条搜索结果
    "title": "css:a",             // 必填
    "bookUrl": "css:a@href",      // 必填，书籍详情页地址
    "author": "", "cover": "", "intro": "", "latest": "" // 可选
  },

  "recommend": {                  // 可选，整段不写就没有推荐；字段和 search 相同
    "url": "/rank",               // 必填，站点的排行榜、首页等任意一个书籍列表页
    "list": "css:.rank li",       // 必填
    "title": "css:a",             // 必填
    "bookUrl": "css:a@href",      // 必填
    "author": "", "cover": "", "intro": "", "latest": "" // 可选
  },

  "detail": {                     // 都是可选的，取到了就覆盖搜索结果里的值
    "title": "", "author": "", "cover": "", "intro": "", "latest": "",
    "tocUrl": ""                  // 目录页地址；留空表示目录就在详情页
  },

  "toc": {
    "list": "css:#list dd a",     // 必填，每一章
    "name": "@text",
    "url": "@href",               // 必填
    "nextPage": "",               // 可选，目录分页的下一页地址
    "reverse": false,             // 可选，站点目录是倒序时设为 true
    "fallback": null              // 可选，另一组 list / name / url / reverse；上面的规则一章都没找到时改用它
  },

  "content": {
    "body": "css:#content",       // 小说源必填，正文
    "images": "",                 // 漫画源必填，图片地址列表
    "nextPage": "",               // 可选，一章分多页时的下一页地址
    "purify": ["请记住本站域名.*"] // 可选，从正文里删除的正则
  }
}
```

`content.nextPage` 会一直跟到取不到地址、地址重复，或者到达下一章的地址为止。

## 推荐和排行

搜索页在没有输入关键词时，用每个书源的 `recommend` 列表（每个书源最多取前 100 本）做两个视图。列表在后端缓存 30 分钟；站点读不到时继续用上一次的结果，5 分钟后再重试。

**排行**：选一个书源，按站点列表原来的顺序显示。

**推荐**：把当前模式下所有书源的列表合并成一份：

1. **去重**：书名和作者去掉空格、标点、大小写后比较。书名相同且作者相同（或有一方没有作者）算同一本，只出现一次；展示有封面的那一份，都有或都没有时取名次靠前的。
2. **排除已读**：书架和历史里已有的书名不再推荐。
3. **打分**：一本书在某个书源排第 n 名得 `1 / (10 + n)` 分，各书源的得分相加。所以多个书源同时上榜的书，会排在只有一个书源排第一的书前面。
4. **偏好**：作者在书架或历史里出现过的书，得分 ×1.5；有封面的书 ×1.1。
5. **每日变化**：得分再乘一个 ±15% 以内的系数，由「用户、日期、书名」算出，同一天内顺序固定（翻页不会错位），第二天略有不同。
6. **书源穿插**：按得分从高到低取，如果和上一本是同一个书源，就在后面 5 本里找一本别的书源的先放。

卡片上的标签：「N 个书源上榜」或「读过这位作者」。

目录最多跟 100 页，一章正文最多跟 30 页，超过会直接报错，不会返回不完整的结果。

## 例子

HTML 小说站：

```json
{
  "name": "演示小说源",
  "type": "novel",
  "baseUrl": "http://127.0.0.1:8080/demo/novel",
  "search": {
    "url": "/search?q={{key}}&page={{page}}",
    "list": "css:.result li",
    "title": "css:a.name",
    "bookUrl": "css:a.name@href",
    "author": "css:.author",
    "cover": "css:img@src",
    "intro": "css:.intro"
  },
  "detail": {
    "title": "css:#info h1",
    "author": "css:#info .author##^作者：",
    "cover": "css:#cover@src",
    "intro": "css:#intro"
  },
  "toc": { "list": "css:#list dd a", "name": "@text", "url": "@href" },
  "content": {
    "body": "css:#content",
    "nextPage": "xpath://a[@id='next']/@href",
    "purify": ["请记住本站域名.*"]
  }
}
```

JSON 接口的漫画站：

```json
{
  "name": "演示漫画源",
  "type": "comic",
  "baseUrl": "http://127.0.0.1:8080/demo/comic",
  "search": {
    "url": "/api/search?q={{key}}",
    "list": "json:data.list",
    "title": "name",
    "bookUrl": "id##^##book/",
    "author": "author",
    "cover": "cover",
    "intro": "desc"
  },
  "detail": { "title": "data.name", "author": "data.author", "cover": "data.cover", "intro": "data.desc" },
  "toc": { "list": "data.chapters", "name": "name", "url": "path", "reverse": true },
  "content": { "images": "data.images" }
}
```

这两个就是 `DEMO_SOURCE=true` 时后端自带的演示书源，对应的假站点代码在 `server/internal/demo/demo.go`。

## 不支持的情况

- 需要执行 JavaScript 才能得到内容的页面
- 需要登录、验证码或有强反爬的站点
