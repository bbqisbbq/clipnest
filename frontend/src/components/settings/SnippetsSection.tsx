import type { Settings } from "../../lib/api";
import { Row, Section, Switch, type SettingsPatch } from "./ui";

// 「片段与缩写」分区：片段在快速粘贴窗里的可见性、全局缩写展开与触发字符。

type Props = {
  draft: Settings;
  patch: SettingsPatch;
};

export default function SnippetsSection({ draft, patch }: Props) {
  return (
    <Section title="片段与缩写" desc="片段是「经常要用的内容」；缩写让你在任意输入框里直接把它敲出来。">
      <Row label="弹窗里显示片段" tip="在快速粘贴列表里输入 -- 切换到片段模式（和 eTool 的 -- 搜索一致），回车按类型执行：文本/图片粘贴、网址打开、程序启动、命令执行。">
        <Switch checked={draft.snippetsInPicker} onChange={(v) => patch({ snippetsInPicker: v })} />
      </Row>
      <Row
        label="缩写自动展开"
        tip={
          `打开后键入「缩写 + 后缀」立即替换成片段内容，例如 addr- → 办公地址。这是一个全局键盘钩子：只在普通权限的窗口生效，提权窗口和部分游戏不生效；关掉开关立刻卸载。`
        }
      >
        <Switch checked={draft.expandEnabled} onChange={(v) => patch({ expandEnabled: v })} />
      </Row>
      <Row label="触发字符" tip="终止符：缩写后面打这些字符中的任意一个就展开（空格、回车、Tab、逗号、句号等）。默认包含空格，所以打完 addr 按空格即可。">
        <input
          className="input is-trigger"
          value={draft.expandEndChars}
          placeholder="<space> \\t,.;:!?)]}"
          onChange={(e) => patch({ expandEndChars: e.target.value })}
        />
      </Row>
    </Section>
  );
}
