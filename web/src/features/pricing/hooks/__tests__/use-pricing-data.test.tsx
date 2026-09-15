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
import {
  QueryClient,
  QueryClientProvider,
  focusManager,
} from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { usePricingData } from '../use-pricing-data'

const prices = {
  success: true,
  data: [
    {
      id: 1,
      model_name: 'client-video-alias',
      quota_type: 1,
      model_ratio: 0,
      completion_ratio: 0,
      model_price: 0.2,
      enable_groups: ['default'],
      is_task_model: true,
    },
  ],
  vendors: [],
  group_ratio: {},
  usable_group: {},
  auto_groups: [],
  supported_endpoint: {},
}
const stats = {
  success: true,
  data: {
    window_start: 1000,
    window_end: 4600,
    models: {
      'client-video-alias': {
        total: 12,
        success: 9,
        failure: 1,
        pending: 2,
        success_rate: 90,
      },
    },
  },
}
const clients: QueryClient[] = []

function wrapper(props: { children: ReactNode }) {
  return (
    <QueryClientProvider client={clients[0]}>
      {props.children}
    </QueryClientProvider>
  )
}

afterEach(() => {
  for (const client of clients) client.clear()
  clients.length = 0
  useAuthStore.getState().auth.setUser(null)
  focusManager.setFocused(undefined)
  vi.useRealTimers()
})

it('shares a batch query across consumers and merges by the client model name without blocking prices', async () => {
  let finishStats: ((value: { data: typeof stats }) => void) | undefined
  const pending = new Promise<{ data: typeof stats }>((resolve) => {
    finishStats = resolve
  })
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/pricing') return { data: prices }
    if (url === '/api/pricing/task-success-rates') return pending
    return { data: { success: true, data: {} } }
  })
  clients.push(
    new QueryClient({ defaultOptions: { queries: { retry: false } } })
  )
  const first = renderHook(() => usePricingData(), { wrapper })
  const second = renderHook(() => usePricingData(), { wrapper })
  await waitFor(() => expect(first.result.current.models).toHaveLength(1))
  expect(first.result.current.models[0]).toMatchObject({
    model_price: 0.2,
    task_success_status: 'loading',
  })
  await act(async () => {
    finishStats?.({ data: stats })
    await pending
  })
  await waitFor(() =>
    expect(second.result.current.models[0].task_success_status).toBe('ready')
  )
  expect(first.result.current.models[0].task_success_stats).toEqual(
    stats.data.models['client-video-alias']
  )
  expect(
    get.mock.calls.filter(([url]) => url === '/api/pricing/task-success-rates')
  ).toHaveLength(1)
})

it('keeps prices usable and exposes a statistics error instead of an empty hour', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/pricing') return { data: prices }
    if (url === '/api/pricing/task-success-rates') {
      throw new Error('unavailable')
    }
    return { data: { success: true, data: {} } }
  })
  clients.push(
    new QueryClient({ defaultOptions: { queries: { retry: false } } })
  )
  const hook = renderHook(() => usePricingData(), { wrapper })
  await waitFor(() =>
    expect(hook.result.current.models[0]?.task_success_status).toBe('error')
  )
  expect(hook.result.current.models[0].model_price).toBe(0.2)
  expect(hook.result.current.error).toBeNull()
})

it("does not reuse another signed-in user's visible-group statistics", async () => {
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/pricing') return { data: prices }
    if (url === '/api/pricing/task-success-rates') {
      if (useAuthStore.getState().auth.user?.id === 1) return { data: stats }
      return { data: { ...stats, data: { ...stats.data, models: {} } } }
    }
    return { data: { success: true, data: {} } }
  })
  useAuthStore.getState().auth.setUser({ id: 1, username: 'first', role: 1 })
  clients.push(
    new QueryClient({ defaultOptions: { queries: { retry: false } } })
  )
  const hook = renderHook(() => usePricingData(), { wrapper })
  await waitFor(() =>
    expect(hook.result.current.models[0]?.task_success_stats?.success).toBe(9)
  )
  act(() =>
    useAuthStore.getState().auth.setUser({ id: 2, username: 'second', role: 1 })
  )
  expect(hook.result.current.models[0].task_success_stats).toBeUndefined()
  await waitFor(() =>
    expect(hook.result.current.models[0].task_success_status).toBe('ready')
  )
  expect(hook.result.current.models[0].task_success_stats).toBeUndefined()
  expect(
    get.mock.calls.filter(([url]) => url === '/api/pricing/task-success-rates')
  ).toHaveLength(2)
})

it('refreshes once per minute while visible and stops polling in the background', async () => {
  vi.useFakeTimers()
  focusManager.setFocused(true)
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/pricing') return { data: prices }
    if (url === '/api/pricing/task-success-rates') return { data: stats }
    return { data: { success: true, data: {} } }
  })
  clients.push(
    new QueryClient({ defaultOptions: { queries: { retry: false } } })
  )
  const hook = renderHook(() => usePricingData(), { wrapper })
  await act(async () => {
    await vi.advanceTimersByTimeAsync(1)
  })
  expect(hook.result.current.models[0]?.task_success_status).toBe('ready')
  await act(async () => {
    await vi.advanceTimersByTimeAsync(60_000)
  })
  expect(
    get.mock.calls.filter(([url]) => url === '/api/pricing/task-success-rates')
  ).toHaveLength(2)
  focusManager.setFocused(false)
  await act(async () => {
    await vi.advanceTimersByTimeAsync(60_000)
  })
  expect(
    get.mock.calls.filter(([url]) => url === '/api/pricing/task-success-rates')
  ).toHaveLength(2)
})
