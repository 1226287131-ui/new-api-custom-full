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
import { describe, expect, it } from 'vitest'

import { buildPricingChanges, type ModelPricingConfig } from '../api'
import {
  applyPriceSyncSelections,
  applyPricingDraft,
  pricingFromDraft,
  pricingOptions,
  pricingRow,
  pricingValuesByModel,
} from '../pricing'

describe('shared model pricing', () => {
  it('preserves explicit zero prices and cache-write configuration', () => {
    expect(
      pricingFromDraft({
        name: 'example',
        billingMode: 'per-token',
        ratio: '0',
        completionRatio: '2',
        cacheRatio: '0',
        createCacheRatio: '1.25',
      })
    ).toEqual({
      'billing_setting.billing_mode': 'ratio',
      ModelRatio: 0,
      CompletionRatio: 2,
      CacheRatio: 0,
      CreateCacheRatio: 1.25,
    })
    expect(
      pricingFromDraft({
        name: 'example',
        billingMode: 'per-request',
        price: '',
      })
    ).not.toHaveProperty('ModelPrice')
    expect(
      pricingFromDraft({
        name: 'example',
        billingMode: 'per-request',
        price: '0',
      })
    ).toHaveProperty('ModelPrice', 0)
  })

  it('keeps token and task expressions intact through both editing and sync', () => {
    for (const expression of [
      'len <= 200000 ? tier("short", p * 2 + cr * 0.2 + cc * 2.5) : tier("long", p * 4)',
      'tier("base", u("seconds") * 0.4)',
    ]) {
      const values = {
        'billing_setting.billing_mode': 'tiered_expr',
        'billing_setting.billing_expr': expression,
        ModelRatio: 1,
      }
      expect(pricingFromDraft(pricingRow('example', values))).toEqual(values)
    }
  })

  it('does not persist another model’s built-in display expression when editing one price', () => {
    const options = pricingOptions({
      ModelPrice: '{"edited":1}',
      BillingMode: '{"builtin":"tiered_expr"}',
      BillingExpr: '{"builtin":"tier(\\"base\\", p * 2)"}',
    })
    const snapshot: ModelPricingConfig = {
      options,
      empty_version: 'empty',
      entries: [
        {
          model_name: 'edited',
          version: 'v1',
          configured: { ModelPrice: 1 },
          effective: { ModelPrice: 1 },
        },
        {
          model_name: 'builtin',
          version: 'empty',
          configured: {},
          effective: {
            'billing_setting.billing_mode': 'tiered_expr',
            'billing_setting.billing_expr': 'tier("base", p * 2)',
          },
        },
      ],
    }
    const after = applyPricingDraft(options, {
      name: 'edited',
      billingMode: 'per-request',
      price: '2',
    })
    expect(buildPricingChanges(snapshot, options, after)).toEqual([
      {
        model_name: 'edited',
        expected_version: 'v1',
        pricing: {
          ModelPrice: 2,
          'billing_setting.billing_mode': 'per-request',
        },
      },
    ])
  })

  it('clears conflicting expression settings when a fixed price is selected for sync', () => {
    const options = pricingOptions({
      ModelRatio: '{"example":1}',
      CreateCacheRatio: '{"example":1.25}',
      BillingMode: '{"example":"tiered_expr"}',
      BillingExpr: '{"example":"tier(\\"base\\", p * 2)"}',
    })
    const after = applyPriceSyncSelections(options, {
      example: { model_price: 0 },
    })
    expect(JSON.parse(after.ModelPrice)).toEqual({ example: 0 })
    expect(JSON.parse(after.ModelRatio)).toEqual({})
    expect(JSON.parse(after.CreateCacheRatio)).toEqual({})
    expect(JSON.parse(after['billing_setting.billing_expr'])).toEqual({})
    expect(JSON.parse(after['billing_setting.billing_mode'])).toEqual({
      example: 'per-request',
    })
  })

  it('rejects invalid prices instead of silently coercing them', () => {
    for (const price of ['-1', 'NaN', 'Infinity', 'invalid']) {
      expect(() =>
        pricingFromDraft({ name: 'example', billingMode: 'per-request', price })
      ).toThrow()
    }
  })
})

