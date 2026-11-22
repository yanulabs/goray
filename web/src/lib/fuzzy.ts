export interface FuzzyMatch {
  score: number;
  indices: number[];
}

// Subsequence match with word-boundary and adjacency bonuses. Works well for
// Go identifiers, package paths, and file names without a third-party library.
export function fuzzyMatch(query: string, text: string): FuzzyMatch | null {
  const needle = query.trim().toLowerCase();
  if (needle === "") {
    return { score: 0, indices: [] };
  }
  const haystack = text.toLowerCase();
  const indices: number[] = [];
  let cursor = 0;
  let previous = -1;
  let score = 0;

  for (let i = 0; i < needle.length; i += 1) {
    const character = needle[i];
    let found = -1;
    while (cursor < haystack.length) {
      if (haystack[cursor] === character) {
        found = cursor;
        break;
      }
      cursor += 1;
    }
    if (found < 0) {
      return null;
    }
    indices.push(found);
    score += 12;
    if (found === 0) {
      score += 18;
    } else {
      const before = text[found - 1] ?? "";
      if (/[\s/._:-]/.test(before)) {
        score += 18;
      }
    }
    if (found === previous + 1) {
      score += 14;
    } else if (previous >= 0) {
      score -= Math.min(12, (found - previous - 1) * 0.6);
    }
    score -= found * 0.08;
    previous = found;
    cursor = found + 1;
  }

  score -= text.length * 0.02;
  return { score, indices };
}

export function highlightSegments(text: string, indices: number[]): Array<{ text: string; match: boolean }> {
  if (indices.length === 0) {
    return [{ text, match: false }];
  }
  const set = new Set(indices);
  const segments: Array<{ text: string; match: boolean }> = [];
  let start = 0;
  let current = set.has(0);
  for (let i = 1; i <= text.length; i += 1) {
    const next = i < text.length ? set.has(i) : !current;
    if (next !== current) {
      segments.push({ text: text.slice(start, i), match: current });
      start = i;
      current = next;
    }
  }
  return segments;
}
