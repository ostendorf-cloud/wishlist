<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import {
  GovuePage,
  GovueSignupForm,
  GovueTotpEnrolment,
  type SignupResult,
} from '@ostendorf-cloud/govue'
import { ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'

import { useAuth } from '../api/auth'
import { messages } from '../messages'

const auth = useAuth()
const router = useRouter()

// The secret is shown once. It lives here until the code confirmed it and is
// dropped after that.
const signup = ref<SignupResult | null>(null)

async function confirmed(): Promise<void> {
  signup.value = null
  await auth.refresh()
  if (auth.state.signedIn) {
    await router.push({ name: 'dashboard' })
  }
}
</script>

<template>
  <GovuePage
    v-if="!auth.state.registrationOpen && !signup"
    width="reading"
    :title="messages.auth.signupClosedTitle"
    :lead="messages.auth.signupClosed"
  >
    <p class="text-sm">
      <RouterLink :to="{ name: 'login' }">{{ messages.auth.loginLink }}</RouterLink>
    </p>
  </GovuePage>

  <GovuePage
    v-else-if="signup"
    width="reading"
    :title="messages.auth.enrolTitle"
    :lead="messages.auth.enrolLead"
  >
    <GovueTotpEnrolment
      :client="auth.client"
      :login="signup.login"
      :enrolment="signup.enrolment"
      @confirmed="confirmed"
    />
  </GovuePage>

  <GovuePage
    v-else
    width="reading"
    :title="messages.auth.signupTitle"
    :lead="messages.auth.signupLead"
  >
    <GovueSignupForm
      :client="auth.client"
      @signed-up="(result: SignupResult) => (signup = result)"
    />
    <p class="mt-4 text-sm">
      <RouterLink :to="{ name: 'login' }">{{ messages.auth.loginLink }}</RouterLink>
    </p>
  </GovuePage>
</template>
