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
import { Info } from 'lucide-react'
import { useId } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { getSuccessRateTextClass } from '@/features/performance-metrics/lib/format'
import { cn } from '@/lib/utils'

import type { PricingModel } from '../types'

export function ModelTaskSuccessRate(props: { model: PricingModel }) {
  const { t } = useTranslation()
  const descriptionId = useId()
  const stats = props.model.task_success_stats
  if (!props.model.is_task_model && !stats) return null

  let status = props.model.task_success_status
  status ??= stats ? 'ready' : 'loading'
  let text = t('Loading success rate…')
  let color = 'text-muted-foreground'
  let showDetails = false
  let hasRate = false
  if (status === 'error') {
    text = t('Success rate temporarily unavailable')
  } else if (status === 'ready') {
    if (!stats || stats.total === 0) {
      text = t('No generations in the last hour')
    } else if (stats.success + stats.failure === 0) {
      text = t('All generations are still in progress')
      showDetails = true
    } else if (
      stats.success_rate !== null &&
      Number.isFinite(stats.success_rate) &&
      stats.success_rate >= 0 &&
      stats.success_rate <= 100
    ) {
      const rate = Number(stats.success_rate.toFixed(1))
      text = `${rate}%`
      color = getSuccessRateTextClass(stats.success_rate)
      showDetails = true
      hasRate = true
    } else {
      text = t('Success rate temporarily unavailable')
    }
  }

  return (
    <div
      role='group'
      aria-label={t('Task success in the last hour')}
      className='flex min-w-0 flex-col gap-0.5 text-xs'
    >
      <div className='flex flex-wrap items-center gap-x-1.5 gap-y-0.5'>
        {hasRate && (
          <span className='text-muted-foreground'>
            {t('Last-hour success rate')}
          </span>
        )}
        <span className={cn('font-medium tabular-nums', color)}>{text}</span>
        <Tooltip>
          <TooltipTrigger
            render={
              <Button variant='ghost' size='icon' className='size-5 shrink-0' />
            }
            aria-label={t('How task success rate is calculated')}
            aria-describedby={descriptionId}
          >
            <Info aria-hidden className='size-3.5' />
          </TooltipTrigger>
          <TooltipContent
            id={descriptionId}
            role='tooltip'
            className='flex max-w-72 flex-col items-start gap-1.5 leading-5 text-pretty'
          >
            <p>
              {t(
                'Tasks submitted in the last hour across your visible groups. Success rate = succeeded ÷ (succeeded + failed). Generating tasks are excluded.'
              )}
            </p>
            {showDetails && stats && (
              <p className='tabular-nums'>
                {t(
                  'Succeeded: {{success}} · Failed: {{failure}} · Generating: {{pending}}',
                  stats
                )}
              </p>
            )}
          </TooltipContent>
        </Tooltip>
      </div>
      {showDetails && stats && stats.pending > 0 && (
        <span className='text-muted-foreground'>
          {t('Generating: {{pending}}', { pending: stats.pending })}
        </span>
      )}
    </div>
  )
}
