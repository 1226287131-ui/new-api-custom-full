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

import { SectionPageLayout } from '@/components/layout'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { getTeamPolicy } from './api'
import { CommissionTable } from './components/commission-table'
import { PolicyForm } from './components/policy-form'
import { TeamError, TeamLoading } from './components/shared'
import { WithdrawalTable } from './components/withdrawal-table'

export function TeamManagement() {
  const { t } = useTranslation()
  const [status, setStatus] = useState('')
  const role = useAuthStore((state) => state.auth.user?.role ?? 0)
  const policy = useQuery({
    queryKey: ['team', 'policy'],
    queryFn: getTeamPolicy,
  })
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Invitation rewards')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <Tabs defaultValue='withdrawals' className='min-w-0'>
          <TabsList variant='line' className='max-w-full overflow-x-auto'>
            <TabsTrigger value='withdrawals'>
              {t('Withdrawal review')}
            </TabsTrigger>
            <TabsTrigger value='rewards'>{t('Reward records')}</TabsTrigger>
            <TabsTrigger value='settings'>{t('Reward settings')}</TabsTrigger>
          </TabsList>
          <TabsContent value='withdrawals' className='space-y-4 pt-4'>
            <NativeSelect
              aria-label={t('Withdrawal status')}
              value={status}
              onChange={(event) => setStatus(event.target.value)}
            >
              <NativeSelectOption value=''>
                {t('All statuses')}
              </NativeSelectOption>
              <NativeSelectOption value='pending'>
                {t('Pending')}
              </NativeSelectOption>
              <NativeSelectOption value='approved'>
                {t('Approved')}
              </NativeSelectOption>
              <NativeSelectOption value='paid'>{t('Paid')}</NativeSelectOption>
              <NativeSelectOption value='rejected'>
                {t('Rejected')}
              </NativeSelectOption>
            </NativeSelect>
            <WithdrawalTable key={status} admin status={status} />
          </TabsContent>
          <TabsContent value='rewards' className='pt-4'>
            <CommissionTable admin />
          </TabsContent>
          <TabsContent value='settings' className='pt-5'>
            {policy.isPending && <TeamLoading />}
            {policy.isError && (
              <TeamError retry={() => void policy.refetch()} />
            )}
            {policy.isSuccess && (
              <PolicyForm
                key={JSON.stringify(policy.data)}
                policy={policy.data}
                writable={role >= ROLE.SUPER_ADMIN}
              />
            )}
          </TabsContent>
        </Tabs>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
