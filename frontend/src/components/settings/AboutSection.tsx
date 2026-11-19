import type { AppInfo } from "../../lib/api";
import { formatBytes } from "../../lib/format";
import { IconFolder, IconTrash } from "../Icons";
import { Section } from "./ui";

// 「数据与关于」分区：版本 / 占用 / 数据目录，以及清空历史、打开目录、退出。
// 这里只读 App 传来的元信息，不碰设置草稿，所以没有 patch。

type Props = {
  appInfo: AppInfo | null;
  storageBytes: number;
  onClearHistory: () => void;
  onOpenDataDir: () => void;
  onQuit: () => void;
};

export default function AboutSection({
  appInfo,
  storageBytes,
  onClearHistory,
  onOpenDataDir,
  onQuit,
}: Props) {
  return (
    <Section title="数据与关于" desc="所有数据都在本机（SQLite 数据库），不同步时不会产生任何网络请求。">
      <dl className="kv is-wide">
        <div>
          <dt>版本</dt>
          <dd>v{appInfo?.version ?? "0.1.0"}</dd>
        </div>
        <div>
          <dt>数据占用</dt>
          <dd>{formatBytes(storageBytes)}</dd>
        </div>
        <div className="span-2">
          <dt>数据目录</dt>
          <dd className="mono">{appInfo?.dataDir ?? "-"}</dd>
        </div>
      </dl>
      <div className="card-actions">
        <button className="btn btn-ghost" onClick={onOpenDataDir}>
          <IconFolder size={14} />
          打开数据目录
        </button>
        <button className="btn btn-danger-ghost" onClick={onClearHistory}>
          <IconTrash size={14} />
          清空未收藏的历史
        </button>
        <button className="btn btn-danger-ghost" onClick={onQuit}>
          退出 ClipNest
        </button>
      </div>
    </Section>
  );
}
