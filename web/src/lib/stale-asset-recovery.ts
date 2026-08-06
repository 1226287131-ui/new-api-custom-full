/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

const STALE_ASSET_RELOAD_PREFIX = 'newapi:stale-asset-reload:'
const STALE_ASSET_RELOAD_PARAM = '__frontend_reload'
const STALE_ASSET_RELOAD_WINDOW_MS = 5 * 60 * 1000

const STALE_ASSET_ERROR_PATTERN =
  /ChunkLoadError|Loading (?:chunk|CSS chunk) .* failed|Failed to fetch dynamically imported module|Importing a module script failed|dynamically imported module/i

function getErrorText(error: unknown): string {
  if (error instanceof Error) return `${error.name}: ${error.message}`
  if (typeof error === 'string') return error
  if (typeof error === 'object' && error !== null) {
    const message = (error as { message?: unknown }).message
    if (typeof message === 'string') return message
  }
  return ''
}

export function isStaleAssetError(error: unknown): boolean {
  return STALE_ASSET_ERROR_PATTERN.test(getErrorText(error))
}

function isStaleAssetTarget(target: EventTarget | null): boolean {
  if (typeof HTMLScriptElement === 'undefined') return false
  return target instanceof HTMLScriptElement && target.src.includes('/static/')
}

function getReloadKey(): string {
  return `${STALE_ASSET_RELOAD_PREFIX}${window.location.pathname}`
}

export function reloadAfterStaleAsset(error: unknown): boolean {
  if (!isStaleAssetError(error) || typeof window === 'undefined') return false

  try {
    const key = getReloadKey()
    const lastReload = Number(window.sessionStorage.getItem(key))
    if (
      Number.isFinite(lastReload) &&
      Date.now() - lastReload < STALE_ASSET_RELOAD_WINDOW_MS
    ) {
      return false
    }

    window.sessionStorage.setItem(key, String(Date.now()))
    const url = new URL(window.location.href)
    url.searchParams.set(STALE_ASSET_RELOAD_PARAM, String(Date.now()))
    window.location.replace(url.toString())
    return true
  } catch {
    return false
  }
}

function handleWindowError(event: ErrorEvent): void {
  if (!isStaleAssetError(event.error) && !isStaleAssetTarget(event.target)) {
    return
  }

  if (reloadAfterStaleAsset(event.error ?? 'ChunkLoadError')) {
    event.preventDefault()
  }
}

function handleUnhandledRejection(event: PromiseRejectionEvent): void {
  if (reloadAfterStaleAsset(event.reason)) event.preventDefault()
}

export function installStaleAssetRecovery(): void {
  if (typeof window === 'undefined') return

  window.addEventListener('error', handleWindowError)
  window.addEventListener('unhandledrejection', handleUnhandledRejection)

  try {
    const url = new URL(window.location.href)
    if (url.searchParams.has(STALE_ASSET_RELOAD_PARAM)) {
      url.searchParams.delete(STALE_ASSET_RELOAD_PARAM)
      window.history.replaceState(window.history.state, '', url.toString())
    }
  } catch {
    // A restricted browser context may not allow history or storage access.
  }
}
