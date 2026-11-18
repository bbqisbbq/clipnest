# 有多少人在用？

两条零成本路线，都**不需要服务器**、**不上传任何东西**。

## 1. 应用内：安装包下载次数

设置 → 使用统计。数据来自 GitHub Releases 公开的 `download_count`，本地读取。

```
累计下载次数 22
版本    安装包                                      下载   发布
v1.0.0  clipnest-1.0.0-amd64-setup.exe              0   2026-11-18
v0.9.0  clipnest-0.9.0-amd64-setup-per-user.exe     1   2026-11-18
...
```

**口径**：下载 ≠ 用户。同一人可能装两次、装了又卸，自动更新也会产生下载。

## 2. 命令行：下载量 + 仓库流量

```powershell
pwsh -File scripts\stats.ps1            # 表格
pwsh -File scripts\stats.ps1 -Json      # 机器可读
```

- 下载量：`/releases` 里每个资产的 `download_count`（累计）
- 浏览量 / 独立访客 / clone：`/traffic/views`、`/traffic/clones`（**只有最近 14 天**，需要带 `repo` 权限的 token）

token 优先级：`-Token` 参数 → `$env:GITHUB_TOKEN` → `git credential fill`（脚本会自动取，通常不用管）。

## 为什么不内置遥测

要做「去重的真实活跃用户」必须有一个收集端：客户端定时 POST 一个匿名心跳，服务端按 install id 去重算 DAU/MAU。
我评估过并写好了（客户端 + 单文件 Go 服务 + 仪表盘），但你不想配服务器，所以**没有启用**：

- 现在这版**没有任何后台请求**，除了两个既有功能：检查更新、WebDAV 同步（都在设置里能看到开关）
- 代码里不留遥测客户端，避免"看着没开、其实在跑"的疑虑

什么时候值得上遥测：真的想区分「多少人装过」和「多少人今天还在用」的时候。那时再花 20 分钟在一台已有服务器上跑一个单文件 Go 服务即可。
