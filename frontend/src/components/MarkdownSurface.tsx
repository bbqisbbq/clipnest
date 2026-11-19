import { useEffect, useImperativeHandle, useRef, useState, forwardRef } from "react";
import type Vditor from "vditor";
import { ClipService } from "../lib/api";
import { MEMO_IMG_RE } from "../lib/images";
import { subscribeToolbarSettings, toolbarFor, toolbarVersion } from "../lib/editorToolbar";

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

// ---- 组件 ----------------------------------------------------------------

const MarkdownSurface = forwardRef<MarkdownSurfaceHandle, Props>(function MarkdownSurface(props, ref) {
  const { preset } = props;
  const hostRef = useRef<HTMLDivElement>(null);
  const vditorRef = useRef<Vditor | null>(null);
  const [ready, setReady] = useState(false);
  const [failed, setFailed] = useState("");

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
  const [toolbarRev, setToolbarRev] = useState<number>(toolbarVersion);
  useEffect(() => subscribeToolbarSettings(() => setToolbarRev(toolbarVersion())), []);

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    let disposed = false;
    let instance: Vditor | null = null;

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
            if (props.autoFocus) instance?.focus();
          },
        });
        // 只在没被卸载的时候登记句柄：否则 ref 会指向一个已经废掉的实例，
        // 后续 getValue()/setValue() 就会报 "reading 'options'"。
        if (!disposed) vditorRef.current = instance;
      } catch (err) {
        console.error("vditor init failed", err);
        setFailed(String(err));
      }
    })();

    return () => {
      disposed = true;
      setReady(false);
      try {
        lastValueRef.current = dehydrateImages(instance?.getValue() ?? "");
      } catch {
        /* 还没初始化完 */
      }
      try {
        instance?.destroy();
      } catch {
        /* 还没初始化完就卸载 */
      }
      vditorRef.current = null;
    };
    // 挂载时初始化；工具栏配置变了（toolbarRev）就重建一次。value 的外部变化由下面的 effect 同步
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [toolbarRev]);

  // 外部换内容（点了另一条条目）→ 覆盖文档，别打断用户输入
  useEffect(() => {
    const v = vditorRef.current;
    if (!v || !ready) return;
    const next = props.value.replace(/\r\n/g, "\n");
    // 编辑器里放的是 data URL，外部传进来的是令牌，比较前先还原
    if (dehydrateImages(v.getValue()) === next) return;
    void hydrateImages(next).then((hydrated) => {
      // 卸载后 vditorRef 已被清空，可选链会兜住
      vditorRef.current?.setValue(hydrated, true);
    });
  }, [props.value, ready]);

  // 深浅主题切换：Vditor 不跟着 CSS 变量走，得显式 setTheme
  useEffect(() => {
    const root = document.documentElement;
    const apply = () => {
      const v = vditorRef.current;
      if (!v) return;
      const theme = currentScheme();
      v.setTheme(theme, theme === "dark" ? "dark" : "light");
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
      focus: () => vditorRef.current?.focus(),
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
