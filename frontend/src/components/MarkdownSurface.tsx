import { useCallback, useEffect, useImperativeHandle, useRef, useState, useSyncExternalStore, forwardRef } from "react";
import type Vditor from "vditor";
import { ClipService } from "../lib/api";
import { MEMO_IMG_RE } from "../lib/images";
import { subscribeToolbarSettings, toolbarFor, toolbarVersion } from "../lib/editorToolbar";
import { applyTag, findTagToken, pickTags, type TagToken } from "../lib/tagSuggest";
import TagSuggest from "./TagSuggest";
import { registerTagHost, repaintTags, unregisterTagHost } from "../lib/tagHighlight";
import { registerPrivacyHost, repaintPrivacy, unregisterPrivacyHost } from "../lib/privacyMask";

// 两种上色（#标签 / 隐私遮罩）都要重算，顺序固定：先遮罩再标签 ——
// 标签高亮会跳过被遮住的文字（见 lib/tagHighlight.ts）。
function repaintAll(): void {
  repaintPrivacy();
  repaintTags();
}

// safeFocus 是**唯一**允许调用 Vditor.focus() 的入口。
//
// 为什么需要它：Vditor 的 focus() 读的是内部状态 `this.vditor.currentMode`，而
// `new Vditor(...)` 返回之后、`init()` 真正跑起来之前，`this.vditor` 还是 undefined ——
// 这个窗口期内调 focus() 就是 "Cannot read properties of undefined (reading 'currentMode')"。
// 记一条窗口（Capture.tsx）挂载即抢焦点，正好踩在这个窗口期上，而且时快时慢（热身是否完成、
// StrictMode 双挂载都会影响），所以表现为"偶发一条 ui error 日志"。
// 处理：先试一次，失败就等一帧再试一次；仍失败就放弃（用户点一下编辑器也能拿回焦点），
// 绝不把异常抛给调用方 —— 这里的失败不值得让整个窗口的操作链路中断。
function safeFocus(v: Vditor | null | undefined): void {
  if (!v) return;
  const attempt = () => {
    try {
      v.focus();
      return true;
    } catch {
      return false;
    }
  };
  if (attempt()) return;
  requestAnimationFrame(() => {
    if (!attempt()) {
      requestAnimationFrame(() => void attempt());
    }
  });
}

// 全应用唯一的 Markdown 编辑器：Vditor（IR 即时渲染）。
//
// 为什么只有这一层：应用里「写字」的地方有三个（详情视图 / 底部记一条 /
// 热键快速输入窗），它们要的是同一个引擎、不同的界面。所以差别只在这里的
// preset 上，别处不许直接 import "vditor" —— 换引擎时只改这个文件。
//
// 三个 preset 的差别（其余完全一致）：
//   detail   —— 完整工具栏，撑满区域
//   composer —— 6 项精简工具栏（粗体/斜体/列表/任务/行内码/链接）
//   capture  —— 隐藏工具栏（浮窗里那排按钮由 Capture.tsx 自己画）
//
// 已知取舍：IR 模式下 getValue() 会按 Lute 规范重排 Markdown（表格对齐、
// [x]→[X]、任务行多一个空格）。这是幂等的「一次性整形」，不是每次保存都漂移；
// 设计文档 3.2.2 有实测记录。hash 不变，所以同步配对不受影响。

export type SurfacePreset = "detail" | "composer" | "capture";

export type MarkdownSurfaceHandle = {
  getValue: () => string;
  setValue: (next: string) => void;
  focus: () => void;
  /** 在光标处插入文本（工具条用） */
  insert: (text: string) => void;
  /** 用 before/after 包住当前选区；没有选区就插入一对标记并把光标放中间 */
  wrap: (before: string, after: string) => void;
};

