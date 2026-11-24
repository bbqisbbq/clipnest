import { useCallback, useEffect, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import { ClipService } from "../lib/api";

// 框选截图的遮罩层（Ctrl+Shift+A）。
//
// 后端（internal/clip/service_shot.go）在按快捷键的那一刻就把整个屏幕拍成一张 PNG，
// 窗口只负责"把这张冻结的画面铺满 + 让人在上面拉一个框"，所以：
//   · 背景用 /shot/current（asset handler 直出 PNG，不走 JSON 传几 MB 的 base64）；
//   · 画面是静止的，用户拖框时不会有窗口在后面动，截出来的东西和看到的一致；
//   · 裁切在前端做 —— 只有这里知道 CSS 像素与图片像素的比例
//     （img.naturalWidth / clientWidth）。后端按 DIP 算会差一圈，缩放屏上尤其明显。
//
// 交互：按下拖动 → 松开即截图；Esc 取消；只点一下不拖（小于 8px）也当取消，
// 免得误触存一张 2×3 的图。

type Rect = { x: number; y: number; w: number; h: number };

const MIN_EDGE = 8;

export default function Shot() {
  const [sel, setSel] = useState<Rect | null>(null);
  const [dragging, setDragging] = useState(false);
  const [busy, setBusy] = useState(false);
  const [flash, setFlash] = useState("");
  const startRef = useRef<{ x: number; y: number } | null>(null);
  const imgRef = useRef<HTMLImageElement | null>(null);
  // 每次唤起都换一个 URL，否则 WebView 会拿上一次缓存的那一帧（桌面早就变了）
  const [src, setSrc] = useState(() => `/shot/current?t=${Date.now()}`);

  // 这个窗口随应用一起创建、一直活着：**每次**唤起都要把上一轮的状态清干净
  // （上次的选框、busy、失败提示），并换一帧背景。后端在 Show 之前已经把新帧拍好了，
  // 所以这里换 URL 拿到的就是这一次的画面。
  useEffect(() => {
    const off = Events.On("shot:opened", () => {
      startRef.current = null;
      setSel(null);
      setDragging(false);
      setBusy(false);
      setFlash("");
      setSrc(`/shot/current?t=${Date.now()}`);
    });
    return () => off();
  }, []);

  const cancel = useCallback(() => {
    void ClipService.CancelShot();
  }, []);

  // Esc 取消。窗口是 SetWindowPos 抢来的焦点，按键能到页面里。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        cancel();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [cancel]);

  const pointFromEvent = (e: React.MouseEvent): { x: number; y: number } => ({
    x: e.clientX,
    y: e.clientY,
  });

  const onMouseDown = (e: React.MouseEvent) => {
    if (e.button !== 0 || busy) return;
    const p = pointFromEvent(e);
    startRef.current = p;
    setSel({ x: p.x, y: p.y, w: 0, h: 0 });
    setDragging(true);
  };

  const onMouseMove = (e: React.MouseEvent) => {
    const start = startRef.current;
    if (!dragging || !start) return;
    const p = pointFromEvent(e);
    setSel({
      x: Math.min(start.x, p.x),
      y: Math.min(start.y, p.y),
      w: Math.abs(p.x - start.x),
      h: Math.abs(p.y - start.y),
    });
  };

  const onMouseUp = useCallback(
    async (e: React.MouseEvent) => {
      if (!dragging) return;
      const start = startRef.current;
      startRef.current = null;
      setDragging(false);
      if (!start) return;
      const p = pointFromEvent(e);
      const rect = {
        x: Math.min(start.x, p.x),
        y: Math.min(start.y, p.y),
        w: Math.abs(p.x - start.x),
        h: Math.abs(p.y - start.y),
      };
      setSel(rect);
      if (rect.w < MIN_EDGE || rect.h < MIN_EDGE) {
        cancel();
        return;
      }
      const img = imgRef.current;
      if (!img || !img.complete || img.naturalWidth === 0 || img.clientWidth === 0) {
        // 图还没解码完（或者这张帧根本没拿到）：直接取消，不要拿 0 去做除法。
        void ClipService.LogClient(
          `shot crop aborted: img=${img ? `${img.complete}/${img.naturalWidth}/${img.clientWidth}` : "none"}`,
        );
        cancel();
        return;
      }
      // CSS 像素 → 图片像素：一块屏幕上两者只差一个缩放比，
      // 用 naturalWidth/clientWidth 量出来，比去问系统 DPI 更准（用户自己缩放也一样对）。
      const scaleX = img.naturalWidth / img.clientWidth;
      const scaleY = img.naturalHeight / img.clientHeight;
      const sx = Math.round(rect.x * scaleX);
      const sy = Math.round(rect.y * scaleY);
      const sw = Math.max(1, Math.round(rect.w * scaleX));
      const sh = Math.max(1, Math.round(rect.h * scaleY));

      const canvas = document.createElement("canvas");
      canvas.width = sw;
      canvas.height = sh;
      const ctx = canvas.getContext("2d");
      if (!ctx) {
        cancel();
        return;
      }
      ctx.drawImage(img, sx, sy, sw, sh, 0, 0, sw, sh);
      const dataURL = canvas.toDataURL("image/png");
      // 换算比例是这类功能最容易错的地方（尤其缩放屏），把四个数都记进日志，
      // 出问题时能一眼看出是哪一环不对。
      void ClipService.LogClient(
        `shot crop: rect=${Math.round(rect.x)},${Math.round(rect.y)} ${Math.round(rect.w)}x${Math.round(rect.h)}` +
          ` natural=${img.naturalWidth}x${img.naturalHeight} client=${img.clientWidth}x${img.clientHeight}` +
          ` dpr=${window.devicePixelRatio} crop=${sw}x${sh} src=${img.src.split("?")[0]}`,
      );

      setBusy(true);
      setFlash("正在保存…");
      try {
        await ClipService.SaveShot(dataURL);
        // 后端已经把窗口藏起来了；这里不用再关，等下次唤起前端会重挂。
      } catch (err) {
        setBusy(false);
        setFlash(`保存失败：${String(err)}`);
      }
    },
    [busy, cancel, dragging],
  );

  const label = sel && sel.w >= MIN_EDGE && sel.h >= MIN_EDGE ? `${Math.round(sel.w)} × ${Math.round(sel.h)}` : "";

  return (
    <div
      className="shot-root"
      onMouseDown={onMouseDown}
      onMouseMove={onMouseMove}
      onMouseUp={(e) => void onMouseUp(e)}
      onContextMenu={(e) => {
        e.preventDefault();
        cancel();
      }}
    >
      {/* 冻结的那一帧就是背景：整屏铺满，用户看到的即截到的 */}
      <img ref={imgRef} className="shot-frame" src={src} alt="" draggable={false} />

      {/* 未开始拖拽时压一层半透明黑，提示"这里可以框选" */}
      <div className={`shot-dim ${dragging || (sel && sel.w > 0) ? "is-selecting" : ""}`} />

      {sel && sel.w > 0 && sel.h > 0 && (
        <div
          className={`shot-zone ${sel.y < 26 ? "is-tight-top" : ""}`}
          style={{ left: sel.x, top: sel.y, width: sel.w, height: sel.h }}
        >
          {label && <span className="shot-size">{label}</span>}
        </div>
      )}

      {!dragging && !sel && (
        <div className="shot-hint">
          拖动鼠标框选要截的区域 · <kbd>Esc</kbd> 取消
        </div>
      )}
      {flash && <div className="shot-flash">{flash}</div>}
    </div>
  );
}