it('tracks nested provider prices by model, preserves :: in model names, and ignores key order', () => {
  const key = 'billing_setting.plugin_billing_expr'
  const before = pricingOptions({
    [key]: JSON.stringify({
      'alpha::shared::model': 'u("seconds")',
      'beta::shared::model': 'u("credits")',
      'alpha::other': '1',
    }),
  })
  const snapshot: ModelPricingConfig = {
    options: before,
    empty_version: 'empty',
    entries: [
      {
        model_name: 'shared::model',
        version: 'v1',
        configured: { [key]: { alpha: 'u("seconds")', beta: 'u("credits")' } },
        effective: {},
      },
    ],
  }
  const reordered = {
    ...before,
    [key]: JSON.stringify({
      'alpha::other': '1',
      'beta::shared::model': 'u("credits")',
      'alpha::shared::model': 'u("seconds")',
    }),
  }
  expect(buildPricingChanges(snapshot, before, reordered)).toEqual([])
  const after = {
    ...before,
    [key]: JSON.stringify({
      'beta::shared::model': 'u("credits") * 2',
      'alpha::other': '1',
    }),
  }
  expect(buildPricingChanges(snapshot, before, after)).toEqual([
    {
      model_name: 'shared::model',
      expected_version: 'v1',
      pricing: { [key]: { beta: 'u("credits") * 2' } },
    },
  ])
  const draft = pricingRow('shared::model', snapshot.entries[0].configured)
  expect(pricingFromDraft(draft)[key]).toEqual(
    snapshot.entries[0].configured[key]
  )
  expect(
    pricingRow('alpha::model', {
      ModelPrice: 0.25,
      'billing_setting.plugin_billing_expr': { beta: 'u("images")' },
    })
  ).toMatchObject({
    name: 'alpha::model',
    price: '0.25',
    billingMode: 'per-request',
  })
})

it('retains provider overrides during model-only price synchronization', () => {
  const key = 'billing_setting.plugin_billing_expr'
  const options = pricingOptions({
    [key]: JSON.stringify({ 'alpha::example': 'u("seconds") * 2' }),
  })
  const after = applyPriceSyncSelections(options, {
    example: { billing_expr: 'tier("base", u("seconds"))' },
  })
  expect(after[key]).toEqual(options[key])
})

it('preserves video resolution zero prices and scheduled discounts through editor drafts', () => {
  const task = {
    mode: 'per-second' as const,
    resolution_prices: { '720p': 0, '4k': 0.5 },
  }
  const discount = {
    enabled: true,
    start: '22:00',
    end: '06:00',
    discount: 0.8,
  }
  const values = {
    'billing_setting.billing_mode': 'per-second',
    'billing_setting.task_billing_pricing': task,
    'billing_setting.scheduled_discount': discount,
  }
  expect(pricingFromDraft(pricingRow('video', values))).toEqual(values)
})

it('retains custom resolution pricing and discount schedules when syncing the base price', () => {
  const options = pricingOptions({
    ImageResolutionPrice: '{"image":{"1K":0,"2K":0.2,"4K":0.4}}',
    TaskBillingPricing:
      '{"video":{"mode":"per-second","resolution_prices":{"720p":0}}}',
    ScheduledDiscount:
      '{"video":{"enabled":true,"start":"22:00","end":"06:00","discount":0.8}}',
  })
  const after = applyPriceSyncSelections(options, {
    video: { model_price: 0.1 },
    image: { model_price: 0.2 },
  })
  expect(after.ImageResolutionPrice).toEqual(options.ImageResolutionPrice)
  expect(after['billing_setting.task_billing_pricing']).toEqual(
    options['billing_setting.task_billing_pricing']
  )
  expect(after['billing_setting.scheduled_discount']).toEqual(
    options['billing_setting.scheduled_discount']
  )
  expect(JSON.parse(after['billing_setting.billing_mode']).video).toBe(
    'per-second'
  )
})

