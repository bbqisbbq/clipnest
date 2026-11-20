import type { AppInfo, Settings } from "../../lib/api";
import HotkeyInput from "../HotkeyInput";
import { Row, Section, Switch, type SettingsPatch } from "./ui";

// 「交互与启动」分区：快速粘贴快捷键、记一笔快捷键、粘贴行为、开机自启。
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
      {/* 这个快捷键以前只存在于 settings 里：界面没入口，改它得手改 settings.json。 */}
      <Row
        label="「记一笔」快捷键"
        tip="在光标处弹出小编辑框，Enter 保存、Esc 关闭；留空表示不注册。撞上别的软件时保存会报错。"
      >
        <HotkeyInput value={draft.captureHotkey} onChange={(v) => patch({ captureHotkey: v })} />
      </Row>
      {/* 窗口四角本来就是透明（圆角外侧透出桌面），这一项调的是卡片底色的 alpha。
          「关」= 实心卡片；拉到 100% = 卡片完全透掉，只剩文字和一圈细边。 */}
      <Row
        label="「记一笔」弹窗透明"
        tip="弹窗窗口本身一直是透明的（只是四角透出桌面），这项调卡片底色的透明程度：越大越能看见背后的窗口。0% 是实心卡片；100% 卡片完全透掉，只剩文字和一圈细边。注意这是真透明不是毛玻璃（糊不了桌面背景）；超过 ~50% 后文字直接压在你背后的内容上，遇到花背景会看不清。浅色主题 + 背后是白文档（Word/网页）时看不出区别——白叠白。"
      >
        <input
          className="slider"
          type="range"
          min={0}
          max={100}
          step={5}
          value={draft.captureTransparency ?? 0}
          aria-label="「记一笔」弹窗透明"
          onChange={(e) => patch({ captureTransparency: Number(e.target.value) })}
        />
        <span className={`slider-value ${(draft.captureTransparency ?? 0) > 0 ? "" : "is-off"}`}>
          {(draft.captureTransparency ?? 0) > 0 ? `${draft.captureTransparency}%` : "关"}
        </span>
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
