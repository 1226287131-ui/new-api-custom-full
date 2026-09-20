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
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { useSecureVerification } from '@/features/auth/secure-verification'

import { savePayout } from '../api'
import { payoutSchema } from '../lib/money'
import type { PayoutAccount, TeamSelf } from '../types'
import { TeamVerification } from './shared'

export function PayoutForm(props: { data: TeamSelf }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const form = useForm<PayoutAccount>({
    resolver: zodResolver(payoutSchema),
    defaultValues: { account: '', name: '' },
    mode: 'onBlur',
  })
  const mutation = useMutation({
    mutationFn: (payload: { body: PayoutAccount; proof: string }) =>
      savePayout(payload.body, payload.proof),
    onSuccess: () => {
      form.reset()
      toast.success(t('Saved successfully'))
      void client.invalidateQueries({ queryKey: ['team', 'self'] })
    },
  })
  const verification = useSecureVerification()
  const disabled =
    !props.data.payout_ready || mutation.isPending || verification.isActive
  return (
    <section className='space-y-4'>
      <h3 className='font-semibold'>{t('Alipay payout account')}</h3>
      <p className='text-muted-foreground text-sm'>
        {props.data.payout.bound
          ? `${props.data.payout.account_masked} · ${props.data.payout.name_masked}`
          : t('Not linked')}
      </p>
      {!props.data.payout_ready && (
        <p role='status' className='text-muted-foreground text-sm'>
          {t('Cash withdrawals are not configured yet.')}
        </p>
      )}
      <form
        onSubmit={form.handleSubmit(async (body) => {
          if (disabled) return
          const proof = await verification.requestVerification({
            scope: 'team.payout.write',
            context: { ...body },
            title: t('Confirm payout account'),
          })
          if (!proof) return
          mutation.mutate({ body, proof: proof.proof_token })
        })}
      >
        <FieldGroup>
          <Field data-invalid={!!form.formState.errors.account}>
            <FieldLabel htmlFor='team-account'>
              {t('Alipay account')}
            </FieldLabel>
            <Input
              id='team-account'
              autoComplete='off'
              maxLength={128}
              disabled={disabled}
              aria-invalid={!!form.formState.errors.account}
              {...form.register('account')}
            />
            {form.formState.errors.account && (
              <FieldDescription role='alert'>
                {t('Enter a valid Alipay account (3-128 characters).')}
              </FieldDescription>
            )}
          </Field>
          <Field data-invalid={!!form.formState.errors.name}>
            <FieldLabel htmlFor='team-name'>
              {t('Account holder name')}
            </FieldLabel>
            <Input
              id='team-name'
              autoComplete='off'
              maxLength={128}
              disabled={disabled}
              aria-invalid={!!form.formState.errors.name}
              {...form.register('name')}
            />
            {form.formState.errors.name && (
              <FieldDescription role='alert'>
                {t('Enter the account holder name (2-128 characters).')}
              </FieldDescription>
            )}
          </Field>
          <Button type='submit' disabled={disabled}>
            {t('Save payout account')}
          </Button>
        </FieldGroup>
      </form>
      <TeamVerification verification={verification} />
    </section>
  )
}
