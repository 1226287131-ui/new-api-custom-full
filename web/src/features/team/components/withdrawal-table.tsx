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
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

import { getWithdrawals } from '../api'
import { teamDate } from '../lib/date'
import { formatCNY } from '../lib/money'
import type { Withdrawal } from '../types'
import {
  TeamEmpty,
  TeamError,
  TeamLoading,
  TeamPagination,
  TeamStatus,
} from './shared'
import { WithdrawalReviewDialog } from './withdrawal-review-dialog'

export function WithdrawalTable(props: { admin?: boolean; status?: string }) {
  const { t, i18n } = useTranslation()
  const [page, setPage] = useState(1)
  const [selected, setSelected] = useState<Withdrawal | null>(null)
  const query = useQuery({
    queryKey: ['team', 'withdrawals', props.admin, props.status, page],
    queryFn: () => getWithdrawals(page, props.admin, props.status),
  })
  if (query.isPending) return <TeamLoading />
  if (query.isError) return <TeamError retry={() => void query.refetch()} />
  return (
    <>
      {query.data.items.length === 0 ? (
        <TeamEmpty />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>ID</TableHead>
              {props.admin && <TableHead>{t('User ID')}</TableHead>}
              <TableHead>{t('Amount')}</TableHead>
              <TableHead>{t('Alipay payout account')}</TableHead>
              <TableHead>{t('Status')}</TableHead>
              <TableHead>{t('Created At')}</TableHead>
              <TableHead>{t('Payment reference / reason')}</TableHead>
              {props.admin && <TableHead>{t('Actions')}</TableHead>}
            </TableRow>
          </TableHeader>
          <TableBody>
            {query.data.items.map((item) => (
              <TableRow key={item.id}>
                <TableCell>{item.id}</TableCell>
                {props.admin && <TableCell>{item.user_id}</TableCell>}
                <TableCell className='tabular-nums'>
                  {formatCNY(item.amount_cents, i18n.language)}
                </TableCell>
                <TableCell className='max-w-52 break-all whitespace-normal'>
                  {item.account_masked}
                  <div className='text-muted-foreground text-xs'>
                    {item.name_masked}
                  </div>
                </TableCell>
                <TableCell>
                  <TeamStatus status={item.status} />
                </TableCell>
                <TableCell>
                  {teamDate(item.created_at, i18n.language)}
                </TableCell>
                <TableCell className='max-w-72 break-all whitespace-normal'>
                  {item.payment_reference || item.rejection_reason || '-'}
                </TableCell>
                {props.admin && (
                  <TableCell>
                    <Button
                      size='sm'
                      variant='outline'
                      onClick={() => setSelected(item)}
                    >
                      {t('Review withdrawal')}
                    </Button>
                  </TableCell>
                )}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      <TeamPagination
        page={page}
        total={query.data.total}
        loading={query.isFetching}
        onChange={setPage}
      />
      {selected && (
        <WithdrawalReviewDialog
          item={selected}
          onClose={() => setSelected(null)}
        />
      )}
    </>
  )
}
