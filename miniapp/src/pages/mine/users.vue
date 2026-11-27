<script setup lang="ts">
import { ref, watch } from 'vue'
import { api, type RegisteredUser } from '@/api'
import { account, errorText } from '@/api/http'
import LoadState from '@/components/LoadState.vue'
import { useAdminPage } from '@/utils/adminPage'

const { allowed, loading: roleLoading, error: roleError, refresh } = useAdminPage()
const users = ref<RegisteredUser[]>([])
const loading = ref(false)
const error = ref('')

watch(allowed, async (canView, _, onCleanup) => {
  let current = true
  onCleanup(() => { current = false })
  users.value = []
  error.value = ''
  if (!canView) return
  loading.value = true
  try {
    const list = await api.users()
    if (current) users.value = list
  } catch (e) {
    if (current) error.value = errorText(e)
  } finally {
    if (current) loading.value = false
  }
}, { flush: 'sync' })

function copy(user: RegisteredUser) {
  if (allowed.value) uni.setClipboardData({ data: user.openid })
}
</script>

<template>
  <LoadState v-if="!allowed" :loading="roleLoading" :error="roleError" empty="只有管理员可以查看账号列表" @retry="refresh" />
  <LoadState v-else-if="loading || error || !users.length" :loading="loading" :error="error" empty="暂无注册账号" @retry="refresh" />
  <view v-else>
    <view v-for="user in users" :key="user.id" class="user">
      <view class="heading">
        <text>账号 #{{ user.id }}{{ user.id === account?.id ? '（我）' : '' }} · {{ user.admin ? '管理员' : '成员' }}</text>
        <text class="link" @click="copy(user)">复制 OpenID</text>
      </view>
      <text class="muted openid" selectable>{{ user.openid }}</text>
    </view>
  </view>
</template>

<style scoped>
.user { padding: 24rpx 32rpx; background: #fff; border-bottom: 1rpx solid var(--line); }
.heading { display: flex; align-items: center; justify-content: space-between; margin-bottom: 8rpx; }
.link { font-size: 24rpx; }
.openid { word-break: break-all; }
</style>
