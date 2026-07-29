// Tiny in-memory query cache for stale-while-revalidate on tab navigation.
// Pages seed their initial state from the last cached value (instant render,
// no spinner) and then revalidate in the background. It is process-local and
// cleared on reload, so it never masks a stale server state across sessions —
// it only removes the blank flash when you switch back to a tab you just saw.

const store = new Map<string, unknown>()

export function cacheGet<T>(key: string): T | undefined {
  return store.get(key) as T | undefined
}

export function cacheSet<T>(key: string, value: T): void {
  store.set(key, value)
}

// Invalidate one key or every key under a prefix (e.g. "members:" after a
// mutation), so the next read revalidates instead of serving a known-stale hit.
export function cacheInvalidate(keyOrPrefix?: string): void {
  if (!keyOrPrefix) {
    store.clear()
    return
  }
  for (const k of [...store.keys()]) {
    if (k === keyOrPrefix || k.startsWith(keyOrPrefix)) store.delete(k)
  }
}
