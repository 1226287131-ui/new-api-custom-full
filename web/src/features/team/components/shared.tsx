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
import { ChevronLeft, ChevronRight } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Empty, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import {
  SecureVerificationDialog,
  type useSecureVerification,
} from '@/features/auth/secure-verification'

export function TeamVerification(props: {
  verification: ReturnType<typeof useSecureVerification>
}) {
  const v = props.verification
  return (
    <SecureVerificationDialog
      open={v.open}
      onOpenChange={(open) => {
        if (!open) v.cancel()
      }}
      methods={v.methods}
      state={v.state}
      onVerify={async (method, code) => {
        try {
          await v.executeVerification(method, code)
        } catch {
          /* The verification hook displays the error and retains the retry. */
        }
      }}
      onCancel={v.cancel}
      onCodeChange={v.setCode}
      onMethodChange={v.switchMethod}
    />
  )
}

export function TeamStatus(props: { status: string }) {
  const { t } = useTranslation()
  const names: Record<string, string> = {
    pending: t('Pending'),
    approved: t('Approved'),
    paid: t('Paid'),
    rejected: t('Rejected'),
    settled: t('Settled'),
  }
  return (
    <Badge variant={props.status === 'rejected' ? 'destructive' : 'secondary'}>
      {names[props.status] || props.status}
    </Badge>
  )
}

export function TeamLoading() {
  const { t } = useTranslation()
  return (
    <div role='status' aria-label={t('Loading')} className='space-y-3 py-5'>
      <Skeleton className='h-12 w-full' />
      <Skeleton className='h-32 w-full' />
    </div>
  )
}

export function TeamError(props: { retry: () => void }) {
  const { t } = useTranslation()
  return (
    <Alert variant='destructive'>
      <AlertDescription className='flex flex-wrap items-center gap-3'>
        {t('Failed to load data')}
        <Button variant='outline' size='sm' onClick={props.retry}>
          {t('Retry')}
        </Button>
      </AlertDescription>
    </Alert>
  )
}

export function TeamEmpty() {
  const { t } = useTranslation()
  return (
    <Empty>
      <EmptyHeader>
        <EmptyTitle>{t('No records yet')}</EmptyTitle>
      </EmptyHeader>
    </Empty>
  )
}

export function TeamPagination(props: {
  page: number
  total: number
  loading: boolean
  onChange: (page: number) => void
}) {
  const { t } = useTranslation()
  const pages = Math.max(1, Math.ceil(props.total / 20))
  return (
    <div className='flex flex-wrap items-center justify-end gap-3 py-3 text-sm'>
      <span className='text-muted-foreground'>
        {t('Page {{page}} of {{pages}}', { page: props.page, pages })}
      </span>
      <Button
        variant='outline'
        size='icon'
        aria-label={t('Previous page')}
        disabled={props.page <= 1 || props.loading}
        onClick={() => props.onChange(props.page - 1)}
      >
        <ChevronLeft />
      </Button>
      <Button
        variant='outline'
        size='icon'
        aria-label={t('Next page')}
        disabled={props.page >= pages || props.loading}
        onClick={() => props.onChange(props.page + 1)}
      >
        <ChevronRight />
      </Button>
    </div>
  )
}
