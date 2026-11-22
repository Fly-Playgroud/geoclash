# geoclash 设计方案

> 状态：v3，已实现（实现过程中的调整见第 16 节）
> 日期：2026-11-22
>
> 变更记录：
> - v2：跨平台、内置开机自启、自动发现 Clash 配置、countries 取值规则、controller 自动发现（命名管道 / unix socket）
> - v3：节点名包含城市和出口 IP 尾段；countries 默认自动模式；脚本自动写入全局扩展脚本并与已有脚本串联；国家集合变化时热重载（附防裸连分析）；通用的挂接与跟随规则（7.4）；第 15 节决策记录

## 1. 背景与需求

机场订阅中，很多节点的实际 IP 归属地与节点名称标注的地区不一致，有些甚至不在同一个国家。

需求：

1. **动态、定期检测**节点的实际归属地。
2. 使用时，节点必须从**指定国家的节点组**里**自动选择**。
3. **跨平台**：Windows / macOS / Linux。
4. **开机自启**：能力内置在二进制中，不依赖外部脚本。
5. **零配置优先**：没有填写订阅链接时，自动找到 Clash 的配置文件并读取；也可以手动指定配置文件路径。
6. 实测节点的名称中包含**具体城市**。

当前开发机环境：

| 项 | 值 |
|---|---|
| 客户端 | Clash Verge Rev（内核 mihomo，`verge-mihomo.exe`） |
| 节点来源 | 机场订阅（当前约 79 个 hysteria2 节点、2 个 trojan 节点） |
| TUN 模式 | 已开启 |
| controller | TCP `external-controller` **已关闭**；仅开启命名管道 `external-controller-pipe` |
| 全局扩展脚本 | **不为空**（注入自定义节点与规则集、DIRECT 规则、payload 转规则、跟随组；定义了 `function main`） |
| 实现语言 | Go（单个可执行文件，各平台交叉编译） |

## 2. 整体思路

不修改订阅本身，另外运行一个常驻的 Go 程序 `geoclash`。它负责实测每个节点的**出口**归属地，按**实际国家**重新分组，再通过本地 HTTP 接口提供给 Clash 使用。

```
节点来源（订阅链接 / 自动发现的 Clash 配置 / 指定文件）
    │
    ▼
geoclash（每 6 小时）
    ├─ 1. 读取全部节点
    ├─ 2. 逐个节点：通过该节点访问 IP 归属地接口 → 出口 IP、国家、城市
    ├─ 3. 按实测国家归入各分组，节点改名为「🇺🇸 洛杉矶 .23.7 | 日本07」
    ├─ 4. 本地 HTTP：/provider/US、/provider/JP …
    ├─ 5. 通知 mihomo 刷新对应 provider（节点变动，最常见）
    └─ 6. 国家集合变化时：更新 Verge 全局扩展脚本 + 热重载（少见）
                │
Clash Verge Rev ◄┘
  ├─ 🇺🇸 美国-实测   (url-test，use: geoclash-US)
  ├─ 🇯🇵 日本-实测   (…)
  └─ 🌍 实测国家     (select：在各国家组里选一个)
```

一句话概括：**geoclash 决定"候选名单"，Clash 在名单内自动选择并转发流量。**

## 3. 节点来源与自动发现

### 3.1 来源解析顺序

1. 配置了 `sources[].url` → 拉取订阅（UA 为 `clash.meta`，优先获取 Clash YAML；如果返回的是 base64 分享链接，则用 mihomo 的 `convert.ConvertsV2Ray` 转换）。
2. 配置了 `sources[].file` → 读取指定的 Clash 配置文件或订阅文件。
3. **两者都没配置** → 自动发现本机的 Clash 配置（见 3.2）。

多个来源可以同时配置，节点合并后统一检测。

### 3.2 自动发现

按以下顺序探测，使用第一个找到的：

| 客户端 | Windows | macOS | Linux |
|---|---|---|---|
| Clash Verge Rev | `%APPDATA%\io.github.clash-verge-rev.clash-verge-rev` | `~/Library/Application Support/io.github.clash-verge-rev.clash-verge-rev` | `$XDG_DATA_HOME`（默认 `~/.local/share`）`/io.github.clash-verge-rev.clash-verge-rev` |
| Clash Verge Rev 便携版 | `<exe 目录>\.config\io.github.clash-verge-rev.clash-verge-rev` | — | — |
| mihomo（裸跑） | `%USERPROFILE%\.config\mihomo\config.yaml` | `~/.config/mihomo/config.yaml` | `~/.config/mihomo/config.yaml`、`/etc/mihomo/config.yaml` |

对于 Clash Verge Rev：

- 读取 `profiles.yaml` 的 `current` 字段，找到**当前激活的订阅**对应的文件 `profiles/<uid>.yaml`，从中读取 `proxies`。
- 如果订阅内使用了 `proxy-providers`，则读取 Verge 数据目录下对应 `path` 的缓存文件。
- 每轮检测都重新读取。你在 Verge 中切换或更新订阅后，geoclash 会自动跟随。
- **会排除 geoclash 自己注入的 `geoclash-*` provider**，避免循环。

### 3.3 controller 自动发现

从运行时配置（Verge 为数据目录下的 `clash-verge.yaml`，裸跑 mihomo 为其 `config.yaml`）中读取以下字段，按顺序使用第一个可用的：

1. `external-controller`（TCP）
2. `external-controller-pipe`（Windows 命名管道，例如 `\\.\pipe\verge-mihomo-sidecar-...`）
3. `external-controller-unix`（macOS / Linux unix socket）

同时读取 `secret`。这些字段也可以在配置文件中手动覆盖。

**从运行中的内核读取实际地址（macOS / Linux）**：Clash Verge Rev 以服务模式运行时，内核由服务以 root 身份启动，并通过命令行参数使用另一个 controller 地址，运行时配置中写的地址并不存在。例如 macOS 上：

```
verge-mihomo -d /Library/Application Support/clash-verge-service/users/501/runtime
             -f .../runtime/config.yaml
             -ext-ctl-unix /var/run/clash-verge-service/users/501/verge-mihomo.sock
```

运行时配置中写的却是 `$TMPDIR/verge-mihomo.sock`。macOS / Linux 的 `ps` 能看到 root 进程的命令行，因此 geoclash 用 `ps -A -ww -o args=` 找到内核进程，解析 `-ext-ctl` / `-ext-ctl-unix` / `-ext-ctl-pipe`，优先使用其中的地址（配置了 TCP 地址时仍用 TCP）。同时解析出的 `-d` 即内核工作目录，provider 缓存位于其下的 `geoclash/`。读取失败时依次尝试 `/var/run/clash-verge-service/users/<uid>/verge-mihomo.sock` 及下述枚举。

