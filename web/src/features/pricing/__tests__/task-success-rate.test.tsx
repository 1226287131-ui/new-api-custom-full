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
import { act, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { afterEach, expect, it, vi } from 'vitest'

import { TooltipProvider } from '@/components/ui/tooltip'
import zh from '@/i18n/locales/zh.json'
import { api } from '@/lib/api'

import { ModelCard } from '../components/model-card'
import { ModelDetailsContent } from '../components/model-details'
import { ModelTaskSuccessRate } from '../components/model-task-success-rate'
import type { PricingModel, TaskSuccessStats } from '../types'

vi.mock('@visactor/react-vchart', () => ({ VChart: () => null }))

const model: PricingModel = {
  id: 1,
  model_name: 'video-test',
  quota_type: 1,
  model_ratio: 0,
  completion_ratio: 0,
  model_price: 0.2,
  enable_groups: ['default'],
  is_task_model: true,
  task_success_status: 'ready',
}
const clients: QueryClient[] = []

afterEach(async () => {
  for (const client of clients) client.clear()
  clients.length = 0
  await i18next.changeLanguage('en')
})

it.each([
  {
    status: 'loading' as const,
    stats: undefined,
    label: 'Loading success rate…',
  },
  {
    status: 'error' as const,
    stats: undefined,
    label: 'Success rate temporarily unavailable',
  },
  {
    status: 'ready' as const,
    stats: undefined,
    label: 'No generations in the last hour',
  },
  {
    status: 'ready' as const,
    stats: { total: 3, success: 0, failure: 0, pending: 3, success_rate: null },
    label: 'All generations are still in progress',
  },
])(
  'distinguishes $label from a zero success rate',
  ({ status, stats, label }) => {
    render(
      <ModelTaskSuccessRate
        model={{
          ...model,
          task_success_status: status,
          task_success_stats: stats,
        }}
      />
    )
    expect(screen.getByText(label)).toBeVisible()
    expect(screen.queryByText('0%')).not.toBeInTheDocument()
    if (stats) expect(screen.getByText('Generating: 3')).toBeVisible()
  }
)

it.each([
  {
    total: 12,
    success: 9,
    failure: 1,
    pending: 2,
    success_rate: 90,
    expected: '90%',
    color: 'text-emerald-500',
  },
  {
    total: 4,
    success: 1,
    failure: 2,
    pending: 1,
    success_rate: 33.333333,
    expected: '33.3%',
    color: 'text-red-600',
  },
  {
    total: 2,
    success: 0,
    failure: 2,
    pending: 0,
    success_rate: 0,
    expected: '0%',
    color: 'text-red-600',
  },
])('shows $expected without counting pending tasks as failed', (stats) => {
  render(
    <ModelTaskSuccessRate model={{ ...model, task_success_stats: stats }} />
  )
  expect(screen.getByText(stats.expected)).toHaveClass(stats.color)
})

it('explains the shared visible-group window and pending count in an accessible tooltip', async () => {
  const stats: TaskSuccessStats = {
    total: 12,
    success: 9,
    failure: 1,
    pending: 2,
    success_rate: 90,
  }
  render(
    <TooltipProvider>
      <ModelTaskSuccessRate model={{ ...model, task_success_stats: stats }} />
    </TooltipProvider>
  )
  await userEvent
    .setup()
    .hover(
      screen.getByRole('button', {
        name: 'How task success rate is calculated',
      })
    )
  const tooltip = await screen.findByRole('tooltip')
  expect(tooltip).toHaveTextContent('across your visible groups')
  expect(tooltip).toHaveTextContent('Succeeded: 9 · Failed: 1 · Generating: 2')
})

it('hides ordinary chat models but displays a model identified by statistics', () => {
  const { rerender } = render(
    <ModelTaskSuccessRate model={{ ...model, is_task_model: false }} />
  )
  expect(screen.queryByRole('group')).not.toBeInTheDocument()
  rerender(
    <ModelTaskSuccessRate
      model={{
        ...model,
        is_task_model: false,
        task_success_stats: {
          total: 1,
          success: 1,
          failure: 0,
          pending: 0,
          success_rate: 100,
        },
      }}
    />
  )
  expect(screen.getByText('100%')).toBeVisible()
})

it('renders Chinese success rates and the exact empty-state copy without a locale error', async () => {
  i18next.addResourceBundle('zhCN', 'translation', zh.translation, true, true)
  await act(() => i18next.changeLanguage('zhCN'))
  const { rerender } = render(
    <ModelTaskSuccessRate
      model={{
        ...model,
        task_success_stats: {
          total: 1,
          success: 1,
          failure: 0,
          pending: 0,
          success_rate: 100,
        },
      }}
    />
  )
  expect(screen.getByText('近1小时成功率')).toBeVisible()
  expect(screen.getByText('100%')).toBeVisible()
  rerender(<ModelTaskSuccessRate model={model} />)
  expect(screen.getByText('近1小时无生成记录')).toBeVisible()
})

it('places success statistics below card pricing and in the model detail overview', () => {
  vi.spyOn(api, 'get').mockResolvedValue({ data: { data: { groups: [] } } })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  const ready = {
    ...model,
    task_success_stats: {
      total: 2,
      success: 2,
      failure: 0,
      pending: 0,
      success_rate: 100,
    },
  }
  render(
    <QueryClientProvider client={client}>
      <div data-testid='card'>
        <ModelCard model={ready} onClick={vi.fn()} />
      </div>
      <div data-testid='details'>
        <ModelDetailsContent
          model={ready}
          groupRatio={{}}
          usableGroup={{}}
          endpointMap={{}}
          autoGroups={[]}
          priceRate={1}
          usdExchangeRate={1}
          tokenUnit='M'
        />
      </div>
    </QueryClientProvider>
  )
  expect(
    within(
      within(screen.getByTestId('card')).getByRole('group', { name: 'Pricing' })
    ).getByText('100%')
  ).toBeVisible()
  expect(
    within(screen.getByTestId('details')).getByRole('group', {
      name: 'Task success in the last hour',
    })
  ).toHaveTextContent('100%')
})
