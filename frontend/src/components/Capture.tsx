import { useCallback, useEffect, useRef, useState } from "react";
import { Events } from "../lib/runtime";
import MarkdownSurface, { type MarkdownSurfaceHandle, warmUpMarkdown } from "./MarkdownSurface";
import PrivacyToggle from "./PrivacyToggle";
import { ClipService, EventNames } from "../lib/api";
import { drag, noDrag } from "../lib/window";
import { useSettingsTheme } from "../hooks/useTheme";
import { useCaptureDraft } from "../hooks/useComposerDraft";

// 快速输入窗（Ctrl+Shift+N）：光标处弹出来，写完 Enter 保存、Esc 关闭。
//
// 几个曾经踩过的点，这里都显式处理：
//   · Esc —— 编辑器会吞按键，必须走它的 esc 回调（onEscape）而不是外层 onKeyDown
//   · 自动聚焦 —— 窗口刚 Show 时还没拿到焦点，要等 window focus 再 focus 编辑器
//   · 预热 —— 这个窗口随应用一起创建，挂载后立刻把 lute 烧热，否则第一次唤起
//     要等 250ms 的引擎初始化（实测数据见 docs/design 3.2.1）
//   · 界面 —— 无边框整块浮层，只有一个「保存」；格式靠 Markdown 语法（- [ ] 就是待办）
//   · 拖动 —— 抬头那条（`.capture-head`）是拖动区（`--wails-draggable: drag`），
//     里面的 × 必须显式 no-drag，否则点它会被当成想拖窗口。改大小不用前端做任何事：
//     窗口去掉 DisableResize 后，Wails 运行时自己把四周边缘变成拉伸热区。
//   · 透明 —— 窗口本身透明（wire.go: BackgroundTypeTransparent），只为了让 10px 圆角
//     外面不垫一块矩形底色。卡片铺多少 alpha 由 Settings.CaptureTransparency 决定
//     （0 = 实心，默认）。注意这是"真透明"而不是毛玻璃：WebView2 糊不了桌面背景。

// 与 Go 侧 clip.MaxCaptureTransparency 同一个数：两边都钳一次，settings.json 被手改
// 或前端算出脏值时都不会越界。100 = 卡片完全透明（只剩文字 + 一圈细边）。
const MAX_TRANSPARENCY = 100;