**命名管道 / unix socket 校验**：Clash Verge Rev 以服务模式运行时，内核实际使用的管道名（`verge-mihomo-production-<hash>`）与运行时配置中写的（`verge-mihomo-sidecar-release-<hash>`）不同。因此配置中的管道不存在时，geoclash 会枚举系统中 `verge-mihomo` 开头的管道，优先选择哈希后缀相同的那个；unix socket 同理，在同目录和 `/tmp/verge` 下寻找 `verge-mihomo*.sock`。

## 4. 实际归属地的检测

### 4.1 判定依据：出口 IP

- **把 mihomo 当 Go 库引用**（`github.com/metacubex/mihomo`，`adapter.ParseProxy`），直接用它的协议实现通过节点拨号。hysteria2、trojan 以及 mihomo 支持的其他协议都能原生使用，无需额外运行内核进程。
- **测出口 IP，而不是节点的 server 地址**。很多机场用中转或 CDN 做入口，server 地址的归属地本身就不准；只有出口 IP 才是目标网站实际看到的位置。目标域名交给节点在远端解析。

### 4.2 归属地接口与国家投票

| 接口 | 国家 | 城市 | 备注 |
|---|---|---|---|
| Cloudflare trace（`/cdn-cgi/trace`） | ✅ `loc` | ❌ | 无频率限制，首选 |
| ip-api（`lang=zh-CN`） | ✅ | ✅ **中文** | 仅 HTTP；每个出口 IP 每分钟 45 次 |
| ipinfo | ✅ | ✅ 英文 | |
| ipwho.is | ✅ | ✅ 英文 | |

- 按顺序查询，有 `min-agree`（默认 2）个接口给出相同的国家就采用；全部查完仍未达成一致时取多数，并标记为**低置信**；所有接口都失败则本轮判定为检测失败。
- 国家使用 **ISO 3166-1 alpha-2** 代码（`US`、`JP`、`HK`、`TW` …），也是 `countries` 的取值依据。
- **出口 IP 优先取 IPv4**：从国家与投票结果一致的接口中选出口 IP 时优先 IPv4。双栈节点的 IPv6 出口通常每个连接都不同，用它命名会导致节点名每轮都变化（实测中 Cloudflare trace 走双栈，ip-api 只走 IPv4）。

### 4.3 城市

- 各家 GeoIP 库对城市的判断，分歧远大于国家，所以**城市只用于显示，分组按国家划分**。
- 城市取值：只采信**国家判断与投票结果一致**的接口。优先使用 ip-api 的中文城市名；没有时依次回退到 ipinfo、ipwho.is 的英文城市名。
- 如果本轮投票过程没有查询到 ip-api，会为取城市额外查询一次。
- **按出口 IP 缓存城市**：同一个出口 IP 的城市不会在两轮检测之间来回跳变，只有出口 IP 改变时才重新获取。这样节点名称保持稳定，避免 url-test 因为改名而重新选择。
- 所有接口都没有城市信息时，名称中省略城市部分。

### 4.4 节点命名

默认模板：`{flag} {city} {ip_tail} | {name}`

```
🇺🇸 洛杉矶 .23.7 | 日本07
🇺🇸 圣何塞 .88.12 | 美国03
🇯🇵 东京 .5.140 | 日本01
🇹🇼 台北 .61.9 | 台湾01家宽
```

| 占位符 | 含义 | 示例 |
|---|---|---|
| `{flag}` | 国旗 emoji | 🇺🇸 |
| `{code}` | 国家代码 | US |
| `{country}` | 国家中文名 | 美国 |
| `{city}` | 城市（中文优先） | 洛杉矶 |
| `{ip}` | 完整出口 IP | 104.28.23.7 |
| `{ip_tail}` | 出口 IP 尾段（IPv4 为后两段，IPv6 为后两组） | .23.7 |
| `{name}` | 原节点名 | 日本07 |

- 占位符的值为空时，自动去掉多余空格。
- 出现重名时自动追加 ` #2`、` #3` 去重（例如两个节点共用同一个出口，且原名相同）。

### 4.5 定期检测

- **订阅变化时增量检测（文件看门狗）**：每 30 秒（`watch`）检查一次本地节点来源文件——Clash 当前订阅文件及其 proxy-provider 缓存——的路径、修改时间和大小。Verge 更新订阅或切换订阅时这些信息会变化；发现变化后等待 `watch-debounce`（默认 10 秒，0 表示立即处理）确认文件写完，然后重新读取节点并与上次比对：
  - 新增的节点：立即检测；
  - 删除的节点：立即从分组中移除；
  - 参数不变的节点（包括只改了名称的）：保留原有结果，不重复检测，名称随来源更新；
  - 然后照常刷新 provider；出现新国家时更新脚本并热重载。
  - 文件被重写但节点没有变化时只记录日志。订阅链接（`sources.url`）不在监视范围内，由定期完整检测负责。
  - 没有采用 mihomo 的 `/logs` 事件流：Verge 与 mihomo 都没有专门的订阅更新事件，日志文本也不是稳定接口。
- **启动时立即接入**：已有上次的检测结果时，启动后先用它接入 Clash（写入脚本区块、热重载或刷新 provider），再开始新一轮检测。升级、重启、开机自启后分组马上可用；全新安装没有结果，需等第一轮检测完成。
- 完整检测：内置定时器，**每 6 小时一次**，用于发现已有节点出口位置的变化；启动时先执行一轮；也可以通过状态页按钮、`POST /api/refresh` 或 `geoclash refresh` 手动触发。
- 用 worker pool 并发检测（默认并发 16，单次请求超时 10s），实测 76 个节点约 25 秒完成。
- **本轮重试**（`probe.retries`，默认 1）：主检测结束后，失败的节点在本轮内再检测一次。实测中部分家宽节点时通时断，单次失败就会被移出国家组长达一个检测间隔。
- 一开始就连续两个接口失败时，判定节点不可用，不再查询其余接口。
- **疑似断网**：一轮（或一次增量）检测中至少 3 个节点且全部失败时，多半是网络尚未就绪（例如刚从睡眠中唤醒，Clash 立即更新了订阅），等待 60 秒后整体重测一次。
- **节点换密码时继承结果**：机场更新订阅时常常更换节点密码，节点标识随之改变。新节点的名称、协议、服务器、端口与某个被删除的节点完全相同时，视为同一个节点，先继承其国家、城市等结果（以及连续失败轮数），再按本次检测结果更新。这样即使本次检测失败，节点也不会立即离开分组。
- **防抖动**：节点偶尔检测失败时保留上次的结果，连续失败 N 轮（默认 2）才从国家组中移出。真正失效的节点由 Clash 的 url-test 健康检查跳过。
- 「剩余流量」「套餐到期」之类的信息节点本身连不通，会被自动排除；另外支持配置正则手动排除。
- 检测结果持久化到本地 JSON 文件，程序重启后可以立即提供上次的结果。

