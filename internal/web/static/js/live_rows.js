// Keep the existing nodes in place whenever the displayed order is unchanged.
// On a filter or order change, move only the rows that need to move.
export function syncChildren(parent, rows, first = parent.firstElementChild) {
  let next = first;
  let desired;
  for (const row of rows) {
    if (row !== next) {
      desired ??= new Set(rows);
      while (next && !desired.has(next)) {
        const obsolete = next;
        next = next.nextElementSibling;
        obsolete.remove();
      }
      if (row !== next) {
        parent.insertBefore(row, next);
        continue;
      }
    }
    next = next.nextElementSibling;
  }
  while (next) {
    const obsolete = next;
    next = next.nextElementSibling;
    obsolete.remove();
  }
}

export function sameValues(a, b) {
  return a?.length === b.length && b.every((value, i) => value === a[i]);
}

// Compare inputs before creating a cell. In particular, a refreshed
// snapshot does not need to rebuild the hosts or their sparkline SVGs.
export function updateCells(row, columns, read, render, previous = []) {
  return columns.map(({ key }, i) => {
    const values = read(key);
    if (sameValues(previous[i], values)) return previous[i];
    const cell = render(key, ...values);
    cell.classList.add(`col-${key}`);
    if (row.cells[i]) row.cells[i].replaceWith(cell);
    else row.append(cell);
    return values;
  });
}
