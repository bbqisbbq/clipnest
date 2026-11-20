import { useCallback, useState } from "react";
import Composer from "./Composer";
import { ClipService } from "../lib/api";
import type { ComposerDraft } from "../hooks/useComposerDraft";

// 全局常驻的"记一条"输入区。
//
// 为什么它住在 App 里而不是 EntriesView 里：输入框是四屏（全部内容 / 待办 / 设置 /
// 任意筛选器）共用的同一件东西。以前它挂在 EntriesView 内部，于是
//   · 切到待办或设置，整块输入框消失；
//   · 草稿是 EntriesView 的 state，切回来（或任何一次卸载）就被清空。
// 现在草稿在 useComposerDraft（落库），渲染在 App 的最后一格，视图怎么切都同一份。
//
// 分工：Composer.tsx 只管"长什么样、怎么拖高"；这里管"存哪儿、存完通知谁"。

type Props = {
  draft: ComposerDraft;
  /** 存下一条之后：让时间线重载并贴底、刷新侧栏计数 */
  onSaved: () => void;
  onNotify: (message: string) => void;
};

export default function ComposerDock({ draft, onSaved, onNotify }: Props) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const save = useCallback(
    async (content?: string) => {
      // content 由 Composer 从编辑器里直接读（state 可能落后一个 keystroke）
      const text = (content ?? draft.text).trim();
      if (!text) {
        setError("写点什么再保存");
        return;
      }
      setBusy(true);
      setError("");
      try {
        await ClipService.SaveEntry({ content: text, source: "manual", kind: "text" } as never);
        draft.reset();
        onNotify("已记下");
        onSaved();
      } catch (err) {
        setError(String(err));
      } finally {
        setBusy(false);
      }
    },
    [draft, onNotify, onSaved],
  );

  // 粘贴/拖入图片：存成附件，返回正文要写的 token，由编辑器自己插进光标处
  const pasteImage = useCallback(async (dataUrl: string) => {
    try {
      return await ClipService.SaveImage(dataUrl);
    } catch (err) {
      setError(String(err));
      return null;
    }
  }, []);

  return (
    <>
      {/* 重启后从库里读回草稿时说一句：不然用户会以为"我上次写的怎么还在" */}
      {draft.restored && draft.text.trim() ? (
        <p className="snip-hint composer-restored">已恢复上次未保存的草稿</p>
      ) : null}
      <Composer
        value={draft.text}
        onChange={draft.setText}
        onSave={(content) => void save(content)}
        onImageFile={pasteImage}
        busy={busy}
        error={error}
      />
    </>
  );
}
