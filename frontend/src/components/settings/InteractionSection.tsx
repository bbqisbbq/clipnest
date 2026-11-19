import type { AppInfo, Settings } from "../../lib/api";
import HotkeyInput from "../HotkeyInput";
import { Row, Section, Switch, type SettingsPatch } from "./ui";

// 「交互与启动」分区：快速粘贴快捷键、粘贴行为、开机自启。
// appInfo 只用来在自启提示里带上当前注册的命令行。

type Props = {
  draft: Settings;
  patch: SettingsPatch;
  appInfo: AppInfo | null;
};

export default function InteractionSection({ draft, patch, appInfo }: Props) {
  return (
    <Section title="交互与启动" desc="快速粘贴窗在光标处弹出，选中后直接粘进当前程序。">
      <Row label="快速粘贴快捷键" tip="点一下然后按下组合键即可录制；留空表示不注册全局快捷键。">
        <HotkeyInput value={draft.hotkey} onChange={(v) => patch({ hotkey: v })} />
      </Row>
      <Row label="选中后自动粘贴" tip="关闭后只写入剪贴板，需要自己按 Ctrl+V。">
        <Switch checked={draft.pasteOnPick} onChange={(v) => patch({ pasteOnPick: v })} />
      </Row>
      <Row label="启动时打开管理窗口" tip="关闭则启动后只驻留托盘。" >
        <Switch checked={draft.showOnLaunch} onChange={(v) => patch({ showOnLaunch: v })} />
      </Row>
      <Row
        label="开机自动启动"
        tip={
          appInfo?.autoStartTarget
            ? `登录 Windows 时自动运行（写入 HKCU\\...\\Run），以 --background 参数启动，只驻留托盘不弹窗。当前命令：${appInfo.autoStartTarget}`
            : "登录 Windows 时自动运行（写入 HKCU\\...\\Run），以 --background 参数启动，只驻留托盘不弹窗。"
        }
      >
        <Switch checked={draft.autoStart} onChange={(v) => patch({ autoStart: v })} />
      </Row>
    </Section>
  );
}
