/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useQuery } from '@tanstack/react-query'
import {
  Activity,
  Coins,
  Hash,
  RefreshCw,
  Server,
  Zap,
  type LucideIcon,
} from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import { getTodayChannelUsage } from '@/features/dashboard/api'
import {
  formatNumber,
  formatQuota,
  formatTimestampToDate,
  formatTokens,
} from '@/lib/format'
import { cn } from '@/lib/utils'

const REFRESH_INTERVAL_MS = 30_000

function ChannelMetric(props: {
  icon: LucideIcon
  label: string
  value: string
  tone: string
}) {
  const Icon = props.icon

  return (
    <div className='bg-muted/40 flex min-w-0 items-center gap-2 rounded-lg px-3 py-2.5'>
      <span
        className={cn(
          'flex size-8 shrink-0 items-center justify-center rounded-lg',
          props.tone
        )}
      >
        <Icon className='size-4' aria-hidden='true' />
      </span>
      <span className='flex min-w-0 flex-col gap-0.5'>
        <span className='text-muted-foreground truncate text-[11px]'>
          {props.label}
        </span>
        <span className='text-foreground truncate font-mono text-sm font-semibold tabular-nums'>
          {props.value}
        </span>
      </span>
    </div>
  )
}

function ChannelUsageSkeleton() {
  return (
    <div className='space-y-3 px-3 py-3 sm:px-4'>
      {[0, 1, 2].map((item) => (
        <div key={item} className='flex items-center gap-3'>
          <Skeleton className='size-2 rounded-full' />
          <Skeleton className='h-4 flex-1' />
          <Skeleton className='h-4 w-20' />
          <Skeleton className='h-4 w-12' />
        </div>
      ))}
    </div>
  )
}

