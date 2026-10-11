/// <reference types="vite/client" />

// Variables de entorno que lee el front (ver .env.example).
interface ImportMetaEnv {
  readonly VITE_PAGOS_URL?: string;
  readonly VITE_MOTOR_URL?: string;
  readonly VITE_MOTOR_WS_URL?: string;
  readonly VITE_SHOW_LATENCY?: string;
  readonly VITE_ROL_SIMULADO?: string; // solo desarrollo: player | admin (sin definir = login real)
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
