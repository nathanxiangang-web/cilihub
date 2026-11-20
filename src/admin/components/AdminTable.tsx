import type { ReactNode } from "react";

export type AdminColumn<T> = {
  key: string;
  title: ReactNode;
  width?: string;
  align?: "left" | "right" | "center";
  render: (row: T) => ReactNode;
};

export type TableSelection<T> = {
  selected: Set<number>;
  getId: (row: T) => number;
  onToggle: (id: number) => void;
  onToggleAll: (checked: boolean) => void;
  allChecked: boolean;
};

export function AdminTable<T>({
  columns,
  rows,
  rowKey,
  empty = "暂无数据",
  rowClassName,
  onRowClick,
  selection,
}: {
  columns: AdminColumn<T>[];
  rows: T[];
  rowKey: (row: T, index: number) => string | number;
  empty?: string;
  rowClassName?: (row: T) => string;
  onRowClick?: (row: T) => void;
  selection?: TableSelection<T>;
}) {
  if (!rows.length) return <div className="cs-empty">{empty}</div>;
  return (
    <div className="cs-table-wrap">
      <table className="cs-table">
        <thead>
          <tr>
            {selection ? (
              <th className="cs-table__check">
                <input
                  type="checkbox"
                  checked={selection.allChecked}
                  onChange={(e) => selection.onToggleAll(e.target.checked)}
                />
              </th>
            ) : null}
            {columns.map((col) => (
              <th
                key={col.key}
                style={{ width: col.width, textAlign: col.align ?? "left" }}
              >
                {col.title}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, index) => (
            <tr
              key={rowKey(row, index)}
              className={`${onRowClick ? "is-clickable " : ""}${rowClassName?.(row) ?? ""}`}
              onClick={onRowClick ? () => onRowClick(row) : undefined}
            >
              {selection ? (
                <td className="cs-table__check" onClick={(e) => e.stopPropagation()}>
                  <input
                    type="checkbox"
                    checked={selection.selected.has(selection.getId(row))}
                    onChange={() => selection.onToggle(selection.getId(row))}
                  />
                </td>
              ) : null}
              {columns.map((col) => (
                <td key={col.key} style={{ textAlign: col.align ?? "left" }}>
                  {col.render(row)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}