import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { fileURLToPath, URL } from "node:url";

// https://vitejs.dev/config/
export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  resolve: {
    alias: {
      // 换壳后组件不再依赖 Wails 运行时：同名别名指向我们自己的 shim
      // （Events/Window 的语义在这里抹平，组件代码不需要改）。
      "@wailsio/runtime": fileURLToPath(new URL("./src/lib/wails-shim.ts", import.meta.url)),
    },
  },
  plugins: [react()],
});
