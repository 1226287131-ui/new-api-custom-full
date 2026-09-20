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
import { Save } from 'lucide-react'
import { Controller, useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { z } from 'zod'

import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'

import { saveTeamPolicy } from '../api'
import { parseHundredths, policySchema } from '../lib/money'
import type { TeamPolicy } from '../types'

export function PolicyForm(props: { policy: TeamPolicy; writable: boolean }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const form = useForm<z.infer<typeof policySchema>>({
    resolver: zodResolver(policySchema),
    mode: 'onBlur',
    defaultValues: {
      enabled: props.policy.enabled,
      mode: props.policy.mode,
      credit_rate: String(props.policy.credit_rate_bps / 100),
      cash_rate: String(props.policy.cash_rate_bps / 100),
      freeze_hours: String(props.policy.freeze_hours),
      minimum: String(props.policy.minimum_withdrawal_cents / 100),
    },
  })
  const mutation = useMutation({
    mutationFn: (policy: TeamPolicy) => saveTeamPolicy(policy),
    onSuccess: () => {
      toast.success(t('Saved successfully'))
      void client.invalidateQueries({ queryKey: ['team'] })
    },
    onError: (error: Error) => toast.error(error.message),
  })
  const disabled = !props.writable || mutation.isPending
  return (
    <form
      className='max-w-3xl space-y-5'
      onSubmit={form.handleSubmit((values) => {
        const credit = parseHundredths(values.credit_rate)
        const cash = parseHundredths(values.cash_rate)
        const minimum = parseHundredths(values.minimum)
        if (credit === null || cash === null || minimum === null) return
        mutation.mutate({
          enabled: values.enabled,
          mode: values.mode,
          credit_rate_bps: credit,
          cash_rate_bps: cash,
          freeze_hours: Number(values.freeze_hours),
          minimum_withdrawal_cents: minimum,
        })
      })}
    >
      <FieldGroup>
        <Field orientation='horizontal'>
          <FieldLabel htmlFor='team-enabled'>
            {t('Enable invitation rewards')}
          </FieldLabel>
          <Controller
            control={form.control}
            name='enabled'
            render={({ field }) => (
              <Switch
                id='team-enabled'
                checked={field.value}
                onCheckedChange={field.onChange}
                disabled={disabled}
              />
            )}
          />
        </Field>
        <Field>
          <FieldLabel id='team-mode-label'>
            {t('Default reward type')}
          </FieldLabel>
          <Controller
            control={form.control}
            name='mode'
            render={({ field }) => (
              <ToggleGroup
                aria-labelledby='team-mode-label'
                variant='outline'
                value={[field.value]}
                disabled={disabled}
                onValueChange={(values) => {
                  if (values[0] === 'credit' || values[0] === 'cash') {
                    field.onChange(values[0])
                  }
                }}
              >
                <ToggleGroupItem value='credit'>
                  {t('Site credits')}
                </ToggleGroupItem>
                <ToggleGroupItem value='cash'>
                  {t('Cash rewards')}
                </ToggleGroupItem>
              </ToggleGroup>
            )}
          />
          <FieldDescription>
            {t('Applies until a user selects their own reward type.')}
          </FieldDescription>
        </Field>
        <div className='grid gap-5 sm:grid-cols-2'>
          {(
            [
              [
                'credit_rate',
                t('Site credit reward (%)'),
                t(
                  'Enter a percentage from 0 to 100, with at most two decimals.'
                ),
              ],
              [
                'cash_rate',
                t('Cash reward (%)'),
                t(
                  'Enter a percentage from 0 to 100, with at most two decimals.'
                ),
              ],
              [
                'freeze_hours',
                t('Reward holding period (hours)'),
                t('Enter a whole number from 0 to 8760.'),
              ],
              [
                'minimum',
                t('Minimum withdrawal (CNY)'),
                t(
                  'Enter a positive amount up to 1,000,000,000 CNY, with at most two decimals.'
                ),
              ],
            ] as const
          ).map(([name, label, error]) => (
            <Field key={name} data-invalid={!!form.formState.errors[name]}>
              <FieldLabel htmlFor={`team-policy-${name}`}>{label}</FieldLabel>
              <Input
                id={`team-policy-${name}`}
                inputMode={name === 'freeze_hours' ? 'numeric' : 'decimal'}
                disabled={disabled}
                aria-invalid={!!form.formState.errors[name]}
                {...form.register(name)}
              />
              {form.formState.errors[name] && (
                <FieldDescription role='alert'>{error}</FieldDescription>
              )}
            </Field>
          ))}
        </div>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Credit rewards = actual payment x reward rate / order-time recharge price. Cash rewards = actual payment x reward rate.'
          )}
        </p>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Changes apply to new recharge orders only. Existing orders retain their original reward terms.'
          )}
        </p>
        {!props.writable && (
          <p className='text-muted-foreground text-sm'>
            {t('Only the root administrator can change reward settings.')}
          </p>
        )}
        {props.writable && (
          <Button type='submit' className='w-fit' disabled={disabled}>
            <Save />
            {t('Save reward settings')}
          </Button>
        )}
      </FieldGroup>
    </form>
  )
}