## 5. countries 的取值

### 5.1 取值规则

- **值为 ISO 3166-1 alpha-2 国家 / 地区代码**，与归属地接口的返回值直接对应，不需要做名称匹配。常用值：`HK` 香港、`TW` 台湾、`JP` 日本、`SG` 新加坡、`US` 美国、`KR` 韩国、`GB` 英国、`DE` 德国。
- 大小写不敏感；内置常见别名，例如 `UK` → `GB`。
- **分组名自动生成**：国旗 emoji + 内置中文名 + 后缀，例如 `US` → `🇺🇸 美国-实测`。也可以手动指定 `name`。
- **多个国家合成一组**：用 `codes` 字段。

### 5.2 默认：自动模式

**默认配置中 `countries` 留空**，即自动模式：根据检测结果，为每个至少有 `auto-min-nodes`（默认 1）个节点的国家生成一个分组，按节点数量从多到少排序。

国家集合发生变化（新增或减少国家）时，geoclash 自动更新脚本并热重载（见 7.6）。平时节点只是在已有国家之间变动时，只刷新 provider，不更新脚本，也不重载。

### 5.3 显式列表

需要固定分组时，可以改为显式列表：

```yaml
countries: [US, JP, SG, TW]                    # 最简写法

countries:                                     # 完整写法，可以混用
  - US
  - { code: JP, type: fallback }
  - { code: TW, name: "🇹🇼 台湾家宽", tolerance: 100 }
  - { id: EU, name: "🇪🇺 欧洲-实测", codes: [DE, FR, NL, GB] }
```

| 字段 | 默认值 | 说明 |
|---|---|---|
| `code` / `codes` | — | 单个或多个国家代码（二选一） |
| `id` | 等于 `code` | provider 名称 `geoclash-<id>`，以及接口路径 `/provider/<id>`；使用 `codes` 时必填 |
| `name` | 自动生成 | 分组显示名称 |
| `type` | `url-test` | `url-test` 或 `fallback` |
| `test-url` | `https://www.gstatic.com/generate_204` | 健康检查地址 |
| `interval` | `300` | 健康检查间隔（秒） |
| `tolerance` | `50` | 仅 `url-test` 使用 |

显式列表模式下，开启 `others: true` 会额外生成一个「🏳️ 其他-实测」分组，包含所有不在列表中的节点。默认关闭。

## 6. 输出：provider 与状态页

- `GET /provider/{id}` 返回 mihomo provider 格式的 YAML（`proxies: [...]`），只包含实测属于该分组的节点；节点参数与原文一致，只修改名称。
- 某个分组本轮一个可用节点都没有时返回 **503**，mihomo 会继续使用上次缓存的 provider 文件，不会出现空分组，也不会退化成 DIRECT 直连。
- `GET /` 状态页：节点原名、新名称、实测国家、城市、出口 IP、各接口结果、置信度、检测时间；带「立即检测」按钮。
- `GET /status`：同样的数据，JSON 格式。
- `GET /verge-script.js`：当前的 geoclash 脚本区块。
- 控制接口（供命令行使用）：`GET /api/health`、`POST /api/refresh`、`POST /api/reset-switch`、`POST /api/stop`。

**安全**：provider 中包含节点凭据，控制接口可以停止进程。因此：

- 默认只监听 `127.0.0.1`。
- 拒绝 `Host` 不是本机地址的请求，防止 DNS rebinding 读取 provider。
- 控制接口必须携带 `X-Geoclash` 请求头。浏览器跨站请求无法在不经预检的情况下携带自定义请求头，因此网页无法伪造这些请求。

## 7. 与 Clash Verge Rev 的交互

### 7.1 两者的关系

二者是**互相独立的两个进程**。geoclash 不接管流量，日常上网流量**完全不经过 geoclash**。

geoclash 对 Verge 文件的写操作**只有一处**：全局扩展脚本中由标记包围的 geoclash 区块（见 7.5）。其余全部为只读。

```
┌──────────────────────┐                     ┌──────────────────────────────┐
│       geoclash       │ ⓪ 只读：订阅文件、  │  Clash Verge Rev (mihomo)    │
│                      │   运行时配置        │                              │
│                      │◄────────────────────│  profiles/、clash-verge.yaml │
│  HTTP :9876          │ ① 拉取 provider     │                              │
│  /provider/US        │◄────────────────────│  proxy-providers             │
│                      │ ② 刷新 provider     │                              │
│  controller client   │────────────────────►│  命名管道 / unix socket / TCP│
│                      │ ③ 检测流量          │                              │
│  probe ──────────────┼──(被 TUN 捕获)─────►│  PROCESS-NAME → DIRECT       │
│                      │ ④ 写入脚本区块      │                              │
│  verge writer ───────┼────────────────────►│  profiles/Script.js（全局）  │
│                      │ ⑤ 热重载（少见）    │                              │
│  controller client ──┼────────────────────►│  PUT /configs                │
└──────────────────────┘                     └──────────────────────────────┘
```

### 7.2 交互点

**⓪ 读取 Verge 的文件（只读）**：读取当前订阅文件以获取节点；读取运行时配置以获取 controller 地址和 secret。

**① Verge 从 geoclash 拉取节点（数据主通道）**：脚本在 Verge 配置中加入 `type: http` 的 `proxy-provider`，地址指向 `http://127.0.0.1:9876/provider/US`，设置 `proxy: DIRECT`。mihomo 定期拉取，并缓存到 `./geoclash/geoclash-US.yaml`。

**② 主动刷新 provider（最常见）**：每轮检测结束后，对内容有变化的分组调用 `PUT /providers/proxies/geoclash-US`。mihomo 立即重新拉取。这一步**不重载配置**，现有连接不受影响。

**③ geoclash 自己的检测流量必须绕开 Clash**：TUN 会捕获系统中的所有出站流量。如果不处理，检测流量会再套一层代理，测出来的出口是错的。因此脚本会在规则最前面插入 `PROCESS-NAME,<geoclash 可执行文件名>,DIRECT`（Windows 为 `geoclash.exe`，macOS / Linux 为 `geoclash`）。geoclash 拉取订阅的请求同样走这条规则直连。

**④ 自动写入全局扩展脚本**：见 7.5。

**⑤ 国家集合变化时热重载**：见 7.6。

**⑥ 首次挂接时自动切换一次**：通过 controller 调用 `PUT /proxies/<分组名>`，把新挂接的分组切到「🌍 实测国家」，每个分组只执行一次（见 7.4）。

### 7.3 脚本注入的内容

geoclash 区块的 `__geoclashApply(config)` 函数会：

