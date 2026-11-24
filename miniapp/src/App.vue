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
  // Shared palette: warm paper background, one accent colour.
  --brand: #c0392b;
  --paper: #f6f3ee;
  --line: #efeae2;
  background: var(--paper);
  color: #2b2622;
  font-size: 28rpx;
}

.link {
  color: var(--brand);
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
