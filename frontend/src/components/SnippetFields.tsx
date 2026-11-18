import { useEffect, useState } from "react";
import { ClipService, type Entry } from "../lib/api";

// 片段的额外编辑项。统一类型下，片段没有自己的页面，
// 但它的"编辑入口"确实和手写备忘不同——这里就是那个差异的落点。

type Props = {
  entry: Entry;
  onSaved: () => void;
  onNotify: (msg: string) => void;
};

export default function SnippetFields({ entry, onSaved, onNotify }: Props) {
  const [trigger, setTrigger] = useState(entry.triggerKey ?? "");
  const [caseSensitive, setCaseSensitive] = useState(!!entry.caseSensitive);
  const [keepTrigger, setKeepTrigger] = useState(!!entry.keepTrigger);
  const [apps, setApps] = useState((entry.apps ?? []).join(", "));
  const [excludeApps, setExcludeApps] = useState((entry.excludeApps ?? []).join(", "));
  const [group, setGroup] = useState(entry.groupName ?? "");
  const [busy, setBusy] = useState(false);

  // 切换到另一条时重置表单
  useEffect(() => {
    setTrigger(entry.triggerKey ?? "");
    setCaseSensitive(!!entry.caseSensitive);
    setKeepTrigger(!!entry.keepTrigger);
    setApps((entry.apps ?? []).join(", "));
    setExcludeApps((entry.excludeApps ?? []).join(", "));
    setGroup(entry.groupName ?? "");
  }, [entry.id, entry.triggerKey, entry.caseSensitive, entry.keepTrigger, entry.apps, entry.excludeApps, entry.groupName]);

  const save = async () => {
    setBusy(true);
    try {
      await ClipService.SaveEntry({
        ...entry,
        triggerKey: trigger.trim(),
        caseSensitive,
        keepTrigger,
        apps: splitCsv(apps),
        excludeApps: splitCsv(excludeApps),
        groupName: group.trim(),
      } as never);
      onNotify("片段设置已保存");
      onSaved();
    } catch (err) {
      onNotify(`保存失败：${String(err)}`);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="snippet-fields">
      <div className="snippet-fields-row">
        <label className="sf-label" title="在任意输入框里打这个缩写，就会展开成这条内容">
          缩写
        </label>
        <input
          className="input is-mono sf-trigger"
          placeholder="例如 addr-"
          value={trigger}
          onChange={(e) => setTrigger(e.target.value)}
        />
        <label className="sf-check" title="区分大小写时，Addr- 和 addr- 算两个">
          <input
            type="checkbox"
            checked={caseSensitive}
            onChange={(e) => setCaseSensitive(e.target.checked)}
          />
          区分大小写
        </label>
        <label className="sf-check" title="展开后是否保留你打的缩写本身">
          <input
            type="checkbox"
            checked={keepTrigger}
            onChange={(e) => setKeepTrigger(e.target.checked)}
          />
          保留缩写
        </label>
        <button className="btn btn-primary btn-sm" onClick={save} disabled={busy}>
          {busy ? "保存中…" : "保存片段设置"}
        </button>
      </div>

      <div className="snippet-fields-row">
        <label className="sf-label" title="只在列出的程序里展开（留空 = 全部）">
          仅在此
        </label>
        <input
          className="input sf-wide"
          placeholder="wechat.exe, chrome.exe"
          value={apps}
          onChange={(e) => setApps(e.target.value)}
        />
        <label className="sf-label" title="在这些程序里不展开">
          排除
        </label>
        <input
          className="input sf-wide"
          placeholder="keepass.exe"
          value={excludeApps}
          onChange={(e) => setExcludeApps(e.target.value)}
        />
        <label className="sf-label">分组</label>
        <input
          className="input sf-group"
          placeholder="例如 话术"
          value={group}
          onChange={(e) => setGroup(e.target.value)}
        />
      </div>
    </div>
  );
}

function splitCsv(text: string): string[] {
  return text
    .split(/[,，\s]+/)
    .map((s) => s.trim())
    .filter(Boolean);
}