1. **先清除**上一次注入的内容：名称以 `geoclash-` 开头的 provider、`use` 中引用了这些 provider 的分组、「🌍 实测国家」选择组及它在其他分组中的引用、geoclash 的 `PROCESS-NAME` 规则。因此重复执行结果不变（幂等）。
2. 每个分组注入一个 `proxy-provider`（`geoclash-<id>`）。
3. 每个分组注入一个代理组，类型为 `url-test` 或 `fallback`，`use: [geoclash-<id>]`。
4. 注入「🌍 实测国家」选择组，包含上面所有分组；`prefer` 中第一个有节点的国家排在最前，作为默认选择（见 7.4）。
5. 把「🌍 实测国家」插入挂接分组的**第一位**，原有成员全部保留（见 7.4）。
6. 在规则最前面插入 `PROCESS-NAME,...,DIRECT`。
7. 分组顺序：实测分组放在分组列表**最前面**——「🌍 实测国家」在首位，其后是各国家组（顺序同「🌍 实测国家」的成员顺序），再后是订阅原有的分组。

使用效果：被挂接的分组默认跟随「🌍 实测国家」，「🌍 实测国家」默认选择优先国家（如「🇺🇸 美国-实测」），组内自动选择**实测在该国**的节点。你也可以随时在任何分组里手动改选原有节点。

### 7.4 挂接与跟随（通用规则）

目标：生成独立的实测分组，让订阅中原有的分组**默认跟随**实测分组，同时保留手动选择原有节点的能力。规则不依赖任何特定订阅或用户脚本的结构。

**默认国家**

- 「🌍 实测国家」的成员顺序：先按 `prefer`（默认 `[US]`）的顺序排列有节点的国家，再按节点数量从多到少排列其余国家。
- mihomo 中，没有选择记录的 select 组默认选第一个成员，因此「🌍 实测国家」默认就是优先国家。
- 你手动改选后，由 mihomo 的 `store-selected` 记住，不会被 geoclash 改回。

**挂接哪些分组**（`attach-to: auto`，默认）

只挂接**代理型 select 组**，即同时满足以下条件的分组：

1. 类型是 `select`。`url-test` / `fallback` / `load-balance` 会按测速自动选节点，嵌入其他分组会改变其语义，因此不挂接。
2. 成员中**直接包含至少一个真实节点**（来自 `proxies` 或 `proxy-providers`）。
   - 「节点选择 [自动选择, 香港01, 日本02 …]」「Netflix [节点选择, 香港01 …]」→ 挂接
   - 「全球直连 [DIRECT, 节点选择]」「广告拦截 [REJECT, DIRECT]」「漏网之鱼 [节点选择, DIRECT]」→ 不挂接。这些分组只包含内置策略或其他分组，它们本来就会通过其他分组间接跟随。
3. 名称不匹配 `attach-exclude`（正则，默认为空）。
4. 不是 geoclash 自己生成的分组。

`attach-to` 的全部取值：

| 取值 | 挂接范围 | 是否检查「成员中包含真实节点」 |
|---|---|---|
| `auto`（默认） | 所有代理型 select 组（上面 4 个条件） | ✅ |
| `all` | 原始配置中**所有 select 组**（排除 geoclash 自己的分组和 `attach-exclude` 匹配的分组） | ❌ |
| `[组名, …]` | 只挂接列出的分组；名称必须完全一致，找不到的会被跳过并提示 | ❌ |
| `[]` | 不挂接 | — |

说明：

- 「原始组」指 geoclash 区块执行时配置中已经存在的分组，包括订阅自带的分组，以及用户脚本、Merge 添加的分组（geoclash 区块在用户的 `main` 之后执行）。
- 所有取值都**只对 select 组生效**。`url-test` / `fallback` / `load-balance` 组即使被显式列出或处于 `all` 范围内，也会被跳过并提示，因为把选择组放进自动测速组会改变它的行为。
- `all` 和显式列表会挂接直连类分组（如「全球直连 [DIRECT, 节点选择]」）。它们的安全性由下面的「挂接位置」和「首次自动切换」两条规则保证：默认行为不变，也不会被自动切到代理。
- `geoclash discover` 会列出当前订阅的全部分组、类型，以及 `auto` / `all` 各自会挂接哪些分组。

由于 geoclash 生成的分组只引用自己的 provider，不引用任何原有分组，因此挂接**不可能产生循环引用**。

**挂接位置**：原有成员保持原顺序，按分组原本的默认选择决定插入位置：

- 分组的第一个成员（即没有选择记录时的默认选择）最终指向**真实节点** → 插入到**第一位**，没有选择记录时默认跟随实测分组。
- 分组的第一个成员最终指向 **DIRECT / REJECT 等内置策略**（如「全球直连 [DIRECT, 节点选择]」「广告拦截 [REJECT, DIRECT]」）→ 插入到**末尾**，保持该分组原有的默认行为。这样在新装、清除缓存、切换到新订阅等没有选择记录的情况下，国内流量也不会被绕到代理。

「最终指向」的判断方法：沿着每一层分组的第一个成员逐层解析，直到遇到真实节点或内置策略。

**首次挂接时自动切换一次**（`auto-switch: once`，默认）

mihomo 会记住每个 select 组上次的选择，「放在第一位」对已有选择记录的分组不起作用。因此，某个分组**第一次被挂接**后，geoclash 会通过 controller 把它切换到「🌍 实测国家」，但要先满足下面的条件：

- 只在该分组当前的选择**最终指向一个真实节点**时才切换。geoclash 会沿着 `now` 字段逐层解析：分组 → 分组 → … → 节点。
  - 当前最终走 `DIRECT` / `REJECT` 等内置策略（比如你有意让「苹果服务」直连）→ **不切换**
  - 当前的链路已经经过「🌍 实测国家」→ 不需要切换
- 已经切换过的分组记录在 `state.json`（按订阅名 + 分组名区分），**以后不再干预**。之后你手动改选别的节点，geoclash 会尊重你的选择。
- 切换在 geoclash 区块首次生效后执行（即脚本写入 + 热重载完成，或检测到 Verge 已加载新配置之后）。

`auto-switch` 的可选值：`once`（默认）、`never`（只放在第一位，不主动切换）。

**重置切换记录**：`geoclash reset-switch` 会清除切换记录，让分组重新执行一次「首次自动切换」，判断条件不变（当前最终指向真实节点才切换）。

```
geoclash reset-switch                  清除当前订阅中所有分组的切换记录
geoclash reset-switch 节点选择 Netflix  只清除指定分组的记录
geoclash reset-switch --all-profiles   清除所有订阅的记录
```

- geoclash 正在运行时，通过本地接口 `POST /api/reset-switch` 通知运行中的实例，**立即**重新判断并切换，不用等下一轮检测。
- geoclash 没有运行时，直接修改 `state.json`，下次启动时生效。
- 命令会输出每个分组的处理结果：已切换、跳过（当前走内置策略）、跳过（已经经过实测分组）、跳过（分组不存在或不是 select 组）。

