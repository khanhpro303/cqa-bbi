export function readLocalStorage(key: string): string | null {
  try {
    return typeof localStorage !== 'undefined' && typeof localStorage.getItem === 'function'
      ? localStorage.getItem(key)
      : null
  } catch {
    return null
  }
}

export function writeLocalStorage(key: string, value: string): void {
  try {
    if (typeof localStorage !== 'undefined' && typeof localStorage.setItem === 'function') {
      localStorage.setItem(key, value)
    }
  } catch {
    // Ignore storage quota errors or restricted test stubs
  }
}

export function readJsonStorage<T>(key: string): T | null {
  const raw = readLocalStorage(key)
  if (!raw) return null
  try {
    return JSON.parse(raw) as T
  } catch {
    return null
  }
}

export function writeJsonStorage(key: string, value: unknown): void {
  try {
    writeLocalStorage(key, JSON.stringify(value))
  } catch {
    // Ignore serialization errors
  }
}
