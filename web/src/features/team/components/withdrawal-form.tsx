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
import { useRef } from 'react'
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

import { requestWithdrawal } from '../api'
import { formatCNY, parseHundredths, withdrawalSchema } from '../lib/money'
import type { TeamSelf } from '../types'
import { TeamVerification } from './shared'

export function WithdrawalForm(props: { data: TeamSelf }) {
  const { t, i18n } = useTranslation()
  const client = useQueryClient()
  const attempt = useRef<{ id: string; amount: number } | null>(null)
  const minimum = props.data.policy.minimum_withdrawal_cents
  const available = props.data.wallet.available_cents
  const form = useForm<{ amount: string }>({
    resolver: zodResolver(withdrawalSchema(minimum, available)),
    defaultValues: { amount: '' },
    mode: 'onBlur',
  })
  const mutation = useMutation({
    mutationFn: (payload: { amount: number; id: string; proof?: string }) =>
      requestWithdrawal(payload.amount, payload.id, payload.proof),
    onSuccess: () => {
      attempt.current = null
      form.reset()
      toast.success(t('Withdrawal requested'))
      void client.invalidateQueries({ queryKey: ['team'] })
    },
  })
  const verification = useSecureVerification()
  const disabled =
    !props.data.policy.enabled ||
    !props.data.payout_ready ||
    !props.data.payout.bound ||
    available < minimum ||
    mutation.isPending ||
    verification.isLoading
  function submitWithdrawal(values: { amount: string }) {
    const cents = parseHundredths(values.amount)
    if (cents === null) return
    if (!attempt.current || attempt.current.amount !== cents) {
      attempt.current = { id: crypto.randomUUID(), amount: cents }
    }
    const payload = attempt.current
    void verification.startVerification(
      (proof) => mutation.mutateAsync({ ...payload, proof }),
      { scope: 'team.withdrawal.write', title: t('Confirm withdrawal') }
    )
  }
  return (
    <section className='space-y-4'>
      <h3 className='font-semibold'>{t('Request withdrawal')}</h3>
      <p className='text-muted-foreground text-sm'>
        {t('Minimum withdrawal: {{amount}}', {
          amount: formatCNY(minimum, i18n.language),
        })}
      </p>
      <form
        onSubmit={(event) => void form.handleSubmit(submitWithdrawal)(event)}
      >
        <FieldGroup>
          <Field data-invalid={!!form.formState.errors.amount}>
            <FieldLabel htmlFor='team-withdraw-amount'>
              {t('Withdrawal amount (CNY)')}
            </FieldLabel>
            <Input
              id='team-withdraw-amount'
              inputMode='decimal'
              autoComplete='off'
              disabled={disabled}
              aria-invalid={!!form.formState.errors.amount}
              {...form.register('amount')}
            />
            {form.formState.errors.amount && (
              <FieldDescription role='alert'>
                {t(
                  'Enter an amount within your available balance and withdrawal minimum, with at most two decimals.'
                )}
              </FieldDescription>
            )}
          </Field>
          <Button type='submit' disabled={disabled || verification.open}>
            {t('Request withdrawal')}
          </Button>
        </FieldGroup>
      </form>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Only cash rewards can be withdrawn. Site credits cannot be exchanged for cash.'
        )}
      </p>
      <TeamVerification verification={verification} />
    </section>
  )
}
