import fs from "node:fs";
import path from "node:path";
import { defineConfig, type Plugin } from "vite";
import uni from "@dcloudio/vite-plugin-uni";

function syncAppConfig(): Plugin {
  const sync = () => {
    const rootConfigPath = path.resolve(__dirname, "../app.config.json");
    const manifestPath = path.resolve(__dirname, "src/manifest.json");
    if (!fs.existsSync(rootConfigPath) || !fs.existsSync(manifestPath)) return;

    const appConfig = JSON.parse(fs.readFileSync(rootConfigPath, "utf-8"));
    const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf-8"));

    if (appConfig.appId && (manifest.appid !== appConfig.appId || manifest["mp-weixin"]?.appid !== appConfig.appId)) {
      manifest.appid = appConfig.appId;
      manifest["mp-weixin"] = {
        ...manifest["mp-weixin"],
        appid: appConfig.appId,
      };
      fs.writeFileSync(manifestPath, JSON.stringify(manifest, null, 2) + "\n", "utf-8");
    }
  };

  return {
    name: "sync-app-config",
    configResolved() {
      sync();
    },
  };
}

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [syncAppConfig(), uni()],
});
