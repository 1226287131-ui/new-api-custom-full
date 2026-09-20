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

import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { toIntlLocale } from '@/i18n/languages'

import { getCommissions } from '../api'
import { teamDate } from '../lib/date'
import { formatCNY } from '../lib/money'
import {
  TeamEmpty,
  TeamError,
  TeamLoading,
  TeamPagination,
  TeamStatus,
} from './shared'

export function CommissionTable(props: { admin?: boolean }) {
  const { t, i18n } = useTranslation()
  const [page, setPage] = useState(1)
  const query = useQuery({
    queryKey: ['team', 'commissions', props.admin, page],
    queryFn: () => getCommissions(page, props.admin),
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
              <TableHead>
                {props.admin ? t('Order Number') : t('Record ID')}
              </TableHead>
              {props.admin && <TableHead>{t('Inviter ID')}</TableHead>}
              <TableHead>{t('Invited user ID')}</TableHead>
              <TableHead>{t('Actual payment')}</TableHead>
              <TableHead>{t('Invitation reward')}</TableHead>
              <TableHead>{t('Status')}</TableHead>
              <TableHead>{t('Created At')}</TableHead>
              <TableHead>{t('Available after')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {query.data.items.map((item) => (
              <TableRow key={item.id}>
                <TableCell className='max-w-64 font-mono text-xs break-all whitespace-normal'>
                  {props.admin ? item.trade_no : item.id}
                </TableCell>
                {props.admin && <TableCell>{item.referrer_id}</TableCell>}
                <TableCell>{item.user_id}</TableCell>
                <TableCell className='tabular-nums'>
                  {formatCNY(item.paid_cents, i18n.language)}
                </TableCell>
                <TableCell className='tabular-nums'>
                  {item.mode === 'cash'
                    ? formatCNY(item.cash_cents, i18n.language)
                    : t('{{amount}} site credits', {
                        amount: (
                          item.credit_quota / Number(item.quota_per_unit)
                        ).toLocaleString(toIntlLocale(i18n.language), {
                          maximumFractionDigits: 6,
                        }),
                      })}
                  <div className='text-muted-foreground text-xs'>
                    {(item.rate_bps / 100).toLocaleString(
                      toIntlLocale(i18n.language)
                    )}
                    %
                  </div>
                </TableCell>
                <TableCell>
                  <TeamStatus status={item.status} />
                </TableCell>
                <TableCell>
                  {teamDate(item.created_at, i18n.language)}
                </TableCell>
                <TableCell>{teamDate(item.ready_at, i18n.language)}</TableCell>
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
    </>
  )
}
