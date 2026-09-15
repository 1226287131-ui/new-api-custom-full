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
    <div className='col-span-full min-w-0 space-y-1'>
      <div className='grid grid-cols-3 gap-2'>
        {metrics.map((metric) => (
          <div key={metric.label}>
            <span className='text-muted-foreground block text-xs'>
              {metric.label}
            </span>
            <span className='font-mono text-sm'>{metric.value}</span>
          </div>
        ))}
      </div>
      <div className='text-muted-foreground text-xs'>/ {unit}</div>
      <ScheduledDiscountNotice state={discount} compact />
    </div>
  )
}
