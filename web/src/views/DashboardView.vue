<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import {
  GovueBadge,
  GovueButton,
  GovueCard,
  GovueCheckbox,
  GovueEmptyState,
  GovueErrorState,
  GovuePage,
  GovueSkeleton,
} from '@ostendorf-cloud/govue'
import { onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'

import { useOwnerApi } from '../api/owner'
import type { List } from '../api/types'
import { describeError, formatDate, type Problem } from '../format'
import { messages } from '../messages'

const api = useOwnerApi()
const router = useRouter()

const lists = ref<List[]>([])
const showArchived = ref(false)
const loading = ref(true)
const problem = ref<Problem | null>(null)

async function load(): Promise<void> {
  loading.value = true
  problem.value = null
  try {
    lists.value = (await api.lists({ archived: showArchived.value })).lists
  } catch (error) {
    problem.value = describeError(error)
  } finally {
    loading.value = false
  }
}

onMounted(load)
watch(showArchived, load)

function open(list: List): void {
  void router.push({ name: 'list', params: { id: list.id } })
}
</script>

<template>
  <GovuePage width="shell" :title="messages.dashboard.title" :lead="messages.dashboard.lead">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <GovueCheckbox v-model="showArchived" :label="messages.dashboard.showArchived" />
      <GovueButton variant="primary" @click="router.push({ name: 'list-new' })">
        {{ messages.dashboard.newList }}
      </GovueButton>
    </div>

    <div v-if="loading" role="status" :aria-label="messages.common.loading">
      <GovueSkeleton :lines="4" />
    </div>

    <GovueErrorState
      v-else-if="problem"
      :title="problem.title"
      :message="problem.message"
      :request-id="problem.requestId"
      :retry-label="messages.common.retry"
      @retry="load"
    />

    <GovueEmptyState
      v-else-if="lists.length === 0"
      :title="messages.dashboard.emptyTitle"
      :description="messages.dashboard.emptyText"
    >
      <template #action>
        <GovueButton variant="primary" @click="router.push({ name: 'list-new' })">
          {{ messages.dashboard.newList }}
        </GovueButton>
      </template>
    </GovueEmptyState>

    <ul v-else class="grid gap-4 sm:grid-cols-2" :aria-label="messages.dashboard.title">
      <li v-for="list in lists" :key="list.id">
        <GovueCard as="article" :title="list.title">
          <div class="flex flex-col gap-2">
            <p v-if="list.occasion" class="text-text-muted">{{ list.occasion }}</p>
            <p v-if="list.event_date" class="text-text-muted">
              {{ messages.dashboard.on(formatDate(list.event_date)) }}
            </p>
            <div class="flex flex-wrap gap-2">
              <GovueBadge>{{ messages.dashboard.wishCount(list.wish_count) }}</GovueBadge>
              <GovueBadge v-if="list.archived" kind="warning">
                {{ messages.dashboard.archivedBadge }}
              </GovueBadge>
              <GovueBadge v-if="list.shared" kind="info">
                {{ messages.dashboard.sharedBadge }}
              </GovueBadge>
            </div>
          </div>
          <template #footer>
            <GovueButton variant="secondary" @click="open(list)">
              {{ messages.dashboard.openList(list.title) }}
            </GovueButton>
          </template>
        </GovueCard>
      </li>
    </ul>
  </GovuePage>
</template>
