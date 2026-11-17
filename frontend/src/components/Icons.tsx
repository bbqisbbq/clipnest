import type { JSX } from "react";

type IconProps = { size?: number; className?: string };

function svg(paths: JSX.Element, { size = 16, className }: IconProps) {
  return (
    <svg
      className={className}
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {paths}
    </svg>
  );
}

export const IconText = (p: IconProps) =>
  svg(
    <>
      <path d="M4 6h16" />
      <path d="M4 12h12" />
      <path d="M4 18h8" />
    </>,
    p,
  );

export const IconLink = (p: IconProps) =>
  svg(
    <>
      <path d="M10 13a5 5 0 0 0 7.07 0l1.4-1.4a5 5 0 0 0-7.07-7.07L10.6 5.3" />
      <path d="M14 11a5 5 0 0 0-7.07 0l-1.4 1.4a5 5 0 0 0 7.07 7.07l.8-.8" />
    </>,
    p,
  );

export const IconImage = (p: IconProps) =>
  svg(
    <>
      <rect x="3" y="4" width="18" height="16" rx="2.5" />
      <circle cx="9" cy="10" r="1.6" />
      <path d="m4 18 5-5 4 4 3-2.5 4 3.5" />
    </>,
    p,
  );

export const IconFiles = (p: IconProps) =>
  svg(
    <>
      <path d="M4 7a2 2 0 0 1 2-2h4l2 2.5h6a2 2 0 0 1 2 2V18a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2z" />
      <path d="M4 11h16" />
    </>,
    p,
  );

export const IconRich = (p: IconProps) =>
  svg(
    <>
      <path d="M5 5h14v14H5z" strokeDasharray="3 3" />
      <path d="M9 9h6M9 13h4" />
    </>,
    p,
  );

export const IconStar = ({ filled, ...p }: IconProps & { filled?: boolean }) => (
  <svg
    className={p.className}
    width={p.size ?? 16}
    height={p.size ?? 16}
    viewBox="0 0 24 24"
    fill={filled ? "currentColor" : "none"}
    stroke="currentColor"
    strokeWidth="1.6"
    strokeLinecap="round"
    strokeLinejoin="round"
    aria-hidden="true"
  >
    <path d="m12 3.6 2.6 5.4 5.9.8-4.3 4.1 1 5.9-5.2-2.8-5.2 2.8 1-5.9L3.5 9.8l5.9-.8z" />
  </svg>
);

export const IconTrash = (p: IconProps) =>
  svg(
    <>
      <path d="M4 7h16" />
      <path d="M9 7V5a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2" />
      <path d="M6 7l1 12a2 2 0 0 0 2 2h6a2 2 0 0 0 2-2l1-12" />
      <path d="M10 11v6M14 11v6" />
    </>,
    p,
  );

export const IconCopy = (p: IconProps) =>
  svg(
    <>
      <rect x="9" y="9" width="11" height="11" rx="2" />
      <path d="M15 9V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v7a2 2 0 0 0 2 2h3" />
    </>,
    p,
  );

export const IconSearch = (p: IconProps) =>
  svg(
    <>
      <circle cx="11" cy="11" r="6.5" />
      <path d="m16 16 4 4" />
    </>,
    p,
  );

export const IconSettings = (p: IconProps) =>
  svg(
    <>
      <circle cx="12" cy="12" r="3" />
      <path d="M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-2.9 1.2 2 2 0 1 1-4 0 1.7 1.7 0 0 0-2.9-1.2l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1A1.7 1.7 0 0 0 3 15a2 2 0 1 1 0-4 1.7 1.7 0 0 0 1.2-2.9l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1A1.7 1.7 0 0 0 10 4.6a2 2 0 1 1 4 0 1.7 1.7 0 0 0 2.9 1.2l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1A1.7 1.7 0 0 0 21 11a2 2 0 1 1 0 4z" />
    </>,
    p,
  );

export const IconClose = (p: IconProps) =>
  svg(
    <>
      <path d="M6 6l12 12M18 6 6 18" />
    </>,
    p,
  );

export const IconExternal = (p: IconProps) =>
  svg(
    <>
      <path d="M14 5h5v5" />
      <path d="M19 5l-7 7" />
      <path d="M18 14v4a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4" />
    </>,
    p,
  );

export const IconFolder = (p: IconProps) =>
  svg(
    <>
      <path d="M3 8a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" />
    </>,
    p,
  );

export const IconAll = (p: IconProps) =>
  svg(
    <>
      <rect x="3.5" y="3.5" width="7" height="7" rx="1.6" />
      <rect x="13.5" y="3.5" width="7" height="7" rx="1.6" />
      <rect x="3.5" y="13.5" width="7" height="7" rx="1.6" />
      <rect x="13.5" y="13.5" width="7" height="7" rx="1.6" />
    </>,
    p,
  );

export const IconPin = ({ filled, ...p }: IconProps & { filled?: boolean }) => (
  <svg
    className={p.className}
    width={p.size ?? 16}
    height={p.size ?? 16}
    viewBox="0 0 24 24"
    fill={filled ? "currentColor" : "none"}
    stroke="currentColor"
    strokeWidth="1.6"
    strokeLinecap="round"
    strokeLinejoin="round"
    aria-hidden="true"
  >
    <path d="M9 4h6l-1 6 3.5 3.5H5.5L9 10z" />
    <path d="M12 13.5V20" />
  </svg>
);

export const IconPause = (p: IconProps) =>
  svg(
    <>
      <rect x="7" y="5" width="3.5" height="14" rx="1.2" />
      <rect x="13.5" y="5" width="3.5" height="14" rx="1.2" />
    </>,
    p,
  );

export const IconPlay = (p: IconProps) =>
  svg(<path d="M8 5.5v13l11-6.5z" />, p);

export const IconOther = (p: IconProps) =>
  svg(
    <>
      <path d="M12 3.5 20 8v8l-8 4.5L4 16V8z" />
      <path d="M12 12v8.5M12 12 4 8M12 12l8-4" />
    </>,
    p,
  );

export const IconSync = (p: IconProps) =>
  svg(
    <>
      <path d="M20 12a8 8 0 0 1-13.7 5.6L4 15.4" />
      <path d="M4 12A8 8 0 0 1 17.7 6.4L20 8.6" />
      <path d="M20 4v4.6h-4.6M4 20v-4.6h4.6" />
    </>,
    p,
  );

export const kindIcon = (kind: string) => {
  switch (kind) {
    case "link":
      return IconLink;
    case "image":
      return IconImage;
    case "files":
      return IconFiles;
    case "rich":
      return IconRich;
    case "custom":
      return IconOther;
    default:
      return IconText;
  }
};