it.each([
  ['per-second', 'per-request'],
  ['per-request', 'per-second'],
] as const)(
  'uses the upstream %s unit instead of a local %s unit',
  (source, local) => {
    const options = pricingOptions({
      ModelPrice: '{"video":1}',
      BillingMode: JSON.stringify({ video: local }),
      TaskBillingPricing: JSON.stringify({
        video: { mode: local, resolution_prices: { '720p': 5 } },
      }),
    })
    const after = applyPriceSyncSelections(options, {
      video: { billing_mode: source, model_price: 0.2 },
    })
    expect(pricingValuesByModel(after).get('video')).toEqual({
      ModelPrice: 0.2,
      'billing_setting.billing_mode': source,
    })
  }
)

it('imports upstream image, video and discount pricing without losing zero prices', () => {
  const image = { '1K': 0, '2K': 0.2, '4K': 0.4 }
  const task = {
    mode: 'per-second' as const,
    resolution_prices: { '720p': 0, '4k': 0.3 },
  }
  const discount = {
    enabled: true,
    start: '22:00',
    end: '06:00',
    discount: 0.8,
  }
  const after = applyPriceSyncSelections(pricingOptions({}), {
    image: { billing_mode: 'image_resolution', image_resolution_price: image },
    video: {
      billing_mode: 'per-second',
      task_billing_pricing: task,
      scheduled_discount: discount,
    },
  })
  expect(pricingValuesByModel(after).get('image')).toEqual({
    ImageResolutionPrice: image,
    'billing_setting.billing_mode': 'image_resolution',
  })
  expect(pricingValuesByModel(after).get('video')).toEqual({
    'billing_setting.task_billing_pricing': task,
    'billing_setting.scheduled_discount': discount,
    'billing_setting.billing_mode': 'per-second',
  })
})

it('clears obsolete image tiers when an upstream explicitly switches to a fixed price', () => {
  const options = pricingOptions({
    ImageResolutionPrice: '{"image":{"1K":0.1,"2K":0.2,"4K":0.4}}',
  })
  const after = applyPriceSyncSelections(options, {
    image: { billing_mode: 'per-request', model_price: 0.5 },
  })
  expect(pricingValuesByModel(after).get('image')).toEqual({
    ModelPrice: 0.5,
    'billing_setting.billing_mode': 'per-request',
  })
})

it('keeps an explicit upstream expression active when dormant image prices are included', () => {
  const after = applyPriceSyncSelections(pricingOptions({}), {
    image: {
      billing_mode: 'tiered_expr',
      billing_expr: 'tier("base", p * 2)',
      image_resolution_price: { '1K': 0, '2K': 0.2, '4K': 0.4 },
    },
  })
  expect(pricingValuesByModel(after).get('image')).toMatchObject({
    'billing_setting.billing_mode': 'tiered_expr',
    'billing_setting.billing_expr': 'tier("base", p * 2)',
  })
})

it('commits source provider edits while preserving target providers during batch copy', () => {
  const key = 'billing_setting.plugin_billing_expr'
  const options = pricingOptions({
    [key]: JSON.stringify({ 'alpha::source': '1', 'beta::target': '2' }),
  })
  const after = applyPricingDraft(
    options,
    {
      name: 'source',
      billingMode: 'tiered_expr',
      billingExpr: 'tier("base", u("seconds"))',
      pluginBillingExpr: { alpha: '3' },
    },
    ['source', 'target']
  )
  expect(JSON.parse(after[key])).toEqual({
    'alpha::source': '3',
    'beta::target': '2',
  })
})
