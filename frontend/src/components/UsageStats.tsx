import { useCallback, useEffect, useState } from "react";
import { ClipService, type AssetDownload } from "../lib/api";

// 「有多少人在用」—— 不用服务器、不发遥测：GitHub 自己公布每个安装包的下载次数。
// 局限写在界面上：下载量 ≠ 用户数（自动更新、重复安装都会计入）。

type Props = {
  appVersion: string;
};

export default function UsageStats(_props: Props) {
  const [assets, setAssets] = useState<AssetDownload[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const list = await ClipService.GetDownloadStats();
      setAssets(list ?? []);
    } catch (err) {
      setError(String(err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const total = (assets ?? []).reduce((sum, a) => sum + (a.downloads ?? 0), 0);
  const rows = [...(assets ?? [])].sort((a, b) =>
    a.release === b.release ? b.downloads - a.downloads : b.release.localeCompare(a.release),
  );

  return (
    <div className="usage-stats">
      <div className="usage-head">
        <div>
          <b>{total}</b>
          <span>累计下载次数</span>
        </div>
        <button className="btn btn-ghost btn-sm" onClick={load} disabled={loading}>
          {loading ? "读取中…" : "刷新"}
        </button>
      </div>

      {error ? <p className="snip-error">{error}</p> : null}

      {rows.length > 0 ? (
        <table className="usage-table">
          <thead>
            <tr>
              <th>版本</th>
              <th>安装包</th>
              <th className="num">下载</th>
              <th>发布</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((a) => (
              <tr key={`${a.release}-${a.name}`}>
                <td>{a.release}</td>
                <td className="name">{a.name}</td>
                <td className="num">{a.downloads}</td>
                <td className="dim">{a.published}</td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : !loading && !error ? (
        <p className="snip-hint">还没有下载记录（或 GitHub API 暂时限流，稍后再刷）。</p>
      ) : null}

      <p className="snip-hint">
        数据来自 GitHub Releases 的公开计数，本地读取、不上传任何东西。
        <br />
        想看「真实活跃人数」（去重的设备数、日活/月活），只能靠匿名心跳，那需要一个收集端
        —— 已经评估过，你需要自己跑一个服务；现在这版没有启用，也没有任何后台请求。
      </p>
    </div>
  );
}
