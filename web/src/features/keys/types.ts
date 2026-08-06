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
import { z } from 'zod'

// ============================================================================
// API Key Schema & Types
// ============================================================================

export const apiKeySchema = z.object({
  id: z.number(),
  name: z.string(),
  key: z.string(),
  status: z.number(), // 1: enabled, 2: disabled, 3: expired, 4: exhausted
  remain_quota: z.number(),
  used_quota: z.number(),
  unlimited_quota: z.boolean(),
  expired_time: z.number(), // -1 for never expires
  created_time: z.number(),
  accessed_time: z.number(),
  group: z.string().nullish().default(''),
  auto_groups: z.array(z.string()).nullish().default(null),
  cross_group_retry: z
    .preprocess((v) => {
      if (v === 1) return true
      if (v === 0) return false
      return v
    }, z.boolean())
    .optional()
    .default(false),
  model_limits_enabled: z.boolean(),
  model_limits: z.string().nullish().default(''),
  allow_ips: z.string().nullish().default(''),
})

export type ApiKey = z.infer<typeof apiKeySchema>

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function asString(value: unknown, fallback = ''): string {
  return typeof value === 'string' ? value : fallback
}

function asFiniteNumber(value: unknown, fallback = 0): number {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string' && value.trim() !== '') {
    const parsed = Number(value)
    if (Number.isFinite(parsed)) return parsed
  }
  return fallback
}

function asBoolean(value: unknown, fallback = false): boolean {
  if (typeof value === 'boolean') return value
  if (value === 1 || value === '1' || value === 'true') return true
  if (value === 0 || value === '0' || value === 'false') return false
  return fallback
}

/**
 * Converts legacy or partially populated token rows into a renderable shape.
 * A single old row must not take down the whole key-management page.
 */
export function normalizeApiKey(value: unknown): ApiKey | null {
  const parsed = apiKeySchema.safeParse(value)
  if (parsed.success) return parsed.data
  if (!isRecord(value)) return null

  const id = asFiniteNumber(value.id, 0)
  if (!Number.isInteger(id) || id <= 0) return null

  const autoGroups = Array.isArray(value.auto_groups)
    ? value.auto_groups.filter(
        (group): group is string => typeof group === 'string'
      )
    : null

  return {
    id,
    name: asString(value.name),
    key: asString(value.key),
    status: asFiniteNumber(value.status, 0),
    remain_quota: asFiniteNumber(value.remain_quota, 0),
    used_quota: asFiniteNumber(value.used_quota, 0),
    unlimited_quota: asBoolean(value.unlimited_quota),
    expired_time: asFiniteNumber(value.expired_time, -1),
    created_time: asFiniteNumber(value.created_time, 0),
    accessed_time: asFiniteNumber(value.accessed_time, 0),
    group: asString(value.group),
    auto_groups: autoGroups,
    cross_group_retry: asBoolean(value.cross_group_retry),
    model_limits_enabled: asBoolean(value.model_limits_enabled),
    model_limits: asString(value.model_limits),
    allow_ips: asString(value.allow_ips),
  }
}

export function normalizeGetApiKeysResponse(
  value: unknown,
  fallbackPage = 1,
  fallbackPageSize = 10
): GetApiKeysResponse {
  if (!isRecord(value) || value.success !== true || !isRecord(value.data)) {
    return {
      success: false,
      message: isRecord(value) ? asString(value.message) : undefined,
    }
  }

  const rawItems = value.data.items
  const items = Array.isArray(rawItems)
    ? rawItems
        .map(normalizeApiKey)
        .filter((item): item is ApiKey => item !== null)
    : []

  return {
    success: true,
    message: asString(value.message) || undefined,
    data: {
      items,
      total: Math.max(0, Math.trunc(asFiniteNumber(value.data.total, 0))),
      page: Math.max(
        1,
        Math.trunc(asFiniteNumber(value.data.page, fallbackPage))
      ),
      page_size: Math.max(
        1,
        Math.trunc(asFiniteNumber(value.data.page_size, fallbackPageSize))
      ),
    },
  }
}

// ============================================================================
// API Request/Response Types
// ============================================================================

export interface ApiResponse<T = unknown> {
  success: boolean
  message?: string
  data?: T
}

export interface GetApiKeysParams {
  p?: number
  size?: number
}

export interface GetApiKeysResponse {
  success: boolean
  message?: string
  data?: {
    items: ApiKey[]
    total: number
    page: number
    page_size: number
  }
}

export interface SearchApiKeysParams {
  keyword?: string
  token?: string
  p?: number
  size?: number
}

export interface ApiKeyFormData {
  name: string
  remain_quota: number
  expired_time: number
  unlimited_quota: boolean
  model_limits_enabled: boolean
  model_limits: string
  allow_ips: string
  group: string
  auto_groups: string[]
  cross_group_retry: boolean
}

export interface TokenAutoGroupsConfig {
  groups: string[]
  max_count: number
}

// ============================================================================
// Dialog Types
// ============================================================================

export type ApiKeysDialogType =
  | 'create'
  | 'update'
  | 'delete'
  | 'batch-delete'
  | 'cc-switch'
