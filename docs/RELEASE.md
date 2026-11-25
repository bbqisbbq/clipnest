# 打包、签名与发版

> 这里是发包操作的**唯一来源**。README 只留"用户怎么装"，构建/自检在 ARCHITECTURE 五。
> 目标平台现状见 [ARCHITECTURE 七](ARCHITECTURE.md#七平台契约多平台).

## 一、安装包长什么样

只发一个包：**全用户安装**（写到 `C:\Program Files\bbqisbbq\ClipNest`，需要管理员权限，向导里可改目录）。
从 2.4.0 起不再提供免 UAC 的 per-user 版本。

| 文件 | 安装到 | 权限 |
| --- | --- | --- |
| `clipnest-<版本>-amd64-setup.exe` | `C:\Program Files\bbqisbbq\ClipNest` | 需要管理员（UAC） |
| `clipnest-<版本>-amd64-setup.exe.sig` | 不安装，自动更新用它验签 | 必须与安装包一起上传 |

每个 Release 只带**它自己那个版本**的安装包（外加 2.7.1 起的 `.sig`）。自动更新只看
`/releases/latest`，所以每个版本都保留自己的安装包：不会被误装，也方便回滚到指定版本。

安装向导（简体中文）问三件事：安装位置、是否「开机自动启动」（默认勾选，写当前用户的
`HKCU\...\Run`，命令是 `"<安装目录>\clipnest.exe" --background`，之后托盘与设置页都能改）、
装完是否立即启动。

其它行为：

- 缺少 WebView2 运行时会自动安装（安装包里内置 bootstrapper 才会联网下载）。
- 卸载在「设置 → 应用 → 已安装的应用」；**会先询问是否删除剪贴板历史**（默认保留），
  静默卸载 `/S` 总是保留数据。同时清掉开机启动项、桌面与开始菜单快捷方式。
- 安装包**未做 Authenticode 签名**，首次运行会触发 SmartScreen 提示（「更多信息 → 仍要运行」）。

## 二、打包

```powershell
# 改了 config.yml 里的版本号后先跑一次，同步 build/**/Info.plist、windows/info.json、wails_tools.nsh
wails3 task common:update:build-assets

# NSIS 全用户安装包（发布用的就这一个）
$env:CGO_ENABLED=1; wails3 task windows:package
# 产物：bin\clipnest-amd64-installer.exe —— 发布前改名 clipnest-<版本>-amd64-setup.exe

# 本地自用想免 UAC 时可以打 per-user 包，但不随发布分发
wails3 task windows:package INSTALL_SCOPE=user
```

> ⚠️ **打包必须带 `CGO_ENABLED=1`**：Taskfile 里默认是 0，那样 `internal/ppocr` 编进来的是 stub，
> 高精度 OCR 与它的下载按钮在装好的版本里等于不存在（Go 还会把整段"下载/解压"代码当死代码删掉）。
> 自查：`Select-String -Path bin\clipnest.exe -Pattern 'releases/download/ocr-assets-v1'` 应当有命中。
>
> ⚠️ **cgo 必须配 `-linkmode=internal`**（`build/windows/Taskfile.yml` 的 ldflags 里已经带上）。
> 没有它会走 gcc 外部链接，mingw 的 `ld` 合并资源时直接报
> `.rsrc merge failure: multiple non-default manifests` —— 因为 `windows:generate:syso` 生成的
> `.syso` 里已经有一份 manifest。**这条报错不会让 task 停下**：后面的 NSIS 步骤会拿上一次的
> `bin\clipnest.exe` 照打一个安装包，看起来"打包成功"，实际发的是旧二进制。
> 打包后务必确认 `bin\clipnest.exe` 的修改时间是刚才（见 RELEASE 四的清单）。

安装脚本在 `build/windows/nsis/project.nsi`（UTF-8 **带 BOM**，NSIS 要求），可自定义欢迎页、
组件页、卸载时是否保留数据、签名命令等。

## 三、签名（自动更新强制验签）

从 **2.7.1** 起，自动更新会校验安装包的 **ed25519 签名**，**失败即拒绝安装**（宁可让用户停在旧版本，
也不装一个来源无法确认的可执行文件）。所以每次发布都必须把 `.sig` 和 `.exe` 一起传上去。

```powershell
# 生成 <安装包>.sig，并自检一遍
pwsh -File scripts\sign-release.ps1 -Installer bin\clipnest-<版本>-amd64-setup.exe
```

- 私钥在 `build\signing\ed25519.key`（**已 gitignore，丢了就再也签不出可被现有版本接受的包**，
  需要重新发一版同时信任新旧公钥的过渡版本才能换根）。
- 公钥硬编码在 `internal/clip/update_sign.go` 的 `updatePubKeyB64`。
- 签名工具：`go run ./scratch/signinstaller keygen|pubkey|sign|verify`。
- 漏传 `.sig` 的后果是可预期的：更新检查会明确报「发布缺少签名文件，已拒绝更新」，不会静默装。

**Authenticode（可选）**：自签名证书在 `build\signing\`（不入库）。自签名不会自动消除"无法验证发布者"
提示，但文件被篡改会被立刻发现；把 `build\signing\clipnest-selfsigned.cer` 导入「受信任的根证书
颁发机构 + 受信任的发布者」后不再报警。

```powershell
# 自签名（默认，无需付费证书）
powershell -File scripts\sign.ps1 -Path "bin\clipnest-<版本>-amd64-setup.exe,bin\clipnest.exe" -SelfSigned

# 正式证书（买了 OV/EV 证书后；带时间戳才能长期有效）
$env:CLIPNEST_SIGN_PFX="D:\certs\clipnest.pfx"; $env:CLIPNEST_SIGN_PASSWORD="***"
powershell -File scripts\sign.ps1 -Path "bin\*.exe" -Timestamp http://timestamp.digicert.com
```

## 四、发版清单

1. 改版本号：`internal/clip/service.go` 的 `Version` 与 `build/config.yml` 的 `info.version` 保持一致；
2. `wails3 task common:update:build-assets`；
3. `$env:CGO_ENABLED=1; wails3 task windows:package` → 改名成 `clipnest-<版本>-amd64-setup.exe`
   （**只发全用户安装包**）。打包后**先确认 `bin\clipnest.exe` 的时间戳是刚才**，再往下走——
   链接器报错不会让 task 停下，NSIS 会拿旧 exe 打出一个"看起来成功"的包。
4. 写 `docs/releases/release-notes-v<版本>.md`（发布页的 `--notes-file`），并把要点加进
   [CHANGELOG](CHANGELOG.md)；
5. 签名：`pwsh -File scripts\sign-release.ps1 -Installer bin\clipnest-<版本>-amd64-setup.exe`；
6. 提交 + 打 tag：`git tag -a v<版本> -m "ClipNest <版本>"`，`git push origin main --tags`；
7. 建 Release，上传**两个**文件：

   ```powershell
   gh release create v<版本> "bin\clipnest-<版本>-amd64-setup.exe" "bin\clipnest-<版本>-amd64-setup.exe.sig" `
     --title "ClipNest <版本>" --notes-file docs\releases\release-notes-v<版本>.md
   ```

   每个版本都保留自己的安装包与 `.sig`；旧 release 的**资产**清掉、release 本身留着当变更记录。
8. 收尾：`bin\` 里历史安装包可以删（都已在 Releases），只留当前版本。

## 五、MyGo 构建（分支 mygo-shell 起）

宿主换成 MyGo 之后，**打包链一行没换**：仍是 NSIS 全用户安装包 + ed25519 签名 + 可选 Authenticode，
只有"Go 那边谁来 build"变了（`wails3 task windows:package` 会重编成 Wails 版，不能用）。

```powershell
# 一条命令走完：版本号 → 前端 vite build → syso 资源 → CGO go build → NSIS → ed25519 签名 → 自检
pwsh -File scripts\package-mygo.ps1 -Version 2.12.0
# 产物：bin\clipnest-2.12.0-amd64-setup.exe（+ .sig）
```

脚本做的事与 `windows:package` 的差别只有第 4 步：

```
wails3 generate syso -arch amd64 -icon windows/icon.ico -manifest windows/wails.exe.manifest -info windows/info.json
$env:CGO_ENABLED=1; go build -tags production -trimpath -buildvcs=false -ldflags="-w -s -H windowsgui -linkmode=internal" -o bin/clipnest.exe .
makensis -DARG_WAILS_AMD64_BINARY=<绝对路径>\bin\clipnest.exe project.nsi
```

坑（都写进脚本的自检里了）：

- **安装包版本号来自 `build/windows/nsis/wails_tools.nsh`**，不是 `info.json`。漏改会出现
  "exe 里是 2.12.0、安装包属性还是 2.11.0"。要么跑 `wails3 task common:update:build-assets`，
  要么像脚本里那样直接同步那个 define。
- `build/windows/nsis/MicrosoftEdgeWebview2Setup.exe` 是 **gitignore 的**（1.7MB），
  新 clone/worktree 里没有它，`makensis` 会报 `File: "MicrosoftEdgeWebview2Setup.exe" -> no files found`。
  从已有 checkout 拷过来，或 `wails3 generate webview2bootstrapper -dir build\windows\nsis`。
- **先 Authenticode、后 ed25519**：Authenticode 会改文件字节，先算的 `.sig` 立刻失效
  （脚本里两步顺序固定，改了记得重跑签名）。
- 打包完自查两条：`bin\clipnest.exe` 的时间戳是刚才；`Select-String -Path bin\clipnest.exe -Pattern 'releases/download/ocr-assets-v1'`
  有命中（否则 ppocr 被编成了 stub）。
  换壳后的产物还能用 `Select-String -Path bin\clipnest.exe -Pattern 'ClipService.GetEntries'` 确认前端是 MyGo 适配层。
