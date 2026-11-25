import { useCallback, useEffect, useState } from "react";
import { Events } from "../../lib/runtime";
import type { OCRProgress, Settings } from "../../lib/api";
import { ClipService, EventNames } from "../../lib/api";
import { Row, Section, type SettingsPatch } from "./ui";

// 「图片文字识别（OCR）」分区。
//
// 两个引擎：系统自带（零依赖、50~500ms、准确率一般）与 PP-OCR（要 42MB 资源、
// 模型加载 0.6 秒、中文与小字明显更准）。资源不是随安装包发的 —— 默认引擎
// 什么都不用下载就能用，愿意换高精度的点「一键下载」（25MB 的 zip，装完 42MB）。
//
// 状态不放在 draft 里：它取决于本机装了什么，跟设置草稿无关，所以单独查。

type Props = {
  draft: Settings;
  patch: SettingsPatch;
};

type Status = { ready: boolean; reason: string };

export default function OcrSection({ draft, patch }: Props) {
  const [status, setStatus] = useState<Status | null>(null);
  const [busy, setBusy] = useState(false);
  const [progress, setProgress] = useState<OCRProgress | null>(null);
  const [error, setError] = useState("");

  const check = useCallback(async () => {
    try {
      const [, ready, reason] = await ClipService.OcrEngineStatus();
      setStatus({ ready: Boolean(ready), reason: reason ?? "" });
    } catch (err) {
      setStatus({ ready: false, reason: String(err) });
    }
  }, []);

  // 切到 PP-OCR 时查一次状态；切回系统引擎就不用查了（那是零依赖的）
  useEffect(() => {
    if (draft.ocrEngine === "ppocr") void check();
    else setStatus(null);
  }, [draft.ocrEngine, check]);

  // 下载进度由后端推事件（40MB 在慢网要几十秒，不能让界面干等着）
  useEffect(() => {
    const off = Events.On(EventNames.OcrProgress, (ev) => {
      const p = ev.data as OCRProgress | null;
      if (!p) return;
      setProgress(p);
      if (p.stage === "done") {
        setBusy(false);
        void check();
      }
    });
    return () => off();
  }, [check]);

  const openAssets = async () => {
    setBusy(true);
    try {
      await ClipService.OpenOcrAssetsDir();
    } finally {
      setBusy(false);
    }
  };

  const download = async () => {
    setBusy(true);
    setError("");
    setProgress({ stage: "download", received: 0, total: 0, text: "正在连接…" });
    try {
      await ClipService.DownloadOCRAssets();
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

  return (
    <Section
      title="图片文字识别"
      desc="把截图里的文字认出来，直接进搜索索引 —— 复制一张截图后，用图里的词就能搜到它。"
    >
      <Row
        label="识别引擎"
        tip="系统自带：Windows.Media.Ocr，离线、无需下载、每张约 0.5 秒。PP-OCR：PaddleOCR 的 PP-OCRv5 模型跑在 onnxruntime 上，中文与小字准确率明显更高，但要额外下载 25MB（装完 42MB）。"
      >
        <select
          className="input is-select"
          value={draft.ocrEngine || "system"}
          onChange={(e) => patch({ ocrEngine: e.target.value })}
        >
          <option value="system">系统自带（无需下载）</option>
          <option value="ppocr">PP-OCR 高精度（需要资源）</option>
        </select>
      </Row>

      {draft.ocrEngine === "ppocr" ? (
        <Row
          label="PP-OCR 资源"
          stacked
          tip="一键下载会校验 SHA256 后解压到数据目录的 ocr/ 下；也可以自己把 onnxruntime.dll、det.onnx、rec.onnx、ppocr_keys.txt 放进去再点「重新检查」。"
        >
          <div className="ocr-stack">
            <div className="ocr-assets">
              <span className={`ocr-status ${status?.ready ? "is-ok" : "is-bad"}`}>
                {status === null ? "正在检查…" : status.ready ? "已就绪" : status.reason || "资源不全"}
              </span>
              {status?.ready ? null : (
                <button type="button" className="btn btn-primary" onClick={() => void download()} disabled={busy}>
                  {busy ? "下载中…" : "一键下载（25 MB）"}
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
        label="识别是本地进行的"
        tip="两条引擎都不联网：图片不出本机，识别结果跟着条目走（也随同步包到其他设备，省得重复算）。"
      >
        <span className="muted">全部在本机完成</span>
      </Row>
    </Section>
  );
}

