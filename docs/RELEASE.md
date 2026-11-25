# 打包、签名与发版

> 发包操作的**唯一来源**。README 只留"用户怎么装"，构建自检在 [ARCHITECTURE 六](ARCHITECTURE.md#六怎么验证)。

## 一、安装包长什么样

只发一个包：**全用户安装**（写到 `C:\Program Files\bbqisbbq\ClipNest`，需要管理员，向导里可改目录）。
从 2.4.0 起不再提供免 UAC 的 per-user 版本。

| 文件 | 安装到 | 权限 |
| --- | --- | --- |
| `clipnest-<版本>-amd64-setup.exe` | `C:\Program Files\bbqisbbq\ClipNest` | 需要管理员（UAC） |
| `clipnest-<版本>-amd64-setup.exe.sig` | 不安装，自动更新用它验签 | 必须与安装包一起上传 |

安装向导（简体中文）问三件事：安装位置、是否「开机自动启动」（默认勾选，写
`HKCU\...\Run`，命令是 `"<安装目录>\clipnest.exe" --background`）、装完是否立即启动。
缺 WebView2 时会自动装（安装包内置 bootstrapper）。卸载先问是否删剪贴板历史（默认保留；
`/S` 静默卸载总是保留）。安装包**未做正式 Authenticode 签名**，首次运行会有 SmartScreen 提示。

## 二、打包（一条命令）

```powershell
pwsh -File scripts\package-mygo.ps1 -Version 2.12.1
# 产物：bin\clipnest-2.12.1-amd64-setup.exe（+ .sig）
```

脚本顺序：① 三处版本号 → ② 前端 `vite build` → ③ `go-winres` 生成 Windows 资源 → ④ `go build`
（`CGO_ENABLED=1`）→ ⑤ `makensis` 打安装包 → ⑥ ed25519 签名 → ⑦ 自检。

前置依赖：`go install github.com/tc-hib/go-winres@latest`、NSIS（`makensis`）、Node 20+。

**四个坑**（都写在脚本里了，改脚本时别删）：

1. **版本号有三处**：`internal/clip/service.go`（应用自报）、`build/windows/winres.json`（exe 资源）、
   `build/windows/nsis/installer.nsh`（安装包，控制面板里显示的就是它）。漏改第三处就会
   "exe 是 2.12.1、安装包属性还是 2.12.0"。
2. **必须 `CGO_ENABLED=1`**，否则 `internal/ppocr` 编成 stub，高精度 OCR 与下载按钮等于不存在。
   自查：`Select-String -Path bin\clipnest.exe -Pattern 'releases/download/ocr-assets-v1'` 要有命中。
3. **cgo 必须配 `-linkmode=internal`**（脚本 ldflags 里带着）。否则走 gcc 外部链接，
   mingw 的 `ld` 合并资源时报 `.rsrc merge failure: multiple non-default manifests`。
   这类链接错误**不会**让脚本停下，NSIS 会拿旧 exe 打个"看起来成功"的包 —— 所以脚本会校验
   `bin\clipnest.exe` 的时间戳。
4. **`build\windows\nsis\MicrosoftEdgeWebview2Setup.exe` 是 gitignore 的**（1.7MB），
   新 clone 里没有它 `makensis` 会报 `no files found`。从已有 checkout 拷一份即可。

> NSIS 的 `project.nsi` 与 `installer.nsh` 必须保持 **UTF-8 带 BOM**，否则 makensis 按 ACP 读、
> 中文向导页直接报 `Bad text encoding`。

## 三、签名

### ed25519（自动更新强制验签）

从 2.7.1 起，自动更新校验安装包的 ed25519 签名，**失败即拒绝安装**。所以 `.sig` 必须和 `.exe`
一起上传。

```powershell
pwsh -File scripts\sign-release.ps1 -Installer bin\clipnest-<版本>-amd64-setup.exe
```

- 私钥在 `build\signing\ed25519.key`（**已 gitignore，丢了就再也签不出被现有版本接受的包**）。
- 公钥硬编码在 `internal/clip/update_sign.go` 的 `updatePubKeyB64`。
- 工具：`go run ./scratch/signinstaller keygen|pubkey|sign <file>|verify <file> <sig>`。

### Authenticode（可选）

```powershell
powershell -File scripts\sign.ps1 -Path "bin\clipnest-<版本>-amd64-setup.exe,bin\clipnest.exe" -SelfSigned
```

自签名证书在 `build\signing\`（不入库）。它不会自动消掉"无法验证发布者"，但文件被改会立刻暴露；
把 `build\signing\clipnest-selfsigned.cer` 导入「受信任的根证书颁发机构 + 受信任的发布者」后不再报警。

> **顺序固定：先 Authenticode，后 ed25519。** Authenticode 会改文件字节，先算的 `.sig` 立刻失效。

## 四、发版清单

1. 改版本号（三处，见第二节），跑 `pwsh -File scripts\package-mygo.ps1 -Version <版本>`；
2. 确认 `bin\clipnest.exe` 时间戳是刚才、安装包属性里的版本正确；
3. Authenticode（可选）→ ed25519 签名 → `verify` 通过；
4. 写 `docs/releases/release-notes-v<版本>.md`，要点同步进 [CHANGELOG](../CHANGELOG.md)；
5. 提交 + 打 tag：`git tag -a v<版本> -m "ClipNest <版本>"`，`git push origin main --tags`；
6. 建 Release，上传**两个**文件：

   ```powershell
   gh release create v<版本> "bin\clipnest-<版本>-amd64-setup.exe" "bin\clipnest-<版本>-amd64-setup.exe.sig" `
     --title "ClipNest <版本>" --notes-file docs\releases\release-notes-v<版本>.md
   ```

   每个 Release 保留自己那一版的安装包与 `.sig`（自动更新只看 `/releases/latest`，
   所以不会被误装，也方便回滚到指定版本）；旧 release 的**资产**清掉、release 本身留着当变更记录。