**示例**：订阅包含「节点选择 [自动选择, 香港01 …]」「流媒体 [节点选择, 香港01 …]」「全球直连 [DIRECT, 节点选择]」，其中「流媒体」当前选的是「节点选择」：

| 分组 | 是否挂接 | 首次是否自动切换 | 原因 |
|---|---|---|---|
| 节点选择 | ✅ | ✅ | 代理型；当前最终指向节点 |
| 流媒体 | ✅ | ✅ | 代理型；当前经由「节点选择」最终指向节点 |
| 全球直连 | ❌ | — | 成员中没有真实节点 |

### 7.5 自动写入全局扩展脚本（与已有脚本共存）

**写入位置**：Verge 的全局扩展脚本（`profiles.yaml` 中 uid 为 `Script` 的条目，即 `profiles/Script.js`），对所有订阅生效，切换订阅后无需重新写入。

**Verge 的执行方式**（已对照 Verge Rev 源码 `src-tauri/src/enhance/script.rs` 确认）：

```js
try {
  <脚本原文>;
  main(globalThis.__verge_config__, '<profile name>')
} catch (err) { ... }
```

执行顺序为：全局 Merge → **全局 Script** → 订阅 Merge → 订阅 Script（`enhance/mod.rs`）。全局脚本运行时，订阅自带的分组已经存在。

**共存方案**：在原内容**末尾**追加带标记的区块，不改动标记之外的任何内容：

```js
// ... 你原有的脚本（含 function main）保持不变 ...

// ===== geoclash:begin =====（由 geoclash 自动生成，请勿修改此区块）
var __geoclash = { /* 分组、provider 地址、进程名等数据 */ };
function __geoclashApply(config) { /* 见 7.3 */ }
try {
  main = (function (userMain) {
    return function (config, profileName) {
      var c = typeof userMain === "function" ? userMain(config, profileName) : config;
      return __geoclashApply(c);
    };
  })(typeof main === "function" ? main : undefined);
} catch (e) {
  console.error("geoclash: 无法包装 main: " + e);
}
// ===== geoclash:end =====
```

- **先执行你原有的 `main`，再执行 geoclash 的注入**。你现有的自定义节点与规则集注入、DIRECT 规则、payload 规则、跟随组全部照常生效。
- 原脚本中没有 `main` 时，区块会自己提供一个 `main`。
- 原脚本用 `const main = ...` 定义时无法重新赋值：此时 `try/catch` 保证你原有的脚本**照常运行**，只是 geoclash 部分不生效；geoclash 会在日志和状态页中给出提示，**不会改写你的代码**。
- **写入前备份**：第一次写入时，把原文件备份为 `Script.js.geoclash.bak`（已有备份时不覆盖）。
- **只在内容变化时写入**；每次写入前重新读取文件，保留你在 Verge 中对标记外内容所做的修改。
- `geoclash uninstall` 会移除该区块，还原为你的原始脚本内容。

### 7.6 国家集合变化时的热重载

**触发条件**：只有国家集合变化（例如新出现一个国家）时才会触发。日常节点在已有国家之间变动，只走 ② 刷新 provider。

**流程**：

1. 更新全局扩展脚本中的 geoclash 区块（7.5）。这保证 Verge 以后每次重新生成配置时，结果都一致。
2. 读取 Verge 当前的运行时配置 `clash-verge.yaml`（即 Verge 生成并加载的最终配置）。
3. **用同一段 JS 处理它**：geoclash 内置 JS 引擎（`goja`），对运行时配置执行 `__geoclashApply`。由于该函数先清除旧注入再写入新内容（幂等），结果与 Verge 重新生成配置时**完全一致**，只有一份逻辑，不存在 Go 和 JS 两套实现不一致的问题。
4. 只修改 `proxy-providers`、`proxy-groups`、`rules`，**不改动** `tun`、`dns`、监听端口、controller 等任何其他字段。
5. 本地用 mihomo 的配置解析器预校验，失败则放弃重载并记录错误。
6. 通过 controller 发送 `PUT /configs`（以 payload 形式提交，不写文件）。

**是否会裸连出网**（已对照 mihomo v1.19.34 源码确认）：**不会**。

| 环节 | 行为 | 源码依据 |
|---|---|---|
| 重载期间的新连接 | 内核先进入 `Suspend` 状态：新 TCP 连接被**直接关闭**，UDP 包被**丢弃**，属于 fail-closed，不会走直连 | `hub/executor/executor.go` `ApplyConfig` 调用 `tunnel.OnSuspend()`；`tunnel/tunnel.go` `handleTCPConn` / `handleUDPConn` 中的 `isHandle` 检查 |
| TUN 网卡与路由 | TUN 配置没有变化时**不重建**，路由始终存在，流量不会绕过 TUN | `listener/listener.go` `ReCreateTun` 中 `tunConf.Equal(LastTunConf)` 直接返回 |
| 系统代理 / 监听端口 | 端口配置没有变化，不重建 | `updateListeners` |
| 已建立的连接 | 不会被主动关闭，继续使用原出口 | `ApplyConfig` 没有关闭连接的步骤 |
| 分组选择 | 由 mihomo 的 `store-selected` 恢复 | `updateProfile` |

**代价**：重载期间（通常一两秒）新建的连接会失败，表现为「短暂断网」，而不是「裸连」。因为只在国家集合变化时触发，实际很少发生。

**非 Verge 环境**（裸跑 mihomo）：不写脚本，也不热重载；状态页提示国家集合已变化，可以用 `geoclash script` 获取最新区块。

### 7.7 职责划分

| 职责 | geoclash | Clash Verge Rev |
|---|---|---|
| 获取节点 | ✅ 拉取订阅或读取 Verge 已下载的订阅文件 | ✅ 照常拉取订阅 |
| 判断节点的实际国家、城市 | ✅ | ❌ |
| 决定哪些节点进入哪个分组 | ✅（provider 的内容） | ❌ |
| 维护分组结构 | ✅（脚本区块） | ✅（执行脚本） |
| 原有分组首次跟随实测分组 | ✅（自动切换一次） | ❌ |
| 记住用户的手动选择 | ❌ | ✅ store-selected |
| 组内选择节点（测延迟、故障转移） | ❌ | ✅ url-test / fallback |
| 转发用户流量 | ❌ | ✅ |

### 7.8 节点会出现两份

provider 中的节点是订阅节点的**副本**，名称为实测格式（如 `🇺🇸 洛杉矶 .23.7 | 日本07`）。原来的「日本07」仍然留在订阅自带的分组里，二者互不干扰。

### 7.9 异常情况

