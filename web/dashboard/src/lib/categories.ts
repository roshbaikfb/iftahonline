import type { Category } from './products';

// The category tree is at most three levels deep (ADR-0011).
export const maxDepth = 3;

export interface Node {
  c: Category;
  depth: number; // 1 for a root
  children: Node[];
}

// tree arranges the store's categories as nested nodes, each level by
// position then name. A category whose parent is missing shows as a root.
export function tree(list: Category[]): Node[] {
  const byParent = new Map<string, Category[]>();
  const ids = new Set(list.map((c) => c.id));
  for (const c of list) {
    const p = c.parent_id && ids.has(c.parent_id) ? c.parent_id : '';
    byParent.set(p, [...(byParent.get(p) ?? []), c]);
  }
  const build = (parent: string, depth: number): Node[] =>
    (byParent.get(parent) ?? [])
      .sort((a, b) => a.position - b.position || a.name.localeCompare(b.name, 'ar'))
      .map((c) => ({ c, depth, children: build(c.id, depth + 1) }));
  return build('', 1);
}

// flat lists the tree depth first, for selects and the screen's rows.
export function flat(nodes: Node[]): Node[] {
  return nodes.flatMap((n) => [n, ...flat(n.children)]);
}

function height(n: Node): number {
  return 1 + Math.max(0, ...n.children.map(height));
}

// parentChoices are where a category may move: not under itself or its
// descendants, and not so deep that its subtree passes three levels. A
// new category (id "") is a single level.
export function parentChoices(nodes: Node[], id: string): Node[] {
  const all = flat(nodes);
  const self = all.find((n) => n.c.id === id);
  const below = new Set(self ? flat([self]).map((n) => n.c.id) : []);
  const levels = self ? height(self) : 1;
  return all.filter((n) => !below.has(n.c.id) && n.depth + levels <= maxDepth);
}
