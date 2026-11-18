import { useEffect } from "react";

// 图片查看器：点条目里的图片弹出大图，Esc / 点背景 / 点图片都能关。

type Props = {
  src: string;
  alt?: string;
  onClose: () => void;
};

export default function Lightbox({ src, alt, onClose }: Props) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        onClose();
      }
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [onClose]);

  return (
    <div className="lightbox" onClick={onClose} role="dialog" aria-modal="true">
      <img src={src} alt={alt ?? ""} onClick={onClose} />
      <button className="lightbox-close" onClick={onClose} title="关闭（Esc）">
        ×
      </button>
    </div>
  );
}
