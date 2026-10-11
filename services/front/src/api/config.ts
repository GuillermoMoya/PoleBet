// Direcciones de los backends. El front no sabe de donde vienen los datos:
// solo conoce estas URLs.
//
//   - En Docker las inyecta docker-compose en build time (VITE_PAGOS_URL,
//     VITE_MOTOR_URL, VITE_MOTOR_WS_URL) y son absolutas.
//   - En desarrollo (`npm run dev`) no se definen y se usa el proxy de Vite
//     (/api/pagos y /api/motor), ver vite.config.ts.

const env = import.meta.env;

export const PAGOS_URL: string = env.VITE_PAGOS_URL || "/api/pagos";
export const MOTOR_URL: string = env.VITE_MOTOR_URL || "/api/motor";

// true = mostrar la latencia de los mensajes de posiciones (demo de RNF-04).
export const MOSTRAR_LATENCIA: boolean = env.VITE_SHOW_LATENCY === "true";

// Devuelve la URL completa del WebSocket del motor. Sin variable de entorno
// arma una del mismo origen (ws://host/api/motor/ws) para que pase por el proxy.
export function urlWsMotor(): string {
  if (env.VITE_MOTOR_WS_URL) return `${env.VITE_MOTOR_WS_URL}/ws`;
  const protocolo = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${protocolo}//${window.location.host}${MOTOR_URL}/ws`;
}
