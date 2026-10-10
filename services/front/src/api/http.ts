// Wrapper de fetch para hablar con Pagos y con el motor.
//
//   - Siempre envia la cookie de sesion (credentials: "include").
//   - Antepone la URL base del servicio.
//   - Convierte cualquier fallo en un ErrorHttp { status, message } con el
//     mensaje en español.
//   - Ante un 401 avisa para limpiar la sesion; ante un 403 avisa para mostrar
//     "acceso denegado". Quien reacciona es el AuthContext (configurarHttp).

import { MOTOR_URL, PAGOS_URL } from "./config";
import type { ErrorApi } from "./tipos";

export type Servicio = "pagos" | "motor";

const BASES: Record<Servicio, string> = { pagos: PAGOS_URL, motor: MOTOR_URL };

const MENSAJES = {
  red: "No se pudo conectar con el servidor. Intenta de nuevo.",
  sesion: "Tu sesión venció. Inicia sesión de nuevo.",
  permiso: "No tienes permiso para esta acción.",
  generico: "Ocurrió un error inesperado.",
};

// Error normalizado que lanza `pedir`.
export class ErrorHttp extends Error implements ErrorApi {
  status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = "ErrorHttp";
    this.status = status;
  }
}

// Reacciones globales a los errores de sesion y de permisos.
export interface ManejadoresHttp {
  sesionVencida?: () => void;
  accesoDenegado?: (mensaje: string) => void;
}

let manejadores: ManejadoresHttp = {};

// Registra quien reacciona al 401 y al 403. Lo llama el AuthContext al montar.
export function configurarHttp(nuevos: ManejadoresHttp): void {
  manejadores = nuevos;
}

export interface OpcionesPeticion {
  method?: string;
  body?: unknown; // se envia como JSON
  headers?: Record<string, string>;
  signal?: AbortSignal;
  fetchFn?: typeof fetch; // reemplaza a fetch en las pruebas
}

// Saca el mensaje de error del cuerpo. El motor responde { error } y FastAPI
// responde { detail }.
function mensajeDelCuerpo(cuerpo: unknown): string | null {
  if (typeof cuerpo !== "object" || cuerpo === null) return null;
  const { error, detail } = cuerpo as { error?: unknown; detail?: unknown };
  if (typeof error === "string" && error !== "") return error;
  if (typeof detail === "string" && detail !== "") return detail;
  return null;
}

// Hace la peticion y devuelve el cuerpo JSON ya tipado. Si algo falla lanza
// un ErrorHttp.
export async function pedir<T>(servicio: Servicio, ruta: string, opciones: OpcionesPeticion = {}): Promise<T> {
  const { method = "GET", body, headers = {}, signal, fetchFn = fetch } = opciones;

  // 1. Enviar. Si no hay respuesta (red caida, backend apagado) es status 0.
  let resp: Response;
  try {
    resp = await fetchFn(`${BASES[servicio]}${ruta}`, {
      method,
      credentials: "include",
      signal,
      headers: body === undefined ? headers : { "Content-Type": "application/json", ...headers },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch (causa) {
    // Una peticion cancelada a proposito no es un error de red.
    if (causa instanceof DOMException && causa.name === "AbortError") throw causa;
    throw new ErrorHttp(0, MENSAJES.red);
  }

  // 2. Leer el cuerpo. Puede venir vacio (204) o no ser JSON.
  let cuerpo: unknown = null;
  try {
    cuerpo = await resp.json();
  } catch {
    /* sin cuerpo JSON */
  }

  if (resp.ok) return cuerpo as T;

  // 3. Traducir el error y avisar si es de sesion o de permisos.
  if (resp.status === 401) {
    manejadores.sesionVencida?.();
    throw new ErrorHttp(401, MENSAJES.sesion);
  }
  if (resp.status === 403) {
    manejadores.accesoDenegado?.(MENSAJES.permiso);
    throw new ErrorHttp(403, MENSAJES.permiso);
  }
  throw new ErrorHttp(resp.status, mensajeDelCuerpo(cuerpo) ?? MENSAJES.generico);
}

// Atajos para los dos metodos mas usados.
export const http = {
  get: <T>(servicio: Servicio, ruta: string, opciones?: OpcionesPeticion) =>
    pedir<T>(servicio, ruta, { ...opciones, method: "GET" }),
  post: <T>(servicio: Servicio, ruta: string, body?: unknown, opciones?: OpcionesPeticion) =>
    pedir<T>(servicio, ruta, { ...opciones, method: "POST", body }),
};
