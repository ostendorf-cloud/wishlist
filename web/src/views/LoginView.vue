<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { GovueLoginForm, GovuePage } from '@ostendorf-cloud/govue'
import { RouterLink, useRouter } from 'vue-router'

import { useAuth } from '../api/auth'
import { messages } from '../messages'

const auth = useAuth()
const router = useRouter()

// The cookie is set by the server. The page asks who it is now, which also
// brings the CSRF token of the new session.
async function signedIn(): Promise<void> {
  await auth.refresh()
  if (auth.state.signedIn) {
    await router.push({ name: 'dashboard' })
  }
}
</script>

<template>
  <GovuePage width="reading" :title="messages.auth.loginTitle" :lead="messages.auth.loginLead">
    <GovueLoginForm :client="auth.client" @success="signedIn" />
    <p v-if="auth.state.registrationOpen" class="mt-4 text-sm">
      <RouterLink :to="{ name: 'signup' }">{{ messages.auth.signupLink }}</RouterLink>
    </p>
  </GovuePage>
</template>
