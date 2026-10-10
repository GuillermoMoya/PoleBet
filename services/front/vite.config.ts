import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// Puertos por defecto de los backends en desarrollo (los mismos del .env.example
// de la raiz) y del mock de scripts/mock-ws.mjs.
const PAGOS_REAL = "http://localhost:8000";
const MOTOR_REAL = "http://localhost:8080";
const MOTOR_MOCK = "http://localhost:8090";

export default defineConfig(({ mode }) => {
  // Con `npm run dev:mock` (modo "mock") el proxy del motor apunta al mock, asi
  // se desarrolla sin el motor de Go y sin tocar el codigo de la app. Las dos
  // variables permiten apuntar a otro lado si hace falta.
  const destinoMotor = process.env.PROXY_MOTOR ?? (mode === "mock" ? MOTOR_MOCK : MOTOR_REAL);
  const destinoPagos = process.env.PROXY_PAGOS ?? PAGOS_REAL;

  return {
    plugins: [react()],
    server: {
      host: true,
      port: 5173,
      // En desarrollo el front habla con los backends por el mismo origen
      // (/api/pagos y /api/motor). Asi la cookie de sesion viaja sola, tambien
      // al abrir el WebSocket (ws: true).
      proxy: {
        "/api/pagos": {
          target: destinoPagos,
          changeOrigin: true,
          rewrite: (ruta) => ruta.replace(/^\/api\/pagos/, ""),
        },
        "/api/motor": {
          target: destinoMotor,
          changeOrigin: true,
          ws: true,
          rewrite: (ruta) => ruta.replace(/^\/api\/motor/, ""),
        },
      },
    },
    // Vitest corre solo las pruebas en TypeScript. La prueba de HU-06
    // (src/apuestas/api.test.js) usa node:test y se corre con `node --test`.
    test: {
      environment: "jsdom",
      globals: true,
      setupFiles: ["./src/pruebas.setup.ts"],
      include: ["src/**/*.test.{ts,tsx}"],
      css: false,
    },
  };
});
