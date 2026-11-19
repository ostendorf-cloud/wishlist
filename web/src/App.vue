<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { GovueFooter, GovueHeader, GovueSkipLink, GovueToastHost } from '@ostendorf-cloud/govue'
import { RouterLink, RouterView } from 'vue-router'

import ThemeToggle from './components/ThemeToggle.vue'
import { messages } from './messages'
</script>

<template>
  <GovueSkipLink>{{ messages.app.skip }}</GovueSkipLink>
  <GovueHeader>
    <template #brand>
      <RouterLink :to="{ name: 'dashboard' }" class="text-text">{{ messages.app.name }}</RouterLink>
    </template>
    <template #actions>
      <ThemeToggle />
    </template>
  </GovueHeader>
  <RouterView />
  <GovueFooter :legal="['imprint', 'privacy']" />
  <GovueToastHost />
</template>
