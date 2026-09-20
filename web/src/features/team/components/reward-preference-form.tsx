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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Save } from 'lucide-react'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'

import { saveRewardPreference } from '../api'
import type { TeamSelf } from '../types'

export function RewardPreferenceForm(props: { data: TeamSelf }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const submitLocked = useRef(false)
  const [mode, setMode] = useState(props.data.effective_reward_mode)
  const mutation = useMutation({
    mutationFn: saveRewardPreference,
    onSuccess: () => {
      toast.success(t('Saved successfully'))
      void client.invalidateQueries({ queryKey: ['team', 'self'] })
    },
  })
  const disabled =
    mutation.isPending || (mode === 'cash' && !props.data.payout_ready)
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault()
        if (
          submitLocked.current ||
          disabled ||
          mode === props.data.reward_preference.mode
        ) {
          return
        }
        submitLocked.current = true
        mutation.mutate(mode, {
          onSettled: () => {
            submitLocked.current = false
          },
        })
      }}
    >
      <FieldGroup className='gap-3'>
        <Field>
          <FieldLabel id='team-preference-label'>
            {t('My reward type')}
          </FieldLabel>
          <div className='flex flex-wrap items-center gap-3'>
            <ToggleGroup
              aria-labelledby='team-preference-label'
              className='grid w-full grid-cols-2 sm:w-auto'
              variant='outline'
              value={[mode]}
              disabled={mutation.isPending}
              onValueChange={(values) => {
                if (values[0] === 'credit' || values[0] === 'cash') {
                  setMode(values[0])
                }
              }}
            >
              <ToggleGroupItem
                value='credit'
                className='h-auto min-h-10 min-w-0 whitespace-normal'
              >
                {t('Site credits')} · {props.data.policy.credit_rate_bps / 100}%
              </ToggleGroupItem>
              <ToggleGroupItem
                value='cash'
                className='h-auto min-h-10 min-w-0 whitespace-normal'
                disabled={!props.data.payout_ready}
              >
                {t('Cash rewards')} · {props.data.policy.cash_rate_bps / 100}%
              </ToggleGroupItem>
            </ToggleGroup>
            <Button
              type='submit'
              variant='outline'
              disabled={disabled || mode === props.data.reward_preference.mode}
            >
              <Save />
              {t('Save reward type')}
            </Button>
          </div>
          <FieldDescription>
            {t(
              'Changes apply to new recharge orders only. Existing orders retain their original reward terms.'
            )}
          </FieldDescription>
          {!props.data.payout_ready && (
            <FieldDescription role='status'>
              {t('Cash withdrawals are not configured yet.')}
            </FieldDescription>
          )}
          {mutation.isError && (
            <FieldDescription role='alert'>
              {mutation.error.message}
            </FieldDescription>
          )}
        </Field>
      </FieldGroup>
    </form>
  )
}
