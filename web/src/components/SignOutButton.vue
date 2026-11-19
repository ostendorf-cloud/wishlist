<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { GovueButton, useToasts } from '@ostendorf-cloud/govue'
import { useRouter } from 'vue-router'

import { useAuth } from '../api/auth'
import { messages } from '../messages'

const auth = useAuth()
const router = useRouter()
const { notify } = useToasts()

async function signOut(): Promise<void> {
  try {
    await auth.signOut()
  } catch {
    // The server may still hold the session: say so instead of pretending.
    notify({
      kind: 'error',
      title: messages.auth.signOutFailed,
      message: messages.auth.signOutFailedText,
    })
    return
  }
  await router.push({ name: 'login' })
}
</script>

<template>
  <GovueButton v-if="auth.state.signedIn" variant="quiet" @click="signOut">
    {{ messages.auth.signOut }}
  </GovueButton>
</template>