| 场景 | 表现 |
|---|---|
| geoclash 未启动或崩溃 | mihomo 拉取失败后使用上次缓存的 provider 文件，分组照常可用，只是数据不再更新；原有分组不受影响 |
| Verge 先于 geoclash 启动（开机时常见） | provider 一开始可能为空；geoclash 第一轮检测完成后通过 ② 主动刷新，自动恢复 |
| 某个分组本轮没有可用节点 | 返回 503，mihomo 继续使用旧缓存 |
| 订阅节点发生变化 | 下一轮重新读取并检测，provider 内容随之更新 |
| controller 不可用 | 跳过 ②、⑤、⑥，依靠 provider 自身的刷新周期以及 Verge 下次生成配置时生效；⑥ 在 controller 恢复后补做 |
| 被挂接的分组当前走 DIRECT / REJECT | 只挂接到第一位，不自动切换 |
| 用户手动改选了已挂接分组 | geoclash 不再干预 |
| 热重载预校验失败 | 放弃重载并记录错误，当前运行的配置不变 |
| 用户在 Verge 中修改了全局脚本 | 标记外的修改保留；标记内的修改会在下次写入时被覆盖 |
| 自动发现失败（未安装 Clash 且未配置订阅） | 记录错误并按检测间隔重试，HTTP 服务正常运行，状态页显示原因 |

## 8. 跨平台

- 用 Go 交叉编译，`CGO_ENABLED=0`。发布目标：`windows/amd64`、`windows/arm64`、`darwin/amd64`、`darwin/arm64`、`linux/amd64`、`linux/arm64`。
- 平台相关代码通过 build tag 隔离（`_windows.go` / `_darwin.go` / `_linux.go`）：

| 平台相关点 | Windows | macOS | Linux |
|---|---|---|---|
| Clash 配置自动发现 | 见 3.2 | 见 3.2 | 见 3.2 |
| controller 连接 | 命名管道（`go-winio`）/ TCP | unix socket / TCP | unix socket / TCP |
| 开机自启 | 注册表 `HKCU\...\Run` | LaunchAgent | systemd user unit，或 XDG autostart |
| 无窗口运行 | GUI 子系统 + AttachConsole（见 9.3） | 天然无窗口 | 天然无窗口 |
| `PROCESS-NAME` 中的进程名 | `geoclash.exe` | `geoclash` | `geoclash` |

- **数据始终与程序放在一起**：geoclash 自己的文件（`config.yaml`、`state.json`、`geoclash.log`）存放在可执行文件所在的目录。首次运行时如果没有 `config.yaml`，会自动生成一份带注释的默认配置。
- **默认位置是用户主目录下的 `.geoclash/`**，三个平台一致：

| 平台 | 默认位置 |
|---|---|
| Windows | `C:\Users\<用户>\.geoclash\` |
| macOS | `/Users/<用户>/.geoclash/` |
| Linux | `/home/<用户>/.geoclash/` |

  `geoclash install` 默认把程序复制到这里，数据随之存放在这里。想放在其他目录时，把程序放过去后执行 `geoclash install --here`。
- 可执行文件所在目录不可写时（例如放在 `/usr/local/bin`），数据存放在默认位置。
- 从其他位置运行 `status`、`stop` 等命令时，如果程序所在目录没有配置，会使用默认位置的配置，因此能找到已安装的实例；这些命令不会在当前目录生成配置文件。
- 可以用 `-c` 指定其他配置文件；状态与日志始终放在配置文件所在目录。

## 9. 命令行与开机自启

### 9.1 子命令

```
geoclash run        前台运行（默认命令）
geoclash install [--here] [--wait]
                    把程序复制到 ~/.geoclash/ 并安装开机自启、在后台启动；
                    --here 表示程序留在当前位置，数据也放在当前位置；
                    --wait 表示等实测分组接入 Clash 后再返回（最长 5 分钟）
geoclash uninstall  移除开机自启、停止后台进程、移除 Verge 脚本中的 geoclash 区块
geoclash start [--wait]
                    在后台启动
geoclash stop       停止后台进程
geoclash status     查看运行状态、自启状态、上一轮检测摘要
geoclash refresh    立即触发一轮检测（请求正在运行的实例）
geoclash reset-switch [组名…] [--all-profiles]
                    清除分组的切换记录，重新执行一次「首次自动切换」（见 7.4）
geoclash script     输出 geoclash 脚本区块
geoclash discover   打印自动发现的结果（Clash 配置路径、controller 地址、脚本路径），用于排查
```

通用参数：`-c <配置文件>`，默认为程序所在目录下的 `config.yaml`（见第 8 节）。

### 9.2 开机自启的实现

全部为**用户级**自启，不需要管理员 / root 权限。这也是必需的：Verge 和它的配置都属于当前用户。

- `geoclash install`：先停止运行中的实例，把程序复制到 `~/.geoclash/`，自启项指向这份程序，数据也在这里。用新版本再执行一次 `install` 即为升级。
- `geoclash install --here`：程序不复制，自启项直接指向当前位置的程序，数据放在它旁边。移动程序后需要在新位置重新执行一次。

**迁移**：目标目录中还没有配置时，`install` 会把旧版本数据目录（`os.UserConfigDir()/geoclash/`，Windows 上为 `%APPDATA%\geoclash\`）中的配置、状态与日志迁移过来，并删除旧的自启副本 `bin/`。切换记录随之保留，各分组不会被重新切换。

| 平台 | 实现 |
|---|---|
| Windows | 写入注册表 `HKCU\Software\Microsoft\Windows\CurrentVersion\Run\geoclash`，值为 `"<副本路径>" run -c "<配置路径>"` |
| macOS | 写入 `~/Library/LaunchAgents/com.geoclash.agent.plist`（`RunAtLoad`、`KeepAlive`），然后执行 `launchctl bootstrap gui/<uid>` |
| Linux | 有 systemd 时写入 `~/.config/systemd/user/geoclash.service`（`Restart=on-failure`），然后执行 `systemctl --user enable --now geoclash`；没有 systemd 时写入 `~/.config/autostart/geoclash.desktop` |

单实例保证：启动时先占用 HTTP 监听端口，端口已被占用则说明已有实例在运行，直接退出。`stop` / `refresh` / `status` 通过本地 HTTP 接口与正在运行的实例通信。

### 9.3 Windows 无窗口运行

Windows 版本用 `-ldflags "-H=windowsgui"` 编译为 GUI 子系统程序，开机自启时不会出现任何窗口。

从终端执行子命令时，程序在启动时调用 `AttachConsole(ATTACH_PARENT_PROCESS)` 挂接到父终端，因此 `geoclash status` 等命令在 cmd / PowerShell 中仍能输出（输出可能出现在提示符之后，这是 Windows 的已知行为）。

## 10. 配置文件示例

所有字段都是可选的，一个空文件也能运行（自动发现 + 自动模式）。

```yaml
listen: 127.0.0.1:9876
interval: 6h
watch: 30s                 # 检查节点来源文件的间隔，变化时增量检测；0 表示关闭
watch-debounce: 10s        # 发现变化后等待文件写完的时间；0 表示立即处理

