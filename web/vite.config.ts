import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { fileURLToPath, URL } from "node:url";

export default defineConfig({
	plugins: [react()],
	build: {
		outDir: fileURLToPath(new URL("../internal/webui/dist", import.meta.url)),
		emptyOutDir: true,
		sourcemap: false,
	},
	server: {
		port: 5173,
		strictPort: true,
		proxy: {
			"/api": "http://localhost:8080",
		},
	},
});
