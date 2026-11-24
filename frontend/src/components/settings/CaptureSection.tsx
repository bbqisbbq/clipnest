import type { Settings } from "../../lib/api";
import { Row, Section, Switch, type SettingsPatch } from "./ui";

// 「采集」分区：什么内容会被记录、单条文本长度上限。
// 状态全部来自父组件的 draft，本组件没有自己的 state。

type Props = {
  draft: Settings;
  patch: SettingsPatch;
};

export default function CaptureSection({ draft, patch }: Props) {
  return (
    <Section title="采集" desc="控制记录什么；关闭总开关后程序仍在托盘运行，只是不再记录。">
      <Row label="自动收集剪贴板" tip="关闭后不会记录任何新内容，历史记录保持不变。">
        <Switch checked={draft.capture} onChange={(v) => patch({ capture: v })} />
      </Row>
      <Row label="文本" tip="包括纯文本与单行内容；单独的网址会自动归类为「链接」。">
        <Switch checked={draft.captureText} onChange={(v) => patch({ captureText: v })} />
      </Row>
      <Row label="链接" tip="从浏览器地址栏或 text/uri-list 复制出来的网址。">
        <Switch checked={draft.captureLink} onChange={(v) => patch({ captureLink: v })} />
      </Row>
      <Row label="图片" tip="支持 PNG、CF_DIB、CF_DIBV5 三种来源，截图工具与浏览器复制的图片都能收录。">
        <Switch checked={draft.captureImage} onChange={(v) => patch({ captureImage: v })} />
      </Row>
      <Row
        label="网页图片自动下载"
        tip="浏览器「复制图片」时经常只把 <img src> 塞进剪贴板、不给位图。开着就去把那张图下载回来存成图片条目（要联网，单张上限 24 MB，超时 8 秒）；关掉则只记一条带图片地址的富文本。"
      >
        <Switch checked={draft.captureRemoteImage} onChange={(v) => patch({ captureRemoteImage: v })} />
      </Row>
      <Row label="文件（仅路径引用）" tip="只记录路径与元信息，不复制实体文件；原文件被移动或删除后会标记为失效。">
        <Switch checked={draft.captureFiles} onChange={(v) => patch({ captureFiles: v })} />
      </Row>
      <Row label="富文本" tip="HTML 片段或 RTF（Word、WordPad、Outlook 等），粘贴回去时保留格式。">
        <Switch checked={draft.captureRich} onChange={(v) => patch({ captureRich: v })} />
      </Row>
      <Row label="其它格式" tip="Excel 区间、颜色、音频等私有格式，按原始字节保存（单条上限 1 MiB），可原样粘回。">
        <Switch checked={draft.captureCustom} onChange={(v) => patch({ captureCustom: v })} />
      </Row>
      <Row label="单条文本长度上限" tip="超过该长度的文本会被跳过，0 表示不限制。">
        <input
          className="input is-num"
          type="number"
          min={0}
          step={1000}
          value={draft.maxTextLength}
          onChange={(e) => patch({ maxTextLength: Number(e.target.value) || 0 })}
        />
        <span className="unit">字符</span>
      </Row>
    </Section>
  );
}
