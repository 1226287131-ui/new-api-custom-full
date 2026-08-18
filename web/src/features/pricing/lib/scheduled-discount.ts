import { useEffect, useState } from 'react'

import type { PricingModel, ScheduledDiscountConfig } from '../types'

const TIME_PATTERN = /^(?:[01]\d|2[0-3]):[0-5]\d$/
const BEIJING_TIME_ZONE = 'Asia/Shanghai'

export type ScheduledDiscountState = {
  configured: boolean
  active: boolean
  start: string
  end: string
  discount: number
  percent: number
}

const EMPTY_STATE: ScheduledDiscountState = {
  configured: false,
  active: false,
  start: '',
  end: '',
  discount: 1,
  percent: 0,
}

function parseMinutes(value: string): number | null {
  if (!TIME_PATTERN.test(value)) return null
  const [hour, minute] = value.split(':').map(Number)
  return hour * 60 + minute
}

function getBeijingMinutes(at: Date): number | null {
  try {
    const parts = new Intl.DateTimeFormat('en-GB', {
      timeZone: BEIJING_TIME_ZONE,
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
    }).formatToParts(at)
    const hour = Number(parts.find((part) => part.type === 'hour')?.value)
    const minute = Number(parts.find((part) => part.type === 'minute')?.value)
    if (!Number.isFinite(hour) || !Number.isFinite(minute)) return null
    return hour * 60 + minute
  } catch {
    return null
  }
}

function isValidConfig(
  config: ScheduledDiscountConfig | undefined
): config is ScheduledDiscountConfig {
  if (!config?.enabled) return false
  const start = parseMinutes(config.start)
  const end = parseMinutes(config.end)
  return (
    start !== null &&
    end !== null &&
    start !== end &&
    Number.isFinite(config.discount) &&
    config.discount > 0 &&
    config.discount <= 1
  )
}

export function getScheduledDiscountState(
  model: PricingModel,
  at = new Date()
): ScheduledDiscountState {
  const config = model.scheduled_discount
  if (!isValidConfig(config)) return EMPTY_STATE

  const start = parseMinutes(config.start) as number
  const end = parseMinutes(config.end) as number
  const current = getBeijingMinutes(at)
  let active = false
  if (current !== null) {
    active =
      start < end
        ? current >= start && current < end
        : current >= start || current < end
  }

  return {
    configured: true,
    active,
    start: config.start,
    end: config.end,
    discount: config.discount,
    percent: Math.max(0, Number(((1 - config.discount) * 100).toFixed(2))),
  }
}

/** Re-render pricing shortly after a Beijing-time window changes state. */
export function useScheduledDiscountClock(intervalMs = 15_000): Date {
  const [now, setNow] = useState(() => new Date())

  useEffect(() => {
    const timer = window.setInterval(() => setNow(new Date()), intervalMs)
    return () => window.clearInterval(timer)
  }, [intervalMs])

  return now
}

export function getScheduledDiscountMultiplier(
  model: PricingModel,
  at = new Date()
): number {
  const state = getScheduledDiscountState(model, at)
  return state.active ? state.discount : 1
}