export function TodayChannelUsage() {
  const { t } = useTranslation()
  const usageQuery = useQuery({
    queryKey: ['dashboard', 'today-channel-usage'],
    queryFn: getTodayChannelUsage,
    refetchInterval: REFRESH_INTERVAL_MS,
    refetchIntervalInBackground: true,
    staleTime: 10_000,
    retry: 1,
  })

  const usage = usageQuery.data?.data
  const channels = usage?.channels ?? []
  const totalQuota = Number(usage?.total_quota ?? 0)
  const totalCount = Number(usage?.total_count ?? 0)
  const totalTokens = Number(usage?.total_token_used ?? 0)
  const isInitialLoading = usageQuery.isLoading && !usage
  const isRefreshing = usageQuery.isFetching && !isInitialLoading
  const hasError = usageQuery.isError || usageQuery.data?.success === false
  const updatedAt = usage?.updated_at
    ? formatTimestampToDate(Number(usage.updated_at))
    : ''

  const rows = useMemo(() => {
    const shareBase = totalQuota > 0 ? totalQuota : totalCount
    return channels.map((channel) => {
      const quota = Number(channel.quota) || 0
      const count = Number(channel.count) || 0
      const value = totalQuota > 0 ? quota : count
      const share = shareBase > 0 ? value / shareBase : 0
      return {
        ...channel,
        quota,
        count,
        tokenUsed: Number(channel.token_used) || 0,
        barWidth: Math.max(4, Math.min(100, share * 100)),
      }
    })
  }, [channels, totalCount, totalQuota])

  return (
    <div className='overflow-hidden rounded-lg border'>
      <div className='flex flex-wrap items-center justify-between gap-3 border-b px-3 py-2 sm:px-5 sm:py-3'>
        <div className='flex min-w-0 items-center gap-2'>
          <span className='bg-success/10 text-success flex size-7 shrink-0 items-center justify-center rounded-lg'>
            <Server className='size-4' aria-hidden='true' />
          </span>
          <div className='min-w-0'>
            <div className='flex flex-wrap items-center gap-2'>
              <h3 className='text-sm font-semibold'>
                {t("Today's channel consumption")}
              </h3>
              <Badge
                variant='outline'
                className='text-success border-success/30 gap-1'
              >
                <Activity className='size-3' aria-hidden='true' />
                {t('Live')}
              </Badge>
            </div>
            <p className='text-muted-foreground truncate text-xs'>
              {t("Live summary of today's successful consumption by channel")}
            </p>
          </div>
        </div>
        <Button
          variant='ghost'
          size='icon-sm'
          onClick={() => void usageQuery.refetch()}
          disabled={isInitialLoading || isRefreshing}
          aria-label={t('Refresh now')}
          title={t('Refresh now')}
        >
          <RefreshCw
            className={cn('size-4', isRefreshing && 'animate-spin')}
          />
        </Button>
      </div>

      <div className='grid gap-2 p-3 sm:grid-cols-3 sm:p-4'>
        <ChannelMetric
          icon={Coins}
          label={t('Total consumption')}
          value={isInitialLoading ? '...' : formatQuota(totalQuota)}
          tone='bg-warning/10 text-warning'
        />
        <ChannelMetric
          icon={Hash}
          label={t('Request Count')}
          value={isInitialLoading ? '...' : formatNumber(totalCount)}
          tone='bg-info/10 text-info'
        />
        <ChannelMetric
          icon={Zap}
          label={t('Total Tokens')}
          value={isInitialLoading ? '...' : formatTokens(totalTokens)}
          tone='bg-chart-4/10 text-chart-4'
        />
      </div>

      {isInitialLoading && <ChannelUsageSkeleton />}
      {!isInitialLoading && hasError && rows.length === 0 && (
        <div className='text-destructive bg-destructive/5 px-4 py-7 text-center text-sm'>
          {t('Failed to load channel consumption. Please retry.')}
        </div>
      )}
      {!isInitialLoading && !hasError && rows.length === 0 && (
        <Empty className='rounded-none border-0 py-8'>
          <EmptyHeader>
            <EmptyMedia variant='icon'>
              <Server />
            </EmptyMedia>
            <EmptyTitle>{t('No channel consumption today')}</EmptyTitle>
            <EmptyDescription>
              {t('No successful consumption records have been recorded today')}
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      )}
      {!isInitialLoading && rows.length > 0 && (
        <div className='border-t'>
          <div className='text-muted-foreground bg-muted/30 grid grid-cols-[minmax(0,1fr)_5rem_5rem_5rem] gap-2 px-3 py-2 text-[11px] font-medium sm:grid-cols-[minmax(0,1fr)_7rem_7rem_7rem] sm:px-4'>
            <span>{t('Channel')}</span>
            <span className='text-right'>{t('Quota')}</span>
            <span className='text-right'>{t('Request Count')}</span>
            <span className='text-right'>{t('Tokens')}</span>
          </div>
          <div className='max-h-80 overflow-y-auto'>
            {rows.map((channel) => (
              <div
                key={channel.channel_id}
                className='grid grid-cols-[minmax(0,1fr)_5rem_5rem_5rem] items-center gap-2 border-t px-3 py-3 sm:grid-cols-[minmax(0,1fr)_7rem_7rem_7rem] sm:px-4'
              >
                <div className='min-w-0'>
                  <div className='flex items-center gap-2'>
                    <span
                      className='bg-success size-1.5 shrink-0 rounded-full'
                      aria-hidden='true'
                    />
                    <span
                      className='truncate text-sm font-medium'
                      title={channel.channel_name}
                    >
                      {channel.channel_name}
                    </span>
                  </div>
                  <div className='bg-muted mt-1.5 h-1 overflow-hidden rounded-full'>
                    <div
                      className='bg-success h-full rounded-full transition-all duration-500'
                      style={{ width: `${channel.barWidth}%` }}
                    />
                  </div>
                </div>
                <span className='text-right font-mono text-xs font-semibold tabular-nums'>
                  {formatQuota(channel.quota)}
                </span>
                <span className='text-right font-mono text-xs tabular-nums'>
                  {formatNumber(channel.count)}
                </span>
                <span className='text-right font-mono text-xs tabular-nums'>
                  {formatTokens(channel.tokenUsed)}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}

      <div className='text-muted-foreground flex flex-wrap items-center justify-between gap-2 border-t px-3 py-2 text-[11px] sm:px-4'>
        <span>{t('Auto-refresh every 30 seconds')}</span>
        <span>
          {updatedAt
            ? t('Last updated {{time}}', { time: updatedAt })
            : t('Waiting for update')}
        </span>
      </div>
    </div>
  )
}
