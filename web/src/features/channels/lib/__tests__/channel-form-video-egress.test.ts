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
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import type { Channel } from '../../types'
import {
  buildSettingJSON,
  CHANNEL_FORM_DEFAULT_VALUES,
  transformChannelToFormDefaults,
} from '../channel-form'

describe('Dedicated video cache egress setting', () => {
  test('serializes only explicit opt-in and restores it for editing', () => {
    const disabled = JSON.parse(
      buildSettingJSON(CHANNEL_FORM_DEFAULT_VALUES)
    ) as Record<string, unknown>
    assert.equal('video_cache_proxy_enabled' in disabled, false)

    const enabledSettings = buildSettingJSON({
      ...CHANNEL_FORM_DEFAULT_VALUES,
      video_cache_proxy_enabled: true,
    })
    assert.equal(
      (JSON.parse(enabledSettings) as Record<string, unknown>)
        .video_cache_proxy_enabled,
      true
    )

    const channel = {
      ...({} as Channel),
      setting: enabledSettings,
      channel_info: {
        is_multi_key: false,
        multi_key_size: 0,
        multi_key_polling_index: 0,
        multi_key_mode: 'random',
      },
    } as Channel
    assert.equal(
      transformChannelToFormDefaults(channel).video_cache_proxy_enabled,
      true
    )
  })
})

describe('Dedicated upstream egress setting', () => {
  test('serializes only explicit opt-in and restores it for editing', () => {
    const disabled = JSON.parse(
      buildSettingJSON(CHANNEL_FORM_DEFAULT_VALUES)
    ) as Record<string, unknown>
    assert.equal('upstream_egress_proxy_enabled' in disabled, false)

    const enabledSettings = buildSettingJSON({
      ...CHANNEL_FORM_DEFAULT_VALUES,
      upstream_egress_proxy_enabled: true,
    })
    assert.equal(
      (JSON.parse(enabledSettings) as Record<string, unknown>)
        .upstream_egress_proxy_enabled,
      true
    )

    const channel = {
      ...({} as Channel),
      setting: enabledSettings,
      channel_info: {
        is_multi_key: false,
        multi_key_size: 0,
        multi_key_polling_index: 0,
        multi_key_mode: 'random',
      },
    } as Channel
    assert.equal(
      transformChannelToFormDefaults(channel).upstream_egress_proxy_enabled,
      true
    )
  })
})
