<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import {
  GovueBadge,
  GovueButton,
  GovueCard,
  GovueCheckbox,
  GovueConfirmDialog,
  GovueDialog,
  GovueEmptyState,
  GovueErrorState,
  GovueField,
  GovuePage,
  GovueSelect,
  GovueSkeleton,
  useToasts,
} from '@ostendorf-cloud/govue'
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'

import { useOwnerApi } from '../api/owner'
import type { Direction, List, Wish } from '../api/types'
import { describeError, formatDate, formatPrice, type Problem } from '../format'
import { messages } from '../messages'

const props = defineProps<{ id: string }>()

const api = useOwnerApi()
const router = useRouter()
const { notify } = useToasts()

const listId = computed(() => Number(props.id))
const list = ref<List | null>(null)
const wishes = ref<Wish[]>([])
const showArchived = ref(false)
const loading = ref(true)
const problem = ref<Problem | null>(null)

async function load(): Promise<void> {
  loading.value = true
  problem.value = null
  try {
    const detail = await api.list(listId.value, { archived: showArchived.value })
    list.value = detail.list
    wishes.value = detail.wishes
  } catch (error) {
    problem.value = describeError(error)
  } finally {
    loading.value = false
  }
}

onMounted(load)
watch([showArchived, listId], load)

/** Runs one action, tells the owner how it went and reloads. */
async function act(action: () => Promise<unknown>, done: string): Promise<boolean> {
  try {
    await action()
  } catch (error) {
    notify({
      kind: 'error',
      title: messages.errors.actionFailed,
      message: describeError(error).message,
    })
    return false
  }
  notify({ kind: 'success', title: done })
  return true
}

async function toggleListArchive(): Promise<void> {
  if (!list.value) {
    return
  }
  const archived = !list.value.archived
  if (
    await act(
      () => api.archiveList(listId.value, archived),
      archived ? messages.list.archived : messages.list.unarchived,
    )
  ) {
    await load()
  }
}

const confirmingList = ref(false)
const working = ref(false)

async function deleteList(): Promise<void> {
  working.value = true
  const ok = await act(() => api.deleteList(listId.value), messages.list.deleted)
  working.value = false
  confirmingList.value = false
  if (ok) {
    await router.push({ name: 'dashboard' })
  }
}

const wishToDelete = ref<Wish | null>(null)
const confirmingWish = computed({
  get: () => wishToDelete.value !== null,
  set: (open: boolean) => {
    if (!open) {
      wishToDelete.value = null
    }
  },
})

async function deleteWish(): Promise<void> {
  const wish = wishToDelete.value
  if (!wish) {
    return
  }
  working.value = true
  const ok = await act(() => api.deleteWish(wish.id), messages.wish.deleted)
  working.value = false
  wishToDelete.value = null
  if (ok) {
    await load()
  }
}

async function toggleWishArchive(wish: Wish): Promise<void> {
  const archived = !wish.archived
  if (
    await act(
      () => api.archiveWish(wish.id, archived),
      archived ? messages.wish.archived : messages.wish.unarchived,
    )
  ) {
    await load()
  }
}

async function move(wish: Wish, direction: Direction): Promise<void> {
  try {
    await api.moveWish(wish.id, direction)
    await load()
  } catch (error) {
    notify({
      kind: 'error',
      title: messages.errors.actionFailed,
      message: describeError(error).message,
    })
  }
}

/** A wish moves among its own kind: live ones among live ones. */
function canMove(index: number, direction: Direction): boolean {
  const neighbour = wishes.value[index + (direction === 'up' ? -1 : 1)]
  return neighbour !== undefined && neighbour.archived === wishes.value[index]?.archived
}

// Moving a wish to another list.
const wishToMove = ref<Wish | null>(null)
const otherLists = ref<List[]>([])
const target = ref('')
const moveOpen = computed({
  get: () => wishToMove.value !== null,
  set: (open: boolean) => {
    if (!open) {
      wishToMove.value = null
    }
  },
})
const targetOptions = computed(() =>
  otherLists.value.map((other) => ({ value: String(other.id), label: other.title })),
)

