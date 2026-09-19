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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  flexRender,
  getCoreRowModel,
  useReactTable,
} from '@tanstack/react-table'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useTaskLogsColumns } from '../components/columns/task-logs-columns'
import { UsageLogsProvider } from '../components/usage-logs-provider'
import type { TaskLog } from '../types'

const task: TaskLog = {
  id: 1,
  user_id: 7,
  platform: 'openai-video',
  task_id: 'task_owned_video',
  model_name: 'video-model',
  action: 'TEXT_GENERATE',
  channel_id: 0,
  group: 'default',
  quota: 100,
  submit_time: 1,
  status: 'SUCCESS',
  legacy_video_available: true,
  result_url: '/video-cache/task_owned_video.mp4',
  request_body: {
    model: 'video-model',
    prompt: 'An ocean at sunrise',
    seconds: 14,
  },
  request_body_complete: true,
}

function TaskActionsTable(props: { log: TaskLog; isAdmin?: boolean }) {
  const columns = useTaskLogsColumns(props.isAdmin ?? false, false)
  const table = useReactTable({
    columns,
    data: [props.log],
    getCoreRowModel: getCoreRowModel(),
  })
  return (
    <table>
      <tbody>
        {table.getRowModel().rows.map((row) => (
          <tr key={row.id}>
            {row.getVisibleCells().map((cell) => (
              <td key={cell.id}>
                {flexRender(cell.column.columnDef.cell, cell.getContext())}
              </td>
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  )
}

let queryClient: QueryClient
const originalGetAnimations = Object.getOwnPropertyDescriptor(
  Element.prototype,
  'getAnimations'
)

beforeEach(() => {
  Object.defineProperty(Element.prototype, 'getAnimations', {
    configurable: true,
    value: vi.fn(() => []),
  })
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
})

afterEach(() => {
  queryClient.clear()
  if (originalGetAnimations) {
    Object.defineProperty(
      Element.prototype,
      'getAnimations',
      originalGetAnimations
    )
  } else {
    Reflect.deleteProperty(Element.prototype, 'getAnimations')
  }
})

function renderTask(log: TaskLog = task, isAdmin = false) {
  return render(
    <QueryClientProvider client={queryClient}>
      <UsageLogsProvider>
        <TaskActionsTable log={log} isAdmin={isAdmin} />
      </UsageLogsProvider>
    </QueryClientProvider>
  )
}

describe('task log actions', () => {
  it.each([false, true])(
    'opens the stored request body for an authorized task (admin=%s)',
    async (isAdmin) => {
      renderTask(task, isAdmin)
      await userEvent.click(
        screen.getByRole('button', { name: 'View request' })
      )
      const dialog = await screen.findByRole('dialog', { name: 'Request Body' })
      expect(dialog).toHaveTextContent('An ocean at sunrise')
      expect(dialog).toHaveTextContent('"seconds": 14')
      expect(within(dialog).getByTitle('Copy to clipboard')).toBeEnabled()
    }
  )

  it('keeps one video preview entry in artifacts and removes the duplicate in details', () => {
    renderTask()
    expect(
      screen.getAllByRole('button', { name: 'Click to preview video' })
    ).toHaveLength(1)
    expect(
      screen.queryByRole('button', { name: 'View video' })
    ).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'View details' })).toBeEnabled()
  })

  it('does not offer an empty request body for historical tasks', () => {
    renderTask({ ...task, request_body: undefined })
    expect(
      screen.queryByRole('button', { name: 'View request' })
    ).not.toBeInTheDocument()
  })

  it('warns users when only a normalized request was recorded', async () => {
    renderTask({ ...task, request_body_complete: false })
    await userEvent.click(screen.getByRole('button', { name: 'View request' }))
    expect(
      await screen.findByRole('dialog', { name: 'Request Body' })
    ).toHaveTextContent(
      'The original request body exceeded the storage limit, so a normalized request was recorded instead.'
    )
  })

  it('still opens failure details without showing a video action', async () => {
    renderTask({
      ...task,
      status: 'FAILURE',
      result_url: '',
      legacy_video_available: false,
      fail_reason: 'Generation failed',
    })
    await userEvent.click(screen.getByRole('button', { name: 'View details' }))
    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveTextContent('Generation failed')
    expect(
      screen.queryByRole('button', { name: 'Click to preview video' })
    ).not.toBeInTheDocument()
  })
})