type Props = {
  value: string;
  onChange: (next: string) => void;
  preset: SurfacePreset;
  onSave?: () => void;
  onEscape?: () => void;
  /** 粘贴/拖入图片：收到 data URL，调用方存进附件并返回正文里要写的 token */
  onImageFile?: (dataUrl: string) => Promise<string | null>;
  placeholder?: string;
  autoFocus?: boolean;
};

/** 静态资源一律走本地：离线可用，这是 Vditor 能在桌面端跑的前提。 */
export const VDITOR_CDN = `${import.meta.env.BASE_URL}vditor`;

function currentScheme(): "dark" | "classic" {
  return document.documentElement.dataset.scheme === "light" ? "classic" : "dark";
}

function fileToDataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result ?? ""));
    reader.onerror = () => reject(reader.error ?? new Error("读取图片失败"));
    reader.readAsDataURL(file);
  });
}

// ---- 图片：memoimg: 令牌 ↔ data URL -----------------------------------------
//
// 正文里存的是 `![](memoimg:<hash>)`（内容寻址，见 lib/images.ts）。Vditor 会把
// 这个 src 当成未知协议去加载，于是渲染成一张破图卡片 —— 所以**进编辑器之前**
// 换成真 data URL，**存回去之前**再换回令牌。两个方向都只在 Markdown 文本层做，
// 不碰 DOM，IR 模式的删除/复制/撤销都不会把令牌弄丢。

const tokenToData = new Map<string, string>(); // "memoimg:xxx" -> data URL
const dataToToken = new Map<string, string>(); // data URL -> "memoimg:xxx"
const DATA_URL_RE = /data:image\/[a-zA-Z0-9.+-]+;base64,[A-Za-z0-9+/=]+/g;

/** 把正文里的 memoimg 令牌换成 data URL（缺的去后端取一次，取不到就原样留着）。 */
async function hydrateImages(md: string): Promise<string> {
  const missing = [...new Set([...md.matchAll(MEMO_IMG_RE)].map((m) => m[1]))].filter(
    (hash) => !tokenToData.has(`memoimg:${hash}`),
  );
  for (const hash of missing) {
    try {
      const data = await ClipService.GetImage(hash);
      const token = `memoimg:${hash}`;
      tokenToData.set(token, data);
      dataToToken.set(data, token);
    } catch {
      /* 附件已被清理：留着令牌，至少不会丢信息 */
    }
  }
  return md.replace(MEMO_IMG_RE, (whole) => tokenToData.get(whole) ?? whole);
}

/** 存回去之前把 data URL 还原成令牌；不认识的 data URL 原样保留。 */
function dehydrateImages(md: string): string {
  return md.replace(DATA_URL_RE, (url) => dataToToken.get(url) ?? url);
}

/** 新粘贴/新选择的图片：先登记映射，正文里用 data URL（这样立刻能看见）。 */
function rememberImage(token: string, dataUrl: string): void {
  tokenToData.set(token, dataUrl);
  dataToToken.set(dataUrl, token);
}

// ---- 预热 ----------------------------------------------------------------
// lute 3.6MB，首次构造实例要 250ms 上下（实测），之后再建实例只要 10~19ms。
// 热键窗（capture）本来就随应用一起创建，所以在它空闲时先把引擎烧热，
// 用户按下热键时就不会卡在解析上。

let warmPromise: Promise<void> | null = null;

