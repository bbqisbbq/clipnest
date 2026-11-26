import { useCallback, useEffect, useState } from "react";
import { Events } from "../../lib/runtime";
import type { EmbedProgress, EmbedStatus, Settings } from "../../lib/api";
import { ClipService, EventNames } from "../../lib/api";
import { Row, Section, Switch, type SettingsPatch } from "./ui";

// 「搜索」分区：语义搜索的开关与资源。
//
// 这个功能对用户是**不可见的**——主界面搜索框一点没变，只是搜得更到
// （"云端传不上去"也能找到那条同步失败的笔记）。所以设置里需要一处能解释它、
// 也能关掉它的地方（有人就是要精确匹配）。
//
// 状态不放在 draft 里：取决于本机装了什么、以及向量索引编到哪儿了，
// 跟设置草稿无关，所以单独查（和 OCR 那一节同一个路子）。

type Props = {
  draft: Settings;
  patch: SettingsPatch;
};

export default function SearchSection({ draft, patch }: Props) {
  const [status, setStatus] = useState<EmbedStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [progress, setProgress] = useState<EmbedProgress | null>(null);
  const [error, setError] = useState("");

  const check = useCallback(async () => {
    try {
      setStatus(await ClipService.EmbedStatus());
    } catch (err) {
      setStatus({
        enabled: false,
        ready: false,
        compiled: false,
        done: 0,
        pending: 0,
        failed: 0,
        reason: String(err),
      });
    }
  }, []);

  useEffect(() => {
    void check();
  }, [check]);

  // 下载进度由后端推事件（23MB 在慢网要几十秒，不能让界面干等着）
  useEffect(() => {
    const off = Events.On(EventNames.EmbedProgress, (ev) => {
      const p = ev.data as EmbedProgress | null;
      if (!p) return;
      setProgress(p);
      if (p.stage === "done") {
        setBusy(false);
        void check();
      }
    });
    return () => off();
  }, [check]);

  // 编码队列跑起来之后进度会一直变：装完资源、或者刚启动补扫时，隔几秒刷一次。
  // 只在"有活没干完"的时候轮询，干完就停（不做一个永远开着的定时器）。
  useEffect(() => {
    if (!status?.pending) return;
    const t = window.setInterval(() => void check(), 3000);
    return () => window.clearInterval(t);
  }, [status?.pending, check]);

  const openAssets = async () => {
    setBusy(true);
    try {
      await ClipService.OpenEmbedAssetsDir();
    } finally {
      setBusy(false);
    }
  };

  const download = async () => {
    setBusy(true);
    setError("");
    setProgress({ stage: "download", received: 0, total: 0, text: "正在连接…" });
    try {
      await ClipService.DownloadEmbedAssets();
      await check();
    } catch (err) {
      setError(`下载失败：${String(err)}`);
      setProgress(null);
    } finally {
      setBusy(false);
    }
  };

  const percent =
    progress && progress.total > 0 && progress.stage === "download"
      ? Math.min(100, Math.round((progress.received * 100) / progress.total))
      : null;

  const compiled = status?.compiled ?? true;

  return (
    <Section
      title="搜索"
      desc="除了按字面找，还能按意思找 —— 「记住过的那段 nginx 配置」这种记不清原话的搜索也能命中。全在本机算，不联网。"
    >
      <Row
        label="语义搜索"
        tip="打开后，搜索框会同时走两条路：字面匹配（原来那套）+ 向量相似度，按名次融合排序。实测：一批「记不住原话」的查询里，字面命中 0%，向量能把三分之二的目标排进前五。关掉就是纯字面搜索。"
      >
        <Switch checked={draft.embedSearch ?? true} onChange={(v) => patch({ embedSearch: v })} />
      </Row>

      {draft.embedSearch ?? true ? (
        <Row
          label="语义模型"
          stacked
          tip="bge-small-zh-v1.5（INT8，约 23MB）+ onnxruntime。一键下载会校验 SHA256 后解压到数据目录的 embed/ 下；也可以自己把 onnxruntime.dll、embed_model.onnx、embed_vocab.txt 放进去再点「重新检查」。模型会话不常驻：闲置五分钟后自动释放，下次用到再加载（0.25 秒）。"
        >
          <div className="ocr-stack">
            <div className="ocr-assets">
              <span className={`ocr-status ${status?.ready ? "is-ok" : "is-bad"}`}>
                {status === null
                  ? "正在检查…"
                  : !compiled
                    ? "这个构建没带引擎（需要 Windows + cgo）"
                    : status.ready
                      ? `已就绪 · 已索引 ${status.done} 条${status.pending ? `，还有 ${status.pending} 条在编` : ""}${status.failed ? `，${status.failed} 条失败` : ""}`
                      : status.reason || "资源不全"}
              </span>
              {status?.ready ? null : (
                <button type="button" className="btn btn-primary" onClick={() => void download()} disabled={busy || !compiled}>
                  {busy ? "下载中…" : "一键下载（23 MB）"}
                </button>
              )}
              <button type="button" className="btn btn-ghost" onClick={() => void openAssets()} disabled={busy}>
                打开资源目录
              </button>
              <button type="button" className="btn btn-ghost" onClick={() => void check()} disabled={busy}>
                重新检查
              </button>
            </div>
            {progress && !status?.ready ? (
              <p className="ocr-progress">
                {progress.text}
                {percent !== null ? `（${percent}%）` : ""}
              </p>
            ) : null}
            {error ? <p className="ocr-error">{error}</p> : null}
          </div>
        </Row>
      ) : null}

      <Row
        label="向量留在本机"
        tip="向量是本机派生物：不进同步包、不参与内容哈希。换台机器装上同一个模型会自动重算（每条约 6 毫秒），不需要同步那 5MB。"
      >
        <span className="muted">不随同步包外传</span>
      </Row>
    </Section>
  );
}
