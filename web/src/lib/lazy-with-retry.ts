import { lazy, type ComponentType, type LazyExoticComponent } from 'react'

const STORAGE_PREFIX = 'orako:chunk-reload:'

export function lazyWithRetry<T extends ComponentType>(
  key: string,
  importer: () => Promise<{ default: T }>,
): LazyExoticComponent<T> {
  return lazy(async () => {
    try {
      const module = await importer()
      removeReloadMarker(key)

      return module
    } catch (error) {
      if (!hasReloadMarker(key)) {
        setReloadMarker(key)
        window.location.reload()

        return new Promise<never>(() => {})
      }

      throw error
    }
  })
}

function hasReloadMarker(key: string): boolean {
  try {
    return sessionStorage.getItem(STORAGE_PREFIX + key) === '1'
  } catch {
    return false
  }
}

function setReloadMarker(key: string): void {
  try {
    sessionStorage.setItem(STORAGE_PREFIX + key, '1')
  } catch {}
}

function removeReloadMarker(key: string): void {
  try {
    sessionStorage.removeItem(STORAGE_PREFIX + key)
  } catch {}
}
