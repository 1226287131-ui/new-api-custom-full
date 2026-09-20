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
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Textarea } from '@/components/ui/textarea'
import { useSecureVerification } from '@/features/auth/secure-verification'

import { saveReferral } from '../api'
import type { ReferralRelation, ReferralUpdate } from '../types'
import { TeamVerification } from './shared'
import { TeamUserSearch } from './team-user-search'

const reasonSchema = z.object({
  reason: z
    .string()
    .trim()
    .min(1)
    .refine((value) => [...value].length <= 200),
})

export function ReferralForm(props: {
  relation: ReferralRelation
  onBusyChange: (busy: boolean) => void
  onRefresh: () => void
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [candidate, setCandidate] = useState({
    id: props.relation.inviter_id,
    username: props.relation.inviter_username,
  })
  const [searching, setSearching] = useState(false)
  const [pending, setPending] = useState<ReferralUpdate | null>(null)
  const form = useForm<z.infer<typeof reasonSchema>>({
    resolver: zodResolver(reasonSchema),
    defaultValues: { reason: '' },
  })
  const verification = useSecureVerification()
  const mutation = useMutation({
    mutationFn: (payload: { body: ReferralUpdate; proof: string }) =>
      saveReferral(props.relation.user_id, payload.body, payload.proof),
    onSuccess: () => {
      toast.success(t('Saved successfully'))
      void client.invalidateQueries({ queryKey: ['team'] })
    },
  })
  const disabled = verification.isActive || mutation.isPending
  const target = `${props.relation.username} (#${props.relation.user_id})`
  const previous = props.relation.inviter_id
    ? `${props.relation.inviter_username} (#${props.relation.inviter_id})`
    : t('Not linked')
  const next = candidate.id
    ? `${candidate.username} (#${candidate.id})`
    : t('Not linked')
  return (
    <section className='space-y-4'>
      <dl className='grid gap-3 sm:grid-cols-2'>
        <div className='min-w-0'>
          <dt className='text-muted-foreground text-sm'>{t('Team member')}</dt>
          <dd className='break-words'>{target}</dd>
        </div>
        <div className='min-w-0'>
          <dt className='text-muted-foreground text-sm'>
            {t('Current referrer')}
          </dt>
          <dd className='break-words'>{previous}</dd>
        </div>
      </dl>
      <FieldGroup className='gap-3'>
        <Field>
          <FieldLabel>{t('New referrer')}</FieldLabel>
          <p className='break-words' role='status'>
            {next}
          </p>
          <div className='flex flex-wrap gap-2'>
            <Button
              type='button'
              variant='outline'
              disabled={disabled}
              onClick={() => setSearching(!searching)}
            >
              {searching ? t('Cancel') : t('Choose referrer')}
            </Button>
            <Button
              type='button'
              variant='outline'
              disabled={disabled || candidate.id === 0}
              onClick={() => {
                setCandidate({ id: 0, username: '' })
                setSearching(false)
              }}
            >
              {t('Remove relationship')}
            </Button>
          </div>
        </Field>
      </FieldGroup>
      {searching && (
        <TeamUserSearch
          label={t('Find a referrer')}
          action={t('Select referrer')}
          excludedID={props.relation.user_id}
          disabled={disabled}
          onSelect={(user) => {
            setCandidate({ id: user.id, username: user.username })
            setSearching(false)
          }}
        />
      )}
      <form
        onSubmit={form.handleSubmit((values) => {
          if (
            disabled ||
            candidate.id === props.relation.user_id ||
            candidate.id === props.relation.inviter_id
          ) {
            return
          }
          setPending({
            expected_inviter_id: props.relation.inviter_id,
            inviter_id: candidate.id,
            reason: values.reason,
          })
        })}
      >
        <FieldGroup className='gap-3'>
          <Field data-invalid={!!form.formState.errors.reason}>
            <FieldLabel htmlFor='team-referral-reason'>
              {t('Reason for change')}
            </FieldLabel>
            <Textarea
              id='team-referral-reason'
              maxLength={400}
              disabled={disabled}
              aria-invalid={!!form.formState.errors.reason}
              {...form.register('reason')}
            />
            {form.formState.errors.reason && (
              <FieldDescription role='alert'>
                {t('Enter a reason (1-200 characters).')}
              </FieldDescription>
            )}
          </Field>
          {mutation.isError && (
            <div className='flex flex-wrap items-center gap-2'>
              <FieldDescription role='alert'>
                {mutation.error.message}
              </FieldDescription>
              <Button
                type='button'
                size='sm'
                variant='outline'
                disabled={disabled}
                onClick={props.onRefresh}
              >
                {t('Refresh')}
              </Button>
            </div>
          )}
          <Button
            type='submit'
            disabled={
              disabled ||
              candidate.id === props.relation.inviter_id ||
              candidate.id === props.relation.user_id
            }
          >
            {t('Review relationship change')}
          </Button>
        </FieldGroup>
      </form>
      <ConfirmDialog
        open={pending !== null}
        onOpenChange={(open) => {
          if (!open) setPending(null)
        }}
        title={t('Confirm relationship change')}
        confirmText={t('Confirm and verify')}
        desc={
          <div className='space-y-2 break-words'>
            <p>
              {t('Team member')}: {target}
            </p>
            <p>
              {t('Current referrer')}: {previous}
            </p>
            <p>
              {t('New referrer')}: {next}
            </p>
            <p>
              {t('Reason')}: {pending?.reason}
            </p>
            <p>
              {t(
                'Relationship changes apply only to newly created recharge orders. Existing orders and rewards are unchanged.'
              )}
            </p>
          </div>
        }
        handleConfirm={async () => {
          if (!pending || disabled) return
          const body = pending
          setPending(null)
          props.onBusyChange(true)
          try {
            const proof = await verification.requestVerification({
              scope: 'team.referral.write',
              context: {
                user_id: props.relation.user_id,
                expected_inviter_id: body.expected_inviter_id,
                inviter_id: body.inviter_id,
                reason: body.reason,
              },
              title: t('Confirm relationship change'),
            })
            if (proof) {
              await mutation.mutateAsync({ body, proof: proof.proof_token })
            }
          } catch {
            // The mutation reports failures through the shared query error handler.
          } finally {
            props.onBusyChange(false)
          }
        }}
      />
      <TeamVerification verification={verification} />
    </section>
  )
}
