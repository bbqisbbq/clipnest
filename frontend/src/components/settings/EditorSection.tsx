import type { Settings } from "../../lib/api";
import {
  DEFAULT_COMPOSER_TOOLBAR,
  DEFAULT_DETAIL_TOOLBAR,
  TOOLBAR_CATALOG,
  TOOLBAR_DIVIDER,
} from "../../lib/editorToolbar";
import { Row, Section, type SettingsPatch } from "./ui";

// 「编辑器工具栏」分区：决定详情视图 / 底部「记一条」各显示哪些按钮。
//
// 两套分开配是因为它们的用途不同：详情视图是长文编辑（要标题、表格、撤销），
// 底部输入框只有一两百像素高（只放最常用的几个）。分隔线也算一个可选项，
// 自己排分组比我们猜分组更准。

type Props = {
  draft: Settings;
  patch: SettingsPatch;
};

type Target = "editorToolbar" | "composerToolbar";

const TARGETS: { key: Target; title: string; desc: string; fallback: string[] }[] = [
  {
    key: "editorToolbar",
    title: "详情视图",
    desc: "右侧打开的完整编辑器",
    fallback: DEFAULT_DETAIL_TOOLBAR,
  },
  {
    key: "composerToolbar",
    title: "底部「记一条」",
    desc: "主界面下方那个输入卡片",
    fallback: DEFAULT_COMPOSER_TOOLBAR,
  },
];

function currentList(draft: Settings, key: Target, fallback: string[]): string[] {
  const raw = draft[key] as string[] | null | undefined;
  return raw && raw.length > 0 ? raw : fallback;
}

export default function EditorSection({ draft, patch }: Props) {
  const toggle = (key: Target, fallback: string[], id: string) => {
    const list = currentList(draft, key, fallback);
    const next = list.includes(id) ? list.filter((x) => x !== id) : [...list, id];
    patch({ [key]: next } as Partial<Settings>);
  };

  const reset = (key: Target, fallback: string[]) => {
    patch({ [key]: [...fallback] } as Partial<Settings>);
  };

  return (
    <Section
      title="编辑器工具栏"
      desc="挑你想在工具栏上看到的按钮。改完点保存，重新打开编辑器后生效（分隔线可以自己排分组）。"
    >
      {TARGETS.map(({ key, title, desc, fallback }) => {
        const list = currentList(draft, key, fallback);
        return (
          <Row key={key} label={title} tip={desc} stacked>
            <div className="toolbar-picker">
              {TOOLBAR_CATALOG.map((item) => (
                <label key={item.id} className={`toolbar-chip ${list.includes(item.id) ? "is-on" : ""}`}>
                  <input
                    type="checkbox"
                    checked={list.includes(item.id)}
                    onChange={() => toggle(key, fallback, item.id)}
                  />
                  {item.label}
                </label>
              ))}
              <label className={`toolbar-chip ${list.includes(TOOLBAR_DIVIDER) ? "is-on" : ""}`}>
                <input
                  type="checkbox"
                  checked={list.includes(TOOLBAR_DIVIDER)}
                  onChange={() => toggle(key, fallback, TOOLBAR_DIVIDER)}
                />
                分隔线
              </label>
              <button className="link-btn toolbar-reset" onClick={() => reset(key, fallback)}>
                恢复默认
              </button>
            </div>
          </Row>
        );
      })}
    </Section>
  );
}
