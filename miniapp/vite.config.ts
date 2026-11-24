import fs from "node:fs";
import path from "node:path";
import { defineConfig, type Plugin, type ResolvedConfig } from "vite";
import uni from "@dcloudio/vite-plugin-uni";

interface AppConfig {
  serverUrl: string;
  appId: string;
  requestTimeout?: number;
}

function loadAppConfig(): AppConfig {
  const localPath = path.resolve(__dirname, "../app.config.json");
  const examplePath = path.resolve(__dirname, "../app.config.example.json");
  const target = fs.existsSync(localPath) ? localPath : examplePath;
  if (!fs.existsSync(target)) {
    return { serverUrl: "http://127.0.0.1:8080", appId: "", requestTimeout: 70000 };
  }
  return JSON.parse(fs.readFileSync(target, "utf-8"));
}

function injectWeixinAppId(appId: string): Plugin {
  let outDir = "";
  return {
    name: "inject-weixin-appid",
    configResolved(config: ResolvedConfig) {
      outDir = config.build.outDir;
    },
    closeBundle() {
      if (!appId || !outDir) return;
      const projectConfigPath = path.resolve(outDir, "project.config.json");
      if (!fs.existsSync(projectConfigPath)) return;
      const projectConfig = JSON.parse(fs.readFileSync(projectConfigPath, "utf-8"));
      if (projectConfig.appid !== appId) {
        projectConfig.appid = appId;
        fs.writeFileSync(projectConfigPath, JSON.stringify(projectConfig, null, 2) + "\n", "utf-8");
      }
    },
  };
}

const appConfig = loadAppConfig();

// https://vitejs.dev/config/
export default defineConfig({
  define: {
    __APP_CONFIG__: JSON.stringify(appConfig),
  },
  css: {
    preprocessorOptions: {
      scss: {
        quietDeps: true,
        silenceDeprecations: ["legacy-js-api", "import", "global-builtin", "color-functions"],
      },
    },
  },
  plugins: [uni(), injectWeixinAppId(appConfig.appId)],
});
