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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Copy, RefreshCw, Settings } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { SectionPageLayout } from '@/components/layout'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useAffiliate } from '@/features/wallet/hooks/use-affiliate'
import { toIntlLocale } from '@/i18n/languages'
import { formatQuota } from '@/lib/format'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { getTeamSelf } from './api'
import { CommissionTable } from './components/commission-table'
import { PayoutForm } from './components/payout-form'
import { TeamError, TeamLoading } from './components/shared'
import { WithdrawalForm } from './components/withdrawal-form'
import { WithdrawalTable } from './components/withdrawal-table'
import { formatCNY } from './lib/money'

export function Team() {
  const { t, i18n } = useTranslation()
  const client = useQueryClient()
  const role = useAuthStore((state) => state.auth.user?.role ?? 0)
  const query = useQuery({ queryKey: ['team', 'self'], queryFn: getTeamSelf })
  const affiliate = useAffiliate()
  const data = query.data
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('My team')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        {role >= ROLE.ADMIN && (
          <Button
            variant='outline'
            render={<Link to='/team/manage' />}
            nativeButton={false}
          >
            <Settings />
            {t('Reward management')}
          </Button>
        )}
        <Button
          variant='outline'
          size='icon'
          title={t('Refresh')}
          aria-label={t('Refresh')}
          disabled={query.isFetching}
          onClick={() => void client.invalidateQueries({ queryKey: ['team'] })}
        >
          <RefreshCw />
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='mx-auto max-w-7xl space-y-6'>
          {query.isPending && <TeamLoading />}
          {query.isError && <TeamError retry={() => void query.refetch()} />}
          {query.isSuccess && data && (
            <>
              <section className='space-y-4 border-b pb-5'>
                <div className='flex flex-wrap items-center gap-2'>
                  <h3 className='font-semibold'>{t('Invitation rewards')}</h3>
                  <Badge variant='secondary'>
                    {data.policy.enabled ? t('Enabled') : t('Disabled')}
                  </Badge>
                  <span className='text-muted-foreground text-sm'>
                    {data.policy.mode === 'cash'
                      ? t('Cash rewards')
                      : t('Site credits')}{' '}
                    ·{' '}
                    {(data.policy.mode === 'cash'
                      ? data.policy.cash_rate_bps
                      : data.policy.credit_rate_bps) / 100}
                    %
                  </span>
                </div>
                <div className='flex max-w-2xl items-center gap-2'>
                  <Input
                    aria-label={t('Referral link')}
                    value={affiliate.affiliateLink}
                    readOnly
                    disabled={affiliate.loading}
                    className='min-w-0 font-mono text-xs'
                  />
                  {affiliate.affiliateLink ? (
                    <CopyButton
                      value={affiliate.affiliateLink}
                      tooltip={t('Copy referral link')}
                      aria-label={t('Copy referral link')}
                      variant='outline'
                    />
                  ) : (
                    <Button
                      variant='outline'
                      size='icon'
                      disabled
                      aria-label={t('Copy referral link')}
                    >
                      <Copy />
                    </Button>
                  )}
                </div>
                {!affiliate.loading && !affiliate.affiliateLink && (
                  <Button
                    variant='outline'
                    size='sm'
                    onClick={() => void affiliate.refetch()}
                  >
                    {t('Retry')}
                  </Button>
                )}
                <p className='text-muted-foreground text-sm'>
                  {t(
                    'Direct invitations only. New paid Epay recharge orders are eligible; manual credits, gifts and historical orders are excluded.'
                  )}
                </p>
                {!data.policy.enabled && (
                  <Alert>
                    <AlertDescription>
                      {t(
                        'Invitation rewards are currently disabled. Existing reward and withdrawal records remain available.'
                      )}
                    </AlertDescription>
                  </Alert>
                )}
              </section>
              <dl className='grid grid-cols-2 gap-x-6 gap-y-5 border-b pb-5 lg:grid-cols-4'>
                {[
                  [
                    t('Invited users'),
                    data.summary.referred_users.toLocaleString(
                      toIntlLocale(i18n.language)
                    ),
                  ],
                  [
                    t('Rewarded orders'),
                    data.summary.order_count.toLocaleString(
                      toIntlLocale(i18n.language)
                    ),
                  ],
                  [
                    t('Credited to balance'),
                    formatQuota(data.summary.settled_credit_quota),
                  ],
                  [
                    t('Pending site credits'),
                    formatQuota(data.summary.pending_credit_quota),
                  ],
                  [
                    t('Available cash'),
                    formatCNY(data.wallet.available_cents, i18n.language),
                  ],
                  [
                    t('Pending cash rewards'),
                    formatCNY(data.summary.pending_cash_cents, i18n.language),
                  ],
                  [
                    t('Withdrawal reserved'),
                    formatCNY(data.wallet.frozen_cents, i18n.language),
                  ],
                  [
                    t('Withdrawn cash'),
                    formatCNY(data.wallet.paid_cents, i18n.language),
                  ],
                ].map(([label, value]) => (
                  <div key={label} className='min-w-0'>
                    <dt className='text-muted-foreground mb-1 text-xs'>
                      {label}
                    </dt>
                    <dd className='text-lg font-semibold break-words tabular-nums'>
                      {value}
                    </dd>
                  </div>
                ))}
              </dl>
              <Tabs defaultValue='rewards' className='min-w-0'>
                <TabsList variant='line' className='max-w-full overflow-x-auto'>
                  <TabsTrigger value='rewards'>
                    {t('Reward records')}
                  </TabsTrigger>
                  <TabsTrigger value='withdrawals'>
                    {t('Withdrawal records')}
                  </TabsTrigger>
                  <TabsTrigger value='payout'>
                    {t('Payout and withdrawal')}
                  </TabsTrigger>
                </TabsList>
                <TabsContent value='rewards' className='pt-4'>
                  <CommissionTable />
                </TabsContent>
                <TabsContent value='withdrawals' className='pt-4'>
                  <WithdrawalTable />
                </TabsContent>
                <TabsContent value='payout' className='pt-5'>
                  <div className='grid max-w-4xl gap-8 md:grid-cols-2'>
                    <PayoutForm data={data} />
                    <WithdrawalForm data={data} />
                  </div>
                </TabsContent>
              </Tabs>
            </>
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
