# geoclash

English | [简体中文](README.zh-CN.md)

geoclash periodically measures where your Clash nodes **actually exit** to the internet, groups them by real country, and wires those groups into Clash Verge Rev / mihomo. Your existing groups follow the measured groups by default, and the node inside each country group is picked automatically.

Many airport subscriptions label nodes with a location that differs from where their traffic really comes out, sometimes not even the same country. geoclash probes every node *through the node itself*, so relays and CDN front-ends are measured by their real exit IP.

The design document (in Chinese) is at [docs/design.md](docs/design.md).

## Quick start

```bash
# 1. Show the detected Clash environment and which groups would be attached
geoclash discover

# 2. Install autostart and start in the background (a default config is created on first run)
geoclash install

# 3. Check status, or open http://127.0.0.1:9876/
geoclash status
```

Nothing needs to be configured by default:

- Reads the currently active subscription of Clash Verge Rev automatically.
- Runs a full check every 6 hours and creates one group per measured country (e.g. `🇺🇸 美国-实测`, "US – measured") plus a selector group `🌍 实测国家` ("measured countries").
- Watches the current subscription file: within about half a minute after a subscription is updated or switched, only new nodes are probed and removed nodes are dropped.
- Writes a geoclash block into the Verge global extension script. It is chained after your existing script, and the original is backed up as `Script.js.geoclash.bak`.
- Hot-reloads the kernel when the set of groups changes. Proxy-type groups are switched to `🌍 实测国家` once, the first time they are attached.

After a fresh install, the groups show up in Clash once the first check finishes (about a minute); add `--wait` to `install` to wait for it. Upgrades, restarts and autostart use the previous results and take effect immediately.

### How it works

- Nodes are dialed with mihomo's own protocol implementations, so Hysteria2, Trojan and every other mihomo protocol are supported.
- Several IP geolocation services vote on the exit country (Cloudflare trace, ip-api, ipinfo, ipwho.is). The city is shown in the node name, Chinese first by default.
- geoclash serves a `proxy-provider` per country on a local HTTP port. Clash fetches the nodes from there, and geoclash asks the kernel to refresh them after each check.
- Group names, countries, selection strategy (`url-test` / `fallback`), the default country (`prefer`) and which groups follow the measured groups (`attach-to`: `auto` / `all` / a list) are all configurable in `config.yaml`. Set `city-lang: en` for English city names.

## Commands

| Command | Description |
|---|---|
| `geoclash run` | Run in the foreground (default command) |
| `geoclash install [--here] [--wait]` | Copy the binary to `~/.geoclash/`, install autostart and start in the background; `--here` keeps it where it is, `--wait` waits until the groups are loaded into Clash |
| `geoclash uninstall` | Remove autostart, stop the process, and remove the geoclash block from the Verge script |
| `geoclash start [--wait]` / `stop` | Start / stop in the background |
| `geoclash status` | Running state, autostart state, summary of the last check |
| `geoclash refresh` | Run a check now |
| `geoclash reset-switch [group…] [--all-profiles]` | Clear switch records so the "first-time auto switch" runs again |
| `geoclash script` | Print the geoclash script block (for manual use outside Verge) |
| `geoclash discover` | Print auto-discovery results and an attach preview |

Common flags: `-c <config file>`, `-log <log file>`.

## File locations

The config (`config.yaml`), state (`state.json`) and log (`geoclash.log`) **always live next to the binary**.

- The default location is `.geoclash/` in your home directory, the same on Windows, macOS and Linux. `geoclash install` copies the binary there and autostarts it from there.
- To use another directory, put the binary there and run `geoclash install --here`.
- If the binary's directory is not writable (e.g. `/usr/local/bin`), data is stored in `~/.geoclash/`.

## Platforms

Windows, macOS and Linux (amd64 / arm64). Autostart is per-user and needs no admin / root rights:

| Platform | Autostart |
|---|---|
| Windows | `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` |
| macOS | LaunchAgent (`~/Library/LaunchAgents`) |
| Linux | systemd user unit, or XDG autostart without systemd |

## Build

Requires Go 1.23 or later.

```bash
go test ./...
scripts/build.sh           # version from git describe; or set it explicitly, e.g. scripts/build.sh 0.2.0
                           # binaries and release archives for 6 platforms are written to dist/
```

Releases are built by GitHub Actions: pushing a `v*` tag runs the tests, builds and packages all platforms, and publishes a GitHub Release with the archives and `SHA256SUMS`.

## Notes

- Clash's `find-process-mode` must not be `off`. Otherwise geoclash's probe traffic is captured by TUN and proxied again, and the measured exit is wrong.
- Nodes in the measured groups are copies of the subscription nodes, named like `🇺🇸 洛杉矶 .23.7 | 日本07` (flag, city, exit IP tail, original name). The original nodes stay in the groups that came with the subscription.

## License

[MIT](LICENSE)
