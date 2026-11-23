import { IconEye, IconEyeOff } from "./Icons";
import { togglePrivacyMode, usePrivacyMode } from "../lib/privacyMode";
import { noDrag } from "../lib/window";

// 隐私模式的小开关：三个写字的地方（详情 / 记一条 / 热键快速输入窗）各放一个，
// 共用同一份状态（lib/privacyMode.ts）。
//
// 语义一句话：打开后**只有光标所在的段落可读**，其余段落在视觉上融进背景。
// 纯视觉，不影响保存、不影响已保存的内容。

type Props = {
  className?: string;
  size?: number;
};

export default function PrivacyToggle(props: Props) {
  const on = usePrivacyMode();
  return (
    <button
      type="button"
      className={`privacy-toggle ${on ? "is-on" : ""} ${props.className ?? ""}`.trim()}
      onClick={(ev) => {
        ev.preventDefault();
        ev.stopPropagation();
        void togglePrivacyMode();
      }}
      // 拖动区里的按钮必须显式声明不参与拖动（Capture 的抬头条就是拖动区）
      style={noDrag}
      title={
        on
          ? "隐私模式：已开启（只有光标所在段落可读）。点击关闭"
          : "隐私模式：只有光标所在的段落保持可读，其余段落融进背景。纯视觉，不影响保存"
      }
      aria-pressed={on}
      aria-label="隐私模式"
    >
      {on ? <IconEyeOff size={props.size ?? 14} /> : <IconEye size={props.size ?? 14} />}
    </button>
  );
}
