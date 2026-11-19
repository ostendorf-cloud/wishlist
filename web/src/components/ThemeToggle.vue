<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// govue's own GovueThemeSwitch is merged but not in a release yet: v0.4.0, the
// version this repository pins, ships only useTheme. This is a thin stand in
// on govue's button and composable. Replace it with GovueThemeSwitch when the
// govue pin moves to a release that has it.
import { GovueButton, useTheme } from '@ostendorf-cloud/govue'
import { computed } from 'vue'

import { messages } from '../messages'

const { mode, cycleMode } = useTheme()

const current = computed(() => messages.app.theme[mode.value])
</script>

<template>
  <GovueButton
    variant="quiet"
    :aria-label="`${messages.app.theme.label}: ${current}`"
    @click="cycleMode"
  >
    {{ current }}
  </GovueButton>
</template>
