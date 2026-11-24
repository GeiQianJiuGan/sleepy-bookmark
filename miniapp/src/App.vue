<script setup lang="ts">
import { onLaunch } from '@dcloudio/uni-app'
import { ensureLogin } from '@/api/http'

onLaunch(() => {
  // Pages retry the login themselves; this only warms the token up.
  ensureLogin().catch(() => {})
})
</script>

<style lang="scss">
page {
  background: #f5f5f5;
  color: #222;
  font-size: 28rpx;
}

.muted {
  color: #8a8a8a;
  font-size: 24rpx;
}

.ellipsis {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.empty {
  padding: 160rpx 40rpx;
  text-align: center;
  color: #8a8a8a;
}
</style>