export function warmUpMarkdown(): Promise<void> {
  if (warmPromise) return warmPromise;
  warmPromise = (async () => {
    try {
      const mod = await import("vditor");
      const V = mod.default;
      const host = document.createElement("div");
      // 放在视口外、不接收任何交互：它只用来把 lute 与 CSS 预热一遍
      host.style.cssText =
        "position:fixed;left:-9999px;top:0;width:120px;height:80px;opacity:0;pointer-events:none;user-select:none";
      host.setAttribute("aria-hidden", "true");
      document.body.appendChild(host);
      await new Promise<void>((resolve) => {
        // 刻意**不销毁**这个实例：Vditor 内部有异步回调（渲染、缓存、hint），
        // 销毁后它们再去读实例属性就是 "Cannot read properties of undefined (reading 'options')"。
        // 留一个 120×80 的隐形实例，代价可以忽略，换来的是不会往控制台/日志里喷错误。
        new V(host, {
          cdn: VDITOR_CDN,
          mode: "ir",
          cache: { enable: false },
          // 预热只要引擎，不要界面：工具栏/大纲全空，省一遍 DOM 与图标
          toolbar: [],
          outline: { enable: false, position: "left" },
          value: "warm",
          after() {
            resolve();
          },
        });
      });
    } catch (err) {
      // 预热失败不该挡住真正的编辑器，它自己会再试一次并报错
      console.error("vditor warm-up failed", err);
      warmPromise = null;
    }
  })();
  return warmPromise;
}

// 插入标签后把光标挪到标签**后面**。
// Vditor.insertValue 会把光标留在插入内容之前，接着打字就打在 `#tag` 前面（实测）。
// 先按"光标正好停在插入文本前"处理（最常见），再兜底找编辑器里最后一次出现。
function caretAfterInserted(host: HTMLElement | null, needle: string): void {
  const sel = window.getSelection();
  if (!host || !sel) return;
  // 落点：标签之后，若紧跟一个空格就跳过它（Vditor 重排后末尾空格可能被拆到
  // 另一个文本节点，所以**只按 `#tag` 找**，别把空格算进查找串 —— 算进去就找不到）。
  const place = (node: Text, idx: number) => {
    const after = idx + needle.length + (node.data[idx + needle.length] === " " ? 1 : 0);
    const r = document.createRange();
    r.setStart(node, Math.min(after, node.data.length));
    r.collapse(true);
    sel.removeAllRanges();
    sel.addRange(r);
  };
  const node = sel.anchorNode;
  if (node && node.nodeType === Node.TEXT_NODE) {
    const t = node as Text;
    const off = sel.anchorOffset;
    if (t.data.startsWith(needle, off)) {
      place(t, off);
      return;
    }
    const here = t.data.lastIndexOf(needle);
    if (here >= 0) {
      place(t, here);
      return;
    }
  }
  const walker = document.createTreeWalker(host, NodeFilter.SHOW_TEXT);
  let found: Text | null = null;
  let at = -1;
  while (walker.nextNode()) {
    const t = walker.currentNode as Text;
    const i = t.data.lastIndexOf(needle);
    if (i >= 0) {
      found = t;
      at = i;
    }
  }
  if (found && at >= 0) place(found, at);
}

// 取编辑器里的 markdown（取不到就当空串：实例可能正在销毁）'
function safeValue(v: Vditor): string {
  try {
    return v.getValue();
  } catch {
    return "";
  }
}

// ---- 组件 ----------------------------------------------------------------

