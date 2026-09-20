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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { useSecureVerification } from '@/features/auth/secure-verification'

import { getWithdrawalPayout, reviewWithdrawal } from '../api'
import { formatCNY } from '../lib/money'
import type { PayoutAccount, Withdrawal, WithdrawalReview } from '../types'
import { TeamStatus, TeamVerification } from './shared'

export function WithdrawalReviewDialog(props: {
  item: Withdrawal
  onClose: () => void
}) {
  const { t, i18n } = useTranslation()
  const client = useQueryClient()
  const [payout, setPayout] = useState<PayoutAccount | null>(null)
  const [reference, setReference] = useState('')
  const [reason, setReason] = useState('')
  const verification = useSecureVerification()
  const mutation = useMutation({
    mutationFn: (payload: { body: WithdrawalReview; proof?: string }) =>
      reviewWithdrawal(props.item.id, payload.body, payload.proof),
    onSuccess: () => {
      toast.success(t('Saved successfully'))
      void client.invalidateQueries({ queryKey: ['team'] })
      props.onClose()
    },
  })
  function review(action: WithdrawalReview['action']) {
    void verification.startVerification(
      (proof) =>
        mutation.mutateAsync({
          body: { action, reference: reference.trim(), reason: reason.trim() },
          proof,
        }),
      {
        scope: 'team.withdrawal.review',
        title:
          action === 'paid'
            ? t('Confirm payment completed')
            : t('Review withdrawal'),
        description: t(
          'Verify the amount and payout account before confirming. This action does not send money automatically.'
        ),
      }
    )
  }
  const busy = verification.open || mutation.isPending
  const reviewable =
    props.item.status === 'pending' || props.item.status === 'approved'
  return (
    <>
      <Dialog
        open
        onOpenChange={(open) => {
          if (!open && !busy) props.onClose()
        }}
        title={t('Review withdrawal')}
      >
        <div className='space-y-5'>
          <div className='flex flex-wrap items-center gap-3'>
            <span className='text-xl font-semibold tabular-nums'>
              {formatCNY(props.item.amount_cents, i18n.language)}
            </span>
            <TeamStatus status={props.item.status} />
            <span className='text-muted-foreground text-sm'>
              #{props.item.id}
            </span>
          </div>
          <div className='text-sm break-all'>
            {payout?.account || props.item.account_masked}
            <br />
            {payout?.name || props.item.name_masked}
          </div>
          <Button
            variant='outline'
            disabled={busy}
            onClick={() =>
              void verification.startVerification(
                async (proof) => {
                  const result = await getWithdrawalPayout(props.item.id, proof)
                  setPayout(result)
                },
                {
                  scope: 'team.withdrawal.read',
                  title: t('Reveal payout account'),
                }
              )
            }
          >
            {t('Reveal payout account')}
          </Button>
          <Alert>
            <AlertDescription>
              {t(
                'Verify the amount and payout account before confirming. This action does not send money automatically.'
              )}
            </AlertDescription>
          </Alert>
          {reviewable && (
            <FieldGroup>
              {props.item.status === 'approved' && (
                <Field>
                  <FieldLabel htmlFor='team-payment-reference'>
                    {t('Payment reference')}
                  </FieldLabel>
                  <Input
                    id='team-payment-reference'
                    maxLength={128}
                    value={reference}
                    onChange={(event) => setReference(event.target.value)}
                    disabled={busy}
                  />
                </Field>
              )}
              <Field>
                <FieldLabel htmlFor='team-reject-reason'>
                  {t('Rejection reason')}
                </FieldLabel>
                <Textarea
                  id='team-reject-reason'
                  maxLength={512}
                  value={reason}
                  onChange={(event) => setReason(event.target.value)}
                  disabled={busy}
                />
              </Field>
              <div className='flex flex-wrap gap-2'>
                {props.item.status === 'pending' && (
                  <Button disabled={busy} onClick={() => review('approve')}>
                    {t('Approve withdrawal')}
                  </Button>
                )}
                {props.item.status === 'approved' && (
                  <Button
                    disabled={busy || !reference.trim()}
                    onClick={() => review('paid')}
                  >
                    {t('Mark as paid')}
                  </Button>
                )}
                <Button
                  variant='destructive'
                  disabled={busy || !reason.trim()}
                  onClick={() => review('reject')}
                >
                  {t('Reject and release funds')}
                </Button>
              </div>
            </FieldGroup>
          )}
        </div>
      </Dialog>
      <TeamVerification verification={verification} />
    </>
  )
}