export default function Capture() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [transparency, setTransparency] = useState(0);
  const editorRef = useRef<MarkdownSurfaceHandle | null>(null);
  // 草稿必须落库（见 hooks/useComposerDraft）：这个窗口闲置就会被销毁（idle.go，
  // 默认 1.5 秒），页面连同 React state 一起没了。以前正文只活在这里的 useState 里，
  // 于是"打了一段字没保存 → 关窗 → 再打开就是空的"。
  const draft = useCaptureDraft(setError);
  const text = draft.text;
  const textRef = useRef(text);
  textRef.current = text;

  // 主题跟着设置走：这个窗随应用一起创建、一直活着，不订阅就永远停在启动时那套配色。
  useSettingsTheme();

  // 卡片底色的 alpha 跟着设置走。这个窗随应用一起创建、一直活着，所以必须听
  // settings:changed——否则在设置页调完得重启才看得到效果。
  useEffect(() => {
    let alive = true;
    const apply = async (value: unknown) => {
      const n = Math.round(Number(value));
      const want = Number.isFinite(n) ? Math.min(MAX_TRANSPARENCY, Math.max(0, n)) : 0;
      // 页面里的半透明像素只有在窗口背后有 DWM backdrop 时才真的与桌面混合，
      // 否则会被合到黑底上（卡片反而比实心更暗）。所以先让后端开关那层 backdrop，
      // 再按"它到底开没开起来"决定卡片透多少——老系统上开不起来就别硬透。
      //
      // 100% 也照样开着：卡片 alpha=0 时露出来的就是这层磨砂玻璃。关掉它不会变成"文字浮在
      // 桌面上"，而是变成一块暗板（WebView2 那层表面本身就是不透明的）——实测白底下
      // 关掉是 rgb(10,11,15)、开着是 rgb(146,146,146)。
      const needBackdrop = want > 0;
      let backdrop = false;
      try {
        backdrop = await ClipService.SetCaptureBackdrop(needBackdrop);
      } catch {
        backdrop = false;
      }
      if (!alive) return;
      // 需要 backdrop 却没开起来 → 退回不透明，别硬透成一块脏色。
      setTransparency(needBackdrop && !backdrop ? 0 : want);
    };
    void ClipService.GetSettings()
      .then((st) => apply(st.captureTransparency))
      .catch(() => undefined);
    const off = Events.On(EventNames.SettingsChanged, (ev) => {
      const st = ev.data as { captureTransparency?: number } | null;
      if (st && st.captureTransparency !== undefined) void apply(st.captureTransparency);
    });
    return () => {
      alive = false;
      off();
    };
  }, []);

  // 挂载即预热：这个窗口在热键按下之前就已经存在了，预热不占用户可感知的时间。
  useEffect(() => {
    void warmUpMarkdown();
  }, []);

  const tags = (() => {
    const found = new Set<string>();
    for (const m of text.matchAll(/#([\p{L}\p{N}_/-]+)/gu)) found.add(m[1]);
    return [...found];
  })();

  const close = useCallback(() => {
    void ClipService.CloseCapture();
  }, []);

  const save = useCallback(async () => {
    // 以编辑器里的真实内容为准（state 可能落后一个 keystroke）
    const content = (editorRef.current?.getValue() ?? textRef.current).trim();
    if (!content) {
      draft.reset();
      close();
      return;
    }
    setBusy(true);
    setError("");
    try {
      await ClipService.SaveEntry({ content, source: "manual", kind: "text" } as never);
      // reset 会先把空值写进库再 flush：否则静默期那一份可能把已经存成条目的
      // 内容又塞回输入框（和主窗口 ComposerDock 的处理一致）
      draft.reset();
      close();
    } catch (err) {
      setError(String(err));
    } finally {
      setBusy(false);
    }
  }, [close, draft]);

  // 窗口显示时编辑器可能还没拿到焦点：补一次，并在窗口重新获得焦点时也补。
  useEffect(() => {
    const focus = () => editorRef.current?.focus();
    focus();
    const t = window.setTimeout(focus, 60);
    window.addEventListener("focus", focus);
    return () => {
      window.clearTimeout(t);
      window.removeEventListener("focus", focus);
    };
  }, []);

  // 卡片底色：0 = 用样式表里的实心 --surface；中间档 = 按比例透；100 = 直接给 transparent
  // （不写成 color-mix(…, 0%, transparent)：那一档在 WebView2 里不生效，卡片仍然是实心的）。
  const cardBackground =
    transparency <= 0
      ? undefined
      : transparency >= MAX_TRANSPARENCY
        ? "transparent"
        : `color-mix(in srgb, var(--surface) ${100 - transparency}%, transparent)`;

  return (
    <div
      className={`capture ${transparency > 0 ? "is-translucent" : ""}`}
      style={cardBackground ? { background: cardBackground } : undefined}
    >
      <div className="capture-head" style={drag}>
        <span className="capture-dot" />
        <span className="capture-title">记一笔</span>
        {tags.length > 0 ? (
          <span className="capture-tags">
            {tags.slice(0, 3).map((t) => (
              <span key={t} className="composer-chip">
                #{t}
              </span>
            ))}
          </span>
        ) : (
          <span className="capture-hint">写 #标签 就能归类</span>
        )}
        <span className="capture-keys">Enter 保存 · Esc 关闭</span>
        <PrivacyToggle className="capture-privacy" />
        <button className="capture-close" style={noDrag} onClick={close} title="关闭（Esc）">
          ×
        </button>
      </div>

      <div className="capture-box">
        <MarkdownSurface
          ref={editorRef}
          preset="capture"
          value={text}
          onChange={draft.setText}
          onSave={() => void save()}
          onEscape={close}
          autoFocus
          placeholder="随手记点什么…（- [ ] 就是待办）"
        />
      </div>

      <div className="capture-foot">
        {/* 从库里读回来的草稿要说一句：不然用户会以为"我上次写的怎么还在"，
            或者反过来，以为自己先前那次保存成功了 */}
        {draft.restored && text.trim() ? (
          <span className="capture-restored">已恢复上次未保存的草稿</span>
        ) : null}
        <div className="capture-foot-right">
          {error ? <span className="snip-error">{error}</span> : null}
          <button className="btn btn-primary btn-sm" onClick={() => void save()} disabled={busy}>
            {busy ? "保存中…" : "保存"}
          </button>
        </div>
      </div>
    </div>
  );
}





