<script setup lang="ts">
import { onLaunch } from '@dcloudio/uni-app'
import { hasSession, refreshSession, toLogin } from '@/api/http'

onLaunch(() => {
  // A device that has logged in before renews its token; any other starts at the login page.
  if (hasSession()) refreshSession().catch(() => {})
  else toLogin()
})
</script>

<style lang="scss">
page {
  // Shared palette: warm paper background, one accent colour.
  --brand: #c0392b;
  --paper: #f6f3ee;
  --line: #efeae2;
  // wot-design-uni takes its accent from this variable.
  --wot-color-theme: #c0392b;
  background: var(--paper);
  color: #2b2622;
  font-size: 28rpx;
  font-family: -apple-system, 'PingFang SC', 'Helvetica Neue', 'Microsoft YaHei', sans-serif;
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
