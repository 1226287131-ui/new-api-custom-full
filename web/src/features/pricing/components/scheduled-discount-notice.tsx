import { Clock3, Percent } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { cn } from '@/lib/utils'

import type { ScheduledDiscountState } from '../lib/scheduled-discount'

export function ScheduledDiscountNotice(props: {
  state: ScheduledDiscountState
  compact?: boolean
}) {
  const { t } = useTranslation()
  if (!props.state.configured) return null

  return (
    <div
      className={cn(
        'flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs',
        props.state.active
          ? 'text-emerald-700 dark:text-emerald-300'
          : 'text-muted-foreground'
      )}
    >
      <span className='inline-flex items-center gap-1 font-medium'>
        <Percent className='size-3' />
        {props.state.active ? t('Discount active') : t('Scheduled discount')}
      </span>
      <span className={cn(!props.compact && 'font-mono')}>
        {t('{{percent}}% off', { percent: props.state.percent })}
      </span>
      <span className='text-muted-foreground/80 inline-flex items-center gap-1'>
        <Clock3 className='size-3' />
        {t('Beijing time {{start}}-{{end}}', {
          start: props.state.start,
          end: props.state.end,
        })}
      </span>
    </div>
  )
}
