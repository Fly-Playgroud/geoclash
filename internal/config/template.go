package config

// Template 是首次运行时写入的默认配置。所有字段都是可选的，删掉任意一项都会使用默认值。
const Template = `# geoclash 配置文件
# 所有字段都是可选的；删除某一项即使用默认值。设计说明见项目 docs/design.md。

# 本地 HTTP 服务地址（provider、状态页、控制接口）
listen: 127.0.0.1:9876

# 完整检测间隔
interval: 6h

# 检查节点来源文件（Clash 当前订阅文件及其 provider 缓存）的间隔。文件变化（例如更新订阅、切换订阅）时，
# 只检测新增的节点、移除已删除的节点。0 表示关闭。订阅链接（sources.url）不在监视范围内
watch: 30s
# 发现文件变化后，等待文件写完（内容不再变化）的时间；0 表示立即处理
watch-debounce: 10s

# 节点来源。留空则自动发现本机 Clash（Clash Verge Rev / mihomo）的当前订阅
sources:
  # - url: "https://<订阅链接>"
  # - file: "/path/to/clash/config.yaml"

# 排除的节点名（正则）
exclude: ["剩余流量", "套餐到期", "过期时间", "下次重置", "官网", "电报群", "防失联"]

probe:
  concurrency: 16        # 并发检测的节点数
  timeout: 10s           # 单次请求超时
  min-agree: 2           # 至少几个归属地接口给出相同国家才采用
  stale-rounds: 2        # 连续失败多少轮后移出国家组
  retries: 1             # 本轮检测失败的节点在本轮内重试的次数（应对时通时断的节点）
  providers: [cloudflare, ipapi, ipinfo, ipwhois]

# 解析节点服务器域名用的 DNS，如 ["https://223.5.5.5/dns-query"]；留空使用系统 DNS
dns: []

# 检测流量绑定的网卡名；留空不绑定（依靠注入的 PROCESS-NAME 直连规则绕开 TUN）
interface: ""

# 实测节点命名模板。占位符：{flag} {code} {country} {city} {ip} {ip_tail} {name}
rename: "{flag} {city} {ip_tail} | {name}"
city-lang: zh-CN         # zh-CN：中文优先，回退英文；en：英文

# 实测分组。留空为自动模式：每个检测到的国家各生成一组
# 显式写法示例：
#   countries: [US, JP, SG, TW]
#   countries:
#     - US
#     - { code: JP, type: fallback }
#     - { id: EU, name: "🇪🇺 欧洲-实测", codes: [DE, FR, NL, GB] }
countries: []
auto-min-nodes: 1        # 自动模式下建组所需的最少节点数
others: false            # 显式列表模式下，是否为列表外的节点生成「其他」组

# 跟随规则
selector-name: "🌍 实测国家"
prefer: [US]             # 「🌍 实测国家」的默认国家，按顺序取第一个有节点的；都没有时取节点最多的
attach-to: auto          # auto：代理型 select 组；all：所有 select 组；也可以写分组名列表；[] 不挂接
attach-exclude: []       # 不挂接的分组名（正则）
auto-switch: once        # once：首次挂接时自动切换一次；never：不切换

# mihomo controller。留空则从 Clash 运行时配置中自动发现
controller:
  # url: "http://127.0.0.1:9097"      # 或 pipe:\\.\pipe\xxx、unix:/path/to/sock
  # secret: "xxx"

verge:
  write-script: true     # 自动写入 Clash Verge Rev 全局扩展脚本中的 geoclash 区块
  hot-reload: true       # 国家集合变化时热重载
`
