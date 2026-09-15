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
import { useTranslation } from 'react-i18next'

import {
  formatImageResolutionPrice,
  formatTaskDefaultPrice,
  formatTaskResolutionPrice,
  formatTaskResolutionLabel,
  formatRequestPrice,
  getTaskResolutionPrices,
  hasImageResolutionPricing,
  hasTaskBillingPricing,
  IMAGE_RESOLUTION_TIERS,
} from '../lib/price'
import {
  getScheduledDiscountState,
  useScheduledDiscountClock,
} from '../lib/scheduled-discount'
import type { PricingModel } from '../types'
import type { ModelPriceCellOptions } from './model-price-cell'
import { ScheduledDiscountNotice } from './scheduled-discount-notice'

export function ResolutionPriceCell(props: {
  model: PricingModel
  options?: ModelPriceCellOptions
}) {
  const { t } = useTranslation()
  const now = useScheduledDiscountClock()
  const discount = getScheduledDiscountState(props.model, now)
  const options = props.options ?? {}
  const args = [
    options.showRechargePrice ?? false,
    options.priceRate ?? 1,
    options.usdExchangeRate ?? 1,
    options.selectedGroup,
  ] as const
  let metrics: { label: string; value: string }[]
  let unit =
    props.model.billing_mode === 'per-second' ||
    props.model.task_billing_pricing?.mode === 'per-second'
      ? t('second')
      : t('request')
  if (hasImageResolutionPricing(props.model)) {
    unit = t('Image')
    metrics = IMAGE_RESOLUTION_TIERS.map((tier) => ({
      label: tier,
      value: formatImageResolutionPrice(props.model, tier, ...args),
    }))
  } else {
    metrics = getTaskResolutionPrices(props.model).map(({ tier }) => ({
      label: formatTaskResolutionLabel(tier),
      value: formatTaskResolutionPrice(props.model, tier, ...args),
    }))
    if (metrics.length === 0) {
      metrics = [
        {
          label: t('Price'),
          value: hasTaskBillingPricing(props.model)
            ? formatTaskDefaultPrice(props.model, ...args)
            : formatRequestPrice(props.model, ...args),
        },
      ]
    }
  }
  return (
    <div className='col-span-full flex min-w-0 flex-col gap-1'>
      <dl className='flex min-w-0 flex-wrap items-baseline gap-x-4 gap-y-1'>
        {metrics.map((metric) => (
          <div
            key={metric.label}
            className='inline-flex max-w-full min-w-0 flex-wrap items-baseline gap-x-1.5 gap-y-0.5'
          >
            <dt className='text-muted-foreground text-xs'>{metric.label}</dt>
            <dd className='inline-flex max-w-full min-w-0 flex-nowrap items-baseline gap-1 font-mono text-sm font-semibold tabular-nums'>
              <span className='min-w-0 [overflow-wrap:anywhere]'>
                {metric.value}
              </span>
              <span className='text-muted-foreground shrink-0 font-sans text-xs font-normal whitespace-nowrap'>
                {' '}
                / {unit}
              </span>
            </dd>
          </div>
        ))}
      </dl>
      <ScheduledDiscountNotice state={discount} compact />
    </div>
  )
}
