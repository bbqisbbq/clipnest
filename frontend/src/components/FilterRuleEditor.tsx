import type { FilterCondition, FilterFieldSpec, FilterGroup } from "../lib/api";

// 规则编辑器：组 + 条件行，最多一层嵌套。
//
// 字段与算子都从后端 GetFilterFields() 来（唯一白名单），这里只负责把它们
// 画成下拉框 —— UI 不许自己发明字段，否则编译期会把它们悄悄丢掉。

const OP_LABEL: Record<string, string> = {
  is: "是",
  isNot: "不是",
  anyOf: "是其中任一",
  has: "有",
  hasNot: "没有",
  contains: "包含",
  notContains: "不包含",
  newerThan: "晚于（天）",
  olderThan: "早于（天）",
};

/** 有固定取值的字段给下拉框，其余给输入框 */
const VALUE_OPTIONS: Record<string, { value: string; label: string }[]> = {
  todo: [
    { value: "open", label: "有未完成" },
    { value: "done", label: "全部完成" },
    { value: "none", label: "没有待办" },
  ],
  pinned: [
    { value: "true", label: "是" },
    { value: "false", label: "否" },
  ],
  archived: [
    { value: "true", label: "是" },
    { value: "false", label: "否" },
  ],
  source: [
    { value: "manual", label: "手写" },
    { value: "clipboard", label: "剪贴板采集" },
    { value: "import", label: "导入" },
  ],
  kind: [
    { value: "text", label: "文本" },
    { value: "link", label: "链接" },
    { value: "image", label: "图片" },
    { value: "files", label: "文件引用" },
    { value: "rich", label: "富文本" },
    { value: "custom", label: "自定义格式" },
  ],
  action: [
    { value: "clipboard", label: "在粘贴列表里" },
    { value: "snippet", label: "是片段" },
  ],
};

const PLACEHOLDER: Record<string, string> = {
  tag: "标签名，多个用逗号分隔",
  text: "关键词",
  ageDays: "天数，例如 30",
  day: "2026-11-19",
};

export const GROUP_OP_LABEL: Record<string, string> = {
  and: "满足全部",
  or: "满足任一",
  not: "都不是",
};

type Props = {
  group: FilterGroup;
  fields: FilterFieldSpec[];
  onChange: (next: FilterGroup) => void;
  /** 内置筛选器：规则不可改（它们的 query 是代码里的扁平字段） */
  disabled?: boolean;
  /** 子组只允许一层：depth=1 时不再给"添加组" */
  depth?: number;
};

function defaultOp(field?: string): string {
  const spec = field ? undefined : undefined;
  void spec;
  return "is";
}

function emptyCondition(fields: FilterFieldSpec[]): FilterCondition {
  const first = fields[0]?.field ?? "tag";
  return { field: first, op: fields[0]?.ops?.[0] ?? defaultOp(), value: "" };
}

export default function FilterRuleEditor({ group, fields, onChange, disabled, depth = 0 }: Props) {
  const conditions = group.conditions ?? [];
  const groups = group.groups ?? [];

  const setConditions = (next: FilterCondition[]) => onChange({ ...group, conditions: next });
  const setGroups = (next: FilterGroup[]) => onChange({ ...group, groups: next });

  const patchCondition = (i: number, patch: Partial<FilterCondition>) => {
    const next = conditions.map((c, idx) => (idx === i ? { ...c, ...patch } : c));
    setConditions(next);
  };

  const changeField = (i: number, field: string) => {
    const spec = fields.find((f) => f.field === field);
    // 换字段时把算子与取值一起重置，避免出现「文字 是 true」这种组合
    patchCondition(i, { field, op: spec?.ops?.[0] ?? "is", value: "" });
  };

  return (
    <div className={`rule-editor ${disabled ? "is-disabled" : ""}`}>
      <div className="rule-op">
        <select
          className="rule-select"
          value={group.op || "and"}
          onChange={(e) => onChange({ ...group, op: e.target.value })}
          disabled={disabled}
        >
          {Object.entries(GROUP_OP_LABEL).map(([value, label]) => (
            <option key={value} value={value}>
              {label}
            </option>
          ))}
        </select>
        <span className="rule-op-hint">
          {group.op === "not" ? "以下条件全部不成立" : "以下条件之间"}
        </span>
      </div>

      {conditions.map((c, i) => {
        const spec = fields.find((f) => f.field === c.field);
        const ops = spec?.ops ?? ["is"];
        const options = VALUE_OPTIONS[c.field];
        return (
          <div className="rule-row" key={`c${i}`}>
            <select
              className="rule-select"
              value={c.field}
              onChange={(e) => changeField(i, e.target.value)}
              disabled={disabled}
            >
              {fields.map((f) => (
                <option key={f.field} value={f.field}>
                  {f.label}
                </option>
              ))}
            </select>
            <select
              className="rule-select is-narrow"
              value={c.op}
              onChange={(e) => patchCondition(i, { op: e.target.value })}
              disabled={disabled}
            >
              {ops.map((op) => (
                <option key={op} value={op}>
                  {OP_LABEL[op] ?? op}
                </option>
              ))}
            </select>
            {options ? (
              <select
                className="rule-select"
                value={c.value || options[0].value}
                onChange={(e) => patchCondition(i, { value: e.target.value })}
                disabled={disabled}
              >
                {options.map((o) => (
                  <option key={o.value} value={o.value}>
                    {o.label}
                  </option>
                ))}
              </select>
            ) : (
              <input
                className="input rule-input"
                value={c.value}
                placeholder={PLACEHOLDER[c.field] ?? ""}
                onChange={(e) => patchCondition(i, { value: e.target.value })}
                disabled={disabled}
              />
            )}
            {!disabled ? (
              <button
                className="rule-remove"
                title="删除这个条件"
                onClick={() => setConditions(conditions.filter((_, idx) => idx !== i))}
              >
                ×
              </button>
            ) : null}
          </div>
        );
      })}

      {groups.map((sub, i) => (
        <div className="rule-subgroup" key={`g${i}`}>
          <div className="rule-subgroup-head">
            <span>子组 {i + 1}</span>
            {!disabled ? (
              <button
                className="rule-remove"
                title="删除这个子组"
                onClick={() => setGroups(groups.filter((_, idx) => idx !== i))}
              >
                ×
              </button>
            ) : null}
          </div>
          <FilterRuleEditor
            group={sub}
            fields={fields}
            disabled={disabled}
            depth={depth + 1}
            onChange={(next) => setGroups(groups.map((g, idx) => (idx === i ? next : g)))}
          />
        </div>
      ))}

      {!disabled ? (
        <div className="rule-add">
          <button className="link-btn" onClick={() => setConditions([...conditions, emptyCondition(fields)])}>
            + 添加条件
          </button>
          {depth === 0 ? (
            <button
              className="link-btn"
              onClick={() =>
                setGroups([...groups, { op: "or", conditions: [emptyCondition(fields)] }])
              }
              title="子组用来表达「(A 且 B) 或 (C 且 D)」这种组合"
            >
              + 添加子组
            </button>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