async function openMove(wish: Wish): Promise<void> {
  try {
    otherLists.value = (await api.lists()).lists.filter((other) => other.id !== listId.value)
  } catch (error) {
    notify({
      kind: 'error',
      title: messages.errors.actionFailed,
      message: describeError(error).message,
    })
    return
  }
  target.value = otherLists.value[0] ? String(otherLists.value[0].id) : ''
  wishToMove.value = wish
}

async function confirmMove(): Promise<void> {
  const wish = wishToMove.value
  if (!wish || target.value === '') {
    return
  }
  working.value = true
  const ok = await act(() => api.relocateWish(wish.id, Number(target.value)), messages.wish.moved)
  working.value = false
  wishToMove.value = null
  if (ok) {
    await load()
  }
}

function shopAndPrice(wish: Wish): string {
  const price = wish.price_cents === null ? '' : formatPrice(wish.price_cents, wish.currency)
  return [wish.shop, price].filter(Boolean).join(' · ')
}

function priorityLabel(priority: number): string | null {
  if (priority === 2) {
    return messages.wish.mostWanted
  }
  return priority === 0 ? messages.wish.idea : null
}
</script>

<template>
  <GovuePage
    width="shell"
    :title="list?.title ?? messages.common.loading"
    :lead="list?.occasion || undefined"
  >
    <div v-if="loading && !list" role="status" :aria-label="messages.common.loading">
      <GovueSkeleton :lines="4" />
    </div>

    <GovueErrorState
      v-else-if="problem"
      :title="problem.title"
      :message="problem.message"
      :request-id="problem.requestId"
      :retry-label="problem.notFound ? messages.common.back : messages.common.retry"
      @retry="problem.notFound ? router.push({ name: 'dashboard' }) : load()"
    />

    <template v-else-if="list">
      <div class="flex flex-wrap items-center gap-2">
        <GovueBadge v-if="list.archived" kind="warning">{{
          messages.list.archivedBadge
        }}</GovueBadge>
        <GovueBadge v-if="list.event_date">
          {{ messages.dashboard.on(formatDate(list.event_date)) }}
        </GovueBadge>
        <GovueBadge v-if="!list.reserve_open">{{ messages.list.guestsClosed }}</GovueBadge>
      </div>
      <p v-if="list.greeting" class="text-text-muted">{{ list.greeting }}</p>

      <div class="flex flex-wrap gap-2">
        <GovueButton
          variant="primary"
          @click="router.push({ name: 'wish-new', params: { listId: list.id } })"
        >
          {{ messages.list.addWish }}
        </GovueButton>
        <GovueButton @click="router.push({ name: 'list-edit', params: { id: list.id } })">
          {{ messages.list.editList }}
        </GovueButton>
        <GovueButton @click="toggleListArchive">
          {{ list.archived ? messages.list.unarchiveList : messages.list.archiveList }}
        </GovueButton>
        <GovueButton variant="danger" @click="confirmingList = true">
          {{ messages.list.deleteList }}
        </GovueButton>
      </div>

      <GovueCheckbox v-model="showArchived" :label="messages.list.showArchived" />

      <GovueEmptyState
        v-if="wishes.length === 0"
        :title="messages.list.emptyTitle"
        :description="messages.list.emptyText"
      >
        <template #action>
          <GovueButton
            variant="primary"
            @click="router.push({ name: 'wish-new', params: { listId: list.id } })"
          >
            {{ messages.list.addWish }}
          </GovueButton>
        </template>
      </GovueEmptyState>

      <ul v-else class="flex flex-col gap-4" :aria-label="messages.list.wishesHeading">
        <li v-for="(wish, index) in wishes" :key="wish.id">
          <GovueCard as="article" :title="wish.title">
            <div class="flex flex-col gap-2">
              <p v-if="wish.description" class="text-text-muted">{{ wish.description }}</p>
              <p v-if="shopAndPrice(wish)" class="text-text-muted">{{ shopAndPrice(wish) }}</p>
              <a
                v-if="wish.url"
                :href="wish.url"
                target="_blank"
                rel="noopener noreferrer"
                class="text-accent-text underline"
              >
                {{ messages.wish.visit }}
              </a>
              <div class="flex flex-wrap gap-2">
                <GovueBadge v-if="priorityLabel(wish.priority)" kind="info">
                  {{ priorityLabel(wish.priority) }}
                </GovueBadge>
                <GovueBadge v-if="wish.quantity > 1">{{
                  messages.wish.quantity(wish.quantity)
                }}</GovueBadge>
                <GovueBadge v-if="wish.group_gift">{{ messages.wish.groupGift }}</GovueBadge>
                <GovueBadge v-if="wish.archived" kind="warning">
                  {{ messages.wish.archivedBadge }}
                </GovueBadge>
              </div>
            </div>
            <template #footer>
              <GovueButton
                :aria-label="messages.wish.forWish(messages.wish.moveUp, wish.title)"
                :disabled="!canMove(index, 'up')"
                @click="move(wish, 'up')"
              >
                {{ messages.wish.moveUp }}
              </GovueButton>
              <GovueButton
                :aria-label="messages.wish.forWish(messages.wish.moveDown, wish.title)"
                :disabled="!canMove(index, 'down')"
                @click="move(wish, 'down')"
              >
                {{ messages.wish.moveDown }}
              </GovueButton>
              <GovueButton
                :aria-label="messages.wish.forWish(messages.common.edit, wish.title)"
                @click="router.push({ name: 'wish-edit', params: { id: wish.id } })"
              >
                {{ messages.common.edit }}
              </GovueButton>
              <GovueButton
                :aria-label="messages.wish.forWish(messages.wish.moveToList, wish.title)"
                @click="openMove(wish)"
              >
                {{ messages.wish.moveToList }}
              </GovueButton>
              <GovueButton
                :aria-label="
                  messages.wish.forWish(
                    wish.archived ? messages.wish.unarchive : messages.wish.archive,
                    wish.title,
                  )
                "
                @click="toggleWishArchive(wish)"
              >
                {{ wish.archived ? messages.wish.unarchive : messages.wish.archive }}
              </GovueButton>
              <GovueButton
                variant="danger"
                :aria-label="messages.wish.forWish(messages.common.delete, wish.title)"
                @click="wishToDelete = wish"
              >
                {{ messages.common.delete }}
              </GovueButton>
            </template>
          </GovueCard>
        </li>
      </ul>
    </template>

    <GovueConfirmDialog
      v-model:open="confirmingList"
      :title="messages.list.deleteTitle"
      :message="messages.list.deleteText"
      :subject="list?.title ?? ''"
      :confirm-label="messages.common.delete"
      :cancel-label="messages.common.cancel"
      :loading="working"
      @confirm="deleteList"
    />

    <GovueConfirmDialog
      v-model:open="confirmingWish"
      :title="messages.wish.deleteTitle"
      :message="messages.wish.deleteText"
      :subject="wishToDelete?.title ?? ''"
      :confirm-label="messages.common.delete"
      :cancel-label="messages.common.cancel"
      :loading="working"
      @confirm="deleteWish"
    />

    <GovueDialog
      v-model:open="moveOpen"
      :title="messages.wish.moveTitle"
      :description="otherLists.length === 0 ? messages.wish.moveNoLists : messages.wish.moveText"
      size="sm"
    >
      <GovueField v-if="otherLists.length > 0" :label="messages.wish.moveTarget">
        <GovueSelect v-model="target" :options="targetOptions" />
      </GovueField>
      <template #actions>
        <GovueButton @click="moveOpen = false">{{ messages.common.cancel }}</GovueButton>
        <GovueButton
          variant="primary"
          :disabled="otherLists.length === 0"
          :loading="working"
          @click="confirmMove"
        >
          {{ messages.wish.moveConfirm }}
        </GovueButton>
      </template>
    </GovueDialog>
  </GovuePage>
</template>