# 留空则自动发现本机 Clash 配置（见第 3 节）
sources:
  # - url: "https://<订阅链接>"
  # - file: "/path/to/clash/config.yaml"

exclude: ["剩余流量", "套餐到期", "过期时间", "下次重置", "官网", "电报群", "防失联"]   # 节点名正则

probe:
  concurrency: 16
  timeout: 10s
  min-agree: 2
  stale-rounds: 2
  retries: 1               # 失败的节点在本轮内重试的次数
  providers: [cloudflare, ipapi, ipinfo, ipwhois]

dns: []                    # 可选，如 ["https://223.5.5.5/dns-query"]；为空时使用系统 DNS
interface: ""              # 可选，检测流量绑定的网卡名；为空时依靠 PROCESS-NAME 直连规则绕开 TUN

rename: "{flag} {city} {ip_tail} | {name}"
city-lang: zh-CN           # zh-CN：中文优先，回退英文；en：英文

countries: []              # 留空为自动模式（见第 5 节）
auto-min-nodes: 1
others: false              # 仅显式列表模式有效

selector-name: "🌍 实测国家"
prefer: [US]               # 「🌍 实测国家」的默认国家，按顺序取第一个有节点的；都没有时取节点最多的
attach-to: auto            # auto：代理型 select 组；all：所有 select 组；也可以写显式列表；[] 表示不挂接（见 7.4）
attach-exclude: []         # 不挂接的分组名正则
auto-switch: once          # once：首次挂接时自动切换一次；never：不切换

controller:                # 留空则从 Clash 运行时配置中自动发现
  # url: "http://127.0.0.1:9097"     # 或 pipe:\\.\pipe\xxx、unix:/path/to/sock
  # secret: "xxx"

verge:
  write-script: true       # 自动写入全局扩展脚本的 geoclash 区块
  hot-reload: true         # 国家集合变化时热重载
