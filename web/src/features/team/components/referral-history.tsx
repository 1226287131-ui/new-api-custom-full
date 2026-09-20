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

import { StaticDataTable } from '@/components/data-table'

import { getReferralHistory } from '../api'
import { teamDate } from '../lib/date'
import { TeamEmpty, TeamError, TeamLoading, TeamPagination } from './shared'

export function ReferralHistory(props: { userID: number }) {
  const { t, i18n } = useTranslation()
  const [page, setPage] = useState(1)
  const query = useQuery({
    queryKey: ['team', 'referral-history', props.userID, page],
    queryFn: () => getReferralHistory(props.userID, page),
  })
  return (
    <section className='space-y-3'>
      <h3 className='font-semibold'>{t('Relationship change history')}</h3>
      {query.isPending && <TeamLoading />}
      {query.isError && <TeamError retry={() => void query.refetch()} />}
      {query.isSuccess && (
        <>
          <StaticDataTable
            data={query.data.items}
            getRowKey={(item) => item.id}
            emptyContent={<TeamEmpty />}
            columns={[
              {
                id: 'old',
                header: t('Previous referrer ID'),
                cell: (item) => item.previous_inviter_id || t('Not linked'),
              },
              {
                id: 'new',
                header: t('New referrer ID'),
                cell: (item) => item.inviter_id || t('Not linked'),
              },
              {
                id: 'actor',
                header: t('Administrator ID'),
                cell: (item) => item.actor_id,
              },
              {
                id: 'reason',
                header: t('Reason'),
                cell: (item) => item.reason,
              },
              {
                id: 'date',
                header: t('Created At'),
                cell: (item) => teamDate(item.created_at, i18n.language),
              },
            ]}
          />
          <TeamPagination
            page={page}
            total={query.data.total}
            loading={query.isFetching}
            onChange={setPage}
          />
        </>
      )}
    </section>
  )
}
