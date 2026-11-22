# geoclash

[English](README.md) | 简体中文

定期实测 Clash 节点的**出口**归属地，按实际国家生成分组，并接入 Clash Verge Rev / mihomo。原有分组默认跟随实测分组，组内自动选择节点。

设计说明见 [docs/design.md](docs/design.md)。

## 快速开始

```bash
# 1. 查看自动发现的 Clash 环境，以及哪些分组会被挂接
geoclash discover

# 2. 安装开机自启并在后台启动（首次运行会生成默认配置）
geoclash install

# 3. 查看状态，或打开 http://127.0.0.1:9876/
geoclash status
```

默认配置下不需要填写任何内容：

- 自动读取 Clash Verge Rev 当前激活的订阅。
- 每 6 小时完整检测一次，按实测国家生成「🇺🇸 美国-实测」等分组，以及「🌍 实测国家」选择组。
- 监视 Clash 当前订阅文件：更新或切换订阅后约半分钟内，只检测新增节点、移除已删除节点。
- 自动写入 Verge 全局扩展脚本中的 geoclash 区块（与已有脚本串联，写入前备份为 `Script.js.geoclash.bak`）。
- 分组集合变化时热重载内核；代理型分组首次挂接时自动切换到「🌍 实测国家」一次。

全新安装时，需要等第一轮检测完成（约 1 分钟）才会接入 Clash，`install` 加 `--wait` 可以等到接入完成再返回。升级、重启、开机自启时会先用上次的检测结果立即接入。

## 命令

| 命令 | 说明 |
|---|---|
| `geoclash run` | 前台运行（默认命令） |
| `geoclash install [--here] [--wait]` | 复制程序到 `~/.geoclash/` 并开机自启、后台启动；`--here` 留在当前位置，`--wait` 等实测分组接入 Clash 后再返回 |
| `geoclash uninstall` | 移除开机自启、停止进程、移除 Verge 脚本中的 geoclash 区块 |
| `geoclash start [--wait]` / `stop` | 在后台启动 / 停止 |
| `geoclash status` | 运行状态、自启状态、上一轮检测摘要 |
| `geoclash refresh` | 立即检测一轮 |
| `geoclash reset-switch [组名…] [--all-profiles]` | 清除切换记录，重新执行一次「首次自动切换」 |
| `geoclash script` | 输出 geoclash 脚本区块（非 Verge 环境手动使用） |
| `geoclash discover` | 打印自动发现结果与挂接预览 |

通用参数：`-c <配置文件>`、`-log <日志文件>`。

## 文件位置

配置（`config.yaml`）、状态（`state.json`）和日志（`geoclash.log`）**始终与程序放在同一目录**。

- 默认位置是用户主目录下的 `.geoclash/`（Windows、macOS、Linux 一致）：`geoclash install` 会把程序复制到这里并从这里开机自启。
- 想放在其他目录：把程序放过去，执行 `geoclash install --here`。
- 程序所在目录不可写时（如 `/usr/local/bin`），数据存放在 `~/.geoclash/`。

## 构建

需要 Go 1.23 或更高版本。

```bash
go test ./...
scripts/build.sh           # 版本号取自 git describe；也可以手动指定，如 scripts/build.sh 0.2.0
                           # 输出 6 个平台的二进制和发布包到 dist/
```

发布由 GitHub Actions 完成：推送 `v*` tag 后自动运行测试、构建并打包全部平台，创建 GitHub Release 并上传压缩包与 `SHA256SUMS`。

## 注意事项

- Clash 的 `find-process-mode` 不能为 `off`，否则 geoclash 的检测流量会被 TUN 再代理一层，测出的出口不正确。
- 实测分组中的节点是订阅节点的副本，名称形如 `🇺🇸 洛杉矶 .23.7 | 日本07`；原节点仍保留在订阅自带的分组中。

## 许可证

[MIT](LICENSE)