```

## 11. 项目结构

```
main.go                       入口、子命令分发
internal/config               配置加载、默认值、首次运行时生成默认配置
internal/discover             Clash 配置 / controller / 脚本路径自动发现（按平台）
internal/source               节点读取（订阅链接、文件、Verge 订阅 + provider 缓存）
internal/probe                节点拨号、归属地接口、国家投票、城市选取
internal/geo                  国家代码表（中文名、别名、国旗 emoji）
internal/naming               节点命名模板、去重
internal/store                检测结果与城市缓存，持久化为 JSON
internal/server               HTTP：provider / 状态页 / 控制接口 / 脚本
internal/controller           mihomo REST 客户端（TCP / 命名管道 / unix socket）
internal/verge                脚本区块生成、写入与移除、goja 执行、热重载
internal/autostart            开机自启（autostart_windows.go / _darwin.go / _linux.go）
internal/console              Windows AttachConsole（其他平台为空实现）
docs/                         设计文档
scripts/build.sh              多平台交叉编译（输出到 dist/）
```

依赖：

- `github.com/metacubex/mihomo`（v1.19.34，`go 1.20`，兼容本机 Go 1.23.7）
- `github.com/dop251/goja`（纯 Go 的 JS 引擎，用于执行脚本区块）
- `gopkg.in/yaml.v3`
- `golang.org/x/sys`（Windows 注册表、控制台 API）
- `github.com/Microsoft/go-winio`（Windows 命名管道）

## 12. 测试计划

- **单元测试**：国家投票、城市选取与缓存、命名模板与去重、订阅解析（YAML / base64）、自动发现（使用临时目录模拟 Verge 目录结构）、provider 输出、503 行为。
- **挂接规则测试**：`auto` / `all` / 显式列表 / `[]` 四种取值；代理型 select 组判定（含直连组、广告拦截组、只含分组的组、url-test 组等典型订阅结构）；插入位置（默认选择指向节点 → 第一位，指向内置策略 → 末尾）；排除正则、自动切换条件（`now` 链路解析到节点 / DIRECT / 已经过实测分组）、「只切换一次」的状态记录；`reset-switch` 的三种用法以及运行中 / 未运行两种情况。
- **脚本共存测试**：用 goja 执行「用户原有脚本 + geoclash 区块」，覆盖以下情况：原脚本为空、有 `function main`、有 `var main`、有 `const main`，以及以当前全局脚本的真实内容作为测试样例；验证串联顺序、幂等性，以及写入、移除后标记外的内容不变。
- **实测验证**：读取本机 Verge 已下载的订阅，通过节点访问归属地接口跑一轮真实检测，核对结果；**不修改** Verge 的配置和脚本。脚本写入与热重载先在临时副本上验证，经确认后再作用于真实环境。

## 13. 注意事项

- 节点服务器域名解析不到 IP 的节点（机场侧已下线）会一直显示为检测失败；它们在 Clash 中同样不可用。

- Clash 的 `find-process-mode` 不能设为 `off`，否则 `PROCESS-NAME` 规则不生效。
- 建议为 controller 设置非默认的 secret；geoclash 会从运行时配置中自动读取，不需要同步改动。
- ip-api 免费额度为每个出口 IP 每分钟 45 次，按默认检测频率远远用不完。
- 节点名包含出口 IP 尾段：如果某些节点的出口 IP 经常变化（例如轮换型家宽），节点名也会随之变化。
- macOS 首次运行未签名的二进制时，可能需要执行 `xattr -d com.apple.quarantine geoclash`。

## 14. 待确认事项

无。所有决策已确认，见第 15 节。

## 15. 决策记录

| # | 事项 | 决定 |
|---|---|---|
| 1 | 归属地判定依据 | 出口 IP |
| 2 | 节点命名格式 | `{flag} {city} {ip_tail} \| {name}`，例如 `🇺🇸 洛杉矶 .23.7 \| 日本07` |
| 3 | 城市名语言 | 中文优先（ip-api `zh-CN`），回退英文 |
| 4 | 检测频率 | 每 6 小时 |
| 5 | countries 默认值 | 留空，自动模式；国家集合变化时自动更新脚本 |
| 6 | 脚本接入方式 | 自动写入，与已有脚本串联，写入前备份 |
| 7 | 脚本写入位置 | 全局扩展脚本（Script.js），当前不为空，采用末尾区块 + 包装 `main` 的方式 |
| 8 | 国家集合变化后的生效方式 | 立即热重载；已确认不会裸连（见 7.6） |
| 9 | Windows 无窗口方式 | 单个 GUI 子系统程序 + AttachConsole |
| 10 | 测试验证 | 用本机订阅实测 |
| 11 | 原有分组如何接入 | 生成独立的实测分组；「🌍 实测国家」插入原有分组的第一位，原有节点保留，可手动选择 |
| 12 | 挂接范围 | 通用规则：自动挂接代理型 select 组（成员中直接包含真实节点），不依赖特定订阅或用户脚本；支持显式列表与排除正则 |
| 13 | 原有分组的跟随方式 | 首次挂接时自动切换一次（仅当前最终指向真实节点时），之后尊重手动选择 |
| 14 | 默认国家 | `prefer: [US]`，没有该国节点时取节点最多的国家 |
| 15 | `attach-to` 取值 | `auto` / `all` / 显式列表 / `[]`；`all` 表示所有原始 select 组都跟随 |
| 16 | 挂接位置 | 默认选择指向真实节点的分组插入第一位；指向内置策略的分组插入末尾，保持原默认行为 |
| 17 | 重置切换记录 | 增加 `geoclash reset-switch` 命令，支持指定分组和 `--all-profiles`；运行中立即生效 |
| 18 | 数据文件位置 | 数据始终与程序放在一起；默认位置为 `~/.geoclash/`（三个平台一致），`install` 默认复制到这里，`--here` 留在原位置；程序目录不可写时使用默认位置 |
| 19 | 订阅更新的同步方式 | 文件看门狗：监视订阅文件变化后增量检测；不使用 mihomo 日志事件流 |

## 16. 实现记录

### 16.1 实现过程中的调整

| 调整 | 原因 |
|---|---|
| 新增 `probe.retries`（默认 1），失败节点在本轮内重试 | 实测部分家宽节点时通时断 |
| 出口 IP 优先取 IPv4 | 双栈节点的 IPv6 出口每个连接都不同，导致节点名每轮变化 |
| 命名管道 / unix socket 不存在时枚举回退 | Verge 服务模式下实际管道名与运行时配置不一致 |
| 新增 `interface` 配置项 | 在 PROCESS-NAME 规则尚未注入时（例如首次验证），可以绑定物理网卡绕开 TUN |
| 默认 `exclude` 增加常见机场信息节点关键词 | 电报群、防失联页等信息节点 |
| 热重载条件增加「脚本区块内容变化」 | 修改分组类型等配置时，分组集合不变但内核也需要更新 |
| 控制接口统一放在 `/api/` 下，并增加 Host 校验和请求头校验 | 见第 6 节「安全」 |
| 数据与程序放在一起，默认位置改为 `~/.geoclash/`，新增 `install --here`，并迁移旧位置的数据 | 用户要求（见第 15 节决策 18） |
| 构建脚本只删除 `dist/geoclash-*`，不再清空整个 `dist/` | 数据可能与程序一起放在 `dist/` |
| 新增文件看门狗（`watch` 默认 30s，`watch-debounce` 默认 10s）与增量检测 | 订阅更新后不必等 6 小时（见第 15 节决策 19） |
| 实测分组放在分组列表最前面（原为末尾） | 用户要求，便于在 Clash 界面中查找 |
| macOS / Linux 从运行中的内核进程读取 controller 地址 | Mac 上 Verge 服务模式的 socket 位于 `/var/run/clash-verge-service/users/<uid>/`，与运行时配置不一致，导致无法热重载 |
| 启动时用上次的检测结果立即接入；`install` / `start` 提示接入时机，新增 `--wait` | 全新安装后约 1 分钟才接入，用户以为没有生效 |
| 添加 MIT 许可证；GitHub Actions：推送 / PR 时三平台测试，推送 `v*` tag 时自动构建并发布 Release | 公开仓库需要许可证；发布不再依赖本地构建 |
| 换密码的节点继承原有结果；全部失败时等待 60 秒重测 | Mac 唤醒后订阅更新，所有节点标识改变且网络未就绪，一次增量检测全部失败 |
| goja 固定为 2025-01 的版本，`x/sys` 保持 mihomo 使用的版本 | 兼容 Go 1.23；新版依赖要求 Go 1.26 |

### 16.2 验证结果（2026-11-22）

- **单元测试**：config、discover、naming、plan、probe、source、store、verge、app 共 9 个包全部通过。
- **跨平台编译**：Windows / macOS / Linux 的 amd64、arm64 共 6 个目标全部编译成功；Windows GUI 子系统版本在 cmd、PowerShell、Git Bash 中都能正常输出。
- **实测**（读取本机 Verge 当前订阅，不修改 Verge）：
  - 76 个节点（排除 5 个信息节点），约 25 秒完成；31 个成功，生成美国 7、日本 10、新加坡 6、台湾 4、印度尼西亚 2、印度 2 共 6 个分组。
  - 失败节点抽查：美国03/04、日本02、新加坡01、临时节点3 通过 Verge 内核测速同样不可用（服务器域名没有解析记录）；台湾01家宽 等属于时通时断，加入本轮重试后改善。
  - 连续两轮检测，节点名完全一致。
  - 本订阅中有标注的节点，实测国家均与标注一致；未标注地区的「临时节点」实测位于印度尼西亚和印度。
- **真实全局脚本共存**（内存中执行，不写文件）：用户原有的自定义节点与规则集注入、DIRECT 规则、跟随组全部保留并先于 geoclash 执行。
- **临时副本验证**：写入脚本副本后，标记外内容与原文逐字一致、备份正确；热重载 payload 只改动 `proxy-groups`、`rules`（另新增 provider），其余 20 个顶层字段不变；通过 mihomo 完整配置解析；对已注入的配置再次执行结果相同。
- **真实环境部署**（用户确认后执行 `geoclash install`）：
  - 已写入全局扩展脚本区块（原文件备份为 `Script.js.geoclash.bak`），已热重载内核，生成 6 个实测分组。
  - 首次自动切换：订阅中 7 个代理型 select 分组均已跟随「🌍 实测国家」，默认选择「🇺🇸 美国-实测」。
  - 已安装开机自启（`HKCU\...\Run\geoclash`）。
  - 升级（重复执行 `install`）后，分组集合不变时不会重写脚本、不会热重载。
  - 内核连接抽样：检测期间 geoclash.exe 的连接全部命中 `PROCESS-NAME,geoclash.exe,DIRECT`。
- **部署中发现并修正**：GLOBAL（内核内置的全局模式选择组）包含所有分组，曾被纳入首次切换判断；已排除（0.1.1）。
- **已知现象**：使用系统 DNS 且 Verge 为 fake-ip 模式时，域名已失效的节点会拿到假 IP，需要等握手超时才判定失败，一轮检测约 70 秒（配置 `dns` 后约 25 秒）。不影响结果。
- **已验证：Verge 更新订阅不会恢复旧选择**。Verge 在 `profiles.yaml` 中记录的各分组选择（如「人工智能 → 日本06」）不会因 geoclash 通过 controller 切换而更新；但用户在 Verge 中更新订阅、重新生成配置后，内核中 7 个分组仍然指向「🌍 实测国家」，旧记录没有被应用。重新生成的运行时配置由 Verge 执行全局脚本中的 geoclash 区块得到，实测分组照常加载节点。
