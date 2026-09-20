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

import { Dialog } from '@/components/dialog'

import { getReferral } from '../api'
import { ReferralForm } from './referral-form'
import { ReferralHistory } from './referral-history'
import { TeamError, TeamLoading } from './shared'
import { TeamUserSearch } from './team-user-search'

export function ReferralManagement() {
  const { t } = useTranslation()
  const [userID, setUserID] = useState<number | null>(null)
  return (
    <>
      <TeamUserSearch
        label={t('Find a team member')}
        action={t('Manage relationship')}
        onSelect={(user) => setUserID(user.id)}
      />
      {userID !== null && (
        <ReferralDialog
          key={userID}
          userID={userID}
          onClose={() => setUserID(null)}
        />
      )}
    </>
  )
}

function ReferralDialog(props: { userID: number; onClose: () => void }) {
  const { t } = useTranslation()
  const [busy, setBusy] = useState(false)
  const relation = useQuery({
    queryKey: ['team', 'referral', props.userID],
    queryFn: () => getReferral(props.userID),
  })
  return (
    <Dialog
      open
      title={t('Invitation relationship')}
      description={t(
        'Relationship changes apply only to newly created recharge orders. Existing orders and rewards are unchanged.'
      )}
      showCloseButton={!busy}
      onOpenChange={(open) => {
        if (!open && !busy) props.onClose()
      }}
    >
      <div className='space-y-6'>
        {relation.isPending && <TeamLoading />}
        {relation.isError && (
          <TeamError retry={() => void relation.refetch()} />
        )}
        {relation.isSuccess && (
          <ReferralForm
            key={`${relation.data.user_id}-${relation.data.inviter_id}`}
            relation={relation.data}
            onBusyChange={setBusy}
            onRefresh={() => void relation.refetch()}
          />
        )}
        <ReferralHistory userID={props.userID} />
      </div>
    </Dialog>
  )
}
