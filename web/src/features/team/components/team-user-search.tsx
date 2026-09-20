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
import { Search } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { getUsers, searchUsers } from '@/features/users/api'
import type { User } from '@/features/users/types'
import { requireServerSuccess } from '@/lib/server-error-message'

import { TeamEmpty, TeamError, TeamLoading, TeamPagination } from './shared'

export function TeamUserSearch(props: {
  label: string
  action: string
  excludedID?: number
  disabled?: boolean
  onSelect: (user: User) => void
}) {
  const { t } = useTranslation()
  const [input, setInput] = useState('')
  const [filter, setFilter] = useState({ keyword: '', page: 1 })
  const query = useQuery({
    queryKey: ['team', 'user-search', filter.keyword, filter.page],
    queryFn: async () => {
      const response = filter.keyword
        ? await searchUsers({
            keyword: filter.keyword,
            p: filter.page,
            page_size: 20,
          })
        : await getUsers({ p: filter.page, page_size: 20 })
      requireServerSuccess(response)
      if (!response.data) throw new Error(t('Failed to load data'))
      return response.data
    },
  })
  return (
    <section className='min-w-0 space-y-3'>
      <form
        className='flex items-center gap-2'
        onSubmit={(event) => {
          event.preventDefault()
          setFilter({ keyword: input.trim(), page: 1 })
        }}
      >
        <Input
          aria-label={props.label}
          placeholder={t('Search by username or user ID')}
          value={input}
          maxLength={100}
          disabled={props.disabled}
          onChange={(event) => setInput(event.target.value)}
        />
        <Button
          type='submit'
          variant='outline'
          size='icon'
          aria-label={t('Search')}
          disabled={props.disabled || query.isFetching}
        >
          <Search />
        </Button>
      </form>
      {query.isPending && <TeamLoading />}
      {query.isError && <TeamError retry={() => void query.refetch()} />}
      {query.isSuccess && (
        <>
          <StaticDataTable
            data={query.data.items}
            getRowKey={(user) => user.id}
            emptyContent={<TeamEmpty />}
            columns={[
              { id: 'id', header: t('User ID'), cell: (user) => user.id },
              {
                id: 'username',
                header: t('Username'),
                cell: (user) => user.username,
              },
              {
                id: 'display',
                header: t('Display name'),
                cell: (user) => user.display_name,
              },
              {
                id: 'actions',
                header: t('Actions'),
                cell: (user) => (
                  <Button
                    size='sm'
                    variant='outline'
                    disabled={props.disabled || user.id === props.excludedID}
                    onClick={() => props.onSelect(user)}
                  >
                    {props.action}
                  </Button>
                ),
              },
            ]}
          />
          <TeamPagination
            page={filter.page}
            total={query.data.total}
            loading={query.isFetching || !!props.disabled}
            onChange={(page) => setFilter({ ...filter, page })}
          />
        </>
      )}
    </section>
  )
}