const MarkdownSurface = forwardRef<MarkdownSurfaceHandle, Props>(function MarkdownSurface(props, ref) {
  const { preset } = props;
  const hostRef = useRef<HTMLDivElement>(null);
  const vditorRef = useRef<Vditor | null>(null);
  const [ready, setReady] = useState(false);
  const [failed, setFailed] = useState("");

  // ---- 标签联想（打 `#` 时候选已有标签）----------------------------------
  //
  // 只在打字/移动光标时算一次"光标是不是停在一个 #后面"，命中就弹候选。
  // 候选来源是后端 GetEntryTags()（按使用次数排序），15 秒缓存一次：
  // 每敲一个字都问一次数据库没必要，而刚存下的标签 15 秒内也会进来。
  const [suggest, setSuggest] = useState<{
    items: string[];
    active: number;
    left: number;
    top: number;
  } | null>(null);
  const suggestRef = useRef<typeof suggest>(null);
  suggestRef.current = suggest;
  const tokenRef = useRef<TagToken | null>(null);
  const tagsCache = useRef<{ at: number; list: string[] }>({ at: 0, list: [] });
  const tagsInflight = useRef(false);
  // 输入法正在组词（拼音/五笔还没上屏）：这时候的 Enter/空格/上下键都是给输入法的，
  // 我们插一句话进去只会把候选确认打断；等 compositionend 之后再来联想。
  const composingRef = useRef(false);
  // 插入标签后"把光标放到标签后面"的重试任务号：用户一动键盘/鼠标就作废
  const caretTaskRef = useRef(0);

  // 排查用开关：localStorage["clipnest.debugTagSuggest"] = "1" 时把选区状态写进
  // clipnest.log（不默认开：每次输入都过一次桥太吵）。
  const debugSuggest = useCallback((msg: string) => {
    try {
      if (localStorage.getItem("clipnest.debugTagSuggest") === "1") void ClipService.LogClient(`tag-suggest: ${msg}`);
    } catch {
      /* ignore */
    }
  }, []);

  const refreshSuggest = useCallback(() => {
    if (composingRef.current) {
      tokenRef.current = null;
      setSuggest(null);
      return;
    }
    const token = findTagToken();
    tokenRef.current = token;
    if (!token || !token.rect) {
      setSuggest(null);
      return;
    }
    // 第一次（或缓存过期）先拉一次标签清单，**回来之后重新算一遍**。
    // 不能在异步回调里比 `tokenRef.current !== token`：每敲一个键都会生成新的
    // token 对象，于是异步结果永远被判成"过期"，弹窗一次都不会出现。
    if (!tagsInflight.current && Date.now() - tagsCache.current.at > 5000) {
      tagsInflight.current = true;
      tagsCache.current = { at: Date.now(), list: tagsCache.current.list };
      void ClipService.GetEntryTags()
        .then((list) => {
          tagsCache.current = { at: Date.now(), list: list ?? [] };
          debugSuggest(`tags=${(list ?? []).length}`);
          refreshSuggestRef.current();
        })
        .catch((err) => {
          debugSuggest(`GetEntryTags failed: ${String(err)}`);
        })
        .finally(() => {
          tagsInflight.current = false;
        });
    }

    const items = pickTags(tagsCache.current.list, token.prefix);
    // 只剩"已经打完整"的那一个时不再挡着输入
    if (items.length === 0) {
      setSuggest(null);
      return;
    }
    if (items.length === 1 && items[0].toLowerCase() === token.prefix.toLowerCase()) {
      setSuggest(null);
      return;
    }
    const rect = token.rect;
    const width = 224;
    const left = Math.max(8, Math.min(rect.left, window.innerWidth - width - 8));
    const top = Math.min(rect.bottom + 6, window.innerHeight - 216);
    debugSuggest(`prefix=${JSON.stringify(token.prefix)} n=${items.length} rect=${Math.round(rect.left)},${Math.round(rect.top)},${Math.round(rect.bottom)} vp=${window.innerWidth}x${window.innerHeight} at=${Math.round(left)},${Math.round(top)}`);
    setSuggest({ items, active: 0, left, top });
  }, [debugSuggest]);
  const refreshSuggestRef = useRef(refreshSuggest);
  refreshSuggestRef.current = refreshSuggest;

  const acceptTag = useCallback((tag: string) => {
    const token = tokenRef.current;
    tokenRef.current = null;
    setSuggest(null);
    if (!token) return;
    const v = vditorRef.current;
    if (!v) return;
    // 先把焦点要回来：execCommand 只作用于"当前文档的焦点选区"。点候选时焦点哪怕被
    // mousedown 的 preventDefault 挡着，也可能已经飘走 —— 那样 delete 会删错东西、
    // insertValue 落空（实测把整个草稿清空了）。
    try {
      v.focus();
    } catch {
      /* 实例正在重建：下面那步会失败，下一次输入会自己修正 */
    }
    // 等一帧：focus() 之后浏览器才把选区真正还给编辑器
    requestAnimationFrame(() => {
      const before = safeValue(v);
      try {
        // 走编辑器自己的插入入口（Vditor.insertValue）：工具条的粗体/斜体也用它，
        // 删选区 + 插入 = wrap() 的同一条路径，别再自己发明别的改法。
        applyTag(token, tag, (text) => v.insertValue(text));
      } catch (err) {
        debugSuggest(`accept failed: ${String(err)}`);
      }
      let after = safeValue(v);
      if (!after.includes(`#${tag}`)) {
        // 兜底：DOM 那条路没把改动落进模型（IR 重排、焦点打架都可能），
        // 就在 markdown 文本上直接换掉光标前最近的那一个 `#prefix`。
        const idx = before.lastIndexOf(`#${token.prefix}`);
        if (idx >= 0) {
          const next = `${before.slice(0, idx)}#${tag} ${before.slice(idx + token.prefix.length + 1)}`;
          v.setValue(next, true);
          after = safeValue(v);
          debugSuggest(`accept fallback setValue ok=${after.includes(`#${tag}`)}`);
        } else {
          debugSuggest(`accept fallback: token not found in value`);
        }
      }
      debugSuggest(`accept tag=${tag} before=${JSON.stringify(before.slice(0, 40))} after=${JSON.stringify(after.slice(0, 40))}`);
      // **必须手工同步一次外层状态**：Vditor 的 insertValue 走它自己的模型，不会派发
      // 原生的 input 事件，而我们那条宿主上的 input 监听（草稿落库、脏标记全靠它）
      // 是听 DOM 事件的 —— 漏了这一步，界面看着是 `#writing`，库里草稿却是空的。
      onChangeRef.current(dehydrateImages(after).replace(/\r\n/g, "\n"));
      // 光标挪到标签后面。**要挪两次以上**：Vditor 在我们插入后会自己重排一次 IR，
      // 那一下会把原生选区重置回它记录的旧范围（实测 1.5s 后又跑到 `#tag` 前面）。
      // 所以隔几个时间点再落一次；一旦内容变了（用户已经开始接着打字）就停手。
      const baseline = after;
      const gen = ++caretTaskRef.current;
      const inserted = `#${tag}`;
      for (const delay of [0, 50, 120, 250, 450, 700, 1000, 1500]) {
        window.setTimeout(() => {
          // 期间用户自己动了（打字/点了别处）就作废，绝不跟人抢光标
          if (caretTaskRef.current !== gen || safeValue(v) !== baseline) return;
          caretAfterInserted(hostRef.current, inserted);
        }, delay);
      }
      repaintAll();
      refreshSuggestRef.current();
    });
  }, [debugSuggest]);

  // 回调放进 ref：Vditor 的选项只在初始化时读一次，不能让它们成为重渲染依赖
  const onChangeRef = useRef(props.onChange);
  const onSaveRef = useRef(props.onSave);
  const onEscapeRef = useRef(props.onEscape);
  const onImageFileRef = useRef(props.onImageFile);
  onChangeRef.current = props.onChange;
  onSaveRef.current = props.onSave;
  onEscapeRef.current = props.onEscape;
  onImageFileRef.current = props.onImageFile;

  const initialValue = useRef(props.value);
  // 工具栏配置变了要重建实例（Vditor 只在 new 时读 toolbar），重建前把内容捞出来
  const lastValueRef = useRef<string | null>(null);
  // 工具栏配置是一个外部 store（设置页保存后变），用 useSyncExternalStore 订阅，
  // 比手写 useState + subscribe 少一处竞态（首帧就能读到正确版本）。
  const toolbarRev = useSyncExternalStore(subscribeToolbarSettings, toolbarVersion);

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    let disposed = false;
    let instance: Vditor | null = null;
    // 原生事件监听的解绑（实例真正建好之后才有）
    let cleanupDom: (() => void) | null = null;

    // 换行符归一：Vditor 只认 \n。重建实例时优先用"上一次编辑器里的内容"
    const initial = (lastValueRef.current ?? initialValue.current).replace(/\r\n/g, "\n");

    void (async () => {
      try {
        const mod = await import("vditor");
        if (disposed) return;
        const V = mod.default;
        const theme = currentScheme();
        const hideToolbar = preset === "capture";
        // 谁显示哪些按钮由设置决定（见 lib/editorToolbar.ts）
        const toolbar = toolbarFor(preset);

        // 令牌 → data URL（要在 Lute 解析之前完成，否则会渲染成破图卡片）
        const hydrated = await hydrateImages(initial);
        if (disposed) return;

        instance = new V(host, {
          cdn: VDITOR_CDN,
          mode: "ir",
          theme,
          icon: "ant",
          lang: "zh_CN",
          height: "auto",
          minHeight: 0,
          value: hydrated,
          placeholder: props.placeholder ?? "",
          cache: { enable: false },
          outline: { enable: false, position: "left" },
          preview: {
            theme: { current: theme === "dark" ? "dark" : "light" },
            hljs: { style: theme === "dark" ? "github-dark" : "github", lineNumber: false },
          },
          toolbar,
          // pin=true 会把工具栏做成 fixed 定位，跟我们的 flex 布局打架（实测会盖住正文顶部）
          toolbarConfig: { hide: hideToolbar, pin: false },
          counter: { enable: false },
          tab: "    ",
          upload: {
            handler: async (files: File[]): Promise<string> => {
              const file = files.find((f) => f.type.startsWith("image/"));
              if (!file || !onImageFileRef.current) return "";
              const dataUrl = await fileToDataURL(file);
              const token = await onImageFileRef.current(dataUrl);
              if (!token) return "";
              // 编辑器里直接写 data URL：立刻能看见图；存盘时 dehydrate 会换回令牌
              rememberImage(token, dataUrl);
              return `![](${dataUrl})\n`;
            },
          },
          input: (value: string) => onChangeRef.current(dehydrateImages(value).replace(/\r\n/g, "\n")),
          ctrlEnter: () => onSaveRef.current?.(),
          esc: () => onEscapeRef.current?.(),
          after: () => {
            if (disposed) {
              // StrictMode 下第一次挂载会被立刻卸载：这个实例谁也不该再用
              try {
                instance?.destroy();
              } catch {
                /* 已经销毁 */
              }
              return;
            }
            vditorRef.current = instance;
            setReady(true);
            repaintAll();
            if (props.autoFocus) safeFocus(instance);
          },
        });
        // 只在没被卸载的时候登记句柄：否则 ref 会指向一个已经废掉的实例，
        // 后续 getValue()/setValue() 就会报 "reading 'options'"。
        if (!disposed) vditorRef.current = instance;

        // 原生 input 事件兜底：Vditor 的 options.input 只在它自己的渲染流程里回调，
        // 实测在 IR 模式下手打文字并不会触发（只有 setValue / 清空这类走 processAfterRender
        // 的路径才会）。上层（草稿落库、详情的「未保存」标记）都指望这个回调，
        // 所以直接在宿主上听一次冒泡的 input —— contenteditable 的每一次修改都会冒上来。
        const onDomInput = () => {
          if (disposed) return;
          const v = vditorRef.current;
          if (!v) return;
          try {
            onChangeRef.current(dehydrateImages(v.getValue()).replace(/\r\n/g, "\n"));
          } catch {
            /* 实例正在销毁：取不到内容就跳过，下一次输入会再来 */
          }
          // 每一次真实输入之后：重绘标签高亮 + 看一眼光标是不是刚停在一个 `#后面
          repaintAll();
          refreshSuggestRef.current();
        };
        const onCompositionStart = () => {
          composingRef.current = true;
          tokenRef.current = null;
          setSuggest(null);
        };
        const onCompositionEnd = () => {
          composingRef.current = false;
          refreshSuggestRef.current();
        };
        const onMouseDown = () => {
          caretTaskRef.current++; // 用户点了别处：别再挪光标
        };
        host.addEventListener("mousedown", onMouseDown, true);
        registerTagHost(host);
        registerPrivacyHost(host);
        host.addEventListener("input", onDomInput, true);
        host.addEventListener("blur", onDomInput, true);
        host.addEventListener("compositionstart", onCompositionStart, true);
        host.addEventListener("compositionend", onCompositionEnd, true);
        cleanupDom = () => {
          host.removeEventListener("mousedown", onMouseDown, true);
          unregisterTagHost(host);
          unregisterPrivacyHost(host);
          host.removeEventListener("input", onDomInput, true);
          host.removeEventListener("blur", onDomInput, true);
          host.removeEventListener("compositionstart", onCompositionStart, true);
          host.removeEventListener("compositionend", onCompositionEnd, true);
        };
      } catch (err) {
        console.error("vditor init failed", err);
        setFailed(String(err));
      }
    })();

    return () => {
      disposed = true;
      setReady(false);
      cleanupDom?.();
      try {
        lastValueRef.current = dehydrateImages(instance?.getValue() ?? "");
      } catch {
        /* 还没初始化完 */
      }
      // 先把 ref 置空再去销毁：Vditor 的 destroy() 会把内部 options 置空，
      // 这期间任何 setTheme（主题监听、重建工具栏）都会抛
      // "Cannot read properties of undefined (reading 'options')"。
      vditorRef.current = null;
      try {
        instance?.destroy();
      } catch {
        /* 还没初始化完就卸载 */
      }
    };
    // 挂载时初始化；工具栏配置变了（toolbarRev）就重建一次。value 的外部变化由下面的 effect 同步
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [toolbarRev]);

  // 候选列表的键盘操作。必须**捕获阶段**挂在宿主上：Vditor 自己在内容区
  // 绑了 keydown（Enter 换行、Esc 退回、Ctrl+Enter 保存），冒泡阶段再拦就晚了。
  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    const onKeyDown = (ev: KeyboardEvent) => {
      caretTaskRef.current++; // 用户开始打字：别再挪光标
      // 229 / isComposing：按键属于输入法组词，一律放行（否则中文候选没法用 Enter 确认）
      if (ev.isComposing || ev.keyCode === 229) return;
      const cur = suggestRef.current;
      if (ev.key === "Enter" || ev.key === "Tab" || ev.key === "Escape") {
        debugSuggest(`keydown ${ev.key} open=${!!cur} n=${cur?.items.length ?? -1} token=${!!tokenRef.current}`);
      }
      if (!cur || cur.items.length === 0) return;
      const move = (delta: number) => {
        const next = (cur.active + delta + cur.items.length) % cur.items.length;
        setSuggest({ ...cur, active: next });
      };
      switch (ev.key) {
        case "ArrowDown":
          ev.preventDefault();
          ev.stopPropagation();
          move(1);
          break;
        case "ArrowUp":
          ev.preventDefault();
          ev.stopPropagation();
          move(-1);
          break;
        case "Enter":
        case "Tab": {
          ev.preventDefault();
          ev.stopPropagation();
          const tag = cur.items[cur.active];
          if (tag) acceptTag(tag);
          break;
        }
        case "Escape":
          ev.preventDefault();
          ev.stopPropagation();
          tokenRef.current = null;
          setSuggest(null);
          break;
        default:
          break;
      }
    };
    host.addEventListener("keydown", onKeyDown, true);
    return () => host.removeEventListener("keydown", onKeyDown, true);
  }, [acceptTag, debugSuggest]);

  // 光标移动（点回来、方向键、输入后）都要重算一次：只看 input 事件的话，
  // 「用鼠标点到一个已经写好的 #标签 后面」永远不会弹候选 —— 以前这里先判断
  // "弹窗已开"才继续，等于把这条路径整个关掉了。
  //
  // 隐私模式同理：「可读段落」是跟着光标走的，点进别的段落就得立刻换过来。
  useEffect(() => {
    const onSelection = () => {
      const host = hostRef.current;
      const sel = window.getSelection();
      if (host && sel?.anchorNode && host.contains(sel.anchorNode)) {
        repaintPrivacy();
      }
      if (!host || !sel?.anchorNode || !host.contains(sel.anchorNode)) {
        if (suggestRef.current) setSuggest(null);
        tokenRef.current = null;
        return;
      }
      refreshSuggest();
    };
    document.addEventListener("selectionchange", onSelection);
    return () => document.removeEventListener("selectionchange", onSelection);
  }, [refreshSuggest]);


  useEffect(() => {
    const v = vditorRef.current;
    if (!v || !ready) return;
    const next = props.value.replace(/\r\n/g, "\n");
    // 编辑器里放的是 data URL，外部传进来的是令牌，比较前先还原
    if (dehydrateImages(v.getValue()) === next) return;
    void hydrateImages(next).then((hydrated) => {
      // 卸载后 vditorRef 已被清空，可选链会兜住
      vditorRef.current?.setValue(hydrated, true);
      // setValue 走的是 Vditor 自己的渲染流程，不会触发我们的 DOM input 监听
      window.setTimeout(repaintAll, 0);
    });
  }, [props.value, ready]);

  // 深浅主题切换：Vditor 不跟着 CSS 变量走，得显式 setTheme
  useEffect(() => {
    const root = document.documentElement;
    const apply = () => {
      const v = vditorRef.current;
      if (!v) return;
      const theme = currentScheme();
      try {
        v.setTheme(theme, theme === "dark" ? "dark" : "light");
      } catch {
        // 实例已被销毁（StrictMode 双挂载、或工具栏配置变化重建时的那一下）：
        // Vditor 的 destroy() 之后 options 是 undefined，setTheme 会抛。丢掉即可。
      }
    };
    const mo = new MutationObserver(apply);
    mo.observe(root, { attributes: true, attributeFilter: ["data-scheme", "data-theme"] });
    return () => mo.disconnect();
  }, []);

  useImperativeHandle(
    ref,
    () => ({
      // 对外一律给令牌（memoimg:xxx），data URL 只活在编辑器内部
      getValue: () => dehydrateImages(vditorRef.current?.getValue() ?? props.value),
      setValue: (next: string) => {
        void hydrateImages(next.replace(/\r\n/g, "\n")).then((hydrated) => {
          vditorRef.current?.setValue(hydrated, true);
        });
      },
      focus: () => safeFocus(vditorRef.current),
      insert: (text: string) => vditorRef.current?.insertValue(text),
      wrap: (before: string, after: string) => {
        const v = vditorRef.current;
        if (!v) return;
        const selected = (() => {
          try {
            return v.getSelection();
          } catch {
            return "";
          }
        })();
        // 有选区：先删掉再插入包好的整段（IR 下 insertValue 只会插在选区后）
        if (selected) document.execCommand("delete");
        v.insertValue(before + selected + after);
      },
    }),
    [props.value],
  );

  return (
    <div className={`md-surface md-surface--${preset}`}>
      <div className="md-surface-host" ref={hostRef} />
      {suggest ? (
        <TagSuggest
          items={suggest.items}
          active={suggest.active}
          anchor={{ left: suggest.left, top: suggest.top }}
          onPick={acceptTag}
          onActive={(i) => setSuggest((cur) => (cur ? { ...cur, active: i } : cur))}
        />
      ) : null}
      {!ready && !failed ? <div className="md-surface-skeleton">编辑器加载中…</div> : null}
      {failed ? (
        <div className="md-surface-error">
          编辑器没能加载：{failed}
          <br />
          多半是 vditor 资源没同步，跑一次 <code>npm run sync-vditor</code>
        </div>
      ) : null}
    </div>
  );
});

export default MarkdownSurface;
